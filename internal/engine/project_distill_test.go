package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	swarm "github.com/LubyRuffy/eino-swarm"
	"github.com/LubyRuffy/eino-swarm/internal/knowledge"
	"github.com/LubyRuffy/eino-swarm/internal/provider"
	"github.com/LubyRuffy/eino-swarm/internal/store"
)

func distillWire(t *testing.T, serve func(http.ResponseWriter, scheduleCompletionRequest, int)) (*Engine, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request scheduleCompletionRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			http.Error(w, "invalid fixture request", http.StatusBadRequest)
			return
		}
		serve(w, request, int(calls.Add(1)))
	}))
	t.Cleanup(endpoint.Close)
	e := newTestEngine(t)
	e.cfg.Models.Providers[0].BaseURL = endpoint.URL
	e.cfg.Models.Providers[0].Model = "fixture"
	e.pool = provider.New(e.cfg)
	return e, &calls
}

func distillEvent(t *testing.T, e *Engine, th *store.Thread, status, kind, text string) (store.Turn, store.Event) {
	t.Helper()
	turn := store.Turn{ThreadID: th.ID, ProjectID: th.ProjectID, ProviderID: th.ProviderID, Status: status}
	if err := e.store.CreateTurn(&turn); err != nil {
		t.Fatal(err)
	}
	event := store.Event{ThreadID: th.ID, TurnID: turn.ID, Kind: kind, Text: text}
	if err := e.store.AppendEvent(&event); err != nil {
		t.Fatal(err)
	}
	return turn, event
}

func waitDistill(t *testing.T, e *Engine, id string) DistillationResult {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		got, err := e.ProjectDistillationStatus(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		if got.State != "running" {
			return got
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("distillation never left running")
	return DistillationResult{}
}

func TestDistillationKeepsPeerSequenceIdentityAndReportsActualPartialWrites(t *testing.T) {
	const first, second, analysis, foreign = "Preserve the working convention.", "Peer observed distinct material.", "A hypothesis remains open.", "Private unrelated evidence."
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	e, calls := distillWire(t, func(w http.ResponseWriter, req scheduleCompletionRequest, step int) {
		<-release
		source := ""
		for _, message := range req.Messages {
			if message.Role == "user" {
				source += message.Content
			}
		}
		if !strings.Contains(source, first) || !strings.Contains(source, second) || !strings.Contains(source, analysis) ||
			strings.Contains(source, foreign) || strings.Count(source, "event_seq=1 ") != 2 {
			t.Errorf("wrong captured scope: %s", source)
		}
		if step == 2 {
			if !strings.Contains(req.Messages[0].Content, "A previous attempt may have saved part of this batch.") {
				t.Error("resuming a partially published batch did not ask the reviewer to inspect the saved destinations")
			}
			nuwaValidationTool(w, req, "resume", distillBatchName, `{"changes":[],"knowledge":[
				{"action":"write_page","id":"peer","type":"entry","title":"Peer","body":"Peer observed distinct material.","evidence":[{"seq":2,"quote":"Peer observed distinct material."}]}
			]}`)
			return
		}
		if step != 1 {
			t.Errorf("unexpected additional model call: %d", step)
		}
		nuwaValidationTool(w, req, "distill", distillBatchName, `{
		  "changes":[{"target":"memory","action":"add","content":"Preserve the working convention.","evidence":[{"seq":1,"quote":"Preserve the working convention."}]}],
		  "knowledge":[
		    {"action":"write_page","id":"hypothesis","type":"entry","title":"Hypothesis","body":"A hypothesis remains open.","evidence":[{"seq":3,"quote":"A hypothesis remains open."}]},
		    {"action":"write_page","id":"existing","type":"entry","title":"Existing","body":"Another observation","revision":"stale","evidence":[{"seq":2,"quote":"Peer observed distinct material."}]}
		  ]
		}`)
	})
	project, err := e.CreateProject("Scoped review", "", "", true)
	if err != nil {
		t.Fatal(err)
	}
	a := knowledgeFixtureThread(t, e, project.ID, "First")
	b := knowledgeFixtureThread(t, e, project.ID, "Peer")
	other := knowledgeFixtureThread(t, e, "", "Foreign")
	distillEvent(t, e, a, store.TurnDone, KindUser, first)
	anchor, _ := distillEvent(t, e, b, store.TurnDone, KindUser, second)
	observed := store.Event{ThreadID: b.ID, TurnID: anchor.ID, Kind: swarm.NotifyAgentMessage.String(), Text: analysis}
	if err := e.store.AppendEvent(&observed); err != nil {
		t.Fatal(err)
	}
	outside := store.Turn{ThreadID: other.ID, Status: store.TurnDone}
	if err := e.store.CreateTurn(&outside); err != nil {
		t.Fatal(err)
	}
	if err := e.store.AppendEvent(&store.Event{ThreadID: other.ID, TurnID: outside.ID, Kind: KindUser, Text: foreign}); err != nil {
		t.Fatal(err)
	}
	kb, err := e.ProjectKnowledge(project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := kb.Enable(); err != nil {
		t.Fatal(err)
	}
	existing, err := kb.WritePage(knowledge.PageWrite{Page: knowledge.Page{ID: "existing", Type: "entry", Title: "Existing", Body: "Keep original."}},
		knowledge.Stamp{UpdatedTurn: anchor.ID, UpdatedThread: b.ID, UpdatedAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	started, err := e.StartProjectDistillation(t.Context(), project.ID, DistillationRequest{Scope: "project"})
	if err != nil {
		t.Fatal(err)
	}
	repeated, err := e.StartProjectDistillation(t.Context(), project.ID, DistillationRequest{Scope: "latest_turn", ThreadID: a.ID})
	if err != nil || repeated.ReviewID != started.ReviewID || repeated.Scope != "project" {
		t.Fatalf("duplicate did not resolve to current run: %+v, %v", repeated, err)
	}
	unblock()
	result := waitDistill(t, e, project.ID)
	if result.State != "partial" || len(result.Changes) != 1 || len(result.Knowledge) != 1 || calls.Load() != 1 {
		t.Fatalf("wrong partial outcome: %+v / requests %d", result, calls.Load())
	}
	retained, err := e.ProjectKnowledgePage(t.Context(), project.ID, existing.ID)
	if err != nil || retained.Revision != existing.Revision {
		t.Fatalf("partial run overwrote a competing revision: %+v / %v", retained, err)
	}
	page, err := e.ProjectKnowledgePage(t.Context(), project.ID, "hypothesis")
	if err != nil || len(page.Sources) != 1 || page.Sources[0].Classification != "analysis" || page.Sources[0].ThreadID != b.ID {
		t.Fatalf("analysis did not retain its precise origin: %+v / %v", page, err)
	}
	if result.Progress.Processed != 0 || !result.Uncertain {
		t.Fatalf("partial file writes advanced the cursor: %+v", result)
	}
	if _, err := e.StartProjectDistillation(t.Context(), project.ID, DistillationRequest{ResumeReviewID: result.ReviewID}); err != nil {
		t.Fatal(err)
	}
	repaired := waitDistill(t, e, project.ID)
	if repaired.State != "completed" || repaired.Uncertain || repaired.Progress.Processed != 3 ||
		len(repaired.Changes) != 1 || len(repaired.Knowledge) != 2 || calls.Load() != 2 {
		t.Fatalf("resume lost saved work or counted it again: %+v", repaired)
	}
}

func TestDistillationRevalidatesEditedSteerBeforeAnyWrite(t *testing.T) {
	ready, proceed := make(chan struct{}), make(chan struct{})
	var proceedOnce sync.Once
	unblock := func() { proceedOnce.Do(func() { close(proceed) }) }
	defer unblock()
	e, _ := distillWire(t, func(w http.ResponseWriter, req scheduleCompletionRequest, step int) {
		if step == 1 {
			close(ready)
			<-proceed
			nuwaValidationTool(w, req, "stale", distillBatchName, `{
				"changes":[{"target":"memory","action":"add","content":"Preserve the previous wording.","evidence":[{"seq":1,"quote":"Preserve the previous wording."}]}],
				"knowledge":[]
			}`)
			return
		}
		if receipt := nuwaValidationReceipt(req, "stale"); !strings.Contains(receipt, "steer source 1 changed") {
			t.Errorf("the actual tool accepted modified evidence: %s", receipt)
		}
		writeContextFlowReply(w, req.Stream, map[string]any{"content": "Evidence changed."}, "stop")
	})
	p, err := e.CreateProject("Mutable source", "", "", true)
	if err != nil {
		t.Fatal(err)
	}
	th := knowledgeFixtureThread(t, e, p.ID, "Mutable")
	turn, event := distillEvent(t, e, th, store.TurnDone, KindSteer, "Preserve the previous wording.")
	if err := e.store.AppendMessages(th.ID, turn.ID, []store.Message{
		{Role: "user", EventSeq: event.Seq, Content: steerModelCaption(event.Text)},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.StartProjectDistillation(t.Context(), p.ID, DistillationRequest{Scope: "thread", ThreadID: th.ID}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ready:
	case <-time.After(4 * time.Second):
		t.Fatal("reviewer never reached HTTP")
	}
	if err := e.store.ReviseSteerMessage(th.ID, event.Seq, steerModelCaption("Replace the earlier instruction.")); err != nil {
		t.Fatal(err)
	}
	unblock()
	result := waitDistill(t, e, p.ID)
	if result.State != "failed" || !strings.Contains(result.Error, "steer source 1 changed") || len(result.Changes) != 0 {
		t.Fatalf("modified source resulted in a write: %+v", result)
	}
}

func TestDistillationMissingCompletionIsInterruptedWithoutReplay(t *testing.T) {
	e := newTestEngine(t)
	p, err := e.CreateProject("Interrupted review", "", "", true)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	saved := DistillationResult{ReviewID: "old-process", Scope: "project", State: "running"}
	row, err := saved.row(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.StartProjectDistillation(ctx, row); err != nil {
		t.Fatal(err)
	}
	status, err := e.ProjectDistillationStatus(ctx, p.ID)
	if err != nil || status.State != "interrupted" || !status.Uncertain || status.ReviewID != saved.ReviewID {
		t.Fatalf("dead process was left running: %+v, %v", status, err)
	}
	if err := e.DeleteProject(p.ID); err != nil {
		t.Fatal(err)
	}
	if persisted, err := e.store.ProjectDistillationReceipt(ctx, p.ID); err != nil || persisted != nil {
		t.Fatalf("project left behind a receipt: %+v, %v", persisted, err)
	}
}

func TestDistillationScopesKeepRetainedSourcesAndHumanOnlyFailedTurns(t *testing.T) {
	e := newTestEngine(t)
	project, err := e.CreateProject("Subject", "", "", true)
	if err != nil {
		t.Fatal(err)
	}
	current := knowledgeFixtureThread(t, e, project.ID, "Current")
	peer := knowledgeFixtureThread(t, e, project.ID, "Peer")
	older, _ := distillEvent(t, e, current, store.TurnDone, KindUser, "First working rule.")
	latest, _ := distillEvent(t, e, current, store.TurnDone, KindUser, "Latest working rule.")
	distillEvent(t, e, current, store.TurnRunning, KindUser, "Running text.")
	failure, _ := distillEvent(t, e, peer, store.TurnError, KindUser, "Retain this requirement even after failure.")
	if err := e.store.AppendEvent(&store.Event{ThreadID: peer.ID, TurnID: failure.ID, Kind: swarm.NotifyAgentMessage.String(), Text: "Unsuccessful guess."}); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		thread string
		latest bool
		want   int
	}{
		{current.ID, true, 1},
		{current.ID, false, 2},
		{"", false, 3},
	} {
		t.Run(fmt.Sprintf("sources-%d", tc.want), func(t *testing.T) {
			turns, omitted, err := e.store.ProjectDistillationHistory(t.Context(), project.ID, tc.thread, tc.latest, distillEventKinds)
			input := selectDistillationSources(turns, omitted)
			if err != nil || input.omitted || len(input.memory) != tc.want {
				t.Fatalf("wrong bounded history: %+v, %v", input, err)
			}
			for key, source := range input.sources {
				_, wiki := input.wiki[key]
				if source.failed == wiki || source.event.TurnID != latest.ID && tc.latest ||
					strings.Contains(source.event.Text, "Running") || strings.Contains(source.event.Text, "guess") ||
					(source.event.TurnID == older.ID && tc.latest) {
					t.Fatalf("wrong evidence authority: %+v wiki=%v", source, wiki)
				}
			}
		})
	}
}

func TestDistillationFollowsCurrentOwnershipBeforeWriteAndTrace(t *testing.T) {
	ready, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	e, _ := distillWire(t, func(w http.ResponseWriter, req scheduleCompletionRequest, step int) {
		if step == 1 {
			close(ready)
			<-release
			nuwaValidationTool(w, req, "old-owner", distillBatchName, `{
				"changes":[{"target":"memory","action":"add","content":"Keep the old project rule.","evidence":[{"seq":1,"quote":"Keep the old project rule."}]}],
				"knowledge":[]
			}`)
			return
		}
		writeContextFlowReply(w, req.Stream, map[string]any{"content": "The project scope changed."}, "stop")
	})
	old, err := e.CreateProject("Original owner", "", "", true)
	if err != nil {
		t.Fatal(err)
	}
	next, err := e.CreateProject("New owner", "", "", true)
	if err != nil {
		t.Fatal(err)
	}
	th := knowledgeFixtureThread(t, e, old.ID, "Moved")
	turn, _ := distillEvent(t, e, th, store.TurnDone, KindUser, "Keep the old project rule.")
	if _, err := e.StartProjectDistillation(t.Context(), old.ID, DistillationRequest{Scope: "thread", ThreadID: th.ID}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ready:
	case <-time.After(4 * time.Second):
		t.Fatal("reviewer never reached HTTP")
	}
	if err := e.MoveThread(th.ID, next.ID); err != nil {
		t.Fatal(err)
	}
	unblock()
	result := waitDistill(t, e, old.ID)
	if result.State != "failed" || result.ThreadID != "" || result.TurnID != "" || len(result.Changes) != 0 {
		t.Fatalf("stale review still claimed ownership: %+v", result)
	}
	trace, err := e.store.ListLLMCalls(turn.ID)
	if err != nil || len(trace) != 0 {
		t.Fatalf("late project review was attributed across ownership: %+v, %v", trace, err)
	}
}

func TestDistillationReadsWikiBeforeAppendAndKeepsOlderSources(t *testing.T) {
	const evidence = "Useful source for the current material."
	var schemaRev string
	var pageRev string
	e, calls := distillWire(t, func(w http.ResponseWriter, req scheduleCompletionRequest, step int) {
		switch step {
		case 1:
			nuwaValidationTool(w, req, "schema", ToolReadKnowledge, `{"action":"schema"}`)
		case 2:
			var reply knowledgeReadReply
			if err := json.Unmarshal([]byte(nuwaValidationReceipt(req, "schema")), &reply); err != nil || reply.Status == nil || reply.Status.Revision != schemaRev {
				t.Errorf("review did not see its live project schema: %+v (%v)", reply, err)
			}
			nuwaValidationTool(w, req, "publish", distillBatchName, fmt.Sprintf(`{
			  "changes":[],
			  "knowledge":[
			    {"action":"append_schema","revision":%q,"add":{"types":[{"id":"variant","dir":"variants"}]},"evidence":[{"seq":1,"quote":%q}]},
			    {"action":"write_page","id":"existing","type":"entry","title":"Current material","body":"Updated material","revision":%q,"evidence":[{"seq":1,"quote":%q}]}
			  ]
			}`, schemaRev, evidence, pageRev, evidence))
		default:
			t.Errorf("unexpected closing model call %d", step)
			writeContextFlowReply(w, req.Stream, map[string]any{"content": "Finished."}, "stop")
		}
	})
	p, err := e.CreateProject("Only wiki", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	th := knowledgeFixtureThread(t, e, p.ID, "Evidence")
	turn, _ := distillEvent(t, e, th, store.TurnDone, KindUser, evidence)
	kb, err := e.ProjectKnowledge(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.StartProjectDistillation(t.Context(), p.ID, DistillationRequest{Scope: "project"}); err != ErrDistillationTargets {
		t.Fatalf("expected explicit enable before wiki-only review: %v", err)
	}
	snap, err := kb.Enable()
	if err != nil {
		t.Fatal(err)
	}
	schemaRev = snap.Status(time.Now()).Revision
	page, err := kb.WritePage(knowledge.PageWrite{Page: knowledge.Page{
		ID: "existing", Type: "entry", Title: "Previous title", Body: "Previous material",
		Sources: []knowledge.Source{{URI: "https://example.test/original"}},
	}}, knowledge.Stamp{UpdatedAt: time.Now(), UpdatedThread: th.ID, UpdatedTurn: turn.ID})
	if err != nil {
		t.Fatal(err)
	}
	pageRev = page.Revision
	if _, err := e.StartProjectDistillation(t.Context(), p.ID, DistillationRequest{Scope: "project"}); err != nil {
		t.Fatal(err)
	}
	receipt := waitDistill(t, e, p.ID)
	if receipt.State != "completed" || receipt.MemoryEnabled || len(receipt.Knowledge) != 2 || calls.Load() != 2 {
		t.Fatalf("invalid wiki-only result: %+v (%d calls)", receipt, calls.Load())
	}
	updated, err := e.ProjectKnowledgePage(t.Context(), p.ID, "existing")
	if err != nil || len(updated.Sources) != 2 || updated.Sources[0].URI != "https://example.test/original" ||
		updated.Sources[1].Classification != "primary" || updated.Title != "Current material" {
		t.Fatalf("revision update lost existing citations: %+v (%v)", updated, err)
	}
}

func TestDistillationToolEvidenceRequiresARealNonDerivedReceipt(t *testing.T) {
	evs := []store.Event{
		{Seq: 1, Kind: "tool_call", ToolCallID: "actual", Text: "inspect()"},
		{Seq: 2, Kind: "tool_result", ToolCallID: "actual", Text: "Observed material."},
		{Seq: 3, Kind: "tool_call", ToolCallID: "fail", Text: "inspect()"},
		{Seq: 4, Kind: "tool_result", ToolCallID: "fail", Text: `{"ok":false,"error":"rejected"}`},
		{Seq: 5, Kind: "tool_call", ToolCallID: "derived", Text: "record_knowledge()"},
		{Seq: 6, Kind: "tool_result", ToolCallID: "derived", Text: "Previous notes"},
		{Seq: 7, Kind: "tool_result", ToolCallID: "unpaired", Text: "Unpaired output"},
	}
	got := selectDistillationSources([]store.DistillationTurn{{Turn: store.Turn{Status: store.TurnDone}, Events: evs}}, false)
	if len(got.sources) != 1 || len(got.memory) != 1 || got.sources[2].event.Seq != 2 ||
		strings.Contains(got.transcript, "rejected") || strings.Contains(got.transcript, "Previous notes") {
		t.Fatalf("unexpected tool authority: %+v", got)
	}
}

func TestDistillationRefusesForeignAndMalformedProposalsBeforeAcceptingCanonicalSteer(t *testing.T) {
	const quote = "Preserve the current observation."
	proposals := []struct{ args, refusal string }{
		{`{"changes":[]}`, "provide both changes and knowledge arrays"},
		{`{"changes":[{"target":"memory","action":"add","content":"Sample","evidence":[{"seq":71,"quote":"Source from elsewhere."}]}],"knowledge":[]}`, "outside this target's evidence"},
		{`{"changes":[],"knowledge":[{"action":"delete_page","id":"target","evidence":[]}]}`, "use write_page"},
		{`{"changes":[],"knowledge":[{"action":"write_page","id":"target","type":"entry","body":"Sample","evidence":[{"seq":1,"quote":"Not in the source."}]}]}`, "evidence validation failed"},
		{`{"changes":[{"target":"memory","action":"add","content":"There were 19 observations.","evidence":[{"seq":1,"quote":"Preserve the current observation."}]}],"knowledge":[]}`, "numeric"},
		{`{"changes":[],"knowledge":[{"action":"write_page","id":"missing","type":"entry","title":"Missing","body":"Example","revision":"stale","evidence":[{"seq":1,"quote":"Preserve the current observation."}]}]}`, "knowledge item not found"},
		{`{"changes":[],"knowledge":[{"action":"write_page","id":"target","type":"entry","title":"Target","body":"Preserve the current observation.","evidence":[{"seq":1,"quote":"Preserve the current observation."}]}]}`, ""},
	}
	e, requests := distillWire(t, func(w http.ResponseWriter, req scheduleCompletionRequest, step int) {
		if step > 1 {
			receipt := nuwaValidationReceipt(req, fmt.Sprintf("proposal-%d", step-1))
			if !strings.Contains(receipt, proposals[step-2].refusal) ||
				!strings.Contains(receipt, `"success":false`) {
				t.Errorf("guard did not refuse proposal %d: %s", step-1, receipt)
			}
		}
		if step <= len(proposals) {
			nuwaValidationTool(w, req, fmt.Sprintf("proposal-%d", step), distillBatchName, proposals[step-1].args)
		} else {
			writeContextFlowReply(w, req.Stream, map[string]any{"content": "Finished."}, "stop")
		}
	})
	e.cfg.Memory.ReviewMaxIterations = 10
	p, err := e.CreateProject("Protected writes", "", "", true)
	if err != nil {
		t.Fatal(err)
	}
	th := knowledgeFixtureThread(t, e, p.ID, "Steer evidence")
	turn, ev := distillEvent(t, e, th, store.TurnDone, KindSteer, quote)
	if err := e.store.AppendMessages(th.ID, turn.ID, []store.Message{{Role: "user", EventSeq: ev.Seq, Content: steerModelCaption(quote)}}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.StartProjectDistillation(t.Context(), p.ID, DistillationRequest{Scope: "thread", ThreadID: th.ID, EnableKnowledge: true}); err != nil {
		t.Fatal(err)
	}
	result := waitDistill(t, e, p.ID)
	if result.State != "completed" || len(result.Changes) != 0 || len(result.Knowledge) != 1 || int(requests.Load()) != len(proposals) {
		t.Fatalf("malformed proposals changed storage or exhausted the run: %+v, %d calls", result, requests.Load())
	}
	page, err := e.ProjectKnowledgePage(t.Context(), p.ID, "target")
	if err != nil || len(page.Sources) != 1 || page.Sources[0].Kind != "message" || page.Sources[0].Available == nil || !*page.Sources[0].Available {
		t.Fatalf("editable steer was not stamped as its canonical message: %+v, %v", page, err)
	}
	if err := e.store.ReviseSteerMessage(th.ID, ev.Seq, steerModelCaption("The observation changed.")); err != nil {
		t.Fatal(err)
	}
	page, err = e.ProjectKnowledgePage(t.Context(), p.ID, "target")
	if err != nil || len(page.Sources) != 1 || page.Sources[0].Available == nil || *page.Sources[0].Available {
		t.Fatalf("changed stamp remained available: %+v, %v", page, err)
	}
}

func TestDistillationCancelsAQueuedReviewAtShutdown(t *testing.T) {
	e := newTestEngine(t)
	p, err := e.CreateProject("Queued review", "", "", true)
	if err != nil {
		t.Fatal(err)
	}
	th := knowledgeFixtureThread(t, e, p.ID, "Queued evidence")
	distillEvent(t, e, th, store.TurnDone, KindUser, "Keep the existing observations.")
	gate := e.reviews.begin(p.ID)
	gate <- struct{}{}
	e.reviews.done()
	if _, err := e.StartProjectDistillation(t.Context(), p.ID, DistillationRequest{Scope: "project"}); err != nil {
		t.Fatal(err)
	}
	if !e.reviews.stop(time.Second) {
		t.Fatal("shutdown did not cancel the queued review")
	}
	<-gate
	result := waitDistill(t, e, p.ID)
	if result.State != "interrupted" || result.Uncertain {
		t.Fatalf("queued review never wrote and must report a known interruption: %+v", result)
	}
	if _, err := e.StartProjectDistillation(t.Context(), p.ID, DistillationRequest{Scope: "project"}); err != ErrRestarting {
		t.Fatalf("stopped reviewer pool accepted a new review: %v", err)
	}
}

func TestDistillationDoesNotRebaseNotesAfterAnIndependentEdit(t *testing.T) {
	ready, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	e, calls := distillWire(t, func(w http.ResponseWriter, req scheduleCompletionRequest, step int) {
		if step == 1 {
			close(ready)
			<-release
		}
		nuwaValidationTool(w, req, "notes-conflict", distillBatchName, `{
			"changes":[{"target":"memory","action":"add","content":"Retain the observed preference.","evidence":[{"seq":1,"quote":"Retain the observed preference."}]}],
			"knowledge":[]
		}`)
	})
	p, err := e.CreateProject("Concurrent notes", "", "", true)
	if err != nil {
		t.Fatal(err)
	}
	th := knowledgeFixtureThread(t, e, p.ID, "Notes")
	distillEvent(t, e, th, store.TurnDone, KindUser, "Retain the observed preference.")
	if _, err := e.StartProjectDistillation(t.Context(), p.ID, DistillationRequest{Scope: "latest_turn", ThreadID: th.ID}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ready:
	case <-time.After(4 * time.Second):
		t.Fatal("model never observed the notes snapshot")
	}
	if _, _, err := e.ProjectMemory(p.ID).Add("A human saved different notes."); err != nil {
		t.Fatal(err)
	}
	unblock()
	result := waitDistill(t, e, p.ID)
	notes, err := e.ProjectMemory(p.ID).Read()
	if err != nil || result.State != "failed" || result.Error == "" || len(result.Changes) != 0 ||
		calls.Load() != 1 || len(notes.Entries) != 1 || notes.Entries[0] != "A human saved different notes." {
		t.Fatalf("conflicted notes were rebased: %+v notes=%+v calls=%d err=%v", result, notes, calls.Load(), err)
	}
}

func TestDistillationDropsADeletedTraceAnchorBeforeModelInput(t *testing.T) {
	e, calls := distillWire(t, func(w http.ResponseWriter, req scheduleCompletionRequest, _ int) {
		t.Errorf("a deleted turn reached the model: %+v", req)
		http.Error(w, "unexpected request", http.StatusBadRequest)
	})
	p, err := e.CreateProject("Deleted anchor", "", "", true)
	if err != nil {
		t.Fatal(err)
	}
	th := knowledgeFixtureThread(t, e, p.ID, "Evidence to delete")
	distillEvent(t, e, th, store.TurnDone, KindUser, "Keep the earlier requirement.")
	gate := e.reviews.begin(p.ID)
	gate <- struct{}{}
	e.reviews.done()
	if _, err := e.StartProjectDistillation(t.Context(), p.ID, DistillationRequest{Scope: "project"}); err != nil {
		<-gate
		t.Fatal(err)
	}
	err = e.DeleteThread(th.ID)
	<-gate
	if err != nil {
		t.Fatal(err)
	}
	result := waitDistill(t, e, p.ID)
	if result.State != "failed" || result.TurnID != "" || calls.Load() != 0 {
		t.Fatalf("deleted turn still has reviewer work or a Trace: %+v requests=%d", result, calls.Load())
	}
	if _, err := e.StartProjectDistillation(t.Context(), p.ID, DistillationRequest{ResumeReviewID: result.ReviewID}); err != nil {
		t.Fatal(err)
	}
	finished := waitDistill(t, e, p.ID)
	if finished.State != "completed" || finished.Progress.Remaining != 0 || finished.Progress.Skipped != 1 || calls.Load() != 0 {
		t.Fatalf("resume did not account for the vanished source: %+v %+v", finished, finished.Progress)
	}
}
