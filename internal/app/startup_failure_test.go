package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/LubyRuffy/eino-swarm/internal/config"
	"github.com/LubyRuffy/eino-swarm/internal/engine"
	"github.com/LubyRuffy/eino-swarm/internal/store"
)

// A recovery candidate read can fail after the store has opened. New must
// return that failure and close its assembled resources, including the WAL
// connection.
func TestNewRejectsFatalRecoveryAndClosesItsDatabase(t *testing.T) {
	for _, name := range []string{"OPENAI_BASE_URL", "OPENAI_API_KEY", "OPENAI_MODEL"} {
		t.Setenv(name, "")
	}
	dir := t.TempDir()
	cfg, err := config.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(cfg.DBPath())
	if err != nil {
		t.Fatal(err)
	}
	thread := &store.Thread{Title: "unfinished conversation"}
	if err := st.CreateThread(thread); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateTurn(&store.Turn{ThreadID: thread.ID}); err != nil {
		t.Fatal(err)
	}
	if err := st.DB().Exec("UPDATE startup_recovery_threads SET worker_revision = ? WHERE thread_id = ?", "invalid-integer", thread.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	// Prove that opening and migration succeed and the fatal error comes from
	// the indexed startup query, before conversation-level recovery.
	checked, err := store.Open(cfg.DBPath())
	if err != nil {
		t.Fatal("fixture failed before recovery: ", err)
	}
	if _, err := checked.StartupRecoveryCandidates(t.Context(), time.Now(), cfg.SwarmSnapshot().GoalAutoTurns()); err == nil || !strings.Contains(err.Error(), "worker_revision") {
		t.Fatalf("fixture did not fail at recovery-candidate decoding: %v", err)
	}
	if err := checked.Close(); err != nil {
		t.Fatal(err)
	}
	a, err := New(Options{DataDir: dir, Mock: true, NoAssets: true})
	if a != nil {
		t.Cleanup(func() { a.Shutdown(context.Background()) })
	}
	var partial *engine.RestoreFailures
	if a != nil || err == nil || errors.As(err, &partial) ||
		!strings.Contains(err.Error(), "store: list startup recovery candidates:") ||
		!strings.Contains(err.Error(), "worker_revision") {
		t.Fatalf("fatal startup recovery lost its scan-error context: app=%v error=%v", a, err)
	}
	// A second live WAL connection prevents changing the journal mode. This
	// checks actual SQL ownership after failure, not only a private close flag.
	probe, err := store.Open(cfg.DBPath())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := probe.Close(); err != nil {
			t.Error(err)
		}
	}()
	var mode string
	if err := probe.DB().Raw("PRAGMA journal_mode=DELETE").Scan(&mode).Error; err != nil || mode != "delete" {
		t.Fatalf("failed app retained a WAL connection: mode=%q err=%v", mode, err)
	}
}
