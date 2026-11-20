package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClientsDefaultOffAndRepairBlankDirs(t *testing.T) {
	cfg := Default()
	if cfg.Clients.Enabled {
		t.Fatal("local clients must start off")
	}
	if cfg.Clients.RecentDays != DefaultClientRecentDays {
		t.Fatalf("recent days = %d", cfg.Clients.RecentDays)
	}
	cfg.normalizeClients()
	if cfg.Clients.ClaudeDir == "" || cfg.Clients.CodexDir == "" || cfg.Clients.CursorDir == "" {
		t.Fatalf("blank dirs were not filled: %+v", cfg.Clients)
	}
	custom := cfg.Clients
	custom.Enabled = true
	custom.ClaudeDir = t.TempDir()
	custom.RecentDays = 0
	custom.RunningStaleSeconds = 0
	cfg.Clients = custom
	cfg.normalizeClients()
	if cfg.Clients.Enabled {
		t.Fatal("a legacy enabled value reactivated local clients")
	}
	if cfg.Clients.ClaudeDir != custom.ClaudeDir {
		t.Fatalf("custom dir replaced: %s", cfg.Clients.ClaudeDir)
	}
	if cfg.Clients.RecentDays != DefaultClientRecentDays || cfg.Clients.RunningStaleSeconds != DefaultClientStaleSeconds {
		t.Fatalf("broken numbers not repaired: %+v", cfg.Clients)
	}
	// A sample prompt must not become a directory default.
	for _, dir := range []string{cfg.Clients.ClaudeDir, cfg.Clients.CodexDir, cfg.Clients.CursorDir} {
		if strings.Contains(strings.ToLower(dir), "notes.md") {
			t.Fatalf("example leaked into %s", dir)
		}
	}
}

func TestLegacyClientsStayDisabledOnStartup(t *testing.T) {
	dir := t.TempDir()
	const yaml = "clients:\n  enabled: true\n  recent_days: 9\n"
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Clients.Enabled || cfg.Clients.RecentDays != 9 {
		t.Fatalf("loaded clients settings = %+v", cfg.Clients)
	}
}
