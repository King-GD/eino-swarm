package engine

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/LubyRuffy/eino-swarm/internal/panelplugins/easm"
	"github.com/LubyRuffy/eino-swarm/internal/taskpanel"
)

// The model emits a schema-valid direct patch containing a disconnected
// cycle. The publisher must refuse it before committing a state or delivery.
func TestEASMDirectPublishRefusesNewCycleAtomically(t *testing.T) {
	const published = `{"schema_version":1,"title":"Subject","collection":{"status":"idle","steps":[]},"nodes":[{"id":"root","kind":"root","label":"Subject"},{"id":"domain:observed.test","kind":"domain","label":"observed.test"}],"edges":[{"source":"root","target":"domain:observed.test","confirmed":true}],"heat":[],"components":[]}`
	e, fixture := newScheduleCompletionHTTPEngine(t, func(w http.ResponseWriter, _ *http.Request, step int, request scheduleCompletionRequest) {
		if step == 1 {
			easmToolCall(w, request, "open-portrait", "open_task_panel", `{"plugin_id":"easm-asset-portrait"}`)
			return
		}
		if step > 3 {
			writeContextFlowReply(w, request.Stream, map[string]any{"content": "The original observation is retained."}, "stop")
			return
		}
		var panel taskpanel.Panel
		result := request.Messages[len(request.Messages)-1].Content
		if err := json.Unmarshal([]byte(result), &panel); err != nil || panel.ID == "" {
			t.Errorf("invalid panel receipt %q: %v", result, err)
		}
		update := taskpanel.Update{ProtocolVersion: 1, PanelID: panel.ID, BaseRevision: panel.Revision}
		if step == 2 {
			update.OperationID, update.Kind, update.State = "valid-observation", "snapshot", json.RawMessage(published)
		} else {
			update.OperationID, update.Kind = "new-cycle", "patch"
			update.Operations = json.RawMessage(`[
				{"op":"add","path":"/nodes/-","value":{"id":"a","kind":"company","label":"A"}},
				{"op":"add","path":"/nodes/-","value":{"id":"b","kind":"company","label":"B"}},
				{"op":"add","path":"/edges/-","value":{"source":"a","target":"b"}},
				{"op":"add","path":"/edges/-","value":{"source":"b","target":"a"}}
			]`)
		}
		raw, err := json.Marshal(update)
		if err != nil {
			t.Error(err)
		}
		easmToolCall(w, request, fmt.Sprintf("direct-portrait-%d", step), "publish_panel_update", string(raw))
	})
	thread, err := e.CreateThread("Portrait direct publish", "", "")
	if err != nil {
		t.Fatal(err)
	}
	turn, err := e.StartTurn(thread.ID, "Show the reported relationships.")
	if err != nil {
		t.Fatal(err)
	}
	waitForTurn(t, e, turn.ID)
	waitSettled(t, e, thread.ID)
	if len(fixture.snapshot()) < 3 {
		t.Fatal("the direct update was not attempted")
	}
	panels, err := e.store.TaskPanels(t.Context(), thread.ID)
	if err != nil || len(panels) != 1 || panels[0].Revision != 2 {
		t.Fatalf("panels=%+v err=%v", panels, err)
	}
	var state easm.State
	if err := json.Unmarshal(panels[0].State, &state); err != nil || len(state.Nodes) != 2 || len(state.Edges) != 1 || state.Edges[0].Target != "domain:observed.test" {
		t.Fatalf("state=%s err=%v", panels[0].State, err)
	}
	changes, err := e.store.TaskPanelChanges(t.Context(), thread.ID, 0)
	if err != nil || len(changes) != 2 || changes[1].Revision != 2 {
		t.Fatalf("changes=%+v err=%v", changes, err)
	}
}
