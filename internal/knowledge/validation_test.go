package knowledge

import (
	"errors"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestSchemaRejectsInvalidIdentityAndVocabulary(t *testing.T) {
	for _, slug := range []string{"", "Caps", "..", "../x", "set:entry", "con", "lpt2", "a--b", "a/", strings.Repeat("x", 101)} {
		if ValidSlug(slug) {
			t.Errorf("unsafe slug %q accepted", slug)
		}
	}
	if !ValidSlug("entry-3") || !ValidSlug(strings.Repeat("x", 100)) {
		t.Fatal("portable slug refused")
	}
	for name, mutate := range map[string]func(*Schema){
		"version":            func(s *Schema) { s.Version = 2 },
		"types":              func(s *Schema) { s.Types = nil },
		"folder":             func(s *Schema) { s.Types[0].Dir = "../outside" },
		"duplicate folder":   func(s *Schema) { s.Types = append(s.Types, PageType{ID: "alpha", Dir: s.Types[0].Dir}) },
		"freshness":          func(s *Schema) { s.Types[0].FreshnessDays = -1 },
		"field":              func(s *Schema) { s.Types[0].Fields = []Field{{ID: "name", Kind: "unknown"}} },
		"duplicate field":    func(s *Schema) { s.Types[0].Fields = []Field{{ID: "name", Kind: "text"}, {ID: "name", Kind: "string"}} },
		"relation endpoints": func(s *Schema) { s.Relations[0].To = nil },
		"duplicate endpoint": func(s *Schema) { s.Relations[0].To = []string{"entry", "entry"} },
		"built-in view":      func(s *Schema) { s.Views = []View{{ID: "pending", RootType: "entry"}} },
		"view depth":         func(s *Schema) { s.Views = []View{{ID: "graph", RootType: "entry", Depth: maxViewDepth + 1}} },
		"view follow":        func(s *Schema) { s.Views = []View{{ID: "graph", RootType: "entry", Follow: []string{"unknown"}}} },
		"view follow duplicate": func(s *Schema) {
			s.Views = []View{{ID: "graph", RootType: "entry", Follow: []string{"related", "related"}}}
		},
	} {
		t.Run(name, func(t *testing.T) {
			schema := starterSchema()
			mutate(&schema)
			_, err := validateSchema(schema)
			assertError(t, err, ErrInvalid)
		})
	}
	for _, input := range []string{"[", "unknown: true", "version: 1\n---\nversion: 1"} {
		_, _, err := parseSchema([]byte(input))
		assertError(t, err, ErrInvalid)
	}
	_, err := appendYAML([]byte("invalid: ["), SchemaAppend{})
	if err == nil {
		t.Fatal("invalid YAML unexpectedly appended")
	}
	_, err = appendYAML([]byte("types: &kinds []\nviews: *kinds\n"), SchemaAppend{Views: []View{{ID: "a"}}})
	assertError(t, err, ErrInvalid)
}

func TestPageFieldsAndSourceShapesAreValidatedBeforeWriting(t *testing.T) {
	store := newWiki(t)
	enableWiki(t, store)
	appendWiki(t, store, SchemaAppend{Types: []PageType{{ID: "alpha", Dir: "alphas", Fields: []Field{
		{ID: "name", Kind: "text", Required: true},
		{ID: "description", Kind: "string"},
		{ID: "amount", Kind: "number"},
		{ID: "count", Kind: "integer"},
		{ID: "flag", Kind: "boolean"},
		{ID: "on", Kind: "date"},
		{ID: "link", Kind: "url"},
		{ID: "tags", Kind: "list", Required: true},
	}}}})
	valid := Page{ID: "valid", Type: "alpha", Title: "Page", Fields: map[string]any{
		"name": "text", "description": "", "amount": 3.25, "count": float64(2), "flag": false,
		"on": "2026-01-02", "link": "https://example.invalid/path", "tags": []string{"one"},
	}, Sources: []Source{{URI: "urn:example:item"}}}
	created := writePage(t, store, valid)
	detail := readPage(t, snapshotWiki(t, store), "valid")
	if detail.Fields["flag"] != false || detail.Fields["count"] != 2 || detail.Fields["amount"] != 3.25 {
		t.Fatalf("valid field values failed round trip: %+v", detail.Fields)
	}
	detail.Fields["tags"].([]any)[0] = "changed"
	detail.Fields["on"] = "changed"
	again := readPage(t, snapshotWiki(t, store), "valid")
	if again.Fields["tags"].([]any)[0] != "one" || again.Fields["on"] != "2026-01-02" {
		t.Fatal("a mutable field response changed its snapshot")
	}
	for test, mutate := range map[string]func(*Page){
		"slug":               func(p *Page) { p.ID = "../escape" },
		"type":               func(p *Page) { p.Type = "missing" },
		"title":              func(p *Page) { p.Title = "" },
		"missing field":      func(p *Page) { delete(p.Fields, "name") },
		"empty field":        func(p *Page) { p.Fields["name"] = " " },
		"undefined field":    func(p *Page) { p.Fields["unknown"] = true },
		"wrong field kind":   func(p *Page) { p.Fields["flag"] = 1 },
		"fractional integer": func(p *Page) { p.Fields["count"] = 2.5 },
		"infinite number":    func(p *Page) { p.Fields["amount"] = math.Inf(1) },
		"date":               func(p *Page) { p.Fields["on"] = "tomorrow" },
		"relative URL":       func(p *Page) { p.Fields["link"] = "/relative" },
		"incomplete URL":     func(p *Page) { p.Fields["link"] = "http:path" },
		"whitespace in URL":  func(p *Page) { p.Fields["link"] = "https://example.invalid/path " },
		"empty array":        func(p *Page) { p.Fields["tags"] = []string{} },
		"array of numbers":   func(p *Page) { p.Fields["tags"] = []any{2} },
		"non-array":          func(p *Page) { p.Fields["tags"] = "one" },
		"as of":              func(p *Page) { p.AsOf = "tomorrow" },
		"alias":              func(p *Page) { p.Aliases = []string{" "} },
		"relative source":    func(p *Page) { p.Sources = []Source{{URI: "relative"}} },
		"mixed source": func(p *Page) {
			p.Sources = []Source{{URI: "https://example.invalid/a", Kind: "message", Seq: 1, ThreadID: "thread"}}
		},
		"unknown history": func(p *Page) { p.Sources = []Source{{Kind: "turn", Seq: 1, ThreadID: "thread"}} },
	} {
		t.Run(test, func(t *testing.T) {
			candidate := clonePage(*created)
			mutate(&candidate)
			_, err := store.WritePage(PageWrite{Page: candidate}, testStamp)
			assertError(t, err, ErrInvalid)
			if fresh := readPage(t, snapshotWiki(t, store), "valid"); fresh.Revision != created.Revision {
				t.Fatal("invalid update changed the page revision")
			}
		})
	}
	_, err := store.WritePage(PageWrite{Page: *created}, Stamp{})
	assertError(t, err, ErrInvalid)
	if fieldValueMatches(Field{Kind: "number"}, "a") {
		t.Fatal("numeric field admitted a string")
	}
	path := filepath.Join(store.Dir(), pagesDir, "alphas", "valid.md")
	unquoted := strings.Replace(string(diskRead(t, path)), "\"2026-01-02\"", "2026-01-02", 1)
	diskWrite(t, path, []byte(unquoted))
	edited := snapshotWiki(t, store)
	if detail := readPage(t, edited, "valid"); detail.Fields["on"] != "2026-01-02" ||
		len(edited.Lint(testStamp.UpdatedAt)) != 0 || detail.Revision == created.Revision {
		t.Fatalf("unquoted field date did not survive hand editing: %+v, issues %+v", detail, edited.Lint(testStamp.UpdatedAt))
	}
}

func TestExternalMalformedPageIdentitiesRemainDiagnosable(t *testing.T) {
	store := newWiki(t)
	enableWiki(t, store)
	appendWiki(t, store, SchemaAppend{Types: []PageType{
		{ID: "alpha", Dir: "alphas", FreshnessDays: 1}, {ID: "beta", Dir: "betas"}, {ID: "other", Dir: "others"},
	}})
	pages := filepath.Join(store.Dir(), pagesDir)
	diskWrite(t, filepath.Join(pages, "entries", "duplicate.md"), []byte("---\nid: duplicate\ntype: entry\ntitle: A\n---\n"))
	diskWrite(t, filepath.Join(pages, "alphas", "duplicate.md"), []byte("---\nid: duplicate\ntype: alpha\ntitle: B\n---\n"))
	diskWrite(t, filepath.Join(pages, "betas", "duplicate.md"), []byte("---\nid: duplicate\ntype: beta\ntitle: C\n---\n"))
	diskWrite(t, filepath.Join(pages, "entries", "wrong.md"), []byte("---\nid: wrong\ntype: alpha\ntitle: Wrong\n---\n"))
	diskWrite(t, filepath.Join(pages, "entries", "boom.md"), []byte("No YAML"))
	diskWrite(t, filepath.Join(pages, "alphas", "bad-data.md"), []byte("---\nid: bad-data\ntype: alpha\ntitle: Bad data\nfields: {nested: {1: b}}\n---\n"))
	diskWrite(t, filepath.Join(pages, "alphas", "bad-value.md"), []byte("---\nid: bad-value\ntype: alpha\ntitle: Same\nfields: {nested: {a: [b]}}\n---\n"))
	diskWrite(t, filepath.Join(pages, "alphas", "same.md"), []byte("---\nid: same\ntype: alpha\ntitle: Same\n---\n"))
	diskWrite(t, filepath.Join(pages, "alphas", "ignored.tmp"), []byte("Temporary"))
	diskWrite(t, filepath.Join(pages, "flat.md"), []byte("Wrong level"))
	snap := snapshotWiki(t, store)
	if status := snap.Status(time.Now().AddDate(0, 0, 2)); status.PageCount != 2 ||
		status.PendingCount != 10 {
		t.Fatalf("lint lost invalid identities or maturity: %+v %+v", status, snap.Lint(time.Now().AddDate(0, 0, 2)))
	}
	_, err := store.WritePage(PageWrite{Page: Page{ID: "duplicate", Type: "other", Title: "D"}}, testStamp)
	assertError(t, err, ErrConflict)
	assertError(t, store.DeletePage("duplicate", "anything"), ErrConflict)
	_, err = snap.Page("duplicate")
	assertError(t, err, ErrNotFound)
	lookup := snap.Search("same", 1)
	if len(lookup) != 1 || lookup[0].ID != "bad-value" {
		t.Fatalf("ambiguous exact matches have unstable tie-break: %+v", lookup)
	}
	detail := readPage(t, snap, "bad-value")
	detail.Fields["nested"].(map[string]any)["a"].([]any)[0] = "changed"
	if result := readPage(t, snap, "bad-value").Fields["nested"].(map[string]any)["a"].([]any)[0]; result != "b" {
		t.Fatalf("nested invalid value mutated cached page: %+v", result)
	}
}

func TestKnowledgeNeverFollowsExternalSymlinks(t *testing.T) {
	outside := t.TempDir()
	diskWrite(t, filepath.Join(outside, "secret.md"), []byte("private"))
	preserve := func(path string) {
		if data := diskRead(t, path); string(data) != "private" {
			t.Fatalf("outside file changed: %s", data)
		}
	}
	link := func(t *testing.T, from, to string) {
		t.Helper()
		if err := os.Symlink(from, to); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
	}
	t.Run("knowledge directory", func(t *testing.T) {
		store := newWiki(t)
		link(t, outside, store.Dir())
		_, err := store.Enable()
		assertError(t, err, ErrInvalid)
		_, err = store.Snapshot()
		assertError(t, err, ErrInvalid)
		_, err = store.WritePage(PageWrite{}, testStamp)
		assertError(t, err, ErrInvalid)
	})
	t.Run("schema file", func(t *testing.T) {
		store := newWiki(t)
		if err := os.Mkdir(store.Dir(), 0700); err != nil {
			t.Fatal(err)
		}
		link(t, filepath.Join(outside, "secret.md"), filepath.Join(store.Dir(), schemaFile))
		_, err := store.Enable()
		assertError(t, err, ErrInvalid)
	})
	t.Run("pages directory", func(t *testing.T) {
		store := newWiki(t)
		enableWiki(t, store)
		link(t, outside, filepath.Join(store.Dir(), pagesDir))
		_, err := store.Snapshot()
		assertError(t, err, ErrInvalid)
		_, err = store.Enable()
		assertError(t, err, ErrInvalid)
	})
	t.Run("type directory", func(t *testing.T) {
		store := newWiki(t)
		enableWiki(t, store)
		pages := filepath.Join(store.Dir(), pagesDir)
		if err := os.Mkdir(pages, 0700); err != nil {
			t.Fatal(err)
		}
		link(t, outside, filepath.Join(pages, "entries"))
		snap := snapshotWiki(t, store)
		if issues := snap.Lint(testStamp.UpdatedAt); len(issues) != 1 || issues[0].Path != "pages/entries" {
			t.Fatalf("unsafe folder not linted: %+v", issues)
		}
		_, err := store.WritePage(PageWrite{Page: Page{ID: "secret", Type: "entry", Title: "Secret"}}, testStamp)
		assertError(t, err, ErrInvalid)
	})
	t.Run("page file", func(t *testing.T) {
		store := newWiki(t)
		enableWiki(t, store)
		writePage(t, store, Page{ID: "safe", Type: "entry", Title: "Safe", Body: "[[secret]]"})
		link(t, filepath.Join(outside, "secret.md"), filepath.Join(store.Dir(), pagesDir, "entries", "secret.md"))
		snap := snapshotWiki(t, store)
		if issues := snap.Lint(testStamp.UpdatedAt); len(issues) != 2 || !slices.Equal(readPage(t, snap, "safe").Unresolved, []string{"secret"}) {
			t.Fatalf("unsafe page not reported as broken: %+v", issues)
		}
		_, err := store.WritePage(PageWrite{Page: Page{ID: "secret", Type: "entry", Title: "Secret"}}, testStamp)
		assertError(t, err, ErrConflict)
	})
	preserve(filepath.Join(outside, "secret.md"))
}

func TestKnowledgeFilesRefuseOversizeWithoutPublishing(t *testing.T) {
	store := newWiki(t)
	enableWiki(t, store)
	_, err := store.WritePage(PageWrite{Page: Page{
		ID: "alpha", Type: "entry", Title: "Alpha", Body: strings.Repeat("a", maxFileBytes),
	}}, testStamp)
	assertError(t, err, ErrInvalid)
	path := filepath.Join(store.Dir(), pagesDir, "entries", "alpha.md")
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("oversized page was published: %v", err)
	}
}
