package engine

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/LubyRuffy/eino-swarm/internal/store"
	"github.com/LubyRuffy/eino-swarm/internal/taskpanel"
)

func TestEASMAgentRelationConfidenceCannotExceedItsSource(t *testing.T) {
	for _, pair := range [][2]string{{`{`, `{}`}, {`{}`, `{`}} {
		if err := easmValidateAgentConfidence(json.RawMessage(pair[0]), json.RawMessage(pair[1])); err == nil {
			t.Fatal("malformed relations were accepted")
		}
	}
	for _, before := range []string{"missing", "unset", "false", "true"} {
		for _, after := range []string{"removed", "unset", "false", "true"} {
			for _, kind := range []string{"patch", "snapshot"} {
				t.Run(before+"/"+after+"/"+kind, func(t *testing.T) {
					edge := func(value string) string {
						if value == "missing" || value == "removed" {
							return `[]`
						}
						flag := ""
						if value != "unset" {
							flag = `,"confirmed":` + value
						}
						return `[{"source":"root","target":"legal"` + flag + `}]`
					}
					state := `{"nodes":[{"id":"root","kind":"root","label":"Fixture"},{"id":"legal","kind":"company","label":"Fixture Legal Entity"}],"edges":` + edge(before) + `}`
					panel := taskpanel.Panel{Definition: taskpanel.Definition{Schema: json.RawMessage(`{"type":"object"}`)}, State: json.RawMessage(state)}
					update := taskpanel.Update{ProtocolVersion: 1, PanelID: "panel", OperationID: "confidence", Kind: kind}
					if kind == "patch" {
						update.Operations = json.RawMessage(`[{"op":"replace","path":"/edges","value":` + edge(after) + `}]`)
					} else {
						update.State = json.RawMessage(strings.Replace(state, edge(before)+`}`, edge(after)+`}`, 1))
					}
					allowed := after == "removed" || after == "false" || before == "unset" && after == "unset" || before == "true" && after == "true"
					if err := easmValidateAgentPublication(panel, update); (err == nil) != allowed {
						t.Fatalf("allowed=%v, got %v", allowed, err)
					}
					if string(panel.State) != state {
						t.Fatal("validation changed the source state")
					}
				})
			}
		}
	}
}

// Agent inference stays pending; source updates alone can establish confirmed
// relations. Original operation hashes still govern retries after source writes.
func TestEASMAgentConfidenceIsBoundedByReportedRelationsAtToolBoundary(t *testing.T) {
	invalid := []struct{ id, operations string }{
		{"new-confirmed", `[{"op":"add","path":"/edges/-","value":{"source":"legal","target":"icp","confirmed":true}}]`},
		{"new-implicit", `[{"op":"add","path":"/edges/-","value":{"source":"legal","target":"icp"}}]`},
		{"pending-promotion", `[{"op":"replace","path":"/edges/0/confirmed","value":true}]`},
		{"unknown-promotion", `[{"op":"add","path":"/edges/2/confirmed","value":true}]`},
		{"pending-flag-removal", `[{"op":"remove","path":"/edges/0/confirmed"}]`},
		{"reported-flag-removal", `[{"op":"remove","path":"/edges/1/confirmed"}]`},
	}
	var e *Engine
	var threadID, panelID, acceptedArgs string
	var observed, accepted taskpanel.Panel
	read := func() taskpanel.Panel {
		panel, err := e.store.TaskPanel(t.Context(), threadID, panelID)
		if err != nil {
			t.Fatal(err)
		}
		return panel
	}
	check := func(want ...string) {
		t.Helper()
		var state struct{ Edges []struct{ Confirmed *bool } }
		if err := json.Unmarshal(read().State, &state); err != nil || len(state.Edges) != len(want) {
			t.Fatalf("relations: %v count=%d", err, len(state.Edges))
		}
		for i, value := range want {
			got := "unset"
			if state.Edges[i].Confirmed != nil {
				got = fmt.Sprint(*state.Edges[i].Confirmed)
			}
			if got != value {
				t.Errorf("edge %d confirmed=%s, want %s", i, got, value)
			}
		}
	}
	call := func(w http.ResponseWriter, request scheduleCompletionRequest, id, operations string) string {
		args := fmt.Sprintf(`{"protocol_version":1,"panel_id":%q,"operation_id":%q,"base_revision":%d,"kind":"patch","operations":%s}`, panelID, id, read().Revision, operations)
		panelAdmissionCall(w, request, id, "publish_panel_update", args)
		return args
	}
	e, _ = newScheduleCompletionHTTPEngine(t, func(w http.ResponseWriter, _ *http.Request, step int, request scheduleCompletionRequest) {
		switch step {
		case 1:
			panelAdmissionCall(w, request, "confidence-open", "open_task_panel", `{"plugin_id":"easm-asset-portrait","subject":"Fixture subject"}`)
		case 2:
			var opened taskpanel.Panel
			if err := json.Unmarshal([]byte(nuwaValidationReceipt(request, "confidence-open")), &opened); err != nil || opened.ID == "" {
				t.Fatalf("open receipt: %v", err)
			}
			panelID = opened.ID
			current := read()
			e.recordMu.Lock()
			var err error
			observed, err = e.publishTaskPanelLocked(t.Context(), current.Source, taskpanel.Update{ProtocolVersion: 1, PanelID: panelID, OperationID: "source-confidence", BaseRevision: current.Revision, Kind: "patch", Operations: json.RawMessage(`[{"op":"replace","path":"/nodes","value":[{"id":"root","kind":"root","label":"Fixture subject"},{"id":"legal","kind":"company","label":"Fixture Legal Entity"},{"id":"domain-one","kind":"domain","label":"one.example.test"},{"id":"domain-two","kind":"domain","label":"two.example.test"},{"id":"icp","kind":"feature","featureKind":"icp","label":"Fixture ICP"}]},{"op":"replace","path":"/edges","value":[{"source":"root","target":"legal","confirmed":false},{"source":"legal","target":"domain-one","confirmed":true},{"source":"legal","target":"domain-two"}]}]`)})
			e.recordMu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			call(w, request, invalid[0].id, invalid[0].operations)
		default:
			completed := step - 3
			if completed < len(invalid) {
				receipt := nuwaValidationReceipt(request, invalid[completed].id)
				if !strings.Contains(receipt, "invalid input") || !strings.Contains(receipt, "pending") || read().Revision != observed.Revision {
					t.Errorf("%s changed source confidence: %s", invalid[completed].id, receipt)
				}
				if completed+1 < len(invalid) {
					call(w, request, invalid[completed+1].id, invalid[completed+1].operations)
				} else {
					acceptedArgs = call(w, request, "pending-evidence", `[{"op":"add","path":"/edges/-","value":{"source":"legal","target":"icp","confirmed":false}}]`)
				}
				return
			}
			switch completed - len(invalid) {
			case 0:
				if err := json.Unmarshal([]byte(nuwaValidationReceipt(request, "pending-evidence")), &accepted); err != nil || accepted.Revision != observed.Revision+1 {
					t.Fatalf("pending evidence was refused: %v", err)
				}
				check("false", "true", "unset", "false")
				current := read()
				e.recordMu.Lock()
				_, err := e.publishTaskPanelLocked(t.Context(), current.Source, taskpanel.Update{ProtocolVersion: 1, PanelID: panelID, OperationID: "source-upgrade", BaseRevision: current.Revision, Kind: "patch", Operations: json.RawMessage(`[{"op":"replace","path":"/edges/0/confirmed","value":true}]`)})
				e.recordMu.Unlock()
				if err != nil {
					t.Fatal(err)
				}
				check("true", "true", "unset", "false")
				panelAdmissionCall(w, request, "confidence-retry", "publish_panel_update", acceptedArgs)
			case 1:
				var retry taskpanel.Panel
				if err := json.Unmarshal([]byte(nuwaValidationReceipt(request, "confidence-retry")), &retry); err != nil || retry.Revision != accepted.Revision || read().Revision != accepted.Revision+1 {
					t.Errorf("accepted retry lost its original receipt: %v", err)
				}
				check("true", "true", "unset", "false")
				panelAdmissionCall(w, request, "confidence-different-retry", "publish_panel_update", strings.Replace(acceptedArgs, `"confirmed":false`, `"confirmed":true`, 1))
			case 2:
				if !strings.Contains(nuwaValidationReceipt(request, "confidence-different-retry"), "conflict") {
					t.Error("different original requests shared an operation identity")
				}
				call(w, request, "source-downgrade", `[{"op":"replace","path":"/edges/1/confirmed","value":false},{"op":"add","path":"/edges/2/confirmed","value":false},{"op":"remove","path":"/edges/3"}]`)
			default:
				if receipt := nuwaValidationReceipt(request, "source-downgrade"); strings.Contains(receipt, `"error"`) {
					t.Fatalf("explicit downgrade or relation removal was refused: %s", receipt)
				}
				check("true", "false", "false")
				writeContextFlowReply(w, request.Stream, map[string]any{"content": "The evidence is available."}, "stop")
			}
		}
	})
	e.cfg.Memory.Enabled, e.cfg.Memory.AutoReview, e.cfg.Swarm.ComposerPredictions = false, false, false
	thread, err := e.CreateThread("Reported relation authority", "", "")
	if err != nil {
		t.Fatal(err)
	}
	threadID = thread.ID
	turn, err := e.StartTurn(thread.ID, "Keep source confirmation separate from inference.")
	if err != nil {
		t.Fatal(err)
	}
	if got := waitForTurn(t, e, turn.ID); got.Status != store.TurnDone {
		t.Fatalf("confidence publication failed: %s", got.Error)
	}
	waitSettled(t, e, thread.ID)
}
