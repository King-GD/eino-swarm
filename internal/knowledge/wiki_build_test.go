package knowledge

import (
	"context"
	"errors"
	"slices"
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

func TestWikiLocalWitnessesAreBoundedAndKeepAnExplicitEdge(t *testing.T) {
	s := newWiki(t)
	if _, err := s.Enable(); err != nil {
		t.Fatal(err)
	}
	if err := s.EnsureWikiSchema(); err != nil {
		t.Fatal(err)
	}
	stamp := Stamp{UpdatedAt: time.Now(), UpdatedTurn: "origin", UpdatedThread: "thread"}
	source := Source{Kind: "event", ThreadID: "thread", Seq: 1}
	a := WikiNode{ID: "alpha", Title: "Alpha", Kind: "entity", Category: "topic"}
	b := WikiNode{ID: "beta", Title: "Beta", Kind: "entity", Category: "topic",
		Evidence: []WikiEvidence{{Source: source, Quote: "Beta", Local: true}}}
	for seq := 1; seq < 40; seq++ {
		ref := source
		ref.Seq = int64(seq)
		a.Evidence = append(a.Evidence, WikiEvidence{Source: ref, Quote: "Alpha", Local: true})
	}
	a.Evidence = append(a.Evidence,
		WikiEvidence{Source: source, Quote: "Alpha refers to Beta.", To: b.ID, Local: true},
		WikiEvidence{Source: source, Quote: "Alpha had a separately reviewed observation."})
	if err := s.StageWikiNodes(t.Context(), "one", []WikiNode{a, b}, stamp); err != nil {
		t.Fatal(err)
	}
	snap, err := s.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	page, err := snap.Page(a.ID)
	if err != nil || len(page.Wiki.Evidence) != wikiWitnessLimit+2 || len(page.Links) != 1 {
		t.Fatalf("repeated local evidence crowded out the explicit edge or semantic observation: %+v %v", page, err)
	}
	// Replaying only redundant evidence in a later run should leave the
	// published article and its revision untouched.
	stamp.UpdatedAt = stamp.UpdatedAt.Add(time.Second)
	if err := s.StageWikiNodes(t.Context(), "two", []WikiNode{a}, stamp); err != nil {
		t.Fatal(err)
	}
	after, err := s.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := after.Page(a.ID)
	if err != nil || replayed.Revision != page.Revision {
		t.Fatalf("replay rewrote an unchanged article: %+v %v", replayed, err)
	}
}

func TestWikiSemanticWitnessesRemainBoundedAcrossReviewBatches(t *testing.T) {
	s := newWiki(t)
	if _, err := s.Enable(); err != nil {
		t.Fatal(err)
	}
	if err := s.EnsureWikiSchema(); err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	stamp := Stamp{UpdatedAt: time.Now(), UpdatedTurn: "origin", UpdatedThread: "thread"}
	source := Source{Kind: "event", ThreadID: "thread", Seq: 1}
	first := WikiEvidence{Source: source, Quote: "Alpha is a verified recurring observation."}
	a := WikiNode{ID: "alpha", Title: "Alpha", Kind: "entity", Category: "topic"}
	b := WikiNode{ID: "beta", Title: "Beta", Kind: "entity", Category: "topic", Evidence: []WikiEvidence{first}}
	c := WikiNode{ID: "gamma", Title: "Gamma", Kind: "entity", Category: "topic", Evidence: []WikiEvidence{first}}
	for seq := range wikiWitnessLimit {
		ref := source
		ref.Seq = int64(seq + 10)
		a.Evidence = append(a.Evidence,
			WikiEvidence{Source: ref, Quote: "Alpha was seen locally.", Local: true},
			WikiEvidence{Source: ref, Quote: "Alpha explicitly refers to Beta.", To: b.ID, Local: true},
		)
	}
	a.Evidence = append(a.Evidence, first)
	if err := s.StageWikiNodes(ctx, "one", []WikiNode{a, b, c}, stamp); err != nil {
		t.Fatal(err)
	}
	snap, err := s.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	detail, err := snap.Page(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	manual := Source{URI: "https://example.test/editorial", Classification: "external"}
	article := detail.Page
	article.Body = "User-authored introduction."
	article.Sources = append(article.Sources, manual)
	if _, err := s.WritePage(PageWrite{article}, stamp); err != nil {
		t.Fatal(err)
	}

	// Every quote fits inside an individual review window. All quotes
	// together would grow one recurring subject past the Markdown limit.
	quote := "Observed Alpha for review. " + strings.Repeat("Context ", 930)
	linked := "Alpha refers to Beta."
	for batch := range 2 {
		a.Evidence = nil
		for i := range 320 {
			ref := source
			ref.Seq = int64(100 + batch*320 + i)
			a.Evidence = append(a.Evidence, WikiEvidence{Source: ref, Quote: quote})
			if i < 40 {
				a.Evidence = append(a.Evidence, WikiEvidence{Source: ref, Quote: linked, To: b.ID})
			}
		}
		if err := s.StageWikiNodes(ctx, "one", []WikiNode{a}, stamp); err != nil {
			t.Fatalf("recurring semantic witnesses blocked batch %d: %v", batch+1, err)
		}
	}
	secondRelation := WikiEvidence{Source: source, Quote: "Alpha separately refers to Gamma.", To: c.ID}
	a.Evidence = []WikiEvidence{secondRelation}
	if err := s.StageWikiNodes(ctx, "one", []WikiNode{a}, stamp); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteWikiDocuments(ctx, "one", 10); err != nil {
		t.Fatal(err)
	}
	snap, err = s.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	detail, err = snap.Page(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	local, semantic := map[string]int{}, map[string]int{}
	for _, evidence := range detail.Wiki.Evidence {
		if evidence.Local {
			local[evidence.To]++
		} else {
			semantic[evidence.To]++
		}
	}
	if local[""] != wikiWitnessLimit || local[b.ID] != wikiWitnessLimit || semantic[""] != wikiWitnessLimit ||
		semantic[b.ID] != wikiWitnessLimit || semantic[c.ID] != 1 {
		t.Fatalf("witness budgets crossed a relationship or mode: local=%v semantic=%v", local, semantic)
	}
	if detail.Wiki.Pending || !strings.HasPrefix(detail.Body, article.Body) || !strings.Contains(detail.Body, first.Quote) ||
		!slices.Contains(detail.Sources, manual) || !slices.Contains(detail.Wiki.Evidence, secondRelation) || len(detail.Links) != 2 {
		t.Fatal("bounded review removed authored content, first valid proofs or a distinct relationship")
	}
}
