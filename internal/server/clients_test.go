package server_test

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/LubyRuffy/eino-swarm/internal/config"
)

func TestLegacySettingsCannotEnableLocalClientTasks(t *testing.T) {
	h := newHarness(t)
	root := t.TempDir()
	path := filepath.Join(root, "projects", "work", "local.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"type":"user","message":{"content":"private local task"}}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	h.json(http.MethodPut, "/api/settings", map[string]any{
		"clients": map[string]any{"enabled": true, "claude_dir": root},
	}, http.StatusOK)
	settings := h.json(http.MethodGet, "/api/settings", nil, http.StatusOK)
	body, _ := settings["settings"].(map[string]any)
	got, _ := body["clients"].(map[string]any)
	if got["enabled"] != false || got["claude_dir"] != root {
		t.Fatalf("settings clients = %v", got)
	}
	cfg, err := config.Load(h.app.Engine.Config().DataDir())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Clients.Enabled || cfg.Clients.ClaudeDir != root {
		t.Fatalf("persisted clients = %+v", cfg.Clients)
	}

	// A raw value also cannot bypass the HTTP compatibility endpoints.
	h.app.Engine.Config().Clients.Enabled = true
	for _, query := range []string{"", "?before=1"} {
		catalog := h.json(http.MethodGet, "/api/clients"+query, nil, http.StatusOK)
		tools, ok := catalog["tools"].([]any)
		if catalog["enabled"] != false || catalog["pending"] != nil || !ok || len(tools) != 0 {
			t.Errorf("catalog %q = %v", query, catalog)
		}
	}
	h.json(http.MethodGet, "/api/clients?before=bad", nil, http.StatusBadRequest)
	h.json(http.MethodGet, "/api/clients/task", nil, http.StatusBadRequest)
	h.json(http.MethodGet, "/api/clients/task?id=claude:local&before=-1", nil, http.StatusBadRequest)
	h.json(http.MethodGet, "/api/clients/task?id=claude:local", nil, http.StatusNotFound)
}
