package provider

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/LubyRuffy/eino-swarm/internal/config"
	"github.com/cloudwego/eino/schema"
)

func TestUnavailableAccountRejectsOpenedHTTPModelAndPreservesOrdinaryPersonalAccess(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"scope reply"},"finish_reason":"stop"}]}`)
	}))
	defer upstream.Close()
	cfg := configFor(t, false)
	cfg.Product.Mode = config.ProductModeFOFA
	cfg.Remote.AuthMode = config.RemoteAuthAccount
	cfg.Models.Providers[0].BaseURL = upstream.URL
	cfg.Models.Providers[0].Model = "personal-model"
	pool := New(cfg)
	source := accountSourceFor(t, upstream.URL, "private-session")
	source.SetModelPolicy(true, nil)
	pool.SetAccountSource(source)
	opened, err := pool.Get(t.Context(), source.id, "server-model")
	if err != nil {
		t.Fatal(err)
	}
	input := []*schema.Message{schema.UserMessage("permission fixture")}
	if _, err := opened.Generate(t.Context(), input); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatal("initial model did not cross HTTP")
	}
	unavailable := NewUnavailableAccountSource(source.id, source.label, source.baseURL, source.token, true)
	pool.SetAccountSource(unavailable)
	if _, err := opened.Generate(t.Context(), input); !errors.Is(err, ErrAccountModelsUnavailable) {
		t.Fatalf("opened model retained revoked catalog: %v", err)
	}
	if calls.Load() != 1 {
		t.Fatal("revoked account model reached HTTP")
	}
	if pool.AccountSourceID() != "" || !pool.ModelPolicy().Configurable {
		t.Fatal("catalog availability changed configuration permission")
	}
	personal, err := pool.Get(t.Context(), cfg.Models.Providers[0].ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := personal.Generate(t.Context(), input); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatal("ordinary account could not use its personal service")
	}
	pool.SetAccountSource(NewUnavailableAccountSource(source.id, source.label, source.baseURL, source.token, false))
	if _, _, err := pool.WorkerModel(t.Context(), cfg.Models.Providers[0].ID, "personal-model", "", nil); !errors.Is(err, ErrModelPolicy) {
		t.Fatalf("managed preset unlocked a worker model: %v", err)
	}
	pool.SetAccountSource(nil)
	if _, err := personal.Generate(t.Context(), input); !errors.Is(err, ErrModelPolicy) {
		t.Fatalf("unknown policy admitted an existing model: %v", err)
	}
	if calls.Load() != 2 {
		t.Fatal("missing policy reached HTTP")
	}
}
