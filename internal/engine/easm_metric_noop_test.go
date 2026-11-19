package engine

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/LubyRuffy/eino-swarm/internal/mcpclient"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// A newer successful observation can confirm the current value without a new
// revision. Its success still prevents an older in-flight response repainting it.
func TestEASMEqualNewerMetricsSettleOlderResponsesWithoutAnotherRevision(t *testing.T) {
	for _, metric := range []struct{ section, name, args, current, older, invalid string }{
		{"heat", "mcp_fofa_asset_heat", `{"query":"range-a"}`, `{"hot_trend":[{"date":"2026-01-01","value":1}]}`, `{"hot_trend":[{"date":"2026-01-01","value":99}]}`, `{"hot_trend":null}`},
		{"components", "mcp_fofa_stats", `{"query":"range-a","tool":"fofa","fields":"product1,product5"}`, `{"aggs":{"product":[{"name":7,"count":4}]}}`, `{"aggs":{"product":[{"name":7,"count":99}]}}`, `{"aggs":{"product":null}}`},
		{"summary", "mcp_fofa_stats", `{"query":"range-a","tool":"fofa","fields":"ip","uniq_count":"ip"}`, `{"size":17,"distinct":{"ip":3}}`, `{"size":99,"distinct":{"ip":9}}`, `{"size":"unknown"}`},
	} {
		for _, outcome := range []string{"equal", "invalid", "error", "nil"} {
			t.Run(metric.section+"/"+outcome, func(t *testing.T) {
				e, source, _ := easmObservedMetricFixture(t)
				seed := easmMetricCallback(t, e, source, "seed", metric.name, metric.args)
				seed(easmMetricResult(metric.current))
				before := easmMetricPanel(t, e, source)
				older := easmMetricCallback(t, e, source, "older", metric.name, metric.args)
				newer := easmMetricCallback(t, e, source, "newer", metric.name, metric.args)
				switch outcome {
				case "equal":
					newer(easmMetricResult(metric.current))
				case "invalid":
					newer(easmMetricResult(metric.invalid))
				case "error":
					newer(&mcp.CallToolResult{IsError: true})
				case "nil":
					newer(nil)
				}
				if observed := easmMetricPanel(t, e, source); observed.Revision != before.Revision || !bytes.Equal(observed.State, before.State) {
					t.Fatal("unchanged or failed observation added a useless revision")
				}
				older(easmMetricResult(metric.older))
				after := easmMetricPanel(t, e, source)
				if outcome == "equal" {
					if after.Revision != before.Revision || !bytes.Equal(after.State, before.State) {
						t.Fatal("older differing response overwrote a newer successful equal observation")
					}
				} else if after.Revision != before.Revision+1 || bytes.Equal(after.State, before.State) {
					t.Fatal("a failed newer request withheld the older available valid observation")
				}
			})
		}
	}
}

func TestEASMValidEmptyProductsRetainTheirCurrentSourceAndRejectOlderProviders(t *testing.T) {
	e, source, _ := easmObservedMetricFixture(t)
	arguments := `{"query":"range-a","tool":"fofa","fields":"product1,product5"}`
	seed := easmMetricCallback(t, e, source, "seed", "mcp_fofa_stats", arguments)
	seed(easmMetricResult(`{"aggs":{"product":[{"name":7,"count":4}]}}`))
	older := easmMetricCallback(t, e, source, "older", "mcp_fofa_stats", arguments)
	source.ToolCallID = "new-provider-empty"
	call := mcpclient.ToolCall{ServerID: "source-b", Name: "mcp_fofa_stats", Arguments: json.RawMessage(arguments)}
	newer := e.bindEASMMetricCall(t.Context(), source, call, "components", "range-a")
	newer(easmMetricResult(`{"aggs":{"product":[]}}`))
	accepted := easmMetricPanel(t, e, source)
	var state struct {
		Scope      easmQueryScope `json:"query_scope"`
		Components []any
	}
	_ = json.Unmarshal(accepted.State, &state)
	if len(state.Components) != 0 || state.Scope.ComponentsServerID != "source-b" {
		t.Fatal("a valid empty provider response retained older buckets or lost its provenance")
	}
	older(easmMetricResult(`{"aggs":{"product":[{"name":7,"count":99}]}}`))
	if saved := easmMetricPanel(t, e, source); saved.Revision != accepted.Revision || !bytes.Equal(saved.State, accepted.State) {
		t.Fatal("a late old-provider response replaced the observed empty result")
	}
	source.ToolCallID = "same-provider-empty"
	newer = e.bindEASMMetricCall(t.Context(), source, call, "components", "range-a")
	newer(easmMetricResult(`{"aggs":{"product":[]}}`))
	if saved := easmMetricPanel(t, e, source); saved.Revision != accepted.Revision {
		t.Fatal("repeating a valid equal empty observation added a revision")
	}
}
