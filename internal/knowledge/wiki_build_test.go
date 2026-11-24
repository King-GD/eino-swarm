package knowledge

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestWikiGraphSeparatesSameNamesAndRefusesUncommittedEndpoints(t *testing.T) {
	s := newWiki(t)
	if err := s.EnsureWikiSchema(); !errors.Is(err, ErrDisabled) {
		t.Fatalf("disabled wiki accepted writes: %v", err)
	}
	if _, err := s.Enable(); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := s.EnsureWikiSchema(); err != nil {
			t.Fatal(err)
		}
	}
	stamp := Stamp{UpdatedAt: time.Now(), UpdatedTurn: "origin", UpdatedThread: "thread"}
	source := Source{Kind: "event", ThreadID: "thread", Seq: 1}
	a := WikiNode{ID: "first-node", Title: "Same name", Kind: "entity", Category: "topic", Evidence: []WikiEvidence{{Source: source, Quote: "First cited name."}}}
	b := WikiNode{ID: "second-node", Title: "Same name", Kind: "entity", Category: "topic", Evidence: []WikiEvidence{{Source: source, Quote: "Another cited name.", To: "missing"}}}
	if err := s.StageWikiNodes(t.Context(), "one", []WikiNode{a, b}, stamp); !errors.Is(err, ErrInvalid) {
		t.Fatalf("uncommitted link endpoint accepted: %v", err)
	}
	snap, err := s.Snapshot()
	if err != nil || snap.Status(time.Now()).PageCount != 0 {
		t.Fatalf("an invalid proposal published its earlier node: %v", err)
	}
	b.Evidence[0].To = a.ID
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if err := s.StageWikiNodes(canceled, "one", []WikiNode{a, b}, stamp); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled batch published: %v", err)
	}
	if err := s.StageWikiNodes(t.Context(), "one", []WikiNode{a, b}, stamp); err != nil {
		t.Fatal(err)
	}
	snap, err = s.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	graph, err := snap.Graph()
	if err != nil || len(graph.Nodes) != 2 || len(graph.Edges) != 1 || graph.Nodes[0].Title == graph.Nodes[1].Title {
		t.Fatalf("same-named nodes were collapsed or the relationship was lost: %+v %v", graph, err)
	}
	if total, written := snap.WikiDocumentProgress("one"); total != 2 || written != 0 {
		t.Fatalf("invalid documents progress before publication: %d/%d", written, total)
	}
}

func TestWikiDraftReplayPreservesArticleAndQuotesDoNotCreateLinks(t *testing.T) {
	s := newWiki(t)
	if _, err := s.Enable(); err != nil {
		t.Fatal(err)
	}
	if err := s.EnsureWikiSchema(); err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	stamp := Stamp{UpdatedAt: time.Now(), UpdatedTurn: "origin", UpdatedThread: "thread"}
	first := WikiEvidence{Source: Source{Kind: "event", ThreadID: "thread", Seq: 1}, Quote: "Raw string [[unrelated]] was observed."}
	node := WikiNode{ID: "first-node", Title: "First", Kind: "entity", Category: "topic", Evidence: []WikiEvidence{first}}
	for range 2 {
		if err := s.StageWikiNodes(ctx, "one", []WikiNode{node}, stamp); err != nil {
			t.Fatal(err)
		}
	}
	snap, err := s.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	page, err := snap.Page(node.ID)
	if err != nil || !page.Wiki.Pending || len(page.Wiki.Evidence) != 1 || page.Body != "" {
		t.Fatalf("draft replay duplicated a quote or published the article: %+v %v", page, err)
	}
	written := page.Page
	written.Wiki = nil
	written.Body = "Human-written introduction."
	if _, err := s.WritePage(PageWrite{written}, stamp); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := s.WriteWikiDocuments(ctx, "one", 10); err != nil {
			t.Fatal(err)
		}
	}
	snap, err = s.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	page, err = snap.Page(node.ID)
	if err != nil || page.Wiki.Pending || !strings.HasPrefix(page.Body, written.Body) ||
		strings.Count(page.Body, "was observed.") != 1 || len(page.Unresolved) != 0 {
		t.Fatalf("document replay lost human prose, duplicated a quote, or created a graph link: %+v %v", page, err)
	}
}
