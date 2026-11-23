package engine

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/LubyRuffy/eino-swarm/internal/knowledge"
	"github.com/LubyRuffy/eino-swarm/internal/store"
)

func TestCompletedTurnAutomaticallyKeepsNotesSkillsAndKnowledgeInOneReviewerBatch(t *testing.T) {
	const original = "Keep the observed result and retain the reusable verification procedure."
	var reviews atomic.Int32
	e, _ := distillWire(t, func(w http.ResponseWriter, req scheduleCompletionRequest, _ int) {
		if len(req.Messages) > 0 && replyNoScheduleClaimsFixture(t, w, req.Messages[0].Content, req.Stream, len(req.Tools)) {
			return
		}
		if !nuwaPlannerOfferedTools(req)[reviewBatchToolName] {
			writeContextFlowReply(w, req.Stream, map[string]any{"content": "The original observation remains tentative."}, "stop")
			return
		}
		if reviews.Add(1) != 1 {
			writeContextFlowReply(w, req.Stream, map[string]any{"content": "The review already proposed its changes."}, "stop")
			return
		}
		if !nuwaPlannerOfferedTools(req)[ToolReadKnowledge] {
			t.Error("automatic reviewer did not receive the project knowledge reader")
		}
		if params, _ := json.Marshal(req.Tools); !strings.Contains(string(params), `"knowledge"`) {
			t.Errorf("automatic batch cannot propose wiki changes: %s", params)
		}
		var src int64
		for _, m := range req.Messages {
			for _, line := range strings.Split(m.Content, "\n") {
				if strings.Contains(line, original) {
					_, _ = fmt.Sscanf(line, "[seq=%d", &src)
					if src != 0 {
						break
					}
				}
			}
		}
		nuwaValidationTool(w, req, "automatic-project-review", reviewBatchToolName, fmt.Sprintf(`{
		  "changes":[
		    {"target":"memory","action":"add","content":"Retain the reusable verification procedure.","evidence":[{"seq":%d,"quote":%q}]},
		    {"target":"skill_manage","action":"create","name":"verify-observation","description":"Verify a project observation.","content":"Inspect the original observation and document uncertainty.","evidence":[{"seq":%d,"quote":%q}]}
		  ],
		  "knowledge":[{"action":"write_page","id":"observation","type":"entry","title":"Observation","body":"A recorded observation.","evidence":[{"seq":%d,"quote":%q}]}]
		}`, src, original, src, original, src, original))
	})
	e.cfg.Memory.AutoReview = true
	p, err := e.CreateProject("Automatic project review", "", "", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.EnableProjectKnowledge(p.ID); err != nil {
		t.Fatal(err)
	}
	th := knowledgeFixtureThread(t, e, p.ID, "Original conversation")
	turn, err := e.StartTurn(th.ID, original)
	if err != nil {
		t.Fatal(err)
	}
	if got := waitForTurn(t, e, turn.ID); got.Status != store.TurnDone {
		t.Fatalf("turn did not succeed: %+v", got)
	}
	ev := waitForReview(t, e, turn.ID)
	out := decodeReview(t, ev)
	page, err := e.ProjectKnowledgePage(t.Context(), p.ID, "observation")
	if err != nil || page.UpdatedTurn != turn.ID || len(page.Sources) != 1 || page.Sources[0].Classification != "primary" {
		t.Fatalf("review did not keep an attributed knowledge page: %+v, %v", page, err)
	}
	if out.Err != "" || len(out.Notes) != 1 || len(out.Skills) != 1 ||
		!strings.Contains(ev.Text, `"id":"observation"`) || reviews.Load() != 1 {
		t.Fatalf("expected one reviewer to commit all destinations: %+v, requests=%d, event=%s", out, reviews.Load(), ev.Text)
	}
}

func TestAutomaticKnowledgeReviewNeedsAnEnabledWikiAndAnEligibleTurn(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status string
		enable bool
		plan   bool
	}{
		{"disabled-wiki", store.TurnDone, false, false},
		{"failed", store.TurnError, true, false},
		{"plan", store.TurnDone, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, calls := distillWire(t, func(w http.ResponseWriter, req scheduleCompletionRequest, _ int) {
				if nuwaPlannerOfferedTools(req)[ToolReadKnowledge] {
					t.Error("read_knowledge was offered outside an eligible successful project turn")
				}
				if params, _ := json.Marshal(req.Tools); strings.Contains(string(params), `"knowledge"`) {
					t.Errorf("ineligible reviewer received wiki write authority: %s", params)
				}
				nuwaValidationTool(w, req, "empty-review", reviewBatchToolName, `{"changes":[]}`)
			})
			e.cfg.Memory.AutoReview = true
			p, err := e.CreateProject("Review scope", "", "", true)
			if err != nil {
				t.Fatal(err)
			}
			if tc.enable {
				if _, err := e.EnableProjectKnowledge(p.ID); err != nil {
					t.Fatal(err)
				}
			}
			th := knowledgeFixtureThread(t, e, p.ID, "Source")
			th.PlanMode = tc.plan
			pc, err := e.projectContextFor(th)
			if err != nil {
				t.Fatal(err)
			}
			turn, _ := distillEvent(t, e, th, tc.status, KindUser, "Keep project summaries useful.")
			e.scheduleReview(th.ID, &turn, tc.status, pc, "")
			receipt := decodeReview(t, waitForReview(t, e, turn.ID))
			if calls.Load() != 1 || receipt.Err != "" || len(receipt.Knowledge) != 0 {
				t.Fatalf("existing Memory behavior was lost: %+v, calls=%d", receipt, calls.Load())
			}
		})
	}
}

func automaticKnowledgeWriteFixture(t *testing.T) (*projectDistillBatch, store.Event) {
	t.Helper()
	e := newTestEngine(t)
	e.cfg.Memory.AutoReview = true
	p, err := e.CreateProject("Automatic review write", "", "", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.EnableProjectKnowledge(p.ID); err != nil {
		t.Fatal(err)
	}
	thread := knowledgeFixtureThread(t, e, p.ID, "Evidence")
	pc, err := e.projectContextFor(thread)
	if err != nil {
		t.Fatal(err)
	}
	turn, ev := distillEvent(t, e, thread, store.TurnDone, KindUser, "Use the recorded observation.")
	batch := e.automaticReviewKnowledge(&turn, pc, []store.Event{ev}, map[int64]store.Event{ev.Seq: ev})
	if batch == nil {
		t.Fatal("automatic wiki proposal was not admitted")
	}
	return batch, ev
}

func TestAutomaticKnowledgeReviewRechecksAuthorityBeforeSaving(t *testing.T) {
	for _, name := range []string{"unknown-source", "moved-thread", "planning", "disabled-wiki", "invalid-schema", "backup"} {
		t.Run(name, func(t *testing.T) {
			batch, ev := automaticKnowledgeWriteFixture(t)
			e := batch.e
			op := distillKnowledgeOperation{knowledgeWriteInput: knowledgeWriteInput{Action: "write_page"},
				Evidence: []reviewCitation{{Seq: ev.Seq, Quote: ev.Text}}}
			var changeErr, wantErr error
			switch name {
			case "unknown-source":
				op.Evidence[0].Seq++
			case "moved-thread":
				changeErr = e.MoveThread(ev.ThreadID, "")
				wantErr = store.ErrHistorySourceUnavailable
			case "planning":
				changeErr = e.SetPlanMode(ev.ThreadID, true)
			case "disabled-wiki":
				changeErr = os.Remove(filepath.Join(batch.work.kb.Dir(), "schema.yaml"))
				wantErr = knowledge.ErrDisabled
			case "invalid-schema":
				changeErr = os.WriteFile(filepath.Join(batch.work.kb.Dir(), "schema.yaml"), []byte("types: ["), 0o600)
			case "backup":
				e.mu.Lock()
				e.backupRunning = true
				e.mu.Unlock()
				t.Cleanup(func() {
					e.mu.Lock()
					e.backupRunning = false
					e.mu.Unlock()
				})
				wantErr = ErrBackupBusy
			}
			if changeErr != nil {
				t.Fatal(changeErr)
			}
			sources, release, err := batch.beginAutomaticKnowledgeWrite(t.Context(), []distillKnowledgeOperation{op})
			if release != nil {
				release()
			}
			if err == nil || (wantErr != nil && !errors.Is(err, wantErr)) || release != nil || sources != nil {
				t.Fatalf("refused review acquired write authority: sources=%v error=%v", sources, err)
			}
		})
	}
}

func TestAutomaticKnowledgeReviewReportsOnlyCommitsBeforeAPageConflict(t *testing.T) {
	batch, ev := automaticKnowledgeWriteFixture(t)
	var mu sync.Mutex
	var outcome reviewOutcome
	tool := &reviewBatchTool{knowledge: batch, mu: &mu, outcome: &outcome}
	page := fmt.Sprintf(`{"action":"write_page","id":"observation","type":"entry","title":"Observation","body":"Observed result.","evidence":[{"seq":%d,"quote":%q}]}`, ev.Seq, ev.Text)
	proposal := `{"changes":[],"knowledge":[` + page + `,` + page + `]}`
	raw, err := tool.InvokableRun(t.Context(), proposal)
	if err != nil || !strings.Contains(raw, `"success":false`) || !tool.complete ||
		!outcome.Changed || len(outcome.Knowledge) != 1 || outcome.Knowledge[0].ID != "observation" || outcome.Err == "" {
		t.Fatalf("expected the first save and the second operation's failure: %s, %+v, %v", raw, outcome, err)
	}
	var retryOutcome reviewOutcome
	retry := &reviewBatchTool{knowledge: batch, mu: &mu, outcome: &retryOutcome}
	for _, proposal := range []string{`{"changes":[],"knowledge":null}`, `{"changes":[],"knowledge":[` + page + `]}`} {
		raw, err := retry.InvokableRun(t.Context(), proposal)
		if err != nil || !strings.Contains(raw, `"success":false`) || retry.complete || retryOutcome.Changed {
			t.Fatalf("refused proposal should allow repair and report no writes: %s, %+v, %v", raw, retryOutcome, err)
		}
	}
}
