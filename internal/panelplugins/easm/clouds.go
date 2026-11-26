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
	merged bool
}

type cloudStatsReply struct {
	Query    json.RawMessage `json:"query"`
	Distinct json.RawMessage `json:"distinct"`
	Aggs     json.RawMessage `json:"aggs"`
}

// clouds ranks providers by the distribution's assets, then fills their
// services and any IP totals that need to span multiple buckets.
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
	rows = rows[:min(len(rows), r.settings.MaxClouds)]
	results := make([]struct {
		cloud *Cloud
		err   error
	}, len(rows))
	var wg sync.WaitGroup
	for i, row := range rows {
		wg.Go(func() { results[i].cloud, results[i].err = r.cloudDetails(ctx, row) })
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return err
	}
	clouds := make([]Cloud, 0, len(rows))
	var failed error
	for _, result := range results {
		if result.cloud != nil {
			clouds = append(clouds, *result.cloud)
		}
		if failed == nil {
			failed = result.err
		}
	}
	r.apply(func(state *State) { state.Clouds = clouds })
	return failed
}

// mergeCloudRows folds the source's spellings into one row per provider and
// orders them by asset count. Bucket-local IP counts can overlap. Record every
// merge, including case variants that use the same filter term. Ties break on
// the merge key.
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
			row = &cloudRow{label: label, key: key, ips: ips}
			byKey[key] = row
			rows = append(rows, row)
		} else {
			row.merged = true
		}
		row.assets += count
		// Case-only spellings fold into one: measured against the live stats
		// API, cloud_name="CLOUDFRONT" returns the same buckets as
		// cloud_name="cloudfront", so the filter matches either spelling.
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

// cloudDetails shares the filtered service reply with the IP lookup. The
// schema requires an exact IP count, so an unresolved merged row is omitted.
func (r *run) cloudDetails(ctx context.Context, row cloudRow) (*Cloud, error) {
	query, err := cloudFilterQuery(r.input.Query, row.names)
	cloud := &Cloud{
		Name:        row.names[0],
		DisplayName: row.label.Display,
		Category:    row.label.Category,
		Assets:      row.assets,
		IPs:         row.ips,
		Services:    []CloudService{},
	}
	if err != nil {
		if row.merged {
			return nil, err
		}
		return cloud, err
	}
	var reply cloudStatsReply
	arguments := map[string]any{
		"query": query, "fields": "product3", "tool": fofaSource,
		"size": max(r.settings.MaxServices, cloudServiceFloor),
	}
	serviceErr := r.call(ctx, RoleStats, arguments, &reply)
	if serviceErr == nil {
		serviceErr = queryMatches(reply.Query, query)
	}
	hasIPs := !row.merged
	if serviceErr == nil {
		if row.merged {
			cloud.IPs, hasIPs = cloudReplyIPs(reply.Distinct)
		}
		var services []CloudService
		services, serviceErr = r.cloudServices(reply.Aggs)
		if serviceErr == nil {
			cloud.Services = services
		}
	}
	if !hasIPs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		cloud.IPs, err = r.cloudDistinctIPs(ctx, query)
		if err != nil {
			return nil, err
		}
	}
	return cloud, serviceErr
}

func cloudReplyIPs(raw json.RawMessage) (int64, bool) {
	var distinct map[string]json.RawMessage
	if err := json.Unmarshal(raw, &distinct); err != nil {
		return 0, false
	}
	ips, ok, err := exactCount(distinct["ip"])
	return ips, ok && err == nil
}

func (r *run) cloudDistinctIPs(ctx context.Context, query string) (int64, error) {
	var reply cloudStatsReply
	arguments := map[string]any{"query": query, "fields": "ip", "uniq_count": "ip", "tool": fofaSource}
	if err := r.call(ctx, RoleStats, arguments, &reply); err != nil {
		return 0, err
	}
	if err := queryMatches(reply.Query, query); err != nil {
		return 0, err
	}
	ips, ok := cloudReplyIPs(reply.Distinct)
	if !ok {
		return 0, fmt.Errorf("reply carries no exact cloud distinct ip count")
	}
	return ips, nil
}

// cloudServices interprets the service aggregation independently of the IP count.
func (r *run) cloudServices(raw json.RawMessage) ([]CloudService, error) {
	var aggs struct {
		Product []struct {
			Name  json.RawMessage `json:"name"`
			Count json.RawMessage `json:"count"`
		} `json:"product"`
	}
	if err := json.Unmarshal(raw, &aggs); err != nil || aggs.Product == nil {
		return nil, fmt.Errorf("reply carries no valid cloud service product buckets")
	}
	services := make([]CloudService, 0, len(aggs.Product))
	seen := map[string]bool{}
	for _, bucket := range aggs.Product {
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
