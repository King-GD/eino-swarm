package engine

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"unicode/utf8"

	swarm "github.com/LubyRuffy/eino-swarm"
	"github.com/LubyRuffy/eino-swarm/internal/provider"
	"github.com/LubyRuffy/eino-swarm/internal/store"
	"github.com/cloudwego/eino/schema"
)

// Promoting a record on its originating model surface changes what a real
// subsequent peer request can see. The source's private sibling never travels.
func TestOrdinaryWorkRecordsStayPrivateUntilPromotedAtModelHTTP(t *testing.T) {
	var fixtureMu sync.Mutex
	var sourceID string
	var fact, private store.NuwaNode
	e, requests := newScheduleCompletionHTTPEngine(t, func(w http.ResponseWriter, _ *http.Request, step int, request scheduleCompletionRequest) {
		fixtureMu.Lock()
		defer fixtureMu.Unlock()
		tools := nuwaPlannerOfferedTools(request)
		if !tools["read_work"] || !tools["record_work"] || !tools["exec"] || tools["list_models"] {
			t.Errorf("ordinary manager received the wrong tool surface: %v", tools)
		}
		call := func(id, name, args string) { nuwaValidationTool(w, request, id, name, args) }
		finish := func() {
			writeContextFlowReply(w, request.Stream, map[string]any{"content": "Inspected the available work record."}, "stop")
		}
		record := func(callID string) store.NuwaNode {
			var receipt struct {
				OK   bool           `json:"ok"`
				Node store.NuwaNode `json:"node"`
			}
			raw := nuwaValidationReceipt(request, callID)
			if err := json.Unmarshal([]byte(raw), &receipt); err != nil || !receipt.OK || receipt.Node.ThreadID != sourceID {
				t.Errorf("invalid work receipt: %s (%v)", raw, err)
			}
			return receipt.Node
		}
		switch step {
		case 1:
			nuwaContextHTTPBlock(t, request, false)
			assertNuwaRecordWorkGuidance(t, request, "ordinary manager")
			call("private-fact", "record_work", `{"kind":"fact","title":"Verified input partition","summary":"The supplied partition was inspected."}`)
		case 2:
			fact = record("private-fact")
			if fact.Scope != store.WorkScopeThread || fact.ProjectID != "" {
				t.Errorf("ordinary create did not remain private: %+v", fact)
			}
			call("private-sibling", "record_work", `{"kind":"fact","title":"Unshared observation"}`)
		case 3:
			private = record("private-sibling")
			finish()
		case 4:
			nuwaContextHTTPBlock(t, request, false)
			call("peer-read-before", "read_work", `{}`)
		case 5:
			var view store.WorkSnapshot
			raw := nuwaValidationReceipt(request, "peer-read-before")
			if err := json.Unmarshal([]byte(raw), &view); err != nil || len(view.Nodes) != 0 {
				t.Errorf("peer read saw private records: %s (%v)", raw, err)
			}
			args, err := json.Marshal(map[string]any{
				"sql":    "SELECT text AS payload FROM events WHERE thread_id=? UNION ALL SELECT content FROM messages WHERE thread_id=? UNION ALL SELECT tool_calls FROM messages WHERE thread_id=?",
				"params": []any{sourceID, sourceID, sourceID},
			})
			if err != nil {
				t.Error(err)
			}
			call("peer-history-before", ToolQueryHistory, string(args))
		case 6:
			raw := nuwaValidationReceipt(request, "peer-history-before")
			if !strings.Contains(raw, "Inspect and record the findings.") || strings.Contains(raw, "Verified input partition") ||
				strings.Contains(raw, "Unshared observation") || strings.Contains(raw, "private-fact") {
				t.Errorf("peer history leaked or hid the wrong records: %s", raw)
			}
			finish()
		case 7:
			call("explicit-promotion", "record_work", fmt.Sprintf(`{"id":%q,"version":%d,"scope":"project"}`, fact.ID, fact.Version))
		case 8:
			promoted := record("explicit-promotion")
			if promoted.ID != fact.ID || promoted.Scope != store.WorkScopeProject || promoted.Version <= fact.Version {
				t.Errorf("promotion lost the record identity or failed to increment version: %+v", promoted)
			}
			finish()
		case 9:
			view := nuwaContextHTTPBlock(t, request, true)
			if len(view.Nodes) != 1 || view.Nodes[0].ID != fact.ID || view.Nodes[0].ID == private.ID {
				t.Errorf("peer preview carried the wrong records: %+v", view.Nodes)
			}
			finish()
		default:
			t.Errorf("unexpected model request %d", step)
			finish()
		}
	})
	e.cfg.Memory.AutoReview = false
	project, err := e.CreateProject("Ordinary work scope", "", "", false, false)
	if err != nil {
		t.Fatal(err)
	}
	source, err := e.CreateThread("Work owner", "", project.ID)
	if err != nil {
		t.Fatal(err)
	}
	fixtureMu.Lock()
	sourceID = source.ID
	fixtureMu.Unlock()
	peer, err := e.CreateThread("Work peer", "", project.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range []struct{ id, text string }{
		{source.ID, "Inspect and record the findings."},
		{peer.ID, "Review the work currently visible here."},
		{source.ID, "Share the partition finding with this project."},
		{peer.ID, "Review the newly shared finding."},
	} {
		started, err := e.StartTurn(step.id, step.text)
		if err != nil {
			t.Fatal(err)
		}
		finished := waitForTurn(t, e, started.ID)
		waitSettled(t, e, step.id)
		if finished.Status != store.TurnDone || !finished.WorkLedger {
			t.Fatalf("turn did not finish with its frozen work tools: %+v", finished)
		}
	}
	eventRows, err := e.store.ListEvents(source.ID, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	var calls, receipts int
	for _, row := range eventRows {
		if row.ToolCallID != "private-fact" && row.ToolCallID != "private-sibling" {
			continue
		}
		if !row.WorkRecordPayload {
			t.Errorf("durable event lost its structured work provenance: %s %s", row.Kind, row.ToolCallID)
		}
		if row.Kind == swarm.NotifyToolCall.String() {
			calls++
		}
		if row.Kind == swarm.NotifyToolResult.String() {
			receipts++
		}
	}
	if calls != 2 || receipts != 2 {
		t.Fatalf("real calls/receipts did not reach the event store: calls=%d receipts=%d", calls, receipts)
	}
	messageRows, err := e.store.ListMessages(source.ID)
	if err != nil {
		t.Fatal(err)
	}
	var protectedMessages int
	for _, row := range messageRows {
		if row.ToolCallID != "private-fact" && row.ToolCallID != "private-sibling" {
			continue
		}
		if !row.WorkRecordPayload {
			t.Errorf("durable transcript lost its structured work provenance: %s", row.ToolCallID)
		}
		protectedMessages++
	}
	if protectedMessages != 2 {
		t.Fatalf("real work receipts did not reach the durable transcript: %d", protectedMessages)
	}

	if len(requests.snapshot()) != 9 {
		t.Fatalf("wrong number of actual model requests: %d", len(requests.snapshot()))
	}
}

func TestStandaloneWorkPlanningAndFrozenRecoveryAtModelHTTP(t *testing.T) {
	for _, test := range []struct {
		name                               string
		enabled, frozenPlan, plan, recover bool
		wantRead, wantWrite                bool
	}{
		{"fresh enabled", true, false, false, false, true, true},
		{"fresh disabled", false, false, false, false, false, false},
		{"fresh planning", true, true, true, false, true, false},
		{"switch-off overrides frozen catalog", false, false, false, true, false, false},
		{"frozen read-only after leaving plan", true, true, false, true, true, false},
		{"frozen catalog after entering plan", true, false, true, true, true, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			const title = "Recorded standalone observation"
			e, requests := newScheduleCompletionHTTPEngine(t, func(w http.ResponseWriter, _ *http.Request, step int, request scheduleCompletionRequest) {
				tools := nuwaPlannerOfferedTools(request)
				if tools["read_work"] != test.wantRead || tools["record_work"] != test.wantWrite || tools["list_models"] {
					t.Errorf("standalone model received the wrong tool surface: %v", tools)
				}
				if step == 1 && test.wantWrite {
					nuwaValidationTool(w, request, "standalone-record", "record_work", `{"kind":"fact","title":"Recorded standalone observation"}`)
					return
				}
				if test.wantWrite {
					var receipt struct {
						OK   bool           `json:"ok"`
						Node store.NuwaNode `json:"node"`
					}
					raw := nuwaValidationReceipt(request, "standalone-record")
					if test.plan {
						if !strings.Contains(raw, "planning can read work records but cannot write them") {
							t.Errorf("active planning admitted a write from a frozen catalog: %s", raw)
						}
					} else {
						if err := json.Unmarshal([]byte(raw), &receipt); err != nil || !receipt.OK || receipt.Node.Title != title ||
							receipt.Node.Scope != store.WorkScopeThread || receipt.Node.ProjectID != "" {
							t.Errorf("standalone record did not retain private ownership: %s (%v)", raw, err)
						}
						nuwaContextHTTPBlock(t, request, true)
					}
				}
				writeContextFlowReply(w, request.Stream, map[string]any{"content": "Finished the permitted work."}, "stop")
			})
			e.cfg.Memory.AutoReview = false
			e.cfg.Swarm.WorkLedger = test.enabled
			thread, err := e.CreateThread("Standalone work", "", "")
			if err != nil {
				t.Fatal(err)
			}
			if test.plan {
				if err := e.SetPlanMode(thread.ID, true); err != nil {
					t.Fatal(err)
				}
			}
			var turn *store.Turn
			if test.recover {
				turn = &store.Turn{
					ThreadID: thread.ID, WorkLedger: true, WorkLedgerReadOnly: test.frozenPlan,
					ProviderID: e.cfg.Models.Providers[0].ID, Model: e.cfg.Models.Providers[0].Model,
					UserText: "Inspect the recorded work.",
				}
				if err := e.store.CreateTurn(turn); err != nil {
					t.Fatal(err)
				}
				if err := e.store.AppendMessages(thread.ID, turn.ID, []store.Message{{
					Role: string(schema.User), Content: turn.UserText, Source: store.MessageSourceHuman,
				}}); err != nil {
					t.Fatal(err)
				}
				if count, err := e.ResumeOrphanedTurns(); err != nil || count != 1 {
					t.Fatalf("recover frozen work surface: count=%d, err=%v", count, err)
				}
			} else {
				turn, err = e.StartTurn(thread.ID, "Inspect the recorded work.")
				if err != nil {
					t.Fatal(err)
				}
			}
			finished := waitForTurn(t, e, turn.ID)
			waitSettled(t, e, thread.ID)
			if finished.Status != store.TurnDone || len(requests.snapshot()) == 0 {
				t.Fatalf("standalone turn could not finish: %+v", finished)
			}
		})
	}
}

func TestRetractedWorkIsMinimalAndOmittedBeforeActiveRecords(t *testing.T) {
	revision := store.WorkRevision{ThreadID: "thread-work", ThreadRevision: 5}
	old := store.NuwaNode{ID: "retired", Kind: "fact", Status: "retracted", Title: "Disproved assumption",
		Summary: "This private observation was disproved.", DependsOn: []string{"previous"}, Version: 4}
	active := store.NuwaNode{ID: "active", Kind: "task", Status: "running", Title: "Follow-up is still active",
		Summary: "Record the remaining observations.", DependsOn: []string{old.ID}, StaleDependencies: []string{old.ID}, Version: 1}
	full, err := renderNuwaContext(revision, []store.NuwaNode{old, active}, "", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	var view nuwaPreview
	if err := json.Unmarshal([]byte(strings.TrimPrefix(full, nuwaContextPrefix)), &view); err != nil ||
		len(view.Nodes) != 2 || view.Nodes[0].ID != active.ID || view.Nodes[1].ID != old.ID ||
		view.Nodes[1].Version != old.Version || view.Nodes[1].Summary != "" || len(view.Nodes[1].DependsOn) != 0 {
		t.Fatalf("invalid record displaced or leaked details: %s (%v)", full, err)
	}
	view.Nodes = view.Nodes[:1]
	view.Omitted, view.InactiveOmitted = 1, 1
	body, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	tight, err := renderNuwaContext(revision, []store.NuwaNode{old, active}, "", utf8.RuneCount(body)+utf8.RuneCountInString(nuwaContextPrefix))
	if err != nil || strings.Contains(tight, old.Title) || !strings.Contains(tight, active.Summary) ||
		!strings.Contains(tight, `"inactive_omitted":1`) {
		t.Fatalf("budget did not omit the inactive record first: %s (%v)", tight, err)
	}
	old.Status, old.Version = "reported", old.Version+1
	restored, err := renderNuwaContext(revision, []store.NuwaNode{old, active}, "", 1<<20)
	if err != nil || !strings.Contains(restored, old.Summary) || !strings.Contains(restored, `"stale_dependencies":["retired"]`) {
		t.Fatalf("restoration did not recover full form and preserve stale dependency: %s (%v)", restored, err)
	}
}

func TestThreadWorkPromotionRejectsWrongDataGeneration(t *testing.T) {
	e := newTestEngine(t)
	thread, err := e.CreateThread("Standalone work", "", "")
	if err != nil {
		t.Fatal(err)
	}
	expected, err := e.ThreadWorkRevision(t.Context(), thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.PromoteThreadWork(t.Context(), thread.ID, []string{"old"}, expected, 99); err != store.ErrNuwaConflict {
		t.Fatalf("unexpected result of stale generation: %v", err)
	}
	view, err := e.ThreadWorkSnapshot(t.Context(), thread.ID)
	if err != nil || view.WorkRevision != expected || len(view.Nodes) != 0 || len(view.Workers) != 0 {
		t.Fatalf("stale promotion changed the current view: %+v (%v)", view, err)
	}
	e.record(store.Event{ThreadID: thread.ID, Kind: swarm.NotifySpawned.String(), AgentID: "reviewer-1", Role: "reviewer", WorkerGeneration: 1})
	view, err = e.ThreadWorkSnapshot(t.Context(), thread.ID)
	if err != nil || view.ThreadRevision <= expected.ThreadRevision || len(view.Workers) != 1 || view.Workers[0].Status != "running" {
		t.Fatalf("standalone workers did not appear in the work snapshot: %+v (%v)", view, err)
	}
}

// The model boundary must see the new version even when title and summary are
// unchanged. The original conversation remains able to retrieve full evidence.
func TestWorkRecordInvalidationAndRestorationAtModelHTTP(t *testing.T) {
	var record store.NuwaNode
	var fixtureMu sync.Mutex
	e, requests := newScheduleCompletionHTTPEngine(t, func(w http.ResponseWriter, _ *http.Request, step int, request scheduleCompletionRequest) {
		fixtureMu.Lock()
		defer fixtureMu.Unlock()
		switch step {
		case 1:
			nuwaValidationTool(w, request, "roundtrip-create", "record_work", `{"kind":"fact","title":"Observed configuration","summary":"Settings match the examined profile.","detail":"Inspection reference is registered."}`)
		case 2:
			view := nuwaContextHTTPBlock(t, request, true)
			if len(view.Nodes) != 1 || view.Nodes[0].Status != "reported" || view.Nodes[0].Version != 1 {
				t.Errorf("initial fact did not reach the next model request: %+v", view)
				return
			}
			record = view.Nodes[0]
			nuwaValidationTool(w, request, "roundtrip-retract", "record_work",
				fmt.Sprintf(`{"id":%q,"version":1,"status":"retracted"}`, record.ID))
		case 3:
			view := nuwaContextHTTPBlock(t, request, true)
			if len(view.Nodes) != 1 || view.Nodes[0].ID != record.ID || view.Nodes[0].Status != "retracted" ||
				view.Nodes[0].Version != 2 || view.Nodes[0].Summary != "" || len(view.Nodes[0].DependsOn) != 0 {
				t.Errorf("retracted fact was not sent in minimal form: %+v", view)
			}
			nuwaValidationTool(w, request, "roundtrip-read", "read_work", fmt.Sprintf(`{"id":%q}`, record.ID))
		case 4:
			raw := nuwaValidationReceipt(request, "roundtrip-read")
			if !strings.Contains(raw, "Inspection reference is registered.") || !strings.Contains(raw, record.Summary) {
				t.Errorf("private details were lost from read_work: %s", raw)
			}
			nuwaValidationTool(w, request, "roundtrip-restore", "record_work",
				fmt.Sprintf(`{"id":%q,"version":2,"status":"reported"}`, record.ID))
		case 5:
			view := nuwaContextHTTPBlock(t, request, true)
			if len(view.Nodes) != 1 || view.Nodes[0].ID != record.ID || view.Nodes[0].Status != "reported" ||
				view.Nodes[0].Summary != record.Summary || view.Nodes[0].Version != 3 {
				t.Errorf("restored fact did not return in full form: %+v", view)
			}
			writeContextFlowReply(w, request.Stream, map[string]any{"content": "The observation is current."}, "stop")
		default:
			t.Errorf("unexpected model request %d", step)
			writeContextFlowReply(w, request.Stream, map[string]any{"content": "The observation was inspected."}, "stop")
		}
	})
	e.cfg.Memory.AutoReview = false
	thread, err := e.CreateThread("Status corrections", "", "")
	if err != nil {
		t.Fatal(err)
	}
	turn, err := e.StartTurn(thread.ID, "Inspect and reconcile a finding.")
	if err != nil {
		t.Fatal(err)
	}
	finished := waitForTurn(t, e, turn.ID)
	waitSettled(t, e, thread.ID)
	if finished.Status != store.TurnDone || len(requests.snapshot()) != 5 {
		t.Fatalf("fact round trip did not settle: %+v requests=%d", finished, len(requests.snapshot()))
	}
}

// An ordinary worker receives the same scoped work tools without enabling
// Nuwa delegation or changing the execution tools in its original role.
func TestOrdinaryWorkerRecordsWorkAtActualModelHTTP(t *testing.T) {
	var managerCalls, workerCalls atomic.Int32
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request scheduleCompletionRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			http.Error(w, "invalid fixture input", http.StatusBadRequest)
			return
		}
		if len(request.Messages) > 0 && replyNoScheduleClaimsFixture(t, w, request.Messages[0].Content, request.Stream, len(request.Tools)) {
			return
		}
		tools := nuwaPlannerOfferedTools(request)
		finish := func(text string) {
			writeContextFlowReply(w, request.Stream, map[string]any{"content": text}, "stop")
		}
		if !tools["read_work"] || !tools["record_work"] || tools["list_models"] {
			t.Errorf("actual work tool surface: %v", tools)
		}
		if !tools["spawn_agent"] {
			if !tools["read"] || !tools["exec"] {
				t.Errorf("worker lost its ordinary execution tools: %v", tools)
			}
			assertNuwaAssignmentAbsentHTTP(t, request)
			switch workerCalls.Add(1) {
			case 1:
				nuwaValidationTool(w, request, "ordinary-worker-record", "record_work", `{"kind":"fact","title":"Worker checked the evidence"}`)
			case 2:
				view := nuwaContextHTTPBlock(t, request, true)
				if len(view.Nodes) != 1 || view.Nodes[0].Title != "Worker checked the evidence" {
					t.Errorf("worker preview lost its observation: %+v", view)
				}
				finish("The check is complete.")
			default:
				t.Error("unexpected worker request")
				finish("The worker has completed its check.")
			}
			return
		}
		switch managerCalls.Add(1) {
		case 1:
			nuwaValidationTool(w, request, "ordinary-worker-spawn", "spawn_agent", `{"role":"reviewer","task":"Review the supplied evidence."}`)
		case 2:
			var worker toolScopeWorkerID
			if err := json.Unmarshal([]byte(nuwaValidationReceipt(request, "ordinary-worker-spawn")), &worker); err != nil || worker.AgentID == "" {
				t.Errorf("ordinary worker could not start: %+v (%v)", worker, err)
				finish("The worker did not start.")
				return
			}
			nuwaValidationTool(w, request, "ordinary-worker-wait", "wait_agents", fmt.Sprintf(`{"agent_ids":[%q],"timeout_s":5}`, worker.AgentID))
		case 3:
			if !strings.Contains(nuwaValidationReceipt(request, "ordinary-worker-wait"), "The check is complete.") {
				t.Error("manager did not collect the result of the worker's actual execution")
			}
			finish("The worker's observation was collected.")
		default:
			t.Error("unexpected manager request")
			finish("The work is complete.")
		}
	}))
	t.Cleanup(endpoint.Close)
	e := newTestEngine(t)
	e.cfg.Swarm.AutoTitle = false
	e.cfg.Swarm.ComposerPredictions = false
	e.cfg.Memory.AutoReview = false
	e.cfg.Models.Providers[0].BaseURL = endpoint.URL
	e.cfg.Models.Providers[0].Model = "ordinary-worker-fixture"
	e.pool = provider.New(e.cfg)
	thread, err := e.CreateThread("Conversation worker", "", "")
	if err != nil {
		t.Fatal(err)
	}
	turn, err := e.StartTurn(thread.ID, "Delegate a review of the current evidence.")
	if err != nil {
		t.Fatal(err)
	}
	finished := waitForTurn(t, e, turn.ID)
	waitSettled(t, e, thread.ID)
	view, err := e.ThreadWorkSnapshot(t.Context(), thread.ID)
	if finished.Status != store.TurnDone || err != nil || len(view.Nodes) != 1 ||
		view.Nodes[0].Scope != store.WorkScopeThread || view.Nodes[0].AgentID == swarm.DefaultManagerID ||
		view.Nodes[0].Generation <= 0 || managerCalls.Load() != 3 || workerCalls.Load() != 2 {
		t.Fatalf("worker work was not committed through the normal execution surface: %+v, manager=%d worker=%d (%v)",
			view, managerCalls.Load(), workerCalls.Load(), err)
	}
}
