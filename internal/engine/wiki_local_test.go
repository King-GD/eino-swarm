package engine

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	swarm "github.com/LubyRuffy/eino-swarm"
	"github.com/LubyRuffy/eino-swarm/internal/knowledge"
	"github.com/LubyRuffy/eino-swarm/internal/store"
)

const wikiLocalWindowBytes = 128 << 10

func TestWikiBuildDefaultsToLocalDocumentsWithoutModelRequests(t *testing.T) {
	const original = "See https://reference.example.test/release for the record."
	e, requests := distillWire(t, func(w http.ResponseWriter, req scheduleCompletionRequest, _ int) {
		nuwaValidationTool(w, req, "review", wikiExtractTool, `{"reviewed":[1],"nodes":[],"links":[]}`)
	})
	p, err := e.CreateProject("Local wiki", "", "", true)
	if err != nil {
		t.Fatal(err)
	}
	th := knowledgeFixtureThread(t, e, p.ID, "Local evidence")
	_, source := distillEvent(t, e, th, store.TurnDone, KindUser, original)
	gate := e.reviews.begin(p.ID)
	gate <- struct{}{}
	release := sync.OnceFunc(func() {
		<-gate
		e.reviews.done()
	})
	defer release()
	if _, err := e.StartProjectDistillation(t.Context(), p.ID, DistillationRequest{Scope: "thread", ThreadID: th.ID, WikiGraph: true}); err != nil {
		t.Fatal(err)
	}
	got := waitLocalWiki(t, e, p.ID)
	if calls := requests.Load(); calls != 0 {
		t.Fatalf("default construction sent %d requests to the actual model HTTP endpoint", calls)
	}
	if got.State != "completed" || got.WikiPhase != "documents" || !got.WikiScanComplete ||
		got.WikiDocuments != 1 || got.WikiDocumented != 1 || got.Progress.Remaining != 0 || got.Progress.Batches != 0 ||
		got.WikiReview == nil || *got.WikiReview {
		t.Fatalf("local run did not finish its scan and documents independently: %+v, progress: %+v", got, got.Progress)
	}
	kb, err := e.ProjectKnowledge(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	snap, err := kb.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	graph, err := snap.Graph()
	if err != nil || len(graph.Nodes) != 1 {
		t.Fatalf("local graph: %+v %v", graph, err)
	}
	page, err := e.ProjectKnowledgePage(t.Context(), p.ID, graph.Nodes[0].ID)
	if err != nil || len(page.WikiObservations) != 1 ||
		page.WikiObservations[0].Source.Seq != source.Seq || !strings.Contains(original, page.WikiObservations[0].Quote) {
		t.Fatalf("local article lost its original evidence: %+v %v", page, err)
	}
	release()
	if _, err := e.StartProjectDistillation(t.Context(), p.ID, DistillationRequest{
		Scope: "thread", ThreadID: th.ID, WikiGraph: true, WikiReview: new(true),
	}); err != nil {
		t.Fatal(err)
	}
	reviewed := waitLocalWiki(t, e, p.ID)
	if reviewed.State != "completed" || reviewed.Progress.Batches != 1 || requests.Load() != 1 ||
		reviewed.WikiReview == nil || !*reviewed.WikiReview {
		t.Fatalf("explicit review must cover the locally scanned source: %+v requests=%d", reviewed, requests.Load())
	}
	after, err := e.ProjectKnowledgePage(t.Context(), p.ID, graph.Nodes[0].ID)
	if err != nil || after.Revision != page.Revision {
		t.Fatalf("review rerendered an unchanged local document: %+v %v", after, err)
	}
}

func TestWikiBuildPublishesLocalNodesWhileTheModelIsWaiting(t *testing.T) {
	const quote = "The reference is https://example.test/notes."
	entered := make(chan struct{})
	release := make(chan struct{})
	var first, closed sync.Once
	defer closed.Do(func() { close(release) })
	e, _ := distillWire(t, func(w http.ResponseWriter, req scheduleCompletionRequest, _ int) {
		first.Do(func() { close(entered) })
		select {
		case <-release:
		case <-t.Context().Done():
			return
		}
		nuwaValidationTool(w, req, "review", wikiExtractTool, `{"reviewed":[1],"nodes":[],"links":[]}`)
	})
	p, err := e.CreateProject("Local observations", "", "", true)
	if err != nil {
		t.Fatal(err)
	}
	th := knowledgeFixtureThread(t, e, p.ID, "History")
	turn, source := distillEvent(t, e, th, store.TurnDone, KindUser, quote)
	if _, err := e.StartProjectDistillation(t.Context(), p.ID, DistillationRequest{Scope: "thread", ThreadID: th.ID, WikiGraph: true, WikiReview: new(true)}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("review did not reach the real model HTTP boundary")
	}
	got, err := e.ProjectWikiBuildStatus(t.Context(), p.ID)
	if err != nil || got.State != "running" || got.Progress.Processed != 0 {
		t.Fatalf("review stage must still be in progress: %+v %v", got, err)
	}
	wire, _ := json.Marshal(got)
	if !strings.Contains(string(wire), `"wiki_scan_complete":true`) {
		t.Fatalf("local scan did not finish before the first model call: %s", wire)
	}
	kb, err := e.ProjectKnowledge(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	snap, err := kb.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	graph, err := snap.Graph()
	if err != nil || len(graph.Nodes) == 0 {
		t.Fatalf("no local node was visible while the model was blocked: %+v %v", graph, err)
	}
	for _, node := range graph.Nodes {
		if node.Title != "example.test" {
			continue
		}
		page, err := e.ProjectKnowledgePage(t.Context(), p.ID, node.ID)
		if err != nil || len(page.Sources) == 0 || page.Sources[0].Seq != source.Seq || page.Sources[0].TurnID != turn.ID ||
			len(page.WikiObservations) != 1 || !strings.Contains(quote, page.WikiObservations[0].Quote) {
			t.Fatalf("local graph node lost its bound source: %+v %v", page, err)
		}
		// Manual edits to a Markdown quote or original record cannot hand
		// the frontend an excerpt that the current original does not contain.
		if err := e.store.DB().Exec("UPDATE events SET text=? WHERE id=?", "Changed source", source.ID).Error; err != nil {
			t.Fatal(err)
		}
		current, err := e.ProjectKnowledgePage(t.Context(), p.ID, node.ID)
		if err != nil || len(current.WikiObservations) != 0 {
			t.Fatalf("a stale quote was returned as original evidence: %+v %v", current, err)
		}
		return
	}
	t.Fatalf("local node title missing from the graph: %+v", graph.Nodes)
}

func TestWikiBuildResumesLegacyReceiptsWithTheirOriginalModelReview(t *testing.T) {
	for _, phase := range []string{"scan", "graph"} {
		t.Run(phase, func(t *testing.T) {
			e, requests := distillWire(t, func(w http.ResponseWriter, req scheduleCompletionRequest, _ int) {
				nuwaValidationTool(w, req, "legacy", wikiExtractTool, `{"reviewed":[1],"nodes":[],"links":[]}`)
			})
			p, err := e.CreateProject("Old wiki build", "", "", true)
			if err != nil {
				t.Fatal(err)
			}
			th := knowledgeFixtureThread(t, e, p.ID, "Original task")
			turn, event := distillEvent(t, e, th, store.TurnDone, KindUser, "Retain this observation.")
			if _, err := e.EnableProjectKnowledge(p.ID); err != nil {
				t.Fatal(err)
			}
			result := DistillationResult{
				ReviewID: "legacy-wiki", State: "interrupted", Scope: "thread",
				FullText: true, WikiGraph: true, WikiPhase: phase, KnowledgeEnabled: true,
				ThreadID: th.ID, TurnID: turn.ID,
			}
			if phase == "graph" {
				result.WikiScanComplete, result.WikiScanProcessed, result.WikiScanCursor = true, 1, event.ID
			}
			row := store.ProjectDistillation{
				ID: result.ReviewID, ProjectID: p.ID, State: result.State, WikiGraph: true, ProviderID: turn.ProviderID,
			}
			if _, err := e.store.StartDistillationQueue(t.Context(), row, th.ID, func(total int) ([]byte, error) {
				result.Progress = &DistillationProgress{Total: total, Remaining: total, CoverageKnown: true}
				return json.Marshal(result)
			}); err != nil {
				t.Fatal(err)
			}
			if _, err := e.StartProjectDistillation(t.Context(), p.ID, DistillationRequest{WikiGraph: true, ResumeReviewID: row.ID}); err != nil {
				t.Fatal(err)
			}
			got := waitLocalWiki(t, e, p.ID)
			if got.State != "completed" || got.ReviewID != row.ID || !got.WikiScanComplete ||
				got.Progress.Processed != 1 || got.Progress.Batches != 1 || requests.Load() != 1 {
				t.Fatalf("legacy %s checkpoint lost its review: %+v progress=%+v requests=%d", phase, got, got.Progress, requests.Load())
			}
		})
	}
}

func TestWikiLocalResumeKeepsPublishedNodesAfterCheckpointFailure(t *testing.T) {
	e, requests := distillWire(t, func(w http.ResponseWriter, req scheduleCompletionRequest, _ int) {
		nuwaValidationTool(w, req, "repair-resume", wikiExtractTool, `{"reviewed":[1],"nodes":[],"links":[]}`)
	})
	p, err := e.CreateProject("Local receipt recovery", "", "", true)
	if err != nil {
		t.Fatal(err)
	}
	th := knowledgeFixtureThread(t, e, p.ID, "Retained record")
	_, source := distillEvent(t, e, th, store.TurnDone, KindUser, "Recorded link https://persisted.example.test/reference")
	db := e.store.DB()
	if err := db.Exec(`CREATE TRIGGER refuse_local_scan_receipt BEFORE UPDATE OF receipt ON project_wiki_builds
		WHEN json_extract(NEW.receipt, '$.wiki_scan_cursor') > 0
		BEGIN SELECT RAISE(ABORT, 'fixture local receipt failure'); END`).Error; err != nil {
		t.Fatal(err)
	}
	req := DistillationRequest{Scope: "thread", ThreadID: th.ID, WikiGraph: true}
	started, err := e.StartProjectDistillation(t.Context(), p.ID, req)
	if err != nil {
		t.Fatal(err)
	}
	failed := waitLocalWiki(t, e, p.ID)
	if failed.State != "failed" || failed.WikiScanCursor != 0 || !failed.Uncertain || requests.Load() != 0 {
		t.Fatalf("local failure advanced or started model review: %+v requests=%d", failed, requests.Load())
	}
	kb, err := e.ProjectKnowledge(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	before, err := kb.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	graph, err := before.Graph()
	if err != nil || len(graph.Nodes) == 0 {
		t.Fatalf("local filesystem publication was lost: %+v %v", graph, err)
	}
	if err := db.Exec("DROP TRIGGER refuse_local_scan_receipt").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := e.StartProjectDistillation(t.Context(), p.ID, DistillationRequest{ResumeReviewID: started.ReviewID, WikiGraph: true}); err != nil {
		t.Fatal(err)
	}
	got := waitLocalWiki(t, e, p.ID)
	if got.State != "completed" || !got.WikiScanComplete || got.WikiScanProcessed != 1 ||
		got.Progress.Processed != 1 || got.Uncertain || got.WikiDocumented != 1 || requests.Load() != 0 {
		t.Fatalf("local checkpoint did not recover: %+v %+v requests=%d", got, got.Progress, requests.Load())
	}
	after, err := kb.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	finalGraph, err := after.Graph()
	if err != nil || len(finalGraph.Nodes) != len(graph.Nodes) || len(finalGraph.Edges) != len(graph.Edges) {
		t.Fatalf("resume duplicated the graph: before=%+v after=%+v %v", graph, finalGraph, err)
	}
	for _, node := range graph.Nodes {
		page, err := e.ProjectKnowledgePage(t.Context(), p.ID, node.ID)
		if err != nil || len(page.Sources) != 1 || page.Sources[0].Seq != source.Seq || len(page.Wiki.Evidence) != 1 {
			t.Fatalf("resume duplicated the receipt's source observation: %+v %v", page, err)
		}
	}
}

func TestWikiBuildOnlyRevisitsNewImmutableHistory(t *testing.T) {
	var resent atomic.Bool
	e, requests := distillWire(t, func(w http.ResponseWriter, req scheduleCompletionRequest, step int) {
		input := req.Messages[1].Content
		if step > 1 && (strings.Contains(input, "retained-start.example.test") || !strings.Contains(input, "retained-next.example.test")) {
			resent.Store(true)
		}
		nuwaValidationTool(w, req, "incremental", wikiExtractTool, `{"reviewed":[1],"nodes":[],"links":[]}`)
	})
	p, err := e.CreateProject("Incremental local wiki", "", "", true)
	if err != nil {
		t.Fatal(err)
	}
	th := knowledgeFixtureThread(t, e, p.ID, "Retained records")
	build := func(body string, total int) {
		t.Helper()
		distillEvent(t, e, th, store.TurnDone, KindUser, body)
		if _, err := e.StartProjectDistillation(t.Context(), p.ID, DistillationRequest{Scope: "project", WikiGraph: true, WikiReview: new(true)}); err != nil {
			t.Fatal(err)
		}
		got := waitLocalWiki(t, e, p.ID)
		if got.State != "completed" || !got.WikiScanComplete || got.WikiScanProcessed != total ||
			got.Progress.Processed != total || got.WikiScanSkipped != 0 || got.Progress.Skipped != 0 {
			t.Fatalf("incremental scope was not fully accounted for: %+v %+v", got, got.Progress)
		}
	}
	build("Read https://retained-start.example.test/view", 1)
	build("Read https://retained-next.example.test/view", 2)
	if requests.Load() != 2 || resent.Load() {
		t.Fatalf("model saw already checked history: calls=%d reused_old=%v", requests.Load(), resent.Load())
	}
	kb, err := e.ProjectKnowledge(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	snap, err := kb.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	graph, err := snap.Graph()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"retained-start.example.test", "retained-next.example.test"} {
		count := 0
		for _, node := range graph.Nodes {
			if node.Title == want {
				count++
			}
		}
		if count != 1 {
			t.Fatalf("preserved and new observations must each appear exactly once: %q found %d", want, count)
		}
	}
}

func waitLocalWiki(t *testing.T, e *Engine, projectID string) DistillationResult {
	t.Helper()
	for deadline := time.Now().Add(8 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		r, err := e.ProjectWikiBuildStatus(t.Context(), projectID)
		if err != nil {
			t.Fatal(err)
		}
		if r.State != "running" && r.State != "pausing" {
			return r
		}
	}
	t.Fatal("wiki build did not settle")
	return DistillationResult{}
}

func TestWikiLocalPreservesLargeStructuredSourceContext(t *testing.T) {
	t.Run("bounded structured source", func(t *testing.T) {
		e := newTestEngine(t)
		p, err := e.CreateProject("Structured observation", "", "", true)
		if err != nil {
			t.Fatal(err)
		}
		th := knowledgeFixtureThread(t, e, p.ID, "Retained structured source")
		body := `{"hostname":"confirmed.example.test","query":"` + strings.Repeat("a", wikiLocalWindowBytes+40) + ` https://ignored.example.test/a"}`
		distillEvent(t, e, th, store.TurnDone, KindUser, body)
		if _, err := e.StartProjectDistillation(t.Context(), p.ID, DistillationRequest{Scope: "thread", ThreadID: th.ID, WikiGraph: true}); err != nil {
			t.Fatal(err)
		}
		result := waitLocalWiki(t, e, p.ID)
		if result.State != "completed" || !result.WikiScanComplete || result.WikiScanProcessed != 1 || result.WikiScanSkipped != 0 || result.WikiScanDeferred != 0 {
			t.Fatalf("the structured record was not scanned: %+v", result)
		}
		kb, err := e.ProjectKnowledge(p.ID)
		if err != nil {
			t.Fatal(err)
		}
		snap, err := kb.Snapshot()
		if err != nil {
			t.Fatal(err)
		}
		graph, err := snap.Graph()
		if err != nil || len(graph.Nodes) != 1 || graph.Nodes[0].Title != "confirmed.example.test" {
			t.Fatalf("split JSON lost a typed observation or admitted query text: %+v %v", graph, err)
		}
	})
	t.Run("above local record budget", func(t *testing.T) {
		e := newTestEngine(t)
		for _, prefix := range []string{`{"query":"`, "Observed https://long.example.test"} {
			row := store.WikiScanSource{Part: store.DistillationText{
				Text: prefix, TotalBytes: wikiLocalRecordBytes + 1,
			}}
			nodes, deferred, err := e.extractLocalWikiSource(t.Context(), nil, row, knowledge.Source{})
			if err != nil || !deferred || len(nodes) != 0 {
				t.Fatalf("an oversized record was not deferred intact: %+v %v %v", nodes, deferred, err)
			}
		}
	})
}

func TestWikiLocalDoesNotInventAnIdentifierAtTheReadBoundary(t *testing.T) {
	e := newTestEngine(t)
	p, err := e.CreateProject("Complete identifiers", "", "", true)
	if err != nil {
		t.Fatal(err)
	}
	th := knowledgeFixtureThread(t, e, p.ID, "History")
	const prefix = "https://crossing.example.te"
	body := strings.Repeat(" ", wikiLocalWindowBytes-len(prefix)) + prefix + "st/document"
	distillEvent(t, e, th, store.TurnDone, KindUser, body)
	if _, err := e.StartProjectDistillation(t.Context(), p.ID, DistillationRequest{Scope: "thread", ThreadID: th.ID, WikiGraph: true}); err != nil {
		t.Fatal(err)
	}
	result := waitLocalWiki(t, e, p.ID)
	if !result.WikiScanComplete || result.WikiScanProcessed != 1 || result.WikiScanDeferred != 0 {
		t.Fatalf("large plain record was not scanned: %+v", result)
	}
	kb, err := e.ProjectKnowledge(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	snap, err := kb.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	graph, err := snap.Graph()
	if err != nil || len(graph.Nodes) != 1 || graph.Nodes[0].Title != "crossing.example.test" {
		t.Fatalf("only the identity in the complete original belongs in the graph: %+v %v", graph, err)
	}
}

func TestWikiLocalReferenceRequiresAnEligibleOriginal(t *testing.T) {
	tests := []struct {
		name, kind, call, want string
		messageSeq             int64
		unavailable, failed    bool
	}{
		{name: "original human input", kind: KindUser, want: "event"},
		{name: "unavailable input", kind: KindUser, unavailable: true},
		{name: "failed turn human input", kind: KindUser, failed: true, want: "event"},
		{name: "failed turn assistant output", kind: swarm.NotifyAgentMessage.String(), failed: true},
		{name: "retained steer message", kind: KindSteer, messageSeq: 29, want: "message"},
		{name: "steer without message", kind: KindSteer},
		{name: "tool without matching call", kind: swarm.NotifyToolResult.String()},
		{name: "derived tool result", kind: swarm.NotifyToolResult.String(), call: ToolQueryHistory + "()"},
		{name: "paired successful tool", kind: swarm.NotifyToolResult.String(), call: "inspect()", want: "event"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			row := store.WikiScanSource{Available: !test.unavailable, CallHead: test.call}
			row.Event.Kind, row.Event.ThreadID, row.Event.Seq = test.kind, "original-thread", 5
			row.Event.CreatedAt = time.Now()
			row.Turn.Status = store.TurnDone
			row.Part.MessageSeq = test.messageSeq
			if test.failed {
				row.Turn.Status = store.TurnError
			}
			ref, ok := localWikiReference(row)
			if !ok && test.want == "" {
				return
			}
			seq := row.Event.Seq
			if test.want == "message" {
				seq = test.messageSeq
			}
			if !ok || test.want == "" || ref != (store.HistoryReference{Kind: test.want, ThreadID: row.Event.ThreadID, Seq: seq}) {
				t.Fatalf("unexpected source reference %+v, available=%v", ref, ok)
			}
		})
	}
}
