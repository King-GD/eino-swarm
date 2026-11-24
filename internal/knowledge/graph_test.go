package knowledge

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestViewsRepeatSharedParentsAndCutOnlyCurrentPath(t *testing.T) {
	store := newWiki(t)
	enableWiki(t, store)
	appendWiki(t, store, SchemaAppend{
		Types: []PageType{{ID: "alpha", Dir: "alphas", FreshnessDays: 2}, {ID: "beta", Dir: "betas"}},
		Relations: []Relation{
			{ID: "branch", From: []string{"alpha", "beta"}, To: []string{"alpha", "beta"}, Inverse: "back"},
			{ID: "bind", From: []string{"alpha"}, To: []string{"beta"}, Required: true},
		},
		Views: []View{{ID: "branches", RootType: "alpha", Follow: []string{"branch"}, Depth: 6}},
	})
	writePage(t, store, Page{ID: "a", Type: "alpha", Title: "A", Aliases: []string{"Alias A"}, AsOf: "2026-01-01",
		Links: []Link{{Rel: "branch", To: "shared"}, {Rel: "bind", To: "shared"}}})
	writePage(t, store, Page{ID: "b", Type: "alpha", Title: "B", AsOf: "2026-01-01",
		Links: []Link{{Rel: "branch", To: "shared"}}, Body: "[[unfinished]]"})
	writePage(t, store, Page{ID: "shared", Type: "beta", Title: "Shared", Links: []Link{{Rel: "branch", To: "a"}}})

	snap := snapshotWiki(t, store)
	tree, err := snap.Tree("branches", testStamp.UpdatedAt)
	if err != nil || len(tree.Nodes) != 2 || tree.Truncated {
		t.Fatalf("schema tree = %+v, %v", tree, err)
	}
	a, b := tree.Nodes[0], tree.Nodes[1]
	if a.ID != "a" || b.ID != "b" || !slices.Equal(a.Aliases, []string{"Alias A"}) {
		t.Fatalf("tree roots or aliases = %+v", tree)
	}
	firstShared, secondShared := pageChild(t, a), pageChild(t, b)
	firstCycle := pageChild(t, firstShared)
	secondA := pageChild(t, secondShared)
	secondCycle := pageChild(t, secondA)
	if firstShared.ID != "shared" || secondShared.ID != "shared" || firstCycle.ID != "a" || !firstCycle.Cycle ||
		secondA.ID != "a" || secondA.Cycle || secondCycle.ID != "shared" || !secondCycle.Cycle ||
		len(firstCycle.Children)+len(secondCycle.Children) != 0 {
		t.Fatalf("shared child or cycle semantics lost: %+v", tree)
	}
	a.Aliases[0] = "tampered"
	byType, err := snap.Tree("", testStamp.UpdatedAt)
	if err != nil || byType.View != "type" || len(byType.Nodes) != 3 || byType.Nodes[1].Children[0].Aliases[0] != "Alias A" {
		t.Fatalf("type view = %+v, %v", byType, err)
	}
	_, err = snap.Tree("missing", testStamp.UpdatedAt)
	assertError(t, err, ErrNotFound)
	pending, err := snap.Tree("pending", testStamp.UpdatedAt.AddDate(0, 0, 2))
	if err != nil || len(pending.Nodes) != 0 {
		t.Fatalf("pending view = %+v, %v", pending, err)
	}
	want := []Issue{
		{Kind: "broken_link", PageID: "b", Target: "unfinished"},
		{Kind: "stale", PageID: "a"},
		{Kind: "stale", PageID: "b"},
		{Kind: "missing_relation", PageID: "b", Relation: "bind"},
	}
	if !slices.Equal(pending.Issues, want) || snap.Status(testStamp.UpdatedAt).PendingCount != 2 ||
		snap.Status(testStamp.UpdatedAt.AddDate(0, 0, 2)).PendingCount != len(want) {
		t.Fatalf("pending without a file write = %+v, want %+v", pending.Issues, want)
	}
}

func pageChild(t *testing.T, parent TreeNode) TreeNode {
	t.Helper()
	if len(parent.Children) != 1 || parent.Children[0].Kind != "relation" ||
		parent.Children[0].Label != "branch" || len(parent.Children[0].Children) != 1 {
		t.Fatalf("expected a grouped child for %+v", parent)
	}
	return parent.Children[0].Children[0]
}

func TestTreeBudgetIncludesAllRenderedNodes(t *testing.T) {
	store := newWiki(t)
	enableWiki(t, store)
	appendWiki(t, store, SchemaAppend{
		Views: []View{{ID: "graph", RootType: "entry", Follow: []string{"related"}, Depth: maxViewDepth}},
	})
	for _, id := range []string{"a", "b", "c", "d", "e", "f", "g", "h"} {
		page := Page{ID: id, Type: "entry", Title: id}
		for _, target := range []string{"a", "b", "c", "d", "e", "f", "g", "h"} {
			if id != target {
				page.Links = append(page.Links, Link{Rel: "related", To: target})
			}
		}
		writePage(t, store, page)
	}
	tree, err := snapshotWiki(t, store).Tree("graph", testStamp.UpdatedAt)
	if err != nil || !tree.Truncated || countNodes(tree.Nodes) > maxTreeNodes {
		t.Fatalf("tree budget = %d/%d, truncated %t, error %v", countNodes(tree.Nodes), maxTreeNodes, tree.Truncated, err)
	}
}

func countNodes(nodes []TreeNode) int {
	total := len(nodes)
	for _, node := range nodes {
		total += countNodes(node.Children)
	}
	return total
}

func TestRelationTypesValidateBothWhenCreatingAndResolvingLinks(t *testing.T) {
	store := newWiki(t)
	enableWiki(t, store)
	appendWiki(t, store, SchemaAppend{
		Types:     []PageType{{ID: "alpha", Dir: "alphas"}},
		Relations: []Relation{{ID: "bind", From: []string{"entry"}, To: []string{"alpha"}}},
	})
	created := writePage(t, store, Page{ID: "from", Type: "entry", Title: "From", Links: []Link{{Rel: "bind", To: "there"}}})
	for _, page := range []Page{
		{ID: "there", Type: "entry", Title: "Wrong target"},
		{ID: "wrong", Type: "entry", Title: "Self", Links: []Link{{Rel: "bind", To: "wrong"}}},
		{ID: "wrong", Type: "alpha", Title: "Wrong source", Links: []Link{{Rel: "bind", To: "there"}}},
	} {
		_, err := store.WritePage(PageWrite{Page: page}, testStamp)
		assertError(t, err, ErrInvalid)
	}
	target := writePage(t, store, Page{ID: "there", Type: "alpha", Title: "Right target"})
	snap := snapshotWiki(t, store)
	incoming := readPage(t, snap, target.ID).Incoming
	if len(incoming) != 1 || incoming[0].Label != "bind" || incoming[0].Target != "from" ||
		!readPage(t, snap, "from").Outgoing[0].Resolved {
		t.Fatalf("valid target never resolved link: %+v", incoming)
	}
	tampered := *created
	tampered.Type = "alpha"
	_, err := store.WritePage(PageWrite{Page: tampered}, testStamp)
	assertError(t, err, ErrInvalid)
	_, err = store.WritePage(PageWrite{Page: Page{ID: "different", Type: "entry", Title: "Does not exist", Revision: target.Revision}}, testStamp)
	assertError(t, err, ErrConflict)

	// A direct human filesystem move keeps stable-ID mentions and diagnoses a
	// newly wrong typed endpoint without publishing an inverse relationship.
	destination := filepath.Join(store.Dir(), pagesDir, "entries", "there.md")
	src := filepath.Join(store.Dir(), pagesDir, "alphas", "there.md")
	body := strings.Replace(string(diskRead(t, src)), "type: alpha\n", "type: entry\n", 1)
	if err := os.Remove(src); err != nil {
		t.Fatal(err)
	}
	diskWrite(t, destination, []byte(body))
	moved := snapshotWiki(t, store)
	if !readPage(t, moved, "from").Outgoing[0].Resolved || len(readPage(t, moved, "there").Incoming) != 0 ||
		!slices.ContainsFunc(moved.Lint(testStamp.UpdatedAt), func(issue Issue) bool {
			return issue.Kind == "invalid_page" && issue.Target == "there"
		}) {
		t.Fatalf("external type move lost stable identity or diagnostics: %+v", moved.Lint(testStamp.UpdatedAt))
	}
	update := *created
	update.Body = "Follow the same [[there]]."
	_, err = store.WritePage(PageWrite{Page: update}, testStamp)
	assertError(t, err, ErrInvalid)
}

func TestWikilinksOnlyIndexMarkdownContent(t *testing.T) {
	content := strings.Join([]string{
		"[[first|Display]] then **[[second]]**.",
		"\x60[[literal]]\x60 and \\[[escaped]] and [[first]] and [[invalid--slug]].",
		"",
		"> ~~~md",
		"> [[fenced]]",
		"> ~~~",
		"",
		"    [[indented]]",
		"",
		"[Existing [[inside]]](https://example.invalid) and ![Picture [[image]]](placeholder).",
		"",
		"| A | B |",
		"| - | - |",
		"| [[table]] | [[last]] |",
	}, "\n")
	if got, want := wikiTargets(content), []string{"first", "second", "table", "last"}; !slices.Equal(got, want) {
		t.Fatalf("wiki targets = %v, want %v", got, want)
	}
	if got := wikiTargets("Plain text"); len(got) != 0 {
		t.Fatalf("non-wiki text produced %v", got)
	}
}

func TestExternalEditsRebuildIndexAndByteCheckRefusesWithoutStatChange(t *testing.T) {
	store := newWiki(t)
	enableWiki(t, store)
	before := writePage(t, store, Page{ID: "alpha", Type: "entry", Title: "Alpha", Body: "Before [[broken]]."})
	first := snapshotWiki(t, store)
	path := filepath.Join(store.Dir(), pagesDir, "entries", "alpha.md")
	oldData := diskRead(t, path)
	old, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	newData := []byte(strings.Replace(string(oldData), "Before", "After!", 1))
	diskWrite(t, path, newData)
	if err := os.Chtimes(path, old.ModTime(), old.ModTime()); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if sameStat(old, after) && snapshotWiki(t, store) != first {
		t.Fatal("index did not use its advertised stat signature")
	}
	update := *before
	update.Body = "Would erase the human edit."
	_, err = store.WritePage(PageWrite{Page: update}, testStamp)
	assertError(t, err, ErrConflict)
	assertError(t, store.DeletePage("alpha", before.Revision), ErrConflict)
	if string(diskRead(t, path)) != string(newData) ||
		readPage(t, snapshotWiki(t, store), "alpha").Body != "After! [[broken]]." {
		t.Fatal("tool overwrote or failed to refresh the external edit")
	}

	// Editors commonly save by replacing the inode. Equal size and time are
	// insufficient to keep the old indexed bytes after a replacement.
	tmp := filepath.Join(filepath.Dir(path), "external.tmp")
	edited := strings.Replace(string(newData), "broken", "target", 1)
	diskWrite(t, tmp, []byte(edited))
	if err := os.Chtimes(tmp, old.ModTime(), old.ModTime()); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, path); err != nil {
		t.Fatal(err)
	}
	second := snapshotWiki(t, store)
	if second == first || readPage(t, second, "alpha").Body != "After! [[target]]." ||
		!slices.Equal(readPage(t, second, "alpha").Unresolved, []string{"target"}) {
		t.Fatalf("replacement did not update derived index: %+v", second.Status(testStamp.UpdatedAt))
	}

	// A broken front matter file remains visible in lint without preventing
	// the healthy pages from being read or leaving stale links in the graph.
	diskWrite(t, path, []byte("---\nid: alpha\nmissing closing delimiter"))
	healthy := writePage(t, store, Page{ID: "other", Type: "entry", Title: "Other", Body: "[[alpha]]"})
	broken := snapshotWiki(t, store)
	if got := broken.Lint(testStamp.UpdatedAt); len(got) != 2 || got[0].Path != "pages/entries/alpha.md" ||
		got[0].Kind != "invalid_page" || got[1].Target != "alpha" || healthy.Revision == "" {
		t.Fatalf("malformed external page did not enter lint queue: %+v", got)
	}
	if _, err := broken.Page("alpha"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("stale page survived malformed replacement: %v", err)
	}
}

func TestKnowledgeGraphRetainsOneNodePerPageAcrossTitleChanges(t *testing.T) {
	store := newWiki(t)
	_, err := snapshotWiki(t, store).Graph()
	assertError(t, err, ErrDisabled)
	enableWiki(t, store)
	empty, err := snapshotWiki(t, store).Graph()
	if err != nil || len(empty.Nodes) != 0 || len(empty.Edges) != 0 {
		t.Fatalf("empty graph: %+v, %v", empty, err)
	}
	appendWiki(t, store, SchemaAppend{
		Relations: []Relation{{ID: "uses", From: []string{"entry"}, To: []string{"entry"}}},
	})
	writePage(t, store, Page{ID: "source", Type: "entry", Title: "Original title", Aliases: []string{"Source alias"},
		Links: []Link{{Rel: "related", To: "target"}, {Rel: "uses", To: "unfinished"}},
		Body:  "[[target]] and [[target|title variant]]. [[unfinished]]"})
	writePage(t, store, Page{ID: "target", Type: "entry", Title: "Target", Body: "[[source]] and [[target]].",
		Links: []Link{{Rel: "related", To: "source"}, {Rel: "uses", To: "source"}}})
	writePage(t, store, Page{ID: "isolated", Type: "entry", Title: "Isolated"})

	snap := snapshotWiki(t, store)
	graph, err := snap.Graph()
	if err != nil || len(graph.Nodes) != 3 || len(graph.Edges) != 2 {
		t.Fatalf("wrong graph: %+v, %v", graph, err)
	}
	if graph.Nodes[1].ID != "source" || graph.Nodes[1].Title != "Original title" || !slices.Equal(graph.Nodes[1].Aliases, []string{"Source alias"}) {
		t.Fatalf("title and identity: %+v", graph.Nodes)
	}
	if graph.Edges[0].Source != "source" || graph.Edges[0].Target != "target" || !graph.Edges[0].Wikilink ||
		!slices.Equal(graph.Edges[0].Relations, []string{"related", "uses"}) ||
		graph.Edges[1].Source != "target" || graph.Edges[1].Target != "target" || !graph.Edges[1].Wikilink {
		t.Fatalf("reciprocal links should share one edge: %+v", graph.Edges)
	}
	graph.Nodes[1].Aliases[0] = "mutated"
	graph.Edges[0].Relations[0] = "mutated"
	again, err := snap.Graph()
	if err != nil || again.Nodes[1].Aliases[0] != "Source alias" || again.Edges[0].Relations[0] != "related" {
		t.Fatalf("graph exposed its snapshot: %+v, %v", again, err)
	}

	page, err := snap.Page("source")
	if err != nil {
		t.Fatal(err)
	}
	page.Title = "New title"
	writePage(t, store, page.Page)
	renamed, err := snapshotWiki(t, store).Graph()
	if err != nil || renamed.Nodes[1].ID != "source" || renamed.Nodes[1].Title != "New title" ||
		renamed.Edges[0].Source != "source" || renamed.Edges[0].Target != "target" || len(renamed.Edges) != 2 {
		t.Fatalf("rename broke the network: %+v, %v", renamed, err)
	}
}
