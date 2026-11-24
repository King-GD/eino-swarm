package easm

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/LubyRuffy/eino-swarm/internal/config"
)

func init() {
	stepFuncs[StepClouds] = stepDef{roles: []string{RoleStats}, fn: (*run).clouds}
}

// cloudBucketLimit asks for more buckets than the table shows: the merge folds
// several source spellings into one provider, so a provider's rank is only
// known after the whole list is in.
const cloudBucketLimit = 100

// cloudTotalMismatch reports a provider whose filtered total disagrees with the
// buckets it was built from. The row's numbers would describe an asset range
// the table never showed, so the step fails instead of publishing them.
const cloudTotalMismatch = "cloud_total_mismatch"

// cloudServiceFloor is the smallest bucket request the stats backend accepts;
// only its other mode goes lower. max_services may be configured below this, so
// the request asks for the floor and the published list is trimmed afterwards.
const cloudServiceFloor = 5

// cloudRow accumulates the buckets that name the same provider. names keeps
// every distinct source spelling so the detail filter covers exactly those
// buckets, rather than trusting the source to treat "aws" and "Amazon AWS"
// alike. Case variants collapse: the filter matched both spellings of one name,
// and the filtered total still has to agree with the buckets it came from.
type cloudRow struct {
	label  config.CloudLabel
	key    string
	names  []string
	assets int64
}

// clouds aggregates the query's assets by hosting provider and then asks each
// shown provider for its own total and services. The second request per row is
// what makes the numbers self-consistent: bucket counts cannot be summed into
// an IP total, and a case-insensitive filter covers spellings the bucket list
// splits apart.
func (r *run) clouds(ctx context.Context) error {
	var reply struct {
		Query json.RawMessage `json:"query"`
		Aggs  struct {
			CloudName *[]struct {
				Name  string          `json:"name"`
				Count json.RawMessage `json:"count"`
			} `json:"cloud_name"`
		} `json:"aggs"`
	}
	arguments := map[string]any{
		"query": r.input.Query, "fields": "cloud_name", "uniq_count": "ip",
		"tool": fofaSource, "size": cloudBucketLimit,
	}
	if err := r.call(ctx, RoleStats, arguments, &reply); err != nil {
		return err
	}
	if err := queryMatches(reply.Query, r.input.Query); err != nil {
		return err
	}
	if reply.Aggs.CloudName == nil {
		return fmt.Errorf("reply carries no cloud buckets")
	}
	rows, err := mergeCloudRows(*reply.Aggs.CloudName)
	if err != nil {
		return err
	}
	if len(rows) > r.settings.MaxClouds {
		rows = rows[:r.settings.MaxClouds]
	}
	clouds := make([]Cloud, 0, len(rows))
	for _, row := range rows {
		cloud, err := r.cloudDetail(ctx, row)
		if err != nil {
			return err
		}
		clouds = append(clouds, cloud)
	}
	r.apply(func(state *State) { state.Clouds = clouds })
	return nil
}

// mergeCloudRows folds the source's spellings into one row per provider and
// orders them by asset count. Ties break on the merge key so two runs over the
// same reply publish the same order.
func mergeCloudRows(buckets []struct {
	Name  string          `json:"name"`
	Count json.RawMessage `json:"count"`
}) ([]cloudRow, error) {
	byKey := map[string]*cloudRow{}
	var rows []*cloudRow
	for _, bucket := range buckets {
		name := strings.TrimSpace(bucket.Name)
		if name == "" {
			return nil, fmt.Errorf("cloud bucket has no name")
		}
		count, ok, err := exactCount(bucket.Count)
		if err != nil || !ok {
			return nil, fmt.Errorf("malformed cloud bucket")
		}
		label := config.CloudLabelFor(name)
		key := strings.ToLower(label.Display)
		if key == "" {
			// An unmapped name is shown as reported; its own lowercase spelling
			// is the only identity available to merge on.
			key = strings.ToLower(name)
		}
		row, seen := byKey[key]
		if !seen {
			row = &cloudRow{label: label, key: key}
			byKey[key] = row
			rows = append(rows, row)
		}
		row.assets += count
		if !containsFold(row.names, name) {
			row.names = append(row.names, name)
		}
	}
	ordered := make([]cloudRow, 0, len(rows))
	for _, row := range rows {
		ordered = append(ordered, *row)
	}
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].assets != ordered[j].assets {
			return ordered[i].assets > ordered[j].assets
		}
		return ordered[i].key < ordered[j].key
	})
	return ordered, nil
}

func containsFold(values []string, value string) bool {
	for _, existing := range values {
		if strings.EqualFold(existing, value) {
			return true
		}
	}
	return false
}

// cloudDetail asks for one provider's own totals and services inside the run's
// query. The filtered total must equal the buckets it was built from; anything
// else means the filter reached a wider asset range than the table shows.
func (r *run) cloudDetail(ctx context.Context, row cloudRow) (Cloud, error) {
	query, err := cloudFilterQuery(r.input.Query, row.names)
	if err != nil {
		return Cloud{}, err
	}
	var reply struct {
		Query    json.RawMessage            `json:"query"`
		Size     json.RawMessage            `json:"size"`
		Distinct map[string]json.RawMessage `json:"distinct"`
		Aggs     struct {
			Product *[]struct {
				Name  json.RawMessage `json:"name"`
				Count json.RawMessage `json:"count"`
			} `json:"product"`
		} `json:"aggs"`
	}
	size := r.settings.MaxServices
	if size < cloudServiceFloor {
		size = cloudServiceFloor
	}
	arguments := map[string]any{
		"query": query, "fields": "product3", "uniq_count": "ip",
		"tool": fofaSource, "size": size,
	}
	if err := r.call(ctx, RoleStats, arguments, &reply); err != nil {
		return Cloud{}, err
	}
	if err := queryMatches(reply.Query, query); err != nil {
		return Cloud{}, err
	}
	assets, ok, err := exactCount(reply.Size)
	if err != nil || !ok {
		return Cloud{}, fmt.Errorf("cloud reply carries no total")
	}
	if assets != row.assets {
		return Cloud{}, &ToolError{Code: cloudTotalMismatch}
	}
	ips, ok, err := exactCount(reply.Distinct["ip"])
	if err != nil || !ok {
		return Cloud{}, fmt.Errorf("cloud reply carries no distinct ip total")
	}
	services, err := cloudServices(reply.Aggs.Product)
	if err != nil {
		return Cloud{}, err
	}
	// The request asks for the backend's floor; the table shows only what the
	// setting allows, counted after vendor labels are dropped.
	if len(services) > r.settings.MaxServices {
		services = services[:r.settings.MaxServices]
	}
	return Cloud{
		Name:        row.names[0],
		DisplayName: row.label.Display,
		Category:    row.label.Category,
		Assets:      assets,
		IPs:         ips,
		Services:    services,
	}, nil
}

// cloudFilterQuery narrows the portrait's query to one provider's buckets. Each
// source spelling becomes its own term: the request never assumes the backend
// normalises aliases.
func cloudFilterQuery(base string, names []string) (string, error) {
	terms := make([]string, 0, len(names))
	for _, name := range names {
		if strings.ContainsAny(name, `"\`) {
			return "", fmt.Errorf("cloud name cannot be filtered")
		}
		terms = append(terms, `cloud_name="`+name+`"`)
	}
	if len(terms) == 0 {
		return "", fmt.Errorf("cloud row has no source name")
	}
	return "(" + base + ") && (" + strings.Join(terms, " || ") + ")", nil
}

// cloudServices returns the provider's most common services. A bucket naming a
// hosting vendor is dropped: the product table describes the edge that answered,
// so "金山云-CDN" under an Aliyun asset is the edge's label, not the services
// the company runs there.
func cloudServices(buckets *[]struct {
	Name  json.RawMessage `json:"name"`
	Count json.RawMessage `json:"count"`
}) ([]CloudService, error) {
	if buckets == nil {
		return []CloudService{}, nil
	}
	vendors := config.CloudVendorNames()
	services := make([]CloudService, 0, len(*buckets))
	seen := map[string]int64{}
	for _, bucket := range *buckets {
		name, _ := productName(bucket.Name)
		count, ok, err := exactCount(bucket.Count)
		if name == "" || err != nil || !ok {
			return nil, fmt.Errorf("malformed cloud service bucket")
		}
		if vendorLabel(name, vendors) {
			continue
		}
		if previous, duplicate := seen[name]; duplicate {
			if previous != count {
				return nil, fmt.Errorf("conflicting cloud service bucket")
			}
			continue
		}
		seen[name] = count
		services = append(services, CloudService{Name: name, Count: count})
	}
	sort.SliceStable(services, func(i, j int) bool {
		if services[i].Count != services[j].Count {
			return services[i].Count > services[j].Count
		}
		return services[i].Name < services[j].Name
	})
	return services, nil
}

// vendorLabel reports whether a service name is a hosting vendor's own label.
// Names split on punctuation so "白山云科技-白山云" and "金山云-CDN" match while
// the real product "Cdn-Cache-Server" does not.
func vendorLabel(name string, vendors map[string]bool) bool {
	lower := strings.ToLower(name)
	if strings.HasSuffix(lower, "-cdn") {
		return true
	}
	for _, part := range strings.FieldsFunc(lower, func(r rune) bool {
		return r == '-' || r == '_' || r == ' ' || r == '/' || r == '.' || r == '(' || r == ')'
	}) {
		if vendors[part] {
			return true
		}
	}
	return false
}
