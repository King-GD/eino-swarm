package engine

import (
	"encoding/json"
	"reflect"
	"testing"

	jsonpatch "github.com/evanphx/json-patch"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const easmSummaryArguments = `{"query":"confirmed scope","fields":"ip","tool":"fofa","uniq_count":"ip"}`

func TestEASMSummaryUsesTheDedicatedAggregateAndRetainsClueEvidence(t *testing.T) {
	result := easmOrganizationResult(`{"error":false,"errmsg":"","size":1234,"distinct":{"ip":76},"aggs":{"ip":[{"name":"192.0.2.1","count":10}]}}`)
	patch := easmSummaryPatch([]byte(easmMetricsState), []byte(easmSummaryArguments), result)
	operations, err := jsonpatch.DecodePatch(patch)
	if err != nil || len(operations) != 1 {
		t.Fatalf("summary patch=%s error=%v", patch, err)
	}
	updated, err := operations.Apply([]byte(easmMetricsState))
	if err != nil {
		t.Fatal(err)
	}
	var before, after map[string]json.RawMessage
	_ = json.Unmarshal([]byte(easmMetricsState), &before)
	_ = json.Unmarshal(updated, &after)
	for field, raw := range before {
		if field != "summary" && !reflect.DeepEqual(raw, after[field]) {
			t.Fatalf("summary update changed %s", field)
		}
	}
	var summary map[string]int
	_ = json.Unmarshal(after["summary"], &summary)
	if !reflect.DeepEqual(summary, map[string]int{"clues": 7, "fofa_assets": 1234, "fofa_ips": 76}) {
		t.Fatalf("summary invented or combined counters: %s", after["summary"])
	}
	if repeated := easmSummaryPatch(updated, []byte(easmSummaryArguments), result); repeated != nil {
		t.Fatalf("repeated aggregate changed the summary: %s", repeated)
	}
}

func TestEASMSummaryOmitsUnknownValuesAndKeepsActualZero(t *testing.T) {
	for _, test := range []struct{ state, result, want string }{
		{`{"heat":[],"components":[]}`, `{"size":0}`, `{"fofa_assets":0}`},
		{`{"summary":null}`, `{"distinct":{"ip":"0"}}`, `{"fofa_ips":0}`},
		{easmMetricsState, `{"size":0}`, `{"clues":7,"fofa_assets":0}`},
		{easmMetricsState, `{"distinct":{"ip":0}}`, `{"clues":7,"fofa_ips":0}`},
	} {
		patch := easmSummaryPatch([]byte(test.state), []byte(easmSummaryArguments), easmOrganizationResult(test.result))
		operations, err := jsonpatch.DecodePatch(patch)
		if err != nil {
			t.Fatalf("patch=%s error=%v", patch, err)
		}
		updated, err := operations.Apply([]byte(test.state))
		if err != nil {
			t.Fatal(err)
		}
		var state map[string]json.RawMessage
		_ = json.Unmarshal(updated, &state)
		if !jsonpatch.Equal(state["summary"], []byte(test.want)) {
			t.Fatalf("unknown counter became zero or old evidence was lost: %s", updated)
		}
	}
}

func TestEASMSummaryRejectsOtherScopesAndIncompleteOrInvalidCounters(t *testing.T) {
	for _, test := range []struct{ state, args, body string }{
		{easmMetricsState, `{}`, `{"size":3}`},
		{easmMetricsState, `{`, `{"size":3}`},
		{easmMetricsState, `{"query":"scope","fields":"product1,product5","tool":"fofa","uniq_count":"ip"}`, `{"size":3}`},
		{easmMetricsState, `{"query":"scope","fields":"ip","uniq_count":"ip"}`, `{"size":3}`},
		{easmMetricsState, `{"query":"scope","fields":"ip","tool":"fofa"}`, `{"size":3}`},
		{easmMetricsState, easmSummaryArguments, `{`},
		{easmMetricsState, easmSummaryArguments, `{"error":true,"size":3}`},
		{easmMetricsState, easmSummaryArguments, `{"isError":true,"size":3}`},
		{easmMetricsState, easmSummaryArguments, `{"errmsg":"Failed","size":3}`},
		{easmMetricsState, easmSummaryArguments, `{}`},
		{easmMetricsState, easmSummaryArguments, `{"asset_scope":{"ip_count":3},"aggs":{"product":[{"name":8,"count":3}]}}`},
		{easmMetricsState, easmSummaryArguments, `{"size":null}`},
		{easmMetricsState, easmSummaryArguments, `{"size":3,"distinct":{"ip":-1}}`},
		{easmMetricsState, easmSummaryArguments, `{"size":3.5}`},
		{easmMetricsState, easmSummaryArguments, `{"size":3,"distinct":{"ip":"unknown"}}`},
		{`{`, easmSummaryArguments, `{"size":3}`},
		{`null`, easmSummaryArguments, `{"size":3}`},
		{`{"summary":[]}`, easmSummaryArguments, `{"size":3}`},
	} {
		if patch := easmSummaryPatch([]byte(test.state), []byte(test.args), easmOrganizationResult(test.body)); patch != nil {
			t.Fatalf("unsupported aggregate changed summary: args=%s body=%s patch=%s", test.args, test.body, patch)
		}
	}
	for _, result := range []*mcp.CallToolResult{nil, {IsError: true}, {StructuredContent: func() {}}} {
		if patch := easmSummaryPatch([]byte(easmMetricsState), []byte(easmSummaryArguments), result); patch != nil {
			t.Fatalf("failed result changed summary: %s", patch)
		}
	}
}
