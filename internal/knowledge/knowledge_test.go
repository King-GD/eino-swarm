package knowledge

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"
)

var testStamp = Stamp{
	UpdatedAt:     time.Date(2026, 1, 1, 2, 3, 4, 0, time.FixedZone("offset", 3600)),
	UpdatedTurn:   "turn-1",
	UpdatedThread: "thread-1",
}

func newWiki(t *testing.T) *Store {
	t.Helper()
	return New(filepath.Join(t.TempDir(), "knowledge"))
}

func enableWiki(t *testing.T, store *Store) *Snapshot {
	t.Helper()
	s, err := store.Enable()
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func snapshotWiki(t *testing.T, store *Store) *Snapshot {
	t.Helper()
	s, err := store.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func appendWiki(t *testing.T, store *Store, add SchemaAppend) *Snapshot {
	t.Helper()
	s, err := store.AppendSchema(add, snapshotWiki(t, store).Status(testStamp.UpdatedAt).Revision)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func writePage(t *testing.T, store *Store, page Page) *Page {
	t.Helper()
	result, err := store.WritePage(PageWrite{Page: page}, testStamp)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func readPage(t *testing.T, snap *Snapshot, id string) *PageDetail {
	t.Helper()
	p, err := snap.Page(id)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func diskRead(t *testing.T, path string) []byte {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func diskWrite(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func assertError(t *testing.T, got, want error) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestWikiEnableIsIdempotentAndSnapshotDetachesSchema(t *testing.T) {
	store := newWiki(t)
	disabled := snapshotWiki(t, store)
	if status := disabled.Status(testStamp.UpdatedAt); status != (Status{}) {
		t.Fatalf("disabled status = %+v", status)
	}
	if disabled.Summary(40) != "" || len(disabled.Search("entry", 10)) != 0 || len(disabled.Lint(testStamp.UpdatedAt)) != 0 {
		t.Fatal("disabled wiki exposed content")
	}
	_, err := disabled.Tree("type", testStamp.UpdatedAt)
	assertError(t, err, ErrDisabled)
	_, err = disabled.Page("alpha")
	assertError(t, err, ErrDisabled)
	_, err = store.WritePage(PageWrite{}, testStamp)
	assertError(t, err, ErrDisabled)
	assertError(t, store.DeletePage("alpha", ""), ErrDisabled)
	_, err = store.AppendSchema(SchemaAppend{}, "")
	assertError(t, err, ErrDisabled)
	if _, err := os.Stat(store.Dir()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("read created wiki directory: %v", err)
	}
	first := enableWiki(t, store)
	status := first.Status(testStamp.UpdatedAt)
	wantBytes := diskRead(t, filepath.Join(store.Dir(), schemaFile))
	if !status.Enabled || status.PageCount != 0 || !ValidSlug(status.Schema.Types[0].ID) {
		t.Fatalf("starter status = %+v", status)
	}
	if !strings.Contains(first.Summary(500), "Type entry") || first.Summary(0) != "" || len(first.Summary(2)) > 2 {
		t.Fatal("missing or unbounded schema summary")
	}
	status.Schema.Types[0].ID = "modified"
	status.Schema.Relations[0].From[0] = "modified"
	second := enableWiki(t, store)
	canonical := second.Status(testStamp.UpdatedAt)
	if canonical.Schema.Types[0].ID != "entry" || canonical.Schema.Relations[0].From[0] != "entry" ||
		string(diskRead(t, filepath.Join(store.Dir(), schemaFile))) != string(wantBytes) {
		t.Fatal("enable or mutable result altered the schema")
	}
	if second != snapshotWiki(t, store) {
		t.Fatal("unchanged stat signature did not cache the index")
	}
}

func TestSchemaAppendKeepsHumanEntriesAndRefusesPartialChanges(t *testing.T) {
	store := newWiki(t)
	enableWiki(t, store)
	path := filepath.Join(store.Dir(), schemaFile)
	original := "# Keep this rationale\n" + string(diskRead(t, path)) + "\nviews: null # Human view notes\n"
	diskWrite(t, path, []byte(original))
	before := snapshotWiki(t, store).Status(testStamp.UpdatedAt).Revision
	changes := SchemaAppend{
		Types:     []PageType{{ID: "alpha", Dir: "alphas", Description: "示例", Fields: []Field{{ID: "label", Kind: "text"}}, Sections: []string{"A"}}},
		Relations: []Relation{{ID: "path", From: []string{"entry"}, To: []string{"alpha"}, Inverse: "back"}},
		Views:     []View{{ID: "alpha-view", RootType: "entry", Follow: []string{"path"}, Depth: 4}},
	}
	after := appendWiki(t, store, changes)
	status := after.Status(testStamp.UpdatedAt)
	actual := string(diskRead(t, path))
	if !strings.Contains(actual, "# Keep this rationale") || !strings.Contains(actual, "# Human view notes") ||
		len(status.Schema.Types) != 2 || len(status.Schema.Relations) != 2 || len(status.Schema.Views) != 1 || status.Revision == before {
		t.Fatalf("append rewrote or lost schema: %s", actual)
	}
	status.Schema.Types[1].Fields[0].ID = "changed"
	status.Schema.Types[1].Sections[0] = "changed"
	status.Schema.Types[1].Description = "changed"
	status.Schema.Relations[1].To[0] = "changed"
	status.Schema.Views[0].Follow[0] = "changed"
	canonical := after.Status(testStamp.UpdatedAt)
	if canonical.Schema.Types[1].Fields[0].ID != "label" || canonical.Schema.Types[1].Sections[0] != "A" ||
		canonical.Schema.Types[1].Description != "示例" || canonical.Schema.Relations[1].To[0] != "alpha" ||
		canonical.Schema.Views[0].Follow[0] != "path" {
		t.Fatal("mutating a response changed its snapshot")
	}
	for budget := 0; budget <= 100; budget++ {
		summary := after.Summary(budget)
		if len(summary) > budget || !utf8.ValidString(summary) || strings.Contains(summary, "human") {
			t.Fatalf("summary invalid for budget %d: %q", budget, summary)
		}
	}
	_, err := store.AppendSchema(SchemaAppend{}, canonical.Revision)
	assertError(t, err, ErrInvalid)
	_, err = store.AppendSchema(changes, before)
	assertError(t, err, ErrConflict)
	_, err = store.AppendSchema(changes, canonical.Revision)
	assertError(t, err, ErrInvalid)
	_, err = store.AppendSchema(SchemaAppend{
		Types:     []PageType{{ID: "beta", Dir: "betas"}},
		Relations: []Relation{{ID: "broken", From: []string{"beta"}, To: []string{"missing"}}},
	}, canonical.Revision)
	assertError(t, err, ErrInvalid)
	if string(diskRead(t, path)) != actual {
		t.Fatal("rejected append changed the schema")
	}
}

func TestPageLifecycleRetainsStableIDAndHostStamps(t *testing.T) {
	store := newWiki(t)
	enableWiki(t, store)
	page := Page{
		ID: "alpha", Type: "entry", Title: " Alpha ", Body: "A body with [[beta|Beta]].\n",
		Aliases: []string{"Alternate alpha"}, Links: []Link{{Rel: "related", To: "beta"}},
		AsOf: "2025-12-31", UpdatedAt: testStamp.UpdatedAt.AddDate(0, 0, -1), UpdatedTurn: "untrusted",
		UpdatedThread: "untrusted",
		Sources: []Source{{URI: "https://example.invalid/document"}, {
			Kind: "event", ThreadID: "thread-2", Seq: 5, Timestamp: testStamp.UpdatedAt,
			RecordID: "row-1", Classification: "primary", TurnID: "untrusted", Available: new(bool),
		}},
	}
	created := writePage(t, store, page)
	old := snapshotWiki(t, store)
	oldDetail := readPage(t, old, "alpha")
	if created.Title != "Alpha" || oldDetail.UpdatedAt.Location() != time.UTC ||
		!oldDetail.UpdatedAt.Equal(testStamp.UpdatedAt) || oldDetail.UpdatedTurn != testStamp.UpdatedTurn ||
		oldDetail.UpdatedThread != testStamp.UpdatedThread || oldDetail.Revision != created.Revision ||
		oldDetail.Body != page.Body || oldDetail.AsOf != page.AsOf {
		t.Fatalf("metadata did not round trip: %+v", oldDetail)
	}
	if len(oldDetail.Outgoing) != 1 || oldDetail.Outgoing[0].Resolved || !slices.Equal(oldDetail.Unresolved, []string{"beta"}) {
		t.Fatalf("unresolved relation = %+v", oldDetail)
	}
	source := oldDetail.Sources[1]
	if source.RecordID != "row-1" || source.Available != nil || source.TurnID != "" ||
		oldDetail.Sources[0].Classification != "external" || !source.Timestamp.Equal(testStamp.UpdatedAt) {
		t.Fatalf("source metadata did not round trip: %+v", oldDetail.Sources)
	}
	encoded, err := json.Marshal(oldDetail)
	if err != nil || strings.Contains(string(encoded), "row-1") || strings.Contains(string(encoded), "untrusted") {
		t.Fatalf("persisted record identity or caller stamp escaped: %s %v", encoded, err)
	}
	beta := writePage(t, store, Page{ID: "beta", Type: "entry", Title: "Beta"})
	renamed := *beta
	renamed.Title = "Renamed beta"
	updated := writePage(t, store, renamed)
	current := snapshotWiki(t, store)
	detail := readPage(t, current, "alpha")
	if updated.Revision == beta.Revision || detail.Outgoing[0].Page.Title != "Renamed beta" || len(detail.Unresolved) != 0 {
		t.Fatalf("stable ID no longer resolves: %+v", detail)
	}
	inverse := readPage(t, current, "beta")
	if len(inverse.Incoming) != 1 || inverse.Incoming[0].Page.ID != "alpha" || inverse.Incoming[0].Label != "related" ||
		!slices.Equal(inverse.Mentions, []PageRef{{ID: "alpha", Type: "entry", Title: "Alpha"}}) {
		t.Fatalf("backlinks = %+v", inverse)
	}
	detail.Outgoing[0].Page.Title = "tampered"
	detail.Aliases[0] = "tampered"
	detail.Sources[0].URI = "tampered"
	detail.Links[0].To = "tampered"
	if fresh := readPage(t, current, "alpha"); fresh.Outgoing[0].Page.Title != "Renamed beta" ||
		fresh.Aliases[0] != page.Aliases[0] || fresh.Sources[0].URI != page.Sources[0].URI || fresh.Links[0].To != "beta" {
		t.Fatalf("consumer changed indexed page: %+v", fresh)
	}
	if readPage(t, old, "alpha").Outgoing[0].Resolved {
		t.Fatal("old snapshot changed after creating a target")
	}
	assertError(t, store.DeletePage("beta", beta.Revision), ErrConflict)
	assertError(t, store.DeletePage("beta", ""), ErrConflict)
	if err := store.DeletePage("beta", updated.Revision); err != nil {
		t.Fatal(err)
	}
	assertError(t, store.DeletePage("beta", updated.Revision), ErrNotFound)
	assertError(t, store.DeletePage("../beta", updated.Revision), ErrInvalid)
	current = snapshotWiki(t, store)
	_, err = current.Page("beta")
	assertError(t, err, ErrNotFound)
	_, err = current.Page("prefix:beta")
	assertError(t, err, ErrInvalid)
	if !slices.Equal(readPage(t, current, "alpha").Unresolved, []string{"beta"}) {
		t.Fatal("delete did not restore a red link")
	}
}

func TestPageCollisionChecksAllNamesAndConcurrentRevisions(t *testing.T) {
	store := newWiki(t)
	enableWiki(t, store)
	page := writePage(t, store, Page{ID: "entry-a", Type: "entry", Title: "Αλφα  ς", Aliases: []string{"Same name", "Café"}, Body: "search body"})
	for _, candidate := range []Page{
		{ID: "entry-a", Type: "entry", Title: "Different"},
		{ID: "other", Type: "entry", Title: "αλφα σ"},
		{ID: "other", Type: "entry", Title: "Different", Aliases: []string{"SAME\tNAME"}},
		{ID: "other", Type: "entry", Title: "entry-a"},
		{ID: "other", Type: "entry", Title: "Cafe\u0301"},
	} {
		_, err := store.WritePage(PageWrite{Page: candidate}, testStamp)
		var duplicate *DuplicatePageError
		if !errors.Is(err, ErrDuplicate) || !errors.As(err, &duplicate) || duplicate.Matched.ID != page.ID {
			t.Fatalf("unreported duplicate: %+v %v", candidate, err)
		}
	}
	other := writePage(t, store, Page{ID: "other", Type: "entry", Title: "Same names"})
	snapshot := snapshotWiki(t, store)
	if result := snapshot.Search("same name", 2); len(result) != 2 || result[0].ID != page.ID ||
		result[1].ID != other.ID {
		t.Fatalf("exact result failed to lead prefix search: %+v", result)
	}
	if result := snapshot.SearchPage("same name", 1, 1); len(result) != 1 || result[0].ID != other.ID {
		t.Fatalf("next search page failed to skip the exact match: %+v", result)
	}
	if result := snapshot.SearchPage("same name", -1, 1); len(result) != 0 {
		t.Fatalf("negative search offset returned %+v", result)
	}
	if result := snapshot.Search("SEARCH BODY", 1); len(result) != 1 || result[0].ID != page.ID {
		t.Fatalf("body search = %+v", result)
	}
	if result := snapshot.Search("", 0); len(result) != 0 {
		t.Fatalf("nonpositive search limit returned %+v", result)
	}

	// Both callers edit the same revision. The losing writer leaves no
	// under-construction file or second update.
	results := make(chan error, 2)
	var both sync.WaitGroup
	for _, body := range []string{"first content", "second content"} {
		both.Go(func() {
			change := *page
			change.Body = body
			_, err := store.WritePage(PageWrite{Page: change}, testStamp)
			results <- err
		})
	}
	both.Wait()
	close(results)
	var successes, conflicts int
	for err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrConflict):
			conflicts++
		default:
			t.Fatal(err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("successes %d conflicts %d", successes, conflicts)
	}
	files, err := os.ReadDir(filepath.Join(store.Dir(), pagesDir, "entries"))
	if err != nil || len(files) != 2 {
		t.Fatalf("failed write left partial files: %v %v", files, err)
	}
}
