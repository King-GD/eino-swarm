package engine

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	swarm "github.com/LubyRuffy/eino-swarm"
	"github.com/LubyRuffy/eino-swarm/internal/knowledge"
	"github.com/LubyRuffy/eino-swarm/internal/provider"
	"github.com/LubyRuffy/eino-swarm/internal/store"
	"github.com/cloudwego/eino/schema"
)

func TestKnowledgeDistillationReadsOriginalHistoryWritesAndReadsBacklinksThroughRegistry(t *testing.T) {
	const observed = "The observed value is unchanged under condition A."
	var peerID, foreignID, projectID string
	var eventSeq int64
	send := func(w http.ResponseWriter, request scheduleCompletionRequest, id, tool string, args any) {
		raw, err := json.Marshal(args)
		if err != nil {
			t.Error(err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		nuwaValidationTool(w, request, id, tool, string(raw))
	}
	write := func(id, title string) map[string]any {
		return map[string]any{"action": "write_page", "id": id, "title": title, "type": "entry", "body": "Page content"}
	}
	e, wire := newScheduleCompletionHTTPEngine(t, func(w http.ResponseWriter, _ *http.Request, step int, request scheduleCompletionRequest) {
		offered := nuwaPlannerOfferedTools(request)
		if !offered[ToolQueryHistory] || !offered[ToolReadKnowledge] || !offered[ToolRecordKnowledge] {
			t.Errorf("manager did not receive history and knowledge tools: %+v", offered)
		}
		for _, candidate := range request.Tools {
			if candidate.Function.Name == ToolQueryHistory && !strings.Contains(candidate.Function.Description, "only when the System instructions explicitly require it") {
				t.Errorf("the real query tool imposed the structured report on ordinary distillation: %s", candidate.Function.Description)
			}
		}
		system := knowledgeSystemText(request)
		if !strings.Contains(system, "Type entry") || !strings.Contains(system, "Assistant text remains unverified analysis") ||
			strings.Contains(system, observed) || strings.Contains(system, "Type variant") {
			t.Errorf("knowledge prompt was not the neutral turn snapshot: %s", system)
		}
		switch step {
		case 1:
			send(w, request, "knowledge-originals", ToolQueryHistory, map[string]any{
				"sql":    "SELECT 'message' AS kind,thread_id,seq,content AS source_text FROM messages WHERE thread_id=? UNION ALL SELECT 'event' AS kind,thread_id,seq,text FROM events WHERE thread_id=? AND kind=?",
				"params": []any{peerID, peerID, "tool_result"},
			})
		case 2:
			data := write("refused", "Refused page")
			data["sources"] = []knowledgeSourceRef{{Kind: "event", ThreadID: peerID, Seq: eventSeq}, {Kind: "message", ThreadID: foreignID, Seq: 1}}
			send(w, request, "knowledge-foreign", ToolRecordKnowledge, data)
		case 3:
			if reply := nuwaValidationReceipt(request, "knowledge-foreign"); !strings.Contains(reply, `"ok":false`) || !strings.Contains(reply, "history source is unavailable") {
				t.Errorf("cross-project source was accepted: %s", reply)
			}
			send(w, request, "knowledge-schema", ToolReadKnowledge, map[string]string{"action": "schema"})
		case 4:
			var receipt knowledgeReadReply
			if err := json.Unmarshal([]byte(nuwaValidationReceipt(request, "knowledge-schema")), &receipt); err != nil || receipt.Status == nil {
				t.Error("actual schema receipt is unavailable")
				writeContextFlowReply(w, request.Stream, map[string]any{"content": "Unable to read schema."}, "stop")
				return
			}
			send(w, request, "knowledge-new-type", ToolRecordKnowledge, map[string]any{
				"action": "append_schema", "revision": receipt.Status.Revision,
				"add": map[string]any{"types": []knowledge.PageType{{ID: "variant", Dir: "variants"}}},
			})
		case 5:
			var found struct {
				OK   bool `json:"ok"`
				Rows []struct {
					knowledgeSourceRef
					Text string `json:"source_text"`
				} `json:"rows"`
			}
			receipt := nuwaValidationReceipt(request, "knowledge-originals")
			if err := json.Unmarshal([]byte(receipt), &found); err != nil || !found.OK || len(found.Rows) != 3 || !strings.Contains(receipt, observed) {
				t.Errorf("history tool did not return the complete original records: %s (%v)", receipt, err)
			}
			sources := make([]knowledgeSourceRef, 0, len(found.Rows)+1)
			for _, row := range found.Rows {
				sources = append(sources, row.knowledgeSourceRef)
			}
			sources = append(sources, knowledgeSourceRef{URI: "https://example.test/record"})
			data := write("first", "First entry")
			data["aliases"] = []string{"Earlier entry"}
			data["links"] = []knowledge.Link{{Rel: "related", To: "second"}}
			data["sources"] = sources
			send(w, request, "knowledge-create-first", ToolRecordKnowledge, data)
		case 6:
			receipt := nuwaValidationReceipt(request, "knowledge-create-first")
			for _, want := range []string{`"ok":true`, `"classification":"primary"`, `"classification":"analysis"`, `"classification":"external"`} {
				if !strings.Contains(receipt, want) {
					t.Errorf("write response omitted %s: %s", want, receipt)
				}
			}
			data := write("duplicate", "Earlier ENTRY")
			send(w, request, "knowledge-duplicate", ToolRecordKnowledge, data)
		case 7:
			receipt := nuwaValidationReceipt(request, "knowledge-duplicate")
			if !strings.Contains(receipt, `"code":"duplicate_page"`) || !strings.Contains(receipt, `"id":"first"`) {
				t.Errorf("duplicate did not return the existing page: %s", receipt)
			}
			send(w, request, "knowledge-create-second", ToolRecordKnowledge, write("second", "Second entry"))
		case 8:
			send(w, request, "knowledge-backlinks", ToolReadKnowledge, map[string]any{"action": "page", "id": "second"})
		case 9:
			var result knowledgeReadReply
			if err := json.Unmarshal([]byte(nuwaValidationReceipt(request, "knowledge-backlinks")), &result); err != nil ||
				result.Page == nil || len(result.Page.Incoming) != 1 || result.Page.Incoming[0].Page == nil || result.Page.Incoming[0].Page.ID != "first" {
				t.Errorf("backlink did not cross the real tool boundary: %+v err=%v", result, err)
			}
			writeContextFlowReply(w, request.Stream, map[string]any{"content": "Reviewed and recorded the retained evidence."}, "stop")
		default:
			t.Errorf("unexpected manager request %d", step)
			writeContextFlowReply(w, request.Stream, map[string]any{"content": "Finished."}, "stop")
		}
	})
	project, err := e.CreateProject("Knowledge fixture", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	projectID = project.ID
	peer := knowledgeFixtureThread(t, e, project.ID, "Earlier conversation")
	peerID = peer.ID
	foreign := knowledgeFixtureThread(t, e, "", "Foreign conversation")
	foreignID = foreign.ID
	current := knowledgeFixtureThread(t, e, project.ID, "Distill retained evidence")
	sourceTurn := &store.Turn{ThreadID: peer.ID, ProjectID: project.ID}
	if err := e.store.CreateTurn(sourceTurn); err != nil {
		t.Fatal(err)
	}
	if err := e.Store().AppendMessages(peer.ID, sourceTurn.ID, []store.Message{
		{Role: "user", Source: store.MessageSourceHuman, Content: observed},
		{Role: "assistant", Content: "The inferred value may also apply elsewhere."},
	}); err != nil {
		t.Fatal(err)
	}
	if err := e.Store().AppendMessages(foreign.ID, "", []store.Message{{Role: "user", Content: "Foreign evidence"}}); err != nil {
		t.Fatal(err)
	}
	originalTime := time.Date(2024, 2, 3, 4, 5, 6, 7, time.UTC)
	originalEvent := &store.Event{ThreadID: peer.ID, TurnID: sourceTurn.ID, Kind: swarm.NotifyToolResult.String(),
		Text: "Original tool output"}
	if err := e.store.AppendEvent(originalEvent); err != nil {
		t.Fatal(err)
	}
	eventSeq = originalEvent.Seq
	if err := e.store.DB().Model(originalEvent).Update("created_at", originalTime).Error; err != nil {
		t.Fatal(err)
	}
	if err := e.store.FinishTurn(sourceTurn.ID, store.TurnDone, "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := e.EnableProjectKnowledge(project.ID); err != nil {
		t.Fatal(err)
	}
	turn, err := e.StartTurn(current.ID, "Distill the earlier conversation in this project.")
	if err != nil {
		t.Fatal(err)
	}
	if got := waitForTurn(t, e, turn.ID); got.Status != store.TurnDone || len(wire.snapshot()) != 9 {
		t.Fatalf("distillation did not finish through live Registry: %+v", got)
	}
	waitSettled(t, e, current.ID)
	page, err := e.ProjectKnowledgePage(t.Context(), projectID, "first")
	if err != nil || len(page.Sources) != 4 || page.UpdatedTurn != turn.ID || page.UpdatedThread != current.ID {
		t.Fatalf("attributed page: %+v err=%v", page, err)
	}
	sources := page.Sources
	if sources[0].Classification != "primary" || sources[1].Classification != "analysis" ||
		sources[2].Classification != "primary" || !sources[2].Timestamp.Equal(originalTime) ||
		sources[2].TurnID != sourceTurn.ID || sources[2].Available == nil || !*sources[2].Available ||
		sources[3].Classification != "external" || sources[3].Available != nil {
		t.Fatalf("original identity and timestamps were not projected: %+v", sources)
	}
	kb, err := e.ProjectKnowledge(project.ID)
	if err != nil {
		t.Fatal(err)
	}
	snap, err := kb.Snapshot()
	if err != nil || snap.Status(time.Now()).PageCount != 2 || len(snap.Status(time.Now()).Schema.Types) != 2 {
		t.Fatalf("partial changes or missing additive schema: %+v err=%v", snap, err)
	}

	// The original API path rewinds the peer. A citation stays visible and its
	// owner link becomes unavailable, while unrelated URI provenance survives.
	if err := e.applyRewind(peer.ID, eventSeq); err != nil {
		t.Fatal(err)
	}
	replacement := &store.Turn{ThreadID: peer.ID, ProjectID: project.ID}
	if err := e.store.CreateTurn(replacement); err != nil {
		t.Fatal(err)
	}
	e.store.ResetSequences() // a cold sequence cache can reuse a deleted transcript seq
	if err := e.store.AppendMessages(peer.ID, replacement.ID, []store.Message{
		{Role: "user", Source: store.MessageSourceHuman, Content: "Different source at the reused sequence"},
	}); err != nil {
		t.Fatal(err)
	}
	messages, err := e.store.ListMessages(peer.ID)
	if err != nil || len(messages) != 1 || messages[0].Seq != sources[0].Seq {
		t.Fatalf("replacement must reuse the original message sequence: %+v err=%v", messages, err)
	}
	if err := e.store.DB().Model(&messages[0]).Update("created_at", sources[0].Timestamp).Error; err != nil {
		t.Fatal(err)
	}
	if err := e.store.FinishTurn(replacement.ID, store.TurnDone, "Replaced.", ""); err != nil {
		t.Fatal(err)
	}
	page, err = e.ProjectKnowledgePage(t.Context(), projectID, "first")
	if err != nil || len(page.Sources) != 4 {
		t.Fatalf("rewind removed the page or references: %+v err=%v", page, err)
	}
	for _, source := range page.Sources[:3] {
		if source.Available == nil || *source.Available || source.TurnID != "" || source.Timestamp.IsZero() {
			t.Errorf("rewound source was not retained as unavailable: %+v", source)
		}
	}
}

func knowledgeFixtureThread(t *testing.T, e *Engine, projectID, title string) *store.Thread {
	t.Helper()
	thread, err := e.CreateThread(title, "", projectID)
	if err != nil {
		t.Fatal(err)
	}
	return thread
}

func knowledgeSystemText(request scheduleCompletionRequest) string {
	var sections []string
	for _, message := range request.Messages {
		if message.Role == schema.System {
			sections = append(sections, message.Content)
		}
	}
	return strings.Join(sections, "\n")
}

func TestKnowledgeToolsFollowPlanningAndNuwaAtManagerAndWorkerHTTP(t *testing.T) {
	for _, mode := range []struct {
		name           string
		nuwa, planning bool
	}{
		{"ordinary", false, false}, {"ordinary plan", false, true},
		{"Nuwa", true, false}, {"Nuwa plan", true, true},
	} {
		t.Run(mode.name, func(t *testing.T) {
			e := newTestEngine(t)
			e.cfg.Swarm.AutoTitle = false
			var managerSteps, workerSteps atomic.Int32
			endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request scheduleCompletionRequest
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				system := knowledgeSystemText(request)
				if replyNoScheduleClaimsFixture(t, w, system, request.Stream, len(request.Tools)) {
					return
				}
				offered := nuwaPlannerOfferedTools(request)
				if !offered[ToolReadKnowledge] || offered[ToolRecordKnowledge] == mode.planning {
					t.Errorf("knowledge tools violate the actual mode: %v", offered)
				}
				if !strings.Contains(system, "## Project knowledge") ||
					strings.Contains(system, "use it for durable knowledge about the subject") == mode.planning {
					t.Errorf("actual System prompt did not match knowledge scope: %s", system)
				}
				finish := func(text string) {
					writeContextFlowReply(w, request.Stream, map[string]any{"content": text}, "stop")
				}
				if strings.Contains(system, "The shared workspace directory is:") {
					switch workerSteps.Add(1) {
					case 1:
						name, args := ToolRecordKnowledge, `{"action":"write_page","id":"entry-a","type":"entry","title":"Entry A","body":"Stored by the worker"}`
						if mode.planning {
							name, args = ToolReadKnowledge, `{"action":"lint"}`
						}
						nuwaValidationTool(w, request, "worker-knowledge-action", name, args)
					case 2:
						receipt := nuwaValidationReceipt(request, "worker-knowledge-action")
						if !strings.Contains(receipt, `"ok":true`) {
							t.Errorf("worker's actual knowledge call was refused: %s", receipt)
						}
						finish("Worker completed the scoped action.")
					default:
						t.Error("unexpected worker invocation")
						finish("Worker finished.")
					}
					return
				}
				if offered["record_work"] != (!mode.planning) {
					t.Errorf("work records do not match planning write admission in %s", mode.name)
				}
				switch managerSteps.Add(1) {
				case 1:
					nuwaValidationTool(w, request, "spawn-knowledge-worker", "spawn_agent", `{"role":"reader","task":"Inspect available project knowledge."}`)
				case 2:
					var spawned toolScopeWorkerID
					if err := json.Unmarshal([]byte(nuwaValidationReceipt(request, "spawn-knowledge-worker")), &spawned); err != nil || spawned.AgentID == "" {
						t.Errorf("worker spawn failed: %+v err=%v", spawned, err)
						finish("Could not delegate.")
						return
					}
					args, _ := json.Marshal(map[string]any{"agent_ids": []string{spawned.AgentID}, "timeout_s": 5})
					nuwaValidationTool(w, request, "wait-knowledge-worker", "wait_agents", string(args))
				default:
					if receipt := nuwaValidationReceipt(request, "wait-knowledge-worker"); !strings.Contains(receipt, "Worker completed the scoped action") {
						t.Errorf("worker result did not reach actual manager: %s", receipt)
					}
					finish("Collected the worker's result.")
				}
			}))
			t.Cleanup(endpoint.Close)
			e.cfg.Models.Providers[0].BaseURL = endpoint.URL
			e.cfg.Models.Providers[0].Model = "offline-knowledge-scope"
			e.pool = provider.New(e.cfg)
			project, err := e.CreateProject("Knowledge tool scope", "", "", false, mode.nuwa)
			if err != nil {
				t.Fatal(err)
			}
			thread := knowledgeFixtureThread(t, e, project.ID, "Scoped task")
			if mode.planning {
				if err := e.SetPlanMode(thread.ID, true); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := e.EnableProjectKnowledge(project.ID); err != nil {
				t.Fatal(err)
			}
			turn, err := e.StartTurn(thread.ID, "Delegate a scoped knowledge action.")
			if err != nil {
				t.Fatal(err)
			}
			got := waitForTurn(t, e, turn.ID)
			waitSettled(t, e, thread.ID)
			if got.Status != store.TurnDone || workerSteps.Load() != 2 || managerSteps.Load() != 3 {
				t.Fatalf("knowledge worker lifecycle: turn=%+v worker=%d manager=%d", got, workerSteps.Load(), managerSteps.Load())
			}
			kb, err := e.ProjectKnowledge(project.ID)
			if err != nil {
				t.Fatal(err)
			}
			snap, err := kb.Snapshot()
			if err != nil || (snap.Status(time.Now()).PageCount == 0) != mode.planning {
				t.Fatalf("worker effect did not obey planning: snapshot=%+v err=%v", snap, err)
			}
		})
	}
}

func TestEnablingKnowledgeMidTurnLeavesDisabledPromptAndToolsUnchanged(t *testing.T) {
	var e *Engine
	var projectID, before string
	e, _ = newScheduleCompletionHTTPEngine(t, func(w http.ResponseWriter, _ *http.Request, step int, request scheduleCompletionRequest) {
		system := knowledgeSystemText(request)
		offered := nuwaPlannerOfferedTools(request)
		wantKnowledge := step > 2
		if offered[ToolReadKnowledge] != wantKnowledge || offered[ToolRecordKnowledge] != wantKnowledge ||
			strings.Contains(system, "## Project knowledge") != wantKnowledge ||
			strings.Contains(system, "With project knowledge enabled") != wantKnowledge {
			t.Errorf("knowledge leaked across a turn boundary at step %d", step)
		}
		switch step {
		case 1:
			before = system
			if _, err := e.EnableProjectKnowledge(projectID); err != nil {
				t.Error(err)
			}
			nuwaValidationTool(w, request, "inspect-original-metadata", ToolQueryHistory, "{}")
			return
		case 2:
			if before != system {
				t.Error("enabling knowledge changed the in-flight disabled System prompt")
			}
		}
		writeContextFlowReply(w, request.Stream, map[string]any{"content": "Finished checking the scope."}, "stop")
	})
	e.cfg.Memory.Enabled = true
	e.cfg.Memory.AutoReview = false
	project, err := e.CreateProject("Snapshot fixture", "", "", true)
	if err != nil {
		t.Fatal(err)
	}
	projectID = project.ID
	thread := knowledgeFixtureThread(t, e, projectID, "Scope check")
	for _, text := range []string{"Check the initial scope.", "Check the scope on a new turn."} {
		turn, err := e.StartTurn(thread.ID, text)
		if err != nil {
			t.Fatal(err)
		}
		if got := waitForTurn(t, e, turn.ID); got.Status != store.TurnDone {
			t.Fatalf("turn not done: %+v", got)
		}
		waitSettled(t, e, thread.ID)
	}
}

func TestKnowledgeAgentRevisionRenameAndDeleteKeepTheOldSourcesUntilExplicitChange(t *testing.T) {
	var firstRevision string
	e, httpFixture := newScheduleCompletionHTTPEngine(t, func(w http.ResponseWriter, _ *http.Request, step int, request scheduleCompletionRequest) {
		expect := func(call, contains string) {
			if reply := nuwaValidationReceipt(request, call); !strings.Contains(reply, contains) {
				t.Errorf("%s result should contain %q: %s", call, contains, reply)
			}
		}
		call := func(id string, args map[string]any) {
			raw, _ := json.Marshal(args)
			nuwaValidationTool(w, request, id, ToolRecordKnowledge, string(raw))
		}
		switch step {
		case 1:
			call("forged-source", map[string]any{"action": "write_page", "id": "invented", "type": "entry", "title": "Invented",
				"sources": []map[string]any{{"uri": "https://example.test", "timestamp": "2000-01-01"}}})
		case 2:
			expect("forged-source", "unknown field")
			call("ambiguous-source", map[string]any{"action": "write_page", "id": "invented", "type": "entry", "title": "Invented",
				"sources": []map[string]any{{"uri": "https://example.test", "kind": "event", "thread_id": "thread", "seq": 1}}})
		case 3:
			expect("ambiguous-source", "URI or history locator")
			call("rename", map[string]any{"action": "write_page", "id": "entry-a", "type": "entry", "title": "Renamed entry",
				"body": "Updated body", "revision": firstRevision})
		case 4:
			expect("rename", `"title":"Renamed entry"`)
			expect("rename", `"thread_id":"deleted-thread"`)
			call("stale-delete", map[string]any{"action": "delete_page", "id": "entry-a", "revision": firstRevision})
		case 5:
			expect("stale-delete", "revision conflict")
			var receipt struct {
				OK   bool                      `json:"ok"`
				Page struct{ Revision string } `json:"page"`
			}
			if err := json.Unmarshal([]byte(nuwaValidationReceipt(request, "rename")), &receipt); err != nil || !receipt.OK || receipt.Page.Revision == "" {
				t.Errorf("rename did not return a usable revision: %+v err=%v", receipt, err)
			}
			call("delete", map[string]any{"action": "delete_page", "id": "entry-a", "revision": receipt.Page.Revision})
		case 6:
			expect("delete", `"deleted":"entry-a"`)
			call("append-without-data", map[string]any{"action": "append_schema"})
		case 7:
			expect("append-without-data", "requires add")
			call("stale-schema", map[string]any{"action": "append_schema", "revision": "old", "add": map[string]any{
				"types": []map[string]string{{"id": "variant", "dir": "variants"}},
			}})
		case 8:
			expect("stale-schema", "revision conflict")
			writeContextFlowReply(w, request.Stream, map[string]any{"content": "Completed the revision-checked change."}, "stop")
		default:
			t.Errorf("unexpected request %d", step)
			writeContextFlowReply(w, request.Stream, map[string]any{"content": "Finished."}, "stop")
		}
	})
	project, err := e.CreateProject("Knowledge mutation fixture", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	thread := knowledgeFixtureThread(t, e, project.ID, "Update one entry")
	if _, err := e.EnableProjectKnowledge(project.ID); err != nil {
		t.Fatal(err)
	}
	kb, err := e.ProjectKnowledge(project.ID)
	if err != nil {
		t.Fatal(err)
	}
	first, err := kb.WritePage(knowledge.PageWrite{Page: knowledge.Page{ID: "entry-a", Type: "entry", Title: "Original entry",
		Sources: []knowledge.Source{{Kind: "event", ThreadID: "deleted-thread", Seq: 1, Classification: "primary", Timestamp: time.Now()}}}},
		knowledge.Stamp{UpdatedAt: time.Now(), UpdatedTurn: "deleted-turn"})
	if err != nil {
		t.Fatal(err)
	}
	firstRevision = first.Revision
	turn, err := e.StartTurn(thread.ID, "Rename the existing page then remove it with the current revision.")
	if err != nil {
		t.Fatal(err)
	}
	got := waitForTurn(t, e, turn.ID)
	waitSettled(t, e, thread.ID)
	if got.Status != store.TurnDone || len(httpFixture.snapshot()) != 8 {
		t.Fatalf("actual revision changes did not complete: %+v", got)
	}
	snap, err := kb.Snapshot()
	if err != nil || snap.Status(time.Now()).PageCount != 0 || len(snap.Status(time.Now()).Schema.Types) != 1 {
		t.Fatalf("refused mutations left a side effect: %+v err=%v", snap, err)
	}
}
