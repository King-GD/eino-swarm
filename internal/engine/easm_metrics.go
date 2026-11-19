package engine

import (
	"encoding/json"
	"math"
	"math/big"
	"sort"
	"strings"
	"time"

	jsonpatch "github.com/evanphx/json-patch"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The caller binds observations to the current asset query before applying
// these sections. Metric responses never establish ownership or summary totals.
func easmMetricsPatch(state json.RawMessage, remoteName string, arguments json.RawMessage, result *mcp.CallToolResult) json.RawMessage {
	patch, _ := easmMetricsObservation(state, remoteName, arguments, result)
	return patch
}

// A valid equal observation settles its request even when it needs no mutation.
func easmMetricsObservation(state json.RawMessage, remoteName string, arguments json.RawMessage, result *mcp.CallToolResult) (json.RawMessage, bool) {
	if result == nil || result.IsError {
		return nil, false
	}
	var request struct {
		Query, Fields, Tool string
		RuleIDs             []json.RawMessage `json:"rule_ids"`
	}
	if json.Unmarshal(arguments, &request) != nil {
		return nil, false
	}
	var previous map[string]json.RawMessage
	if json.Unmarshal(state, &previous) != nil || previous == nil {
		return nil, false
	}
	var reply struct {
		Error    any
		IsError  bool
		Errmsg   string
		HotTrend json.RawMessage            `json:"hot_trend"`
		Aggs     map[string]json.RawMessage `json:"aggs"`
		Results  json.RawMessage            `json:"results"`
	}
	if json.Unmarshal(easmOrganizationBody(result), &reply) != nil || reply.IsError || reply.Errmsg != "" || reply.Error != nil && reply.Error != false {
		return nil, false
	}
	var path string
	var next json.RawMessage
	switch remoteName {
	case "mcp_fofa_asset_heat":
		if strings.TrimSpace(request.Query) == "" {
			return nil, false
		}
		path, next = "heat", easmHeatData(reply.HotTrend)
	case "mcp_fofa_stats":
		if strings.TrimSpace(request.Query) == "" || !easmProductFields(request.Fields) || request.Tool != "fofa" {
			return nil, false
		}
		path, next = "components", easmProductData(previous["components"], reply.Aggs["product"])
	case "mcp_fofa_rule_lookup":
		path, next = "components", easmProductLabels(previous["components"], request.RuleIDs, reply.Results)
	default:
		return nil, false
	}
	var current []json.RawMessage
	if next == nil || json.Unmarshal(previous[path], &current) != nil || current == nil {
		return nil, false
	}
	if jsonpatch.Equal(previous[path], next) {
		return nil, true
	}
	patch, _ := json.Marshal([]map[string]any{{"op": "replace", "path": "/" + path, "value": next}})
	return patch, true
}

func easmProductFields(fields string) bool {
	parts := strings.Split(fields, ",")
	if len(parts) != 2 {
		return false
	}
	first, second := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
	return first == "product1" && second == "product5" || first == "product5" && second == "product1"
}

func easmHeatData(raw json.RawMessage) json.RawMessage {
	var source []struct {
		Date  string
		Value json.RawMessage
	}
	if json.Unmarshal(raw, &source) != nil || source == nil {
		return nil
	}
	type point struct {
		Date  string      `json:"date"`
		Value json.Number `json:"value"`
	}
	points := make([]point, 0, len(source))
	values := make(map[string]json.Number, len(source))
	for _, item := range source {
		if _, err := time.Parse(time.DateOnly, item.Date); err != nil {
			return nil
		}
		value, ok := easmMetricNumber(item.Value)
		if !ok {
			return nil
		}
		if previous, duplicate := values[item.Date]; duplicate {
			if previous != value {
				return nil
			}
			continue
		}
		values[item.Date] = value
		points = append(points, point{item.Date, value})
	}
	sort.Slice(points, func(i, j int) bool { return points[i].Date < points[j].Date })
	data, _ := json.Marshal(points)
	return data
}

func easmProductData(previous, raw json.RawMessage) json.RawMessage {
	var existing []map[string]json.RawMessage
	var buckets []struct {
		Name, Count json.RawMessage
	}
	if json.Unmarshal(previous, &existing) != nil || existing == nil || json.Unmarshal(raw, &buckets) != nil || buckets == nil {
		return nil
	}
	known := make(map[string]map[string]json.RawMessage, len(existing))
	for _, component := range existing {
		var name string
		if json.Unmarshal(component["name"], &name) != nil || name == "" || known[name] != nil {
			return nil
		}
		known[name] = component
	}
	components := make([]map[string]json.RawMessage, 0, len(buckets))
	counts := make(map[string]json.Number, len(buckets))
	for _, bucket := range buckets {
		var name string
		if json.Unmarshal(bucket.Name, &name) != nil {
			name = easmProductID(bucket.Name)
		}
		count, ok := easmMetricNumber(bucket.Count)
		if strings.TrimSpace(name) == "" || !ok {
			return nil
		}
		if previous, duplicate := counts[name]; duplicate {
			if previous != count {
				return nil
			}
			continue
		}
		counts[name] = count
		component := known[name]
		if component == nil {
			component = map[string]json.RawMessage{}
			component["name"], _ = json.Marshal(name)
		}
		component["count"], _ = json.Marshal(count)
		components = append(components, component)
	}
	data, _ := json.Marshal(components)
	return data
}

func easmProductLabels(previous json.RawMessage, requested []json.RawMessage, raw json.RawMessage) json.RawMessage {
	var components []map[string]json.RawMessage
	var rules []struct {
		RuleID  json.RawMessage `json:"rule_id"`
		Product string          `json:"product"`
	}
	if len(requested) == 0 || json.Unmarshal(previous, &components) != nil || components == nil || json.Unmarshal(raw, &rules) != nil || rules == nil {
		return nil
	}
	allowed := make(map[string]bool, len(requested))
	for _, rawID := range requested {
		id := easmProductID(rawID)
		if id == "" {
			return nil
		}
		allowed[id] = true
	}
	labels := make(map[string]string, len(rules))
	for _, rule := range rules {
		id := easmProductID(rule.RuleID)
		if !allowed[id] || strings.TrimSpace(rule.Product) == "" {
			continue
		}
		if previous, duplicate := labels[id]; duplicate && previous != rule.Product {
			return nil
		}
		labels[id] = rule.Product
	}
	for _, component := range components {
		var name string
		if json.Unmarshal(component["name"], &name) != nil {
			return nil
		}
		if label, found := labels[name]; found {
			component["display_name"], _ = json.Marshal(label)
		}
	}
	data, _ := json.Marshal(components)
	return data
}

func easmMetricNumber(raw json.RawMessage) (json.Number, bool) {
	var number json.Number
	if json.Unmarshal(raw, &number) != nil {
		return "", false
	}
	value, err := number.Float64()
	return number, err == nil && value >= 0 && !math.IsInf(value, 0) && !math.IsNaN(value)
}

func easmProductID(raw json.RawMessage) string {
	var number json.Number
	if json.Unmarshal(raw, &number) != nil {
		return ""
	}
	var id big.Int
	if _, ok := id.SetString(number.String(), 10); !ok || id.Sign() < 0 {
		return ""
	}
	return id.String()
}
