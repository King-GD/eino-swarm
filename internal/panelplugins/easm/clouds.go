package easm

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/LubyRuffy/eino-swarm/internal/config"
)

func init() {
	stepFuncs[StepClouds] = stepDef{roles: []string{RoleStats}, fn: (*run).clouds}
}

// cloudBucketLimit asks for more buckets than the table shows: the merge folds
// several source spellings into one provider, so a provider's rank is only
// known after the whole list is in.
const cloudBucketLimit = 100

// cloudServiceFloor is the smallest bucket request the stats API accepts.
// max_services may be configured below it, so the request asks for the floor
// and the published list is trimmed afterwards.
const cloudServiceFloor = 5

// cloudServiceLookups bounds the per-provider service requests in flight. The
// stats backend is rate limited to five requests a second; FOFA AI uses the
// same bound for its chart requests.
const cloudServiceLookups = 4

// cloudBucket is one cloud_name bucket. uniq carries the bucket's own distinct
// IP count because the request asks for uniq_count=ip.
type cloudBucket struct {
	Name  string          `json:"name"`
	Count json.RawMessage `json:"count"`
	Uniq  struct {
		IP json.RawMessage `json:"ip"`
	} `json:"uniq"`
}

// cloudRow accumulates the buckets that name the same provider. names keeps
// every distinct source spelling so the service filter covers exactly those
// buckets rather than trusting the source to treat "aws" and "Amazon AWS" alike.
type cloudRow struct {
	label  config.CloudLabel
	key    string
	names  []string
	assets int64
	ips    int64
}

// clouds aggregates the query's assets by hosting provider. One request gives
// every row its asset and distinct-IP counts; each shown provider then gets one
// filtered request for its services. A provider whose service lookup fails
// keeps its counts with an empty service list: the counts do not depend on it.
func (r *run) clouds(ctx context.Context) error {
	var reply struct {
		Query json.RawMessage `json:"query"`
		Aggs  struct {
			CloudName *[]cloudBucket `json:"cloud_name"`
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
	clouds := make([]Cloud, len(rows))
	slots := make(chan struct{}, cloudServiceLookups)
	var wg sync.WaitGroup
	for i, row := range rows {
		clouds[i] = Cloud{
			Name:        row.names[0],
			DisplayName: row.label.Display,
			Category:    row.label.Category,
			Assets:      row.assets,
			IPs:         row.ips,
			Services:    []CloudService{},
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case slots <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-slots }()
			if services, err := r.cloudServices(ctx, row); err == nil {
				clouds[i].Services = services
			}
		}()
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return err
	}
	r.apply(func(state *State) { state.Clouds = clouds })
	return nil
}

// mergeCloudRows folds the source's spellings into one row per provider and
// orders them by asset count. Merged IP counts are summed: an address reported
// under two spellings of one provider is counted twice, which the documentation
// states. Ties break on the merge key so two runs over the same reply publish
// the same order.
func mergeCloudRows(buckets []cloudBucket) ([]cloudRow, error) {
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
		ips, ok, err := exactCount(bucket.Uniq.IP)
		if err != nil || !ok {
			return nil, fmt.Errorf("cloud bucket carries no distinct ip count")
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
		row.ips += ips
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

// cloudServices asks for one provider's most common services inside the run's
// query and keeps the first max_services. The buckets are shown as reported.
func (r *run) cloudServices(ctx context.Context, row cloudRow) ([]CloudService, error) {
	query, err := cloudFilterQuery(r.input.Query, row.names)
	if err != nil {
		return nil, err
	}
	var reply struct {
		Query json.RawMessage `json:"query"`
		Aggs  struct {
			Product *[]struct {
				Name  json.RawMessage `json:"name"`
				Count json.RawMessage `json:"count"`
			} `json:"product"`
		} `json:"aggs"`
	}
	arguments := map[string]any{
		"query": query, "fields": "product3", "tool": fofaSource,
		"size": max(r.settings.MaxServices, cloudServiceFloor),
	}
	if err := r.call(ctx, RoleStats, arguments, &reply); err != nil {
		return nil, err
	}
	if err := queryMatches(reply.Query, query); err != nil {
		return nil, err
	}
	if reply.Aggs.Product == nil {
		return []CloudService{}, nil
	}
	services := make([]CloudService, 0, len(*reply.Aggs.Product))
	seen := map[string]bool{}
	for _, bucket := range *reply.Aggs.Product {
		name, _ := productName(bucket.Name)
		count, ok, err := exactCount(bucket.Count)
		if name == "" || err != nil || !ok {
			return nil, fmt.Errorf("malformed cloud service bucket")
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		services = append(services, CloudService{Name: name, Count: count})
	}
	sort.SliceStable(services, func(i, j int) bool {
		if services[i].Count != services[j].Count {
			return services[i].Count > services[j].Count
		}
		return services[i].Name < services[j].Name
	})
	if len(services) > r.settings.MaxServices {
		services = services[:r.settings.MaxServices]
	}
	return services, nil
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
	return "(" + base + ") && (" + strings.Join(terms, " || ") + ")", nil
}
