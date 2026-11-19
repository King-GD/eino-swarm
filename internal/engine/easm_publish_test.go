package engine

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/LubyRuffy/eino-swarm/frontend"
	"github.com/LubyRuffy/eino-swarm/internal/store"
	"github.com/LubyRuffy/eino-swarm/internal/taskpanel"
)

const easmPublicationState = `{"schema_version":1,"title":"Fixture subject","query_scope":{"id":"scope-a","query":"domain==\"fixture.example.test\""},"nodes":[{"id":"root","kind":"root","label":"Fixture subject"}],"edges":[],"heat":[{"date":"2026-01-01","value":11}],"components":[{"name":"z-source","count":3},{"name":"a-source","count":2}],"summary":{"clues":1,"fofa_assets":9007199254740993,"fofa_ips":7}}`

func TestEASMAgentPublicationPreservesSourceValuesAcrossUpdateForms(t *testing.T) {
	var definitions []taskpanel.Definition
	if err := json.Unmarshal(frontend.TaskPanelCatalog(), &definitions); err != nil {
		t.Fatal(err)
	}
	var definition taskpanel.Definition
	for _, candidate := range definitions {
		if candidate.ID == easmPanelID {
			definition = candidate
		}
	}
	for _, test := range []struct {
		name       string
		operations string
		snapshot   string
		allowed    bool
	}{
		{"change heat", `[{"op":"replace","path":"/heat/0/value","value":12}]`, "", false},
		{"drop heat", `[{"op":"replace","path":"/heat","value":[]}]`, "", false},
		{"change component count", `[{"op":"replace","path":"/components/0/count","value":4}]`, "", false},
		{"change component identity", `[{"op":"replace","path":"/components/0/name","value":"different-source"}]`, "", false},
		{"drop components", `[{"op":"replace","path":"/components","value":[]}]`, "", false},
		{"large integer exactness", `[{"op":"replace","path":"/summary/fofa_assets","value":9007199254740992}]`, "", false},
		{"drop IP count", `[{"op":"remove","path":"/summary/fofa_ips"}]`, "", false},
		{"copy changes a source value", `[{"op":"copy","from":"/components/0/count","path":"/heat/0/value"}]`, "", false},
		{"move removes a source value", `[{"op":"move","from":"/summary/fofa_ips","path":"/summary/clues"}]`, "", false},
		{"drop scope", `[{"op":"remove","path":"/query_scope"}]`, "", false},
		{"change scope identity", `[{"op":"replace","path":"/query_scope/id","value":"scope-b"}]`, "", false},
		{"change scope query", `[{"op":"replace","path":"/query_scope/query","value":"domain==\"other.example.test\""}]`, "", false},
		{"snapshot without scope", "", strings.Replace(easmPublicationState, `"query_scope":{"id":"scope-a","query":"domain==\"fixture.example.test\""},`, "", 1), false},
		{"snapshot changes metrics", "", strings.Replace(easmPublicationState, `"value":11`, `"value":12`, 1), false},
		{"same snapshot", "", easmPublicationState, true},
		{"equivalent number spelling", `[{"op":"replace","path":"/heat/0/value","value":1.1e1},{"op":"replace","path":"/components/0/count","value":3.000},{"op":"replace","path":"/summary/fofa_ips","value":7e0}]`, "", true},
		{"component ordering and labels", `[{"op":"add","path":"/components/0/display_name","value":"Observed display name"},{"op":"move","from":"/components/0","path":"/components/1"}]`, "", true},
		{"graph evidence", `[{"op":"add","path":"/nodes/0/detail","value":{"reason":"Additional evidence"}},{"op":"replace","path":"/summary/clues","value":2}]`, "", true},
		{"invalid update fails validation", `[{"op":"replace","path":"/heat/0/value","value":-1}]`, "", false},
		{"invalid patch path", `[{"op":"replace","path":"/missing/value","value":1}]`, "", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			panel := taskpanel.Panel{ID: "panel", Revision: 3, Definition: definition, State: json.RawMessage(easmPublicationState)}
			before := append([]byte(nil), panel.State...)
			update := taskpanel.Update{ProtocolVersion: 1, PanelID: panel.ID, BaseRevision: panel.Revision, OperationID: "source-value-check", Kind: "patch", Operations: json.RawMessage(test.operations)}
			if test.snapshot != "" {
				update.Kind, update.State, update.Operations = "snapshot", json.RawMessage(test.snapshot), nil
			}
			err := easmValidateAgentPublication(panel, update)
			if (err == nil) != test.allowed {
				t.Fatalf("allowed=%v error=%v", test.allowed, err)
			}
			if !reflect.DeepEqual(before, []byte(panel.State)) {
				t.Fatal("validation mutated current source state")
			}
		})
	}

	legacy := taskpanel.Panel{ID: "panel", Revision: 3, Definition: definition, State: json.RawMessage(strings.Replace(easmPublicationState, `"query_scope":{"id":"scope-a","query":"domain==\"fixture.example.test\""},`, "", 1))}
	update := taskpanel.Update{ProtocolVersion: 1, PanelID: legacy.ID, BaseRevision: legacy.Revision, OperationID: "legacy-evidence", Kind: "patch", Operations: json.RawMessage(`[{"op":"replace","path":"/heat/0/value","value":12}]`)}
	if err := easmValidateAgentPublication(legacy, update); err != nil {
		t.Fatalf("legacy publication lost compatibility: %v", err)
	}
	update.Operations = json.RawMessage(`[{"op":"add","path":"/query_scope","value":{"id":"invented","query":"domain==\"fixture.example.test\""}}]`)
	if err := easmValidateAgentPublication(legacy, update); err == nil {
		t.Fatal("an ordinary publication invented a host query identity")
	}
}

func TestEASMSourceValueProjectionRejectsMalformedDataWithoutRounding(t *testing.T) {
	for _, raw := range []string{
		`{`,
		`{"query_scope":{},"heat":[{"value":null}]}`,
		`{"query_scope":{},"components":[{"count":true}]}`,
		`{"query_scope":{},"summary":{"fofa_assets":{}}}`,
	} {
		if _, err := easmPublicationValues(json.RawMessage(raw)); err == nil {
			t.Errorf("accepted malformed projection %s", raw)
		}
	}
	for _, test := range []struct{ raw, expected string }{
		{"", ""}, {"0", "0"}, {"3.000", "3"}, {"7e0", "7"}, {"9007199254740993", "9007199254740993"},
	} {
		value, err := easmPublicationNumber(json.RawMessage(test.raw))
		if err != nil || value != test.expected {
			t.Errorf("number %q = %q, %v", test.raw, value, err)
		}
	}
	left, err := easmPublicationValues(json.RawMessage(`{"query_scope":{},"components":[{"name":"same","count":3},{"name":"same","count":2}]}`))
	if err != nil {
		t.Fatal(err)
	}
	right, err := easmPublicationValues(json.RawMessage(`{"query_scope":{},"components":[{"name":"same","count":2},{"name":"same","count":3}]}`))
	if err != nil || !reflect.DeepEqual(left, right) {
		t.Errorf("equivalent duplicate identities changed source values: %v", err)
	}
	// Pinned older contracts can be less restrictive than the current catalog.
	// Guard projection must still refuse malformed metric values in that case.
	permissive := taskpanel.Panel{ID: "panel", Definition: taskpanel.Definition{Schema: json.RawMessage(`{"type":"object"}`)}, State: json.RawMessage(`{"query_scope":{},"heat":[{"value":null}]}`)}
	update := taskpanel.Update{ProtocolVersion: 1, PanelID: "panel", OperationID: "malformed-source", Kind: "snapshot", State: json.RawMessage(`{"query_scope":{}}`)}
	if err := easmValidateAgentPublication(permissive, update); err == nil {
		t.Fatal("malformed stored metrics passed publication protection")
	}
	permissive.State, update.State = update.State, permissive.State
	if err := easmValidateAgentPublication(permissive, update); err == nil {
		t.Fatal("malformed candidate metrics passed publication protection")
	}
}

// The model can add evidence without replacing a metric already observed by Go.
// Accepted retries still return their original receipt after a later host update.
func TestEASMAgentCannotOverwriteObservedMetricsAndCanRetryEvidence(t *testing.T) {
	var e *Engine
	var threadID, panelID string
	var observed, accepted taskpanel.Panel
	var enrichment string
	read := func() taskpanel.Panel {
		panel, err := e.store.TaskPanel(t.Context(), threadID, panelID)
		if err != nil {
			t.Fatal(err)
		}
		return panel
	}
	e, _ = newScheduleCompletionHTTPEngine(t, func(w http.ResponseWriter, _ *http.Request, step int, request scheduleCompletionRequest) {
		switch step {
		case 1:
			panelAdmissionCall(w, request, "guard-open", "open_task_panel", `{"plugin_id":"easm-asset-portrait","subject":"Fixture subject","query":"domain==\"fixture.example.test\""}`)
		case 2:
			var opened taskpanel.Panel
			if err := json.Unmarshal([]byte(nuwaValidationReceipt(request, "guard-open")), &opened); err != nil || opened.ID == "" {
				t.Fatalf("open receipt: %v", err)
			}
			panelID = opened.ID
			current := read()
			var err error
			observed, err = e.store.PublishTaskPanel(t.Context(), current.Source, taskpanel.Update{
				ProtocolVersion: 1, PanelID: current.ID, BaseRevision: current.Revision, OperationID: "guard-observed", Kind: "patch",
				Operations: json.RawMessage(`[{"op":"add","path":"/summary","value":{"fofa_assets":9007199254740993,"fofa_ips":7}},{"op":"replace","path":"/heat","value":[{"date":"2026-01-01","value":11}]},{"op":"replace","path":"/components","value":[{"name":"source-id","count":3}]}]`),
			}, taskpanel.Limits{})
			if err != nil {
				t.Fatal(err)
			}
			panelAdmissionCall(w, request, "guard-overwrite", "publish_panel_update", fmt.Sprintf(`{"protocol_version":1,"panel_id":%q,"operation_id":"guard-overwrite","base_revision":%d,"kind":"patch","operations":[{"op":"replace","path":"/heat","value":[]}]}`, panelID, observed.Revision))
		case 3:
			if !strings.Contains(nuwaValidationReceipt(request, "guard-overwrite"), "Go") || read().Revision != observed.Revision {
				t.Error("a model publication replaced the host's observed metric")
			}
			enrichment = fmt.Sprintf(`{"protocol_version":1,"panel_id":%q,"operation_id":"guard-evidence","base_revision":%d,"kind":"patch","operations":[{"op":"add","path":"/nodes/0/detail","value":{"reason":"Additional source evidence"}},{"op":"add","path":"/components/0/display_name","value":"Sourced label"},{"op":"add","path":"/components/0/hot_days","value":2},{"op":"add","path":"/components/0/tags","value":[{"type":"hot","summary":"Source-supported label"}]},{"op":"add","path":"/summary/clues","value":1}]}`, panelID, observed.Revision)
			panelAdmissionCall(w, request, "guard-enrich", "publish_panel_update", enrichment)
		case 4:
			if err := json.Unmarshal([]byte(nuwaValidationReceipt(request, "guard-enrich")), &accepted); err != nil || accepted.Revision != observed.Revision+1 {
				t.Errorf("evidence enrichment rejected: %v receipt=%s", err, nuwaValidationReceipt(request, "guard-enrich"))
			}
			current := read()
			if _, err := e.store.PublishTaskPanel(t.Context(), current.Source, taskpanel.Update{
				ProtocolVersion: 1, PanelID: current.ID, BaseRevision: current.Revision, OperationID: "guard-later-host", Kind: "patch",
				Operations: json.RawMessage(`[{"op":"replace","path":"/heat/0/value","value":12}]`),
			}, taskpanel.Limits{}); err != nil {
				t.Fatal(err)
			}
			panelAdmissionCall(w, request, "guard-retry", "publish_panel_update", enrichment)
		case 5:
			var retry taskpanel.Panel
			if err := json.Unmarshal([]byte(nuwaValidationReceipt(request, "guard-retry")), &retry); err != nil || retry.Revision != accepted.Revision {
				t.Errorf("accepted retry lost its original receipt: %v %s", err, nuwaValidationReceipt(request, "guard-retry"))
			}
			current := read()
			if current.Revision != accepted.Revision+1 || !strings.Contains(string(current.State), `"value":12`) {
				t.Errorf("retry overwrote later source data: %s", current.State)
			}
			panelAdmissionCall(w, request, "guard-missing", "publish_panel_update", `{"protocol_version":1,"panel_id":"missing-panel","operation_id":"missing-panel","base_revision":1,"kind":"patch","operations":[]}`)
		default:
			if !strings.Contains(nuwaValidationReceipt(request, "guard-missing"), "not found") || read().Revision != accepted.Revision+1 {
				t.Error("missing panel publication changed current observations")
			}
			writeContextFlowReply(w, request.Stream, map[string]any{"content": "The current evidence and metrics are available."}, "stop")
		}
	})
	e.cfg.Memory.Enabled, e.cfg.Memory.AutoReview, e.cfg.Swarm.ComposerPredictions = false, false, false
	thread, err := e.CreateThread("Observed source values", "", "")
	if err != nil {
		t.Fatal(err)
	}
	threadID = thread.ID
	turn, err := e.StartTurn(thread.ID, "Keep the collected source values while adding evidence.")
	if err != nil {
		t.Fatal(err)
	}
	if got := waitForTurn(t, e, turn.ID); got.Status != store.TurnDone {
		t.Fatalf("publication guard failed: %s", got.Error)
	}
	waitSettled(t, e, thread.ID)
}
