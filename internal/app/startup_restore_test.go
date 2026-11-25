package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/LubyRuffy/eino-swarm/internal/config"
	"github.com/LubyRuffy/eino-swarm/internal/store"
)

// A listener must be available before recovery, while the scheduler and new
// work remain gated; a client can attach and see the durable history meanwhile.
func TestDeferredRestoreListensBeforeTasksAreRecovered(t *testing.T) {
	a, err := New(Options{DataDir: t.TempDir(), Addr: "127.0.0.1:0", Mock: true, NoAssets: true, DeferRestore: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Shutdown(context.Background()) })
	url, err := a.Listen()
	if err != nil {
		t.Fatal(err)
	}
	served := make(chan error, 1)
	go func() { served <- a.Serve() }()
	t.Cleanup(func() {
		a.Shutdown(context.Background())
		if err := <-served; err != nil {
			t.Error(err)
		}
	})
	resp, err := http.Get(url + "/api/meta")
	if err != nil {
		t.Fatal(err)
	}
	var meta struct {
		Startup string `json:"startup"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&meta); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if meta.Startup != "restoring" || a.Engine.ActiveWork() == 0 {
		t.Fatalf("not protected during recovery: %+v", meta)
	}
	if err := a.Restore(context.Background()); err != nil {
		t.Fatal(err)
	}
	resp, err = http.Get(url + "/api/meta")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(&meta); err != nil {
		t.Fatal(err)
	}
	if meta.Startup != "ready" {
		t.Fatalf("not ready after recovery: %+v", meta)
	}
}

func TestSemanticStartupBackfillWaitsForRecovery(t *testing.T) {
	embedded := make(chan struct{}, 1)
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/embeddings" {
			http.NotFound(w, r)
			return
		}
		var req struct{ Input []string }
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		select {
		case embedded <- struct{}{}:
		default:
		}
		data := make([]map[string]any, len(req.Input))
		for i := range data {
			data[i] = map[string]any{"index": i, "embedding": []float64{1, 0}}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
	defer model.Close()
	dir := t.TempDir()
	cfg, err := config.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Product.Mode = config.ProductModeLocal
	cfg.Models.Providers = []config.Provider{{ID: "local-test", Model: "chat", BaseURL: model.URL}}
	cfg.Models.Default = "local-test"
	cfg.Search.EmbeddingProvider, cfg.Search.EmbeddingModel = "local-test", "embedding"
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(cfg.DBPath())
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CreateThread(&store.Thread{Title: "Keyword witness"}); err != nil {
		_ = st.Close()
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	a, err := New(Options{DataDir: dir, NoAssets: true, DeferRestore: true})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Shutdown(context.Background())
	a.Search.Reload()
	if hits, err := a.Store.SearchThreads("witness", 10); err != nil || len(hits) != 1 {
		t.Fatalf("keyword index unavailable during startup: hits=%v err=%v", hits, err)
	}
	select {
	case <-embedded:
		t.Fatal("background embeddings began while recovery was pending")
	case <-time.After(100 * time.Millisecond):
	}
	if err := a.Restore(t.Context()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-embedded:
	case <-time.After(3 * time.Second):
		t.Fatal("semantic backfill did not run after recovery")
	}
	if state, _ := a.Engine.StartupStatus(); state != "ready" {
		t.Fatalf("embedding service started at %q", state)
	}
}

func TestCancelledRestoreStaysRecoverableAndShutdownCancelsItsOwner(t *testing.T) {
	a, err := New(Options{DataDir: t.TempDir(), Mock: true, NoAssets: true, DeferRestore: true})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := a.Restore(ctx); err == nil {
		t.Fatal("cancelled restoration accepted")
	}
	if state, _ := a.Engine.StartupStatus(); state != "restoring" {
		t.Fatal("cancellation opened admission")
	}
	if err := a.Restore(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := a.Restore(context.Background()); err != nil {
		t.Fatal(err)
	}
	a.Shutdown(context.Background())
	b, err := New(Options{DataDir: t.TempDir(), Mock: true, NoAssets: true, DeferRestore: true})
	if err != nil {
		t.Fatal(err)
	}
	b.Shutdown(context.Background())
	if err := b.Restore(context.Background()); err == nil {
		t.Fatal("shutdown revived restoration")
	}
}
