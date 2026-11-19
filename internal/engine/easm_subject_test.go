package engine

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/LubyRuffy/eino-swarm/internal/store"
)

// Exercise skill_view, ask_user, and open_task_panel through the real manager
// so the skill can obtain the subject before the panel shows a root.
func TestEASMAsksForAnAmbiguousSubjectBeforeOpening(t *testing.T) {
	const skillStep = "When the intended company is unclear, use ask_user and wait before opening the panel."
	const subject = "Juniper Archive Services Ltd"
	e, fixture := newScheduleCompletionHTTPEngine(t, func(w http.ResponseWriter, _ *http.Request, step int, req scheduleCompletionRequest) {
		switch step {
		case 1:
			sys := req.Messages[0].Content
			if strings.Contains(sys, "Resolve the subject before opening the panel") {
				t.Error("panel instructions duplicated the skill's clarification rule")
			}
			for _, offered := range req.Tools {
				if offered.Function.Name == "open_task_panel" {
					if strings.Contains(offered.Function.Description, ToolAskUser) {
						t.Error("the panel tool still tells the model when to ask")
					}
					if !strings.Contains(offered.Function.Parameters.Properties["subject"].Description, "graph root") {
						t.Error("the panel tool did not describe how it uses the subject")
					}
				}
			}
			panelAdmissionCall(w, req, "view-skill", "skill_view", `{"name":"portrait-skill"}`)
		case 2:
			if !strings.Contains(nuwaValidationReceipt(req, "view-skill"), skillStep) {
				t.Error("the clarification rule did not reach the model through skill_view")
			}
			panelAdmissionCall(w, req, "ask-company", ToolAskUser, `{"questions":[{"id":"subject","prompt":"Which company does the name refer to?","options":[{"id":"legal_name","label":"Identify by legal name"},{"id":"website","label":"Identify by official website"}]}]}`)
		case 3:
			var answer struct{ Answers AskAnswers }
			if err := json.Unmarshal([]byte(nuwaValidationReceipt(req, "ask-company")), &answer); err != nil ||
				len(answer.Answers["subject"].Answers) != 1 || answer.Answers["subject"].Answers[0] != subject {
				t.Errorf("confirmed company did not reach the model: %v, %v", answer, err)
			}
			panelAdmissionCall(w, req, "open-confirmed", "open_task_panel",
				fmt.Sprintf(`{"plugin_id":%q,"subject":%q}`, easmPanelID, subject))
		default:
			writeContextFlowReply(w, req.Stream, map[string]any{"content": "The asset portrait is open for the confirmed company."}, "stop")
		}
	})
	e.cfg.Memory.Enabled, e.cfg.Memory.AutoReview = true, false
	e.cfg.Swarm.ComposerPredictions = false
	if _, err := e.LibraryMemory().WriteSkill("portrait-skill", "Create an asset portrait.", skillStep); err != nil {
		t.Fatal(err)
	}
	thread, err := e.CreateThread("Ambiguous company", "", "")
	if err != nil {
		t.Fatal(err)
	}
	turn, err := e.StartTurn(thread.ID, "Create an asset portrait for Juniper Archive.")
	if err != nil {
		t.Fatal(err)
	}
	waitAskArmed(t, e, thread.ID)
	if panels, err := e.store.TaskPanels(t.Context(), thread.ID); err != nil || len(panels) != 0 {
		t.Fatalf("the conversation opened a panel before clarification: count=%d err=%v", len(panels), err)
	}
	if err := e.AnswerTurnText(thread.ID, subject); err != nil {
		t.Fatal(err)
	}
	got := waitForTurn(t, e, turn.ID)
	waitSettled(t, e, thread.ID)
	if got.Status != store.TurnDone || len(fixture.snapshot()) != 4 {
		t.Fatalf("turn status=%s error=%s", got.Status, got.Error)
	}
	panel, err := e.store.TaskPanelByPlugin(t.Context(), thread.ID, easmPanelID)
	if err != nil {
		t.Fatal(err)
	}
	var state struct {
		Nodes []struct{ Kind, Label string }
	}
	if err = json.Unmarshal(panel.State, &state); err != nil || len(state.Nodes) != 1 ||
		state.Nodes[0].Kind != "root" || state.Nodes[0].Label != subject {
		t.Fatalf("the confirmed subject must become the root: %s, err=%v", panel.State, err)
	}
}
