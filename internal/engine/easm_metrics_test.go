package engine

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	jsonpatch "github.com/evanphx/json-patch"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const easmMetricsState = `{"schema_version":1,"title":"Example","nodes":[{"id":"root","kind":"root","label":"Example"}],"edges":[],"summary":{"clues":7,"fofa_assets":100,"fofa_ips":9},"heat":[{"date":"2026-01-01","value":3}],"components":[{"name":"8","display_name":"Existing sourced label","count":99,"hot_days":2,"tags":[{"type":"hot","evidence":[{"title":"Source evidence"}]}]}]}`
const easmHeatArguments = `{"query":"confirmed scope"}`
const easmProductArguments = `{"query":"confirmed scope","fields":"product1,product5","tool":"fofa"}`

func applyEASMMetricPatch(t *testing.T, state, name, arguments string, result *mcp.CallToolResult) []byte {
	t.Helper()
	patch := easmMetricsPatch([]byte(state), name, []byte(arguments), result)
	operations, err := jsonpatch.DecodePatch(patch)
	if err != nil || len(operations) != 1 {
		t.Fatalf("metric patch=%s error=%v", patch, err)
	}
	updated, err := operations.Apply([]byte(state))
	if err != nil {
		t.Fatal(err)
	}
	var before, after map[string]json.RawMessage
	_ = json.Unmarshal([]byte(state), &before)
	_ = json.Unmarshal(updated, &after)
	path, _ := operations[0].Path()
	for key, raw := range before {
		if path != "/"+key && !jsonpatch.Equal(raw, after[key]) {
			t.Errorf("metric update changed unrelated section %s", key)
		}
	}
	if repeated := easmMetricsPatch(updated, name, []byte(arguments), result); repeated != nil {
		t.Fatalf("repeated metric payload changed the state: %s", repeated)
	}
	return updated
}

func TestEASMHeatUsesOnlyReturnedDatesAndValuesBeforeModelPublication(t *testing.T) {
	result := easmOrganizationResult(`{"error":false,"errmsg":"","start_date":"2026-01-01","end_date":"2026-01-31","asset_scope":{"ip_count":567},"hot_trend":[{"date":"2026-01-15","value":"8"},{"date":"2026-01-02","value":"0"},{"date":"2026-01-15","value":8},{"date":"2026-01-10","value":2.5}]}`)
	updated := applyEASMMetricPatch(t, easmMetricsState, "mcp_fofa_asset_heat", easmHeatArguments, result)
	var state struct {
		Heat []struct {
			Date  string
			Value float64
		}
		Summary struct{ Clues, FofaAssets, FofaIPs int }
	}
	_ = json.Unmarshal(updated, &state)
	if len(state.Heat) != 3 || state.Heat[0].Date != "2026-01-02" || state.Heat[0].Value != 0 || state.Heat[1].Value != 2.5 || state.Heat[2].Date != "2026-01-15" || state.Heat[2].Value != 8 {
		t.Fatalf("heat invented a date or changed a source value: %s", updated)
	}
}

func TestEASMProductsRetainSourceIDsAndExistingEvidence(t *testing.T) {
	result := &mcp.CallToolResult{StructuredContent: map[string]any{
		"error": false, "size": 4321, "distinct": map[string]int{"ip": 999},
		"aggs": map[string]any{"product": []any{
			map[string]any{"name": 8, "count": 5}, map[string]any{"name": 695312, "count": "4"},
			map[string]any{"name": 923316, "count": 4}, map[string]any{"name": 8, "count": "5"},
		}},
	}}
	updated := applyEASMMetricPatch(t, easmMetricsState, "mcp_fofa_stats", easmProductArguments, result)
	var before, after struct{ Components []map[string]json.RawMessage }
	_ = json.Unmarshal([]byte(easmMetricsState), &before)
	_ = json.Unmarshal(updated, &after)
	if len(after.Components) != 3 {
		t.Fatalf("product buckets were lost or duplicated: %s", updated)
	}
	for _, field := range []string{"name", "display_name", "hot_days", "tags"} {
		if !reflect.DeepEqual(before.Components[0][field], after.Components[0][field]) {
			t.Fatalf("product identity or source evidence %s changed", field)
		}
	}
	for i, name := range []string{`"8"`, `"695312"`, `"923316"`} {
		if string(after.Components[i]["name"]) != name {
			t.Fatalf("product ID changed: %s", after.Components[i]["name"])
		}
	}
	if string(after.Components[0]["count"]) != "5" || after.Components[1]["display_name"] != nil || after.Components[2]["tags"] != nil {
		t.Fatal("product counts, labels or tags were fabricated")
	}
}

func TestEASMProductFieldOrderDoesNotHideReturnedBuckets(t *testing.T) {
	result := easmOrganizationResult(`{"aggs":{"product":[{"name":8,"count":5}]}}`)
	for _, fields := range []string{"product1,product5", "product5, product1", " product1 , product5 "} {
		arguments, _ := json.Marshal(map[string]string{"query": "confirmed scope", "fields": fields, "tool": "fofa"})
		updated := applyEASMMetricPatch(t, easmMetricsState, "mcp_fofa_stats", string(arguments), result)
		var state struct {
			Components []struct {
				Name  string
				Count int
			}
		}
		_ = json.Unmarshal(updated, &state)
		if len(state.Components) != 1 || state.Components[0].Name != "8" || state.Components[0].Count != 5 {
			t.Fatalf("field order discarded source data: %s", updated)
		}
	}
}

func TestEASMProductsKeepReturnedNamesAndLargeIntegerIdentities(t *testing.T) {
	result := easmOrganizationResult(`{"aggs":{"product":[{"name":"Source Product","count":0},{"name":9007199254740993,"count":1}]}}`)
	updated := applyEASMMetricPatch(t, easmMetricsState, "mcp_fofa_stats", easmProductArguments, result)
	var state struct{ Components []struct{ Name string } }
	_ = json.Unmarshal(updated, &state)
	if state.Components[0].Name != "Source Product" || state.Components[1].Name != "9007199254740993" {
		t.Fatalf("source labels or numeric identities changed: %s", updated)
	}
}

func TestEASMProductNamesComeOnlyFromRequestedRuleRecords(t *testing.T) {
	state := strings.Replace(easmMetricsState, `"components":[`, `"components":[{"name":"695312","count":4},`, 1)
	result := easmOrganizationResult(`{"results":[{"rule_id":8,"product":"Source Product","en_product":"Other language"},{"rule_id":8,"product":"Source Product"},{"rule_id":695312,"product":""},{"rule_id":999,"product":"Unrequested"},{"product":"No identity"}]}`)
	updated := applyEASMMetricPatch(t, state, "mcp_fofa_rule_lookup", `{"rule_ids":[8,"695312"]}`, result)
	var before, after struct{ Components []map[string]json.RawMessage }
	_ = json.Unmarshal([]byte(state), &before)
	_ = json.Unmarshal(updated, &after)
	if !reflect.DeepEqual(before.Components[0], after.Components[0]) {
		t.Fatal("an empty label overwrote an unmapped product")
	}
	if string(after.Components[1]["name"]) != `"8"` || string(after.Components[1]["display_name"]) != `"Source Product"` {
		t.Fatalf("rule records changed the stable product ID: %s", updated)
	}
	for _, field := range []string{"count", "hot_days", "tags"} {
		if !reflect.DeepEqual(before.Components[1][field], after.Components[1][field]) {
			t.Fatalf("rule label update replaced %s", field)
		}
	}
}

func TestEASMExplicitEmptyMetricArraysClearOnlyTheirKnownSection(t *testing.T) {
	for _, test := range []struct{ name, arguments, body, section string }{
		{"mcp_fofa_asset_heat", easmHeatArguments, `{"hot_trend":[]}`, "heat"},
		{"mcp_fofa_stats", easmProductArguments, `{"aggs":{"product":[]}}`, "components"},
	} {
		t.Run(test.name, func(t *testing.T) {
			updated := applyEASMMetricPatch(t, easmMetricsState, test.name, test.arguments, easmOrganizationResult(test.body))
			var state map[string]json.RawMessage
			_ = json.Unmarshal(updated, &state)
			if string(state[test.section]) != "[]" {
				t.Fatalf("known empty observation did not clear %s", test.section)
			}
		})
	}
}

func TestEASMMetricsRefuseMalformedAmbiguousOrUnrelatedResults(t *testing.T) {
	const validHeat = `{"hot_trend":[{"date":"2026-01-02","value":5}]}`
	for _, test := range []struct{ name, state, arguments, body string }{
		{"unknown", easmMetricsState, `{}`, validHeat},
		{"mcp_fofa_asset_heat", `{`, easmHeatArguments, validHeat},
		{"mcp_fofa_asset_heat", `null`, easmHeatArguments, validHeat},
		{"mcp_fofa_asset_heat", easmMetricsState, `{`, validHeat},
		{"mcp_fofa_asset_heat", easmMetricsState, `{"query":" "}`, validHeat},
		{"mcp_fofa_asset_heat", easmMetricsState, easmHeatArguments, `{`},
		{"mcp_fofa_asset_heat", easmMetricsState, easmHeatArguments, `{"error":true}`},
		{"mcp_fofa_asset_heat", easmMetricsState, easmHeatArguments, `{"error":{"code":"unavailable"}}`},
		{"mcp_fofa_asset_heat", easmMetricsState, easmHeatArguments, `{"isError":true}`},
		{"mcp_fofa_asset_heat", easmMetricsState, easmHeatArguments, `{"errmsg":"Source failed"}`},
		{"mcp_fofa_asset_heat", easmMetricsState, easmHeatArguments, `{}`},
		{"mcp_fofa_asset_heat", easmMetricsState, easmHeatArguments, `{"hot_trend":null}`},
		{"mcp_fofa_asset_heat", easmMetricsState, easmHeatArguments, `{"hot_trend":{}}`},
		{"mcp_fofa_asset_heat", easmMetricsState, easmHeatArguments, `{"hot_trend":[{"date":"2026-02-30","value":2}]}`},
		{"mcp_fofa_asset_heat", easmMetricsState, easmHeatArguments, `{"hot_trend":[{"date":"2026-1-2","value":2}]}`},
		{"mcp_fofa_asset_heat", easmMetricsState, easmHeatArguments, `{"hot_trend":[{"date":"2026-01-02"}]}`},
		{"mcp_fofa_asset_heat", easmMetricsState, easmHeatArguments, `{"hot_trend":[{"date":"2026-01-02","value":2},{"date":"2026-01-02","value":3}]}`},
		{"mcp_fofa_asset_heat", `{"heat":null}`, easmHeatArguments, validHeat},
		{"mcp_fofa_asset_heat", `{"heat":{}}`, easmHeatArguments, validHeat},
		{"mcp_fofa_stats", easmMetricsState, `{"query":"scope","fields":"ip","tool":"fofa"}`, `{"aggs":{"ip":[]}}`},
		{"mcp_fofa_stats", easmMetricsState, `{"query":"scope","fields":"product1,product5"}`, `{"aggs":{"product":[]}}`},
		{"mcp_fofa_stats", easmMetricsState, `{"query":"scope","fields":"product1,product3,product5","tool":"fofa"}`, `{"aggs":{"product":[]}}`},
		{"mcp_fofa_stats", easmMetricsState, easmProductArguments, `{"aggs":{"ip":[]}}`},
		{"mcp_fofa_stats", easmMetricsState, easmProductArguments, `{"aggs":{"product":null}}`},
		{"mcp_fofa_stats", easmMetricsState, easmProductArguments, `{"aggs":{"product":{}}}`},
		{"mcp_fofa_stats", easmMetricsState, easmProductArguments, `{"aggs":{"product":[{"name":8}]}}`},
		{"mcp_fofa_stats", easmMetricsState, easmProductArguments, `{"aggs":{"product":[{"name":false,"count":2}]}}`},
		{"mcp_fofa_stats", easmMetricsState, easmProductArguments, `{"aggs":{"product":[{"name":" ","count":2}]}}`},
		{"mcp_fofa_stats", easmMetricsState, easmProductArguments, `{"aggs":{"product":[{"name":8,"count":2},{"name":"8","count":3}]}}`},
		{"mcp_fofa_stats", `{"components":null}`, easmProductArguments, `{"aggs":{"product":[]}}`},
		{"mcp_fofa_stats", `{"components":[{}]}`, easmProductArguments, `{"aggs":{"product":[]}}`},
		{"mcp_fofa_stats", `{"components":[{"name":true}]}`, easmProductArguments, `{"aggs":{"product":[]}}`},
		{"mcp_fofa_stats", `{"components":[{"name":"8"},{"name":"8"}]}`, easmProductArguments, `{"aggs":{"product":[]}}`},
		{"mcp_fofa_rule_lookup", easmMetricsState, `{"rule_ids":[]}`, `{"results":[]}`},
		{"mcp_fofa_rule_lookup", easmMetricsState, `{"rule_ids":["unknown"]}`, `{"results":[]}`},
		{"mcp_fofa_rule_lookup", easmMetricsState, `{"rule_ids":[8]}`, `{"results":[{"rule_id":8,"product":"A"},{"rule_id":8,"product":"B"}]}`},
		{"mcp_fofa_rule_lookup", easmMetricsState, `{"rule_ids":[8]}`, `{"results":[{"rule_id":8,"product":123}]}`},
		{"mcp_fofa_rule_lookup", `{"components":[{"name":true}]}`, `{"rule_ids":[8]}`, `{"results":[]}`},
		{"mcp_fofa_rule_lookup", easmMetricsState, `{"rule_ids":[8]}`, `{"results":null}`},
	} {
		t.Run(fmt.Sprintf("%s/%s/%s", test.name, test.arguments, test.body), func(t *testing.T) {
			if patch := easmMetricsPatch([]byte(test.state), test.name, []byte(test.arguments), easmOrganizationResult(test.body)); patch != nil {
				t.Fatalf("unreliable metric result changed the projection: %s", patch)
			}
		})
	}
	for _, result := range []*mcp.CallToolResult{nil, {IsError: true}, {StructuredContent: func() {}}} {
		if patch := easmMetricsPatch([]byte(easmMetricsState), "mcp_fofa_asset_heat", []byte(easmHeatArguments), result); patch != nil {
			t.Fatalf("failed MCP envelope produced metrics: %s", patch)
		}
	}
}

func TestEASMMetricNumbersRetainValidSourceValues(t *testing.T) {
	for _, raw := range []string{"0", `"0"`, "123", `"12.5"`, "1e3", "-0"} {
		if value, ok := easmMetricNumber([]byte(raw)); !ok || value.String() == "" {
			t.Fatalf("valid source number %s was lost", raw)
		}
	}
	for _, raw := range []string{"", "null", "true", "{}", "[]", `"NaN"`, `"Infinity"`, "1e309", "-1", `"-3"`, `"01"`, `""`} {
		if _, ok := easmMetricNumber([]byte(raw)); ok {
			t.Fatalf("invalid source number %s was accepted", raw)
		}
	}
}

func TestEASMProductIDsPreserveTheirExactIntegerIdentity(t *testing.T) {
	for _, raw := range []string{"0", "8", `"8"`, "9007199254740993"} {
		if id := easmProductID([]byte(raw)); id != strings.Trim(raw, `"`) {
			t.Fatalf("product ID %s became %s", raw, id)
		}
	}
	for _, raw := range []string{"null", "false", `"Product"`, "-1", "8.5", "8e3", "{}", `""`} {
		if id := easmProductID([]byte(raw)); id != "" {
			t.Fatalf("non-ID %s became product %s", raw, id)
		}
	}
}

func TestEASMLargeMetricBatchesUseOneSectionReplacement(t *testing.T) {
	var heat, products []map[string]any
	for i := 0; i < 500; i++ {
		heat = append(heat, map[string]any{"date": timeDateForMetricTest(i), "value": i})
		products = append(products, map[string]any{"name": i, "count": i})
	}
	for _, test := range []struct {
		name, args string
		body       any
	}{
		{"mcp_fofa_asset_heat", easmHeatArguments, map[string]any{"hot_trend": heat}},
		{"mcp_fofa_stats", easmProductArguments, map[string]any{"aggs": map[string]any{"product": products}}},
	} {
		applyEASMMetricPatch(t, easmMetricsState, test.name, test.args, &mcp.CallToolResult{StructuredContent: test.body})
	}
}

func timeDateForMetricTest(day int) string {
	return fmt.Sprintf("%04d-%02d-%02d", 2025+day/336, 1+day%336/28, 1+day%28)
}
