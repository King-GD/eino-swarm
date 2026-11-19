package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/LubyRuffy/eino-swarm/internal/store"
	"github.com/LubyRuffy/eino-swarm/internal/taskpanel"
)

// Scope changes must clear the previous query's metrics in the same publication
// while retaining graph evidence collected before the range was selected.
func TestEASMQuerySelectionKeepsEvidenceAndFencesReturningRanges(t *testing.T) {
	var e *Engine
	var threadID, panelID, firstID, secondID string
	var unchangedRevision int64
	read := func() taskpanel.Panel {
		panel, err := e.store.TaskPanel(t.Context(), threadID, panelID)
		if err != nil {
			t.Fatal(err)
		}
		return panel
	}
	check := func(query string, empty bool) taskpanel.Panel {
		panel := read()
		var state struct {
			Scope            struct{ ID, Query string } `json:"query_scope"`
			Nodes            []struct{ ID string }
			Edges            []any
			Summary          map[string]any
			Heat, Components []any
		}
		if err := json.Unmarshal(panel.State, &state); err != nil {
			t.Fatal(err)
		}
		if state.Scope.ID == "" || state.Scope.Query != query || len(state.Nodes) != 2 || len(state.Edges) != 1 || state.Nodes[1].ID != "original-domain" {
			t.Errorf("query selection erased evidence or omitted scope: %s", panel.State)
		}
		if empty && (len(state.Summary) != 0 || len(state.Heat) != 0 || len(state.Components) != 0) {
			t.Errorf("new query retained previous metrics: %s", panel.State)
		}
		return panel
	}
	e, _ = newScheduleCompletionHTTPEngine(t, func(w http.ResponseWriter, r *http.Request, step int, request scheduleCompletionRequest) {
		switch step {
		case 1:
			panelAdmissionCall(w, request, "scope-open", "open_task_panel", `{"plugin_id":"easm-asset-portrait","subject":"Fixture legal entity"}`)
		case 2:
			var opened taskpanel.Panel
			if err := json.Unmarshal([]byte(nuwaValidationReceipt(request, "scope-open")), &opened); err != nil || opened.ID == "" {
				t.Errorf("initial panel receipt: %v", err)
			}
			panelID = opened.ID
			state := `{"schema_version":1,"title":"Fixture legal entity","nodes":[{"id":"root","kind":"root","label":"Fixture legal entity"},{"id":"original-domain","kind":"domain","label":"evidence.example.test"}],"edges":[{"source":"root","target":"original-domain","confirmed":false}],"summary":{"clues":1,"fofa_assets":9},"heat":[{"date":"2026-01-01","value":9}],"components":[{"name":"original-product","count":3}]}`
			panelAdmissionCall(w, request, "scope-evidence", "publish_panel_update", fmt.Sprintf(`{"protocol_version":1,"panel_id":%q,"operation_id":"scope-evidence","base_revision":%d,"kind":"snapshot","state":%s}`, panelID, opened.Revision, state))
		case 3:
			var seeded taskpanel.Panel
			receipt := nuwaValidationReceipt(request, "scope-evidence")
			if err := json.Unmarshal([]byte(receipt), &seeded); err != nil || seeded.ID != panelID || seeded.Revision == 0 || seeded.Source.ToolCallID != "scope-evidence" {
				t.Errorf("initial evidence publication failed: %s", receipt)
				writeContextFlowReply(w, request.Stream, map[string]any{"content": "Fixture setup failed."}, "stop")
				return
			}
			panelAdmissionCall(w, request, "select-range-a", "open_task_panel", `{"plugin_id":"easm-asset-portrait","query":"domain==\"range-a.example.test\""}`)
		case 4:
			panel := check(`domain=="range-a.example.test"`, true)
			var state map[string]json.RawMessage
			_ = json.Unmarshal(panel.State, &state)
			var scope struct{ ID string }
			_ = json.Unmarshal(state["query_scope"], &scope)
			firstID = scope.ID
			observed, err := e.store.PublishTaskPanel(t.Context(), panel.Source, taskpanel.Update{
				ProtocolVersion: 1, PanelID: panel.ID, BaseRevision: panel.Revision, OperationID: "scope-source-metrics", Kind: "patch",
				Operations: json.RawMessage(`[{"op":"add","path":"/summary","value":{"fofa_assets":12}},{"op":"replace","path":"/heat","value":[{"date":"2026-01-02","value":12}]},{"op":"replace","path":"/components","value":[{"name":"observed-product","count":4}]}]`),
			}, taskpanel.Limits{})
			if err != nil {
				t.Error(err)
			}
			unchangedRevision = observed.Revision
			panelAdmissionCall(w, request, "repeat-range-a", "open_task_panel", `{"plugin_id":"easm-asset-portrait","query":"domain==\"range-a.example.test\""}`)
		case 5:
			panel := check(`domain=="range-a.example.test"`, false)
			var state struct {
				Scope            struct{ ID string } `json:"query_scope"`
				Heat, Components []any
			}
			_ = json.Unmarshal(panel.State, &state)
			if state.Scope.ID != firstID || panel.Revision != unchangedRevision || len(state.Heat) != 1 || len(state.Components) != 1 {
				t.Errorf("same query discarded observed metrics: %s", panel.State)
			}
			panelAdmissionCall(w, request, "select-range-b", "open_task_panel", `{"plugin_id":"easm-asset-portrait","query":"domain==\"range-b.example.test\""}`)
		case 6:
			panel := check(`domain=="range-b.example.test"`, true)
			var state struct {
				Scope struct{ ID string } `json:"query_scope"`
			}
			_ = json.Unmarshal(panel.State, &state)
			secondID = state.Scope.ID
			if secondID == firstID {
				t.Error("different query reused the prior scope identity")
			}
			panelAdmissionCall(w, request, "return-range-a", "open_task_panel", `{"plugin_id":"easm-asset-portrait","query":"domain==\"range-a.example.test\""}`)
		case 7:
			panel := check(`domain=="range-a.example.test"`, true)
			var state struct {
				Scope struct{ ID string } `json:"query_scope"`
			}
			_ = json.Unmarshal(panel.State, &state)
			if state.Scope.ID == firstID || state.Scope.ID == secondID {
				t.Error("returning range accepts a delayed result from its previous visit")
			}
			unchangedRevision = panel.Revision
			panelAdmissionCall(w, request, "empty-range", "open_task_panel", `{"plugin_id":"easm-asset-portrait","query":"  "}`)
		case 8:
			if !strings.Contains(nuwaValidationReceipt(request, "empty-range"), "query") || read().Revision != unchangedRevision {
				t.Error("empty query changed the active range")
			}
			panelAdmissionCall(w, request, "null-range", "open_task_panel", `{"plugin_id":"easm-asset-portrait","query":null}`)
		case 9:
			if !strings.Contains(nuwaValidationReceipt(request, "null-range"), "query") || read().Revision != unchangedRevision {
				t.Error("null query changed the active range")
			}
			panelAdmissionCall(w, request, "nonstring-range", "open_task_panel", `{"plugin_id":"easm-asset-portrait","query":true}`)
		case 10:
			if !strings.Contains(nuwaValidationReceipt(request, "nonstring-range"), "query") || read().Revision != unchangedRevision {
				t.Error("nonstring query changed the active range")
			}
			panelAdmissionCall(w, request, "unrelated-panel-query", "open_task_panel", `{"plugin_id":"fofa-world-map","query":"domain==\"range-a.example.test\""}`)
		default:
			if !strings.Contains(nuwaValidationReceipt(request, "unrelated-panel-query"), "EASM") || read().Revision != unchangedRevision {
				t.Error("a non-EASM query request bypassed validation")
			}
			panels, err := e.store.TaskPanels(t.Context(), threadID)
			if err != nil || len(panels) != 1 {
				t.Errorf("invalid query created an unrelated panel: %v", err)
			}
			writeContextFlowReply(w, request.Stream, map[string]any{"content": "Selected query observations are available."}, "stop")
		}
	})
	e.cfg.Memory.Enabled, e.cfg.Memory.AutoReview, e.cfg.Swarm.ComposerPredictions = false, false, false
	thread, err := e.CreateThread("Selected asset range", "", "")
	if err != nil {
		t.Fatal(err)
	}
	threadID = thread.ID
	turn, err := e.StartTurn(thread.ID, "Keep source evidence while selecting the current asset range.")
	if err != nil {
		t.Fatal(err)
	}
	if got := waitForTurn(t, e, turn.ID); got.Status != store.TurnDone {
		t.Fatalf("query selection failed: %s", got.Error)
	}
	waitSettled(t, e, thread.ID)
}

func TestEASMQueryScopeReadsOnlyCompleteSelections(t *testing.T) {
	for _, state := range []string{`{`, `null`, `{}`, `{"query_scope":{"id":"s"}}`, `{"query_scope":{"query":"value"}}`, `{"query_scope":{"id":"s","query":" \n "}}`} {
		if scope, ok := easmReadQueryScope(json.RawMessage(state)); ok || scope != (easmQueryScope{}) {
			t.Errorf("incomplete query scope accepted: %s", state)
		}
	}
	scope, ok := easmReadQueryScope(json.RawMessage(`{"query_scope":{"id":"selected","query":" domain==\"example.test\" "}}`))
	if !ok || scope.ID != "selected" || scope.Query != ` domain=="example.test" ` {
		t.Errorf("selected query bytes changed: %+v", scope)
	}
}

func TestEASMQueryOwnershipCannotBeTakenByAnOlderTurn(t *testing.T) {
	e := newTestEngine(t)
	thread, err := e.CreateThread("Scope ownership", "", "")
	if err != nil {
		t.Fatal(err)
	}
	first := &store.Turn{ID: store.NewID("tn_"), ThreadID: thread.ID, Status: store.TurnDone}
	second := &store.Turn{ID: store.NewID("tn_"), ThreadID: thread.ID, Status: store.TurnRunning}
	for _, turn := range []*store.Turn{first, second} {
		if err := e.store.CreateTurn(turn); err != nil {
			t.Fatal(err)
		}
	}
	definitions, err := e.TaskPanelCatalog()
	if err != nil {
		t.Fatal(err)
	}
	var definition taskpanel.Definition
	for _, d := range definitions {
		if d.ID == easmPanelID {
			definition = d
		}
	}
	source := taskpanel.Source{ThreadID: thread.ID, TurnID: first.ID, AgentID: "manager", ToolCallID: "original-range"}
	panel, err := e.store.OpenTaskPanel(t.Context(), definition, source)
	if err != nil {
		t.Fatal(err)
	}
	panel, err = e.selectEASMQueryLocked(t.Context(), panel, source, " original query ")
	if err != nil {
		t.Fatal(err)
	}
	original := panel
	current := source
	current.TurnID, current.ToolCallID = second.ID, "continue-range"
	panel, err = e.selectEASMQueryLocked(t.Context(), panel, current, " original query ")
	if err != nil || panel.Source.TurnID != second.ID || panel.Revision != original.Revision+1 || !scopeJSONEqual(panel.State, original.State) {
		t.Fatalf("newer turn did not retain selected query: revision=%d err=%v", panel.Revision, err)
	}
	for _, query := range []string{" original query ", "replacement query"} {
		if _, err := e.selectEASMQueryLocked(t.Context(), panel, source, query); !errors.Is(err, taskpanel.ErrConflict) {
			t.Errorf("older publisher changed query: %v", err)
		}
	}
	missingPrevious := panel
	missingPrevious.Source.TurnID = "missing"
	missingCurrent := current
	missingCurrent.TurnID = "missing"
	invalidState := panel
	invalidState.State = json.RawMessage(`{`)
	nullState := panel
	nullState.State = json.RawMessage(`null`)
	for _, test := range []struct {
		name   string
		panel  taskpanel.Panel
		source taskpanel.Source
		query  string
	}{
		{"non EASM", taskpanel.Panel{PluginID: "other"}, current, "query"},
		{"empty query", panel, current, " \t "},
		{"missing previous owner", missingPrevious, current, "query"},
		{"missing current owner", panel, missingCurrent, "query"},
		{"invalid state", invalidState, current, "query"},
		{"null state", nullState, current, "query"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := e.selectEASMQueryLocked(t.Context(), test.panel, test.source, test.query); err == nil {
				t.Error("invalid range selection succeeded")
			}
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := e.selectEASMQueryLocked(ctx, panel, current, "replacement query"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled query selection continued: %v", err)
	}
	saved, err := e.store.TaskPanel(t.Context(), thread.ID, panel.ID)
	if err != nil || saved.Revision != panel.Revision || !scopeJSONEqual(saved.State, panel.State) {
		t.Fatal("refused query selection changed the durable panel")
	}
}

func TestEASMQuerySelectionLeavesPinnedHistoricalSchemaUntouched(t *testing.T) {
	e := newTestEngine(t)
	thread, err := e.CreateThread("Historical scope compatibility", "", "")
	if err != nil {
		t.Fatal(err)
	}
	turn := &store.Turn{ID: store.NewID("tn_"), ThreadID: thread.ID, Status: store.TurnRunning}
	if err := e.store.CreateTurn(turn); err != nil {
		t.Fatal(err)
	}
	definitions, _ := e.TaskPanelCatalog()
	var old taskpanel.Definition
	for _, d := range definitions {
		if d.ID == easmPanelID {
			old = d
		}
	}
	var schema map[string]any
	_ = json.Unmarshal(old.Schema, &schema)
	delete(schema["properties"].(map[string]any), "query_scope")
	old.Schema, _ = json.Marshal(schema)
	source := taskpanel.Source{ThreadID: thread.ID, TurnID: turn.ID, AgentID: "manager", ToolCallID: "old-open"}
	panel, err := e.store.OpenTaskPanel(t.Context(), old, source)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.selectEASMQueryLocked(t.Context(), panel, source, "current query"); !errors.Is(err, taskpanel.ErrInvalid) || !strings.Contains(err.Error(), "fresh EASM panel") {
		t.Fatalf("historical schema did not produce actionable refusal: %v", err)
	}
	saved, err := e.store.TaskPanel(t.Context(), thread.ID, panel.ID)
	if err != nil || saved.Revision != panel.Revision || !scopeJSONEqual(saved.State, panel.State) || string(saved.Definition.Schema) != string(old.Schema) {
		t.Fatal("selecting a query migrated historical state")
	}
}

func scopeJSONEqual(a, b json.RawMessage) bool {
	var left, right any
	return json.Unmarshal(a, &left) == nil && json.Unmarshal(b, &right) == nil && reflect.DeepEqual(left, right)
}
