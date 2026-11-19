package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/LubyRuffy/eino-swarm/internal/config"
	"github.com/LubyRuffy/eino-swarm/internal/mcpclient"
	"github.com/LubyRuffy/eino-swarm/internal/store"
	"github.com/LubyRuffy/eino-swarm/internal/taskpanel"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestEASMMetricRequestsSelectOnlySupportedSourceFields(t *testing.T) {
	for _, test := range []struct {
		name, args, section, query string
	}{
		{"mcp_fofa_asset_heat", `{"query":"range"}`, "heat", "range"},
		{"mcp_fofa_rule_lookup", `{"rule_ids":[1]}`, "labels", ""},
		{"mcp_fofa_stats", `{"tool":"fofa","fields":"product1,product5","query":"range"}`, "components", "range"},
		{"mcp_fofa_stats", `{"tool":"fofa","fields":"product5, product1","query":"range"}`, "components", "range"},
		{"mcp_fofa_stats", `{"tool":"fofa","fields":"ip","uniq_count":"ip","query":"range"}`, "summary", "range"},
		{"mcp_fofa_stats", `{"fields":"product1,product5"}`, "", ""},
		{"mcp_fofa_stats", `{"tool":"other","fields":"product1,product5"}`, "", ""},
		{"mcp_fofa_stats", `{"tool":"fofa","fields":"product1,product1"}`, "", ""},
		{"mcp_fofa_stats", `{"tool":"fofa","fields":"product1,product5,product2"}`, "", ""},
		{"mcp_fofa_stats", `{"tool":"fofa","fields":"product1,,product5"}`, "", ""},
		{"mcp_fofa_stats", `{"tool":"fofa","fields":"ip"}`, "", ""},
		{"mcp_fofa_stats", `{`, "", ""},
		{"unrelated", `{}`, "", ""},
	} {
		section, query := easmMetricRequest(mcpclient.ToolCall{Name: test.name, Arguments: json.RawMessage(test.args)})
		if section != test.section || query != test.query {
			t.Errorf("%s %s: got %q/%q, want %q/%q", test.name, test.args, section, query, test.section, test.query)
		}
	}
}

func easmObservedMetricFixture(t *testing.T) (*Engine, taskpanel.Source, taskpanel.Panel) {
	t.Helper()
	e := newTestEngine(t)
	thread, err := e.CreateThread("Observed metric ranges", "", "")
	if err != nil {
		t.Fatal(err)
	}
	turn := &store.Turn{ID: store.NewID("tn_"), ThreadID: thread.ID, Status: store.TurnRunning}
	if err := e.store.CreateTurn(turn); err != nil {
		t.Fatal(err)
	}
	source := taskpanel.Source{ThreadID: thread.ID, TurnID: turn.ID, AgentID: "manager", ToolCallID: "select-a"}
	definitions, err := e.TaskPanelCatalog()
	if err != nil {
		t.Fatal(err)
	}
	var definition taskpanel.Definition
	for _, candidate := range definitions {
		if candidate.ID == easmPanelID {
			definition = candidate
		}
	}
	panel, err := e.openEASMWithSubject(t.Context(), definition, source, "Fixture entity")
	if err != nil {
		t.Fatal(err)
	}
	panel, err = e.selectEASMQueryLocked(t.Context(), panel, source, "range-a")
	if err != nil {
		t.Fatal(err)
	}
	return e, source, panel
}

func easmMetricCallback(t *testing.T, e *Engine, source taskpanel.Source, id, name, args string) func(*mcp.CallToolResult) {
	t.Helper()
	source.ToolCallID = id
	call := mcpclient.ToolCall{ServerID: "source-a", Name: name, CallID: id, Arguments: json.RawMessage(args)}
	section, query := easmMetricRequest(call)
	complete := e.bindEASMMetricCall(t.Context(), source, call, section, query)
	if complete == nil {
		t.Fatalf("could not bind %s", id)
	}
	return complete
}

func easmMetricResult(body string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: body}}}
}

func easmMetricPanel(t *testing.T, e *Engine, source taskpanel.Source) taskpanel.Panel {
	t.Helper()
	panel, err := e.store.TaskPanelByPlugin(t.Context(), source.ThreadID, easmPanelID)
	if err != nil {
		t.Fatal(err)
	}
	return panel
}

// A later request must not withhold a batch that is already ready. Only a
// successfully published newer result can supersede an older response.
func TestEASMReadyBatchesAppearWhileLaterRequestsArePending(t *testing.T) {
	e, source, _ := easmObservedMetricFixture(t)
	first := easmMetricCallback(t, e, source, "heat-first", "mcp_fofa_asset_heat", `{"query":"range-a"}`)
	second := easmMetricCallback(t, e, source, "heat-second", "mcp_fofa_asset_heat", `{"query":"range-a"}`)
	first(easmMetricResult(`{"hot_trend":[{"date":"2026-01-01","value":"1"}]}`))
	panel := easmMetricPanel(t, e, source)
	if !bytes.Contains(panel.State, []byte(`"value":1`)) {
		t.Fatal("ready first batch waited for the later request")
	}
	second(easmMetricResult(`{"hot_trend":[{"date":"2026-01-01","value":"2"}]}`))
	newer := easmMetricPanel(t, e, source)
	first(easmMetricResult(`{"hot_trend":[{"date":"2026-01-01","value":"1"}]}`))
	if current := easmMetricPanel(t, e, source); current.Revision != newer.Revision || !bytes.Equal(current.State, newer.State) {
		t.Fatal("an old response replaced a newer published batch")
	}
	invalid := easmMetricCallback(t, e, source, "heat-invalid", "mcp_fofa_asset_heat", `{"query":"range-a"}`)
	invalid(easmMetricResult(`{"hot_trend":"invalid"}`))
	if current := easmMetricPanel(t, e, source); current.Revision != newer.Revision {
		t.Fatal("invalid source data changed the panel")
	}
}

func TestEASMLateResponsesCannotReenterAReturningQuery(t *testing.T) {
	e, source, panel := easmObservedMetricFixture(t)
	first := easmMetricCallback(t, e, source, "old-a", "mcp_fofa_asset_heat", `{"query":"range-a"}`)
	source.ToolCallID = "select-b"
	panel, err := e.selectEASMQueryLocked(t.Context(), panel, source, "range-b")
	if err != nil {
		t.Fatal(err)
	}
	second := easmMetricCallback(t, e, source, "old-b", "mcp_fofa_asset_heat", `{"query":"range-b"}`)
	source.ToolCallID = "return-a"
	panel, err = e.selectEASMQueryLocked(t.Context(), panel, source, "range-a")
	if err != nil {
		t.Fatal(err)
	}
	// No new request is required to fence an earlier visit.
	first(easmMetricResult(`{"hot_trend":[{"date":"2026-01-01","value":90}]}`))
	if current := easmMetricPanel(t, e, source); current.Revision != panel.Revision {
		t.Fatal("old range returned before a new request and changed the panel")
	}
	current := easmMetricCallback(t, e, source, "new-a", "mcp_fofa_asset_heat", `{"query":"range-a"}`)
	second(easmMetricResult(`{"hot_trend":[{"date":"2026-01-01","value":80}]}`))
	current(easmMetricResult(`{"hot_trend":[{"date":"2026-01-01","value":2}]}`))
	if saved := easmMetricPanel(t, e, source); saved.Source.ToolCallID != "new-a" || !bytes.Contains(saved.State, []byte(`"value":2`)) {
		t.Fatal("current range did not retain its own response")
	}
}

func TestEASMProductNamesStayWithTheirActualSource(t *testing.T) {
	e, source, _ := easmObservedMetricFixture(t)
	products := easmMetricCallback(t, e, source, "products", "mcp_fofa_stats", `{"query":"range-a","tool":"fofa","fields":"product1,product5"}`)
	products(easmMetricResult(`{"aggs":{"product":[{"name":7,"count":4}]}}`))
	labels := easmMetricCallback(t, e, source, "labels", "mcp_fofa_rule_lookup", `{"rule_ids":[7]}`)
	source.ToolCallID = "foreign-labels"
	foreign := mcpclient.ToolCall{ServerID: "source-b", Name: "mcp_fofa_rule_lookup", Arguments: json.RawMessage(`{"rule_ids":[7]}`)}
	if e.bindEASMMetricCall(t.Context(), source, foreign, "labels", "") != nil {
		t.Fatal("a different source acquired another source's product IDs")
	}
	labels(easmMetricResult(`{"results":[{"rule_id":7,"product":"Fixture product"}]}`))
	summary := easmMetricCallback(t, e, source, "summary", "mcp_fofa_stats", `{"query":"range-a","tool":"fofa","fields":"ip","uniq_count":"ip"}`)
	summary(easmMetricResult(`{"size":17,"distinct":{"ip":3}}`))
	panel := easmMetricPanel(t, e, source)
	if !bytes.Contains(panel.State, []byte(`"display_name":"Fixture product"`)) || !bytes.Contains(panel.State, []byte(`"fofa_assets":17`)) {
		t.Fatal("source names or independent summary did not publish")
	}
	// A response already bound to the old component provider must also be
	// refused if a new provider publishes the current bucket set in between.
	late := easmMetricCallback(t, e, source, "late-label", "mcp_fofa_rule_lookup", `{"rule_ids":[7]}`)
	source.ToolCallID = "new-provider"
	foreign.Name, foreign.Arguments = "mcp_fofa_stats", json.RawMessage(`{"query":"range-a","tool":"fofa","fields":"product1,product5"}`)
	complete := e.bindEASMMetricCall(t.Context(), source, foreign, "components", "range-a")
	complete(easmMetricResult(`{"aggs":{"product":[{"name":7,"count":4}]}}`))
	newer := easmMetricPanel(t, e, source)
	if bytes.Contains(newer.State, []byte(`"display_name"`)) {
		t.Fatal("new source's product IDs inherited another source's names")
	}
	late(easmMetricResult(`{"results":[{"rule_id":7,"product":"Stale label"}]}`))
	if saved := easmMetricPanel(t, e, source); saved.Revision != newer.Revision {
		t.Fatal("late rule metadata crossed source providers")
	}
}

func TestEASMMetricCallbacksRespectOwnershipCancellationAndPublicationFailure(t *testing.T) {
	e, source, panel := easmObservedMetricFixture(t)
	heat := mcpclient.ToolCall{ServerID: "source-a", Name: "mcp_fofa_asset_heat", Arguments: json.RawMessage(`{"query":"range-a"}`)}
	if e.beginEASMToolCall(t.Context(), heat) != nil || e.beginEASMToolCall(withHistoryReadOwner(t.Context(), source.ThreadID, source.TurnID), heat) != nil {
		t.Fatal("metric call acquired an untrusted runtime owner")
	}
	if e.beginEASMToolCall(t.Context(), mcpclient.ToolCall{Name: "unrelated"}) != nil {
		t.Fatal("unrelated tool acquired a metric callback")
	}
	missing := source
	missing.ThreadID = "missing"
	if e.bindEASMMetricCall(t.Context(), missing, heat, "heat", "range-a") != nil || e.bindEASMMetricCall(t.Context(), source, heat, "heat", " \n ") != nil {
		t.Fatal("unowned or empty range acquired a callback")
	}
	blank := heat
	blank.ServerID = ""
	if e.bindEASMMetricCall(t.Context(), source, blank, "heat", "range-a") != nil {
		t.Fatal("missing configured source acquired a callback")
	}
	old := source
	old.TurnID = "earlier-turn"
	if e.bindEASMMetricCall(t.Context(), old, heat, "heat", "range-a") != nil {
		t.Fatal("an earlier owner acquired this turn's range")
	}
	if e.bindEASMMetricCall(t.Context(), source, heat, "labels", "") != nil {
		t.Fatal("labels acquired a scope before component observations")
	}
	complete := easmMetricCallback(t, e, source, "guarded-heat", heat.Name, string(heat.Arguments))
	complete(nil)
	complete(&mcp.CallToolResult{IsError: true})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	e.completeEASMMetricCall(ctx, panel.ID, source, heat, "scope", "heat", 1, easmMetricResult(`{}`))
	if current := easmMetricPanel(t, e, source); current.Revision != panel.Revision {
		t.Fatal("failed or canceled observations mutated the panel")
	}
	var logs bytes.Buffer
	e.log = slog.New(slog.NewTextHandler(&logs, nil))
	if err := e.cfg.Update(func(cfg *config.Config) error { cfg.TaskPanels.Enabled = new(bool); return nil }); err != nil {
		t.Fatal(err)
	}
	complete(easmMetricResult(`{"hot_trend":[{"date":"2026-01-01","value":2}]}`))
	if current := easmMetricPanel(t, e, source); current.Revision != panel.Revision || !bytes.Contains(logs.Bytes(), []byte("could not apply EASM metric update")) {
		t.Fatal("failed publication changed accepted data or lost its warning")
	}
}
