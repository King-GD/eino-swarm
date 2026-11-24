package server_test

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LubyRuffy/eino-swarm/internal/server"
)

func TestProjectKnowledgeRoutesKeepPageAndProjectIdentity(t *testing.T) {
	h := newHarness(t)
	project := h.newProject(map[string]any{"name": "Knowledge"})
	id := project["id"].(string)
	projectPath := "/api/projects/" + id
	base := projectPath + "/knowledge"
	dir := filepath.Join(h.app.Engine.Config().ProjectDir(id), "knowledge")
	schemaPath := filepath.Join(dir, "schema.yaml")

	get := func(path string, status int) map[string]any { return h.json(http.MethodGet, base+path, nil, status) }
	enable := func() map[string]any { return h.json(http.MethodPost, base, nil, http.StatusOK) }

	if got := get("", http.StatusOK); got["enabled"] != false || got["page_count"] != float64(0) {
		t.Fatalf("disabled status: %v", got)
	}
	assertKnowledgeAbsent(t, dir)
	get("/tree", http.StatusNotFound)
	get("/graph", http.StatusNotFound)
	get("/pages/first", http.StatusNotFound)
	h.json(http.MethodPost, "/api/projects/pj_missing/knowledge", nil, http.StatusNotFound)
	h.json(http.MethodGet, "/api/projects/pj_missing/knowledge", nil, http.StatusNotFound)
	h.json(http.MethodPost, base+"/reveal", nil, http.StatusNotImplemented)

	first := enable()
	initialSchema, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatal(err)
	}
	editedSchema := append(initialSchema, []byte("\n# Keep direct edits.\n")...)
	if err := os.WriteFile(schemaPath, editedSchema, 0o600); err != nil {
		t.Fatal(err)
	}
	second := enable()
	if first["enabled"] != true || second["enabled"] != true || first["revision"] == "" || second["revision"] == first["revision"] {
		t.Fatalf("enable did not read existing edited schema: %v / %v", first, second)
	}
	if current, err := os.ReadFile(schemaPath); err != nil || string(current) != string(editedSchema) {
		t.Fatalf("enabling an existing library replaced schema: %s, %v", current, err)
	}

	entryDir := filepath.Join(dir, "pages", "entries")
	if err := os.MkdirAll(entryDir, 0o700); err != nil {
		t.Fatal(err)
	}
	for id, markdown := range map[string]string{
		"first":  "---\nid: first\ntype: entry\ntitle: First\nlinks:\n  - rel: related\n    to: second\n---\nBody with [[missing]] and [[second]].\n",
		"second": "---\nid: second\ntype: entry\ntitle: Second\n---\nSecond body with [[first]].\n",
	} {
		if err := os.WriteFile(filepath.Join(entryDir, id+".md"), []byte(markdown), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if status := get("", http.StatusOK); status["page_count"] != float64(2) || status["pending_count"] != float64(1) {
		t.Fatalf("files did not reach status and lint: %v", status)
	}
	if tree := get("/tree?view=type", http.StatusOK); tree["view"] != "type" || len(tree["nodes"].([]any)) != 1 {
		t.Fatalf("type view: %v", tree)
	}
	if tree := get("/tree?view=pending", http.StatusOK); !strings.Contains(fmt.Sprint(tree["issues"]), "missing") {
		t.Fatalf("pending view omitted the red link: %v", tree)
	}
	graph := get("/graph", http.StatusOK)
	nodes, edges := graph["nodes"].([]any), graph["edges"].([]any)
	if len(nodes) != 2 || len(edges) != 1 {
		t.Fatalf("graph included a dangling endpoint or lost a page: %v", graph)
	}
	edge := edges[0].(map[string]any)
	if edge["source"] != "first" || edge["target"] != "second" || edge["wikilink"] != true ||
		fmt.Sprint(edge["relations"]) != "[related]" || nodes[0].(map[string]any)["title"] != "First" {
		t.Fatalf("graph lost the title or merged evidence: %v", graph)
	}
	get("/tree?view=other", http.StatusBadRequest)
	if page := get("/pages/first", http.StatusOK); page["id"] != "first" ||
		!strings.Contains(fmt.Sprint(page["body"]), "[[missing]]") ||
		!strings.Contains(fmt.Sprint(page["outgoing"]), "second") ||
		!strings.Contains(fmt.Sprint(page["mentions"]), "second") {
		t.Fatalf("source page: %v", page)
	}
	if page := get("/pages/second", http.StatusOK); page["id"] != "second" ||
		!strings.Contains(fmt.Sprint(page["incoming"]), "first") ||
		!strings.Contains(fmt.Sprint(page["mentions"]), "first") {
		t.Fatalf("linked page: %v", page)
	}
	get("/pages/missing", http.StatusNotFound)

	for _, invalid := range []string{"first.md", "foreign:first", "%2E%2E%2Fschema", "..%5Cschema", "%252e%252e%252fsecond"} {
		t.Run(invalid, func(t *testing.T) {
			resp := h.do(http.MethodGet, base+"/pages/"+invalid, nil)
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusBadRequest && resp.StatusCode != http.StatusNotFound {
				t.Fatalf("invalid page id was not refused: %s", resp.Status)
			}
		})
	}
	other := h.newProject(map[string]any{"name": "Other knowledge"})
	otherBase := "/api/projects/" + other["id"].(string) + "/knowledge"
	h.json(http.MethodPost, otherBase, nil, http.StatusOK)
	otherGraph := h.json(http.MethodGet, otherBase+"/graph", nil, http.StatusOK)
	if len(otherGraph["nodes"].([]any)) != 0 || len(otherGraph["edges"].([]any)) != 0 {
		t.Fatalf("the other project's graph leaked evidence: %v", otherGraph)
	}
	h.json(http.MethodGet, otherBase+"/pages/first", nil, http.StatusNotFound)
	if err := os.WriteFile(schemaPath, []byte("version: wrong\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	get("/tree", http.StatusBadRequest)
	get("/graph", http.StatusBadRequest)
	h.json(http.MethodPost, base, nil, http.StatusBadRequest)
	if got, err := os.ReadFile(schemaPath); err != nil || string(got) != "version: wrong\n" {
		t.Fatalf("re-enabling a malformed schema changed the file: %q, %v", got, err)
	}

	h.json(http.MethodDelete, projectPath, nil, http.StatusNoContent)
	assertKnowledgeAbsent(t, dir)
	h.json(http.MethodGet, base, nil, http.StatusNotFound)
	h.json(http.MethodPost, base, nil, http.StatusNotFound)
	assertKnowledgeAbsent(t, dir)
	h.json(http.MethodGet, otherBase, nil, http.StatusOK)
}

func TestProjectKnowledgeRevealUsesTheCheckedProjectDirectory(t *testing.T) {
	h := newHarness(t)
	id := h.newProject(map[string]any{"name": "Reveal"})["id"].(string)
	base := "/api/projects/" + id + "/knowledge"
	dir := filepath.Join(h.app.Engine.Config().ProjectDir(id), "knowledge")
	var revealed []string
	var revealErr error
	srv, err := server.New(server.Options{
		Engine: h.app.Engine,
		Mode:   server.ModeDesktop,
		Reveal: func(path string) error {
			revealed = append(revealed, path)
			return revealErr
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	postReveal := func(want int) {
		t.Helper()
		response := httptest.NewRecorder()
		srv.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodPost, base+"/reveal", strings.NewReader(`{"path":"/outside"}`)))
		if response.Code != want {
			t.Fatalf("reveal: %d, want %d: %s", response.Code, want, response.Body.String())
		}
	}

	postReveal(http.StatusNotFound)
	if len(revealed) != 0 {
		t.Fatalf("disabled library reached file manager: %v", revealed)
	}
	h.json(http.MethodPost, base, nil, http.StatusOK)
	postReveal(http.StatusOK)
	if len(revealed) != 1 || revealed[0] != dir {
		t.Fatalf("reveal used an unexpected directory: %v", revealed)
	}
	revealErr = errors.New("file manager unavailable")
	postReveal(http.StatusInternalServerError)

	outside := t.TempDir()
	if err := os.Rename(dir, filepath.Join(outside, "moved")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "moved"), dir); err != nil {
		t.Fatal(err)
	}
	postReveal(http.StatusBadRequest)
	if len(revealed) != 2 {
		t.Fatalf("symlink reached the file manager: %v", revealed)
	}
}

func assertKnowledgeAbsent(t *testing.T, dir string) {
	t.Helper()
	if _, err := os.Lstat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("knowledge directory should be absent: %v", err)
	}
}
