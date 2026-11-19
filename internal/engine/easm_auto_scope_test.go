package engine

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/LubyRuffy/eino-swarm/internal/mcpclient"
	"github.com/LubyRuffy/eino-swarm/internal/store"
	"github.com/LubyRuffy/eino-swarm/internal/taskpanel"
)

func TestEASMEachDedicatedMetricCanEstablishItsOwnRange(t *testing.T) {
	for _, test := range []struct{ name, args, body, section string }{
		{"mcp_fofa_asset_heat", `{"query":"first range"}`, `{"hot_trend":[{"date":"2026-01-01","value":4}]}`, "heat"},
		{"mcp_fofa_stats", `{"query":"first range","tool":"fofa","fields":"product1,product5"}`, `{"aggs":{"product":[{"name":7,"count":4}]}}`, "components"},
		{"mcp_fofa_stats", `{"query":"first range","tool":"fofa","fields":"ip","uniq_count":"ip"}`, `{"size":17,"distinct":{"ip":3}}`, "summary"},
	} {
		t.Run(test.section, func(t *testing.T) {
			e, source, panel := easmObservedMetricFixture(t)
			panel, err := e.store.PublishTaskPanel(t.Context(), source, taskpanel.Update{
				ProtocolVersion: 1, PanelID: panel.ID, BaseRevision: panel.Revision,
				OperationID: "unselected-fixture", Kind: "patch", Operations: json.RawMessage(`[{"op":"remove","path":"/query_scope"}]`),
			}, taskpanel.Limits{})
			if err != nil {
				t.Fatal(err)
			}
			labels := mcpclient.ToolCall{ServerID: "source-a", Name: "mcp_fofa_rule_lookup", Arguments: json.RawMessage(`{"rule_ids":[7]}`)}
			if e.bindEASMMetricCall(t.Context(), source, labels, "labels", "") != nil {
				t.Fatal("rule metadata selected a range without metric evidence")
			}
			if saved := easmMetricPanel(t, e, source); saved.Revision != panel.Revision {
				t.Fatal("rule lookup changed the unselected portrait")
			}
			complete := easmMetricCallback(t, e, source, "automatic-first", test.name, test.args)
			selected := easmMetricPanel(t, e, source)
			scope, ok := easmReadQueryScope(selected.State)
			if !ok || scope.Query != "first range" || selected.Revision != panel.Revision+1 {
				t.Fatal("the first dedicated request still requires explicit model setup")
			}
			complete(easmMetricResult(test.body))
			published := easmMetricPanel(t, e, source)
			var state map[string]json.RawMessage
			_ = json.Unmarshal(published.State, &state)
			if published.Revision != selected.Revision+1 || bytes.Equal(state[test.section], []byte(`[]`)) || bytes.Equal(state[test.section], []byte(`{}`)) {
				t.Fatalf("first metric was not independently published: %s", published.State)
			}
		})
	}
}

func TestEASMAutomaticReturningRangesRefuseLateResultsFromEarlierVisits(t *testing.T) {
	e, source, initial := easmObservedMetricFixture(t)
	first := easmMetricCallback(t, e, source, "initial-a", "mcp_fofa_asset_heat", `{"query":"range-a"}`)
	oldScope, _ := easmReadQueryScope(initial.State)
	second := easmMetricCallback(t, e, source, "automatic-b", "mcp_fofa_asset_heat", `{"query":"range-b"}`)
	current := easmMetricCallback(t, e, source, "returning-a", "mcp_fofa_stats", `{"query":"range-a","tool":"fofa","fields":"ip","uniq_count":"ip"}`)
	selected := easmMetricPanel(t, e, source)
	scope, ok := easmReadQueryScope(selected.State)
	if !ok || scope.Query != oldScope.Query || scope.ID == oldScope.ID {
		t.Fatal("automatic return reused the earlier visit's identity")
	}
	first(easmMetricResult(`{"hot_trend":[{"date":"2026-01-01","value":90}]}`))
	second(easmMetricResult(`{"hot_trend":[{"date":"2026-01-01","value":80}]}`))
	if saved := easmMetricPanel(t, e, source); saved.Revision != selected.Revision || !bytes.Equal(saved.State, selected.State) {
		t.Fatal("a delayed request from another visit reselected or repainted the range")
	}
	current(easmMetricResult(`{"size":17,"distinct":{"ip":3}}`))
	if saved := easmMetricPanel(t, e, source); saved.Source.ToolCallID != "returning-a" || !bytes.Contains(saved.State, []byte(`"fofa_assets":17`)) {
		t.Fatal("the current automatically selected range did not publish its result")
	}
}

func TestEASMAutomaticRangeSelectionDoesNotMigrateHistoricalPanels(t *testing.T) {
	e, _, original := easmObservedMetricFixture(t)
	thread, err := e.CreateThread("Pinned metric compatibility", "", "")
	if err != nil {
		t.Fatal(err)
	}
	turn := &store.Turn{ID: store.NewID("tn_"), ThreadID: thread.ID, Status: store.TurnRunning}
	if err := e.store.CreateTurn(turn); err != nil {
		t.Fatal(err)
	}
	definition := original.Definition
	var schema map[string]any
	_ = json.Unmarshal(definition.Schema, &schema)
	delete(schema["properties"].(map[string]any), "query_scope")
	definition.Schema, _ = json.Marshal(schema)
	source := taskpanel.Source{ThreadID: thread.ID, TurnID: turn.ID, AgentID: "manager", ToolCallID: "old-schema-request"}
	panel, err := e.store.OpenTaskPanel(t.Context(), definition, source)
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	e.log = slog.New(slog.NewTextHandler(&logs, nil))
	call := mcpclient.ToolCall{ServerID: "source-a", Name: "mcp_fofa_asset_heat", Arguments: json.RawMessage(`{"query":"current range"}`)}
	if e.bindEASMMetricCall(t.Context(), source, call, "heat", "current range") != nil {
		t.Fatal("an old pinned schema accepted a new range by migration")
	}
	saved := easmMetricPanel(t, e, source)
	if saved.Revision != panel.Revision || !bytes.Equal(saved.State, panel.State) || !bytes.Equal(saved.Definition.Schema, panel.Definition.Schema) || !bytes.Contains(logs.Bytes(), []byte("could not select EASM metric range")) {
		t.Fatal("automatic selection rewrote historical state or hid its incompatibility")
	}
}
