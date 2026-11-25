package knowledge

import (
	"slices"
	"testing"
	"time"
)

func TestWikiNonSubjectPreservesNamedFactsAndRejectsEditorialLabels(t *testing.T) {
	tests := []struct {
		kind, category, title, quote string
		excluded                     bool
	}{
		{"event", "AGENT_ACTION", "Checking records", "Checking records before the next step.", true},
		{"event", "planned-action", "Check the archive", "I will check the archive before continuing.", true},
		{"entity", "page_title", "Waiting for confirmation", "Waiting for confirmation, ready=false", true},
		{"event", "request", "Look up the record", "Look up the record when you can.", true},
		{"event", "meeting", "Overview", "## **Overview**", true},
		{"event", "timeline_observation", "Operational timeline", "| 1 | **Operational timeline** | Sites reopened last week. |", true},
		{"event", "timeline_observation", "撤离演练（时间轴）", "| 撤离演练（时间轴） | 部署于周五完成 |", true},
		{"event", "timeline_observation", "Spring Opening", "| 1 April | Spring Opening | Central Station welcomed passengers. |", false},
		{"event", "timeline_observation", "Operational timeline", "The Operational timeline was presented at the annual meeting.", false},
		{"entity", "organization", "Independent Archive", "Independent Archive catalogued the record.", false},
		{"entity", "request", "REQ-24", "The filed record REQ-24 is assigned to the archive.", false},
		{"entity", "certificate", "Certificate fingerprint", "Certificate fingerprint recorded by inspection.", false},
	}
	for _, tc := range tests {
		if actual := WikiNonSubject(tc.kind, tc.category, tc.title, tc.quote); actual != tc.excluded {
			t.Errorf("exclusion of %q = %t, want %t", tc.title, actual, tc.excluded)
		}
	}
}

func TestLegacyWikiLabelsRemainInspectableOutsideBrowsing(t *testing.T) {
	kb := newWiki(t)
	enableWiki(t, kb)
	if err := kb.EnsureWikiSchema(); err != nil {
		t.Fatal(err)
	}
	writePage(t, kb, Page{ID: "manual", Type: "entry", Title: "Operational timeline"})
	source := Source{Kind: "event", ThreadID: "thread", Seq: 1}
	nodes := []WikiNode{
		{ID: "caption", Title: "Operational timeline", Kind: "event", Category: "timeline_observation",
			Evidence: []WikiEvidence{{Source: source, Quote: "| Section | Operational timeline | Sites reopened. |", To: "station"}}},
		{ID: "status", Title: "Processing", Kind: "entity", Category: "page_title",
			Evidence: []WikiEvidence{{Source: source, Quote: "Processing"}}},
		{ID: "station", Title: "Central Station", Kind: "entity", Category: "place",
			Evidence: []WikiEvidence{{Source: source, Quote: "Central Station welcomed passengers."}}},
		{ID: "opening", Title: "Spring Opening", Kind: "event", Category: "ceremony",
			Evidence: []WikiEvidence{{Source: source, Quote: "Central Station hosted the Spring Opening.", To: "station"},
				{Source: source, Quote: "The source included a Processing label.", To: "status"}}},
	}
	if err := kb.StageWikiNodes(t.Context(), "old-review", nodes, testStamp); err != nil {
		t.Fatal(err)
	}
	snapshot := snapshotWiki(t, kb)
	if _, err := kb.AppendSchema(SchemaAppend{Views: []View{{ID: "linked", RootType: wikiNodeType, Follow: []string{wikiRelation}, Depth: 1}}}, snapshot.revision); err != nil {
		t.Fatal(err)
	}
	snapshot = snapshotWiki(t, kb)

	graph, err := snapshot.Graph()
	if err != nil || len(graph.Nodes) != 3 || len(graph.Edges) != 1 ||
		graph.Edges[0].Source != "opening" || graph.Edges[0].Target != "station" {
		t.Fatalf("legacy labels or edges leaked into the graph: %+v %v", graph, err)
	}
	manual := snapshot.Search("Operational timeline", 10)
	if len(manual) != 1 || manual[0].ID != "manual" {
		t.Fatalf("manual page hidden or duplicate legacy title still searchable: %+v", manual)
	}
	for _, view := range []string{typeView, "linked"} {
		tree, err := snapshot.Tree(view, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		var ids []string
		nodes := append([]TreeNode(nil), tree.Nodes...)
		for len(nodes) > 0 {
			next := nodes[0]
			nodes = append(nodes[1:], next.Children...)
			ids = append(ids, next.ID)
		}
		if slices.Contains(ids, "status") || slices.Contains(ids, "caption") || !slices.Contains(ids, "opening") {
			t.Fatalf("view %q mixed review-only and browseable nodes: %v", view, ids)
		}
	}

	pending, err := snapshot.Tree(pendingView, time.Now())
	if err != nil || len(pending.Issues) != 2 {
		t.Fatalf("legacy pages not surfaced for review: %+v %v", pending, err)
	}
	for _, issue := range pending.Issues {
		page, err := snapshot.Page(issue.PageID)
		if issue.Kind != "wiki_non_subject" || err != nil || page.Wiki == nil || len(page.Wiki.Evidence) == 0 {
			t.Fatalf("the original citation is unavailable: %+v %v", issue, err)
		}
	}
}
