package provider

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/LubyRuffy/eino-swarm/internal/config"
	"github.com/cloudwego/eino/schema"
)

// Equal model names must retain the account source, credential and revocation
// boundary at the real HTTP client, including handles opened before a refresh.
func TestLocalCatalogRequestsKeepTheirSourceAcrossRefreshAndRevocation(t *testing.T) {
	var calls atomic.Int32
	paths := make(chan string, 8)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		paths <- r.URL.Path
		want := "Bearer preset-session"
		if strings.HasPrefix(r.URL.Path, "/extra/") {
			want = "Bearer extra-session"
		}
		if r.Header.Get("Authorization") != want {
			t.Error("source credential differs")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"source reply"},"finish_reason":"stop"}]}`)
	}))
	defer upstream.Close()
	cfg := configFor(t, true)
	cfg.Product.Mode = config.ProductModeLocal
	pool := New(cfg)
	primary := accountSourceFor(t, upstream.URL+"/managed", "managed-session", "fixed")
	primary.SetModelPolicy(false, map[string]string{"conversation": "fixed"})
	pool.SetAccountSource(primary)
	preset := accountSourceFor(t, upstream.URL+"/preset", "preset-session", "same")
	extra, err := NewAccountSource(config.AccountSourcePrefix+strings.Repeat("a", 32), "Extra", upstream.URL+"/extra", "extra-session", Catalog{Names: []string{"same"}})
	if err != nil {
		t.Fatal(err)
	}
	pool.SetLocalAccountSources([]*AccountSource{nil, preset, extra})
	pool.SetLocalAccountSources([]*AccountSource{nil, preset, extra})
	if len(pool.LocalAccountSources()) != 3 {
		t.Fatal("LOCAL catalog publication changed the source set")
	}
	first, err := pool.Get(t.Context(), preset.ID(), "same")
	if err != nil {
		t.Fatal(err)
	}
	second, err := pool.Get(t.Context(), extra.ID(), "same")
	if err != nil {
		t.Fatal(err)
	}
	input := []*schema.Message{schema.UserMessage("source fixture")}
	if _, err := first.Generate(t.Context(), input); err != nil {
		t.Fatal(err)
	}
	if _, err := second.Generate(t.Context(), input); err != nil {
		t.Fatal(err)
	}
	if a, b := <-paths, <-paths; a != "/preset/chat/completions" || b != "/extra/chat/completions" {
		t.Fatalf("source routes = %q %q", a, b)
	}
	if err := pool.validateModelSelection(extra.ID(), "removed-model"); !errors.Is(err, ErrModelPolicy) {
		t.Fatalf("unlisted LOCAL model = %v", err)
	}
	renewed := accountSourceFor(t, upstream.URL+"/preset", "renewed-session", "same")
	pool.SetLocalAccountSources([]*AccountSource{nil, renewed, extra})
	if _, err := first.Generate(t.Context(), input); !errors.Is(err, ErrModelPolicy) {
		t.Fatalf("old account credential survived renewal: %v", err)
	}
	pool.SetLocalAccountSources([]*AccountSource{renewed})
	if _, err := second.Generate(t.Context(), input); !errors.Is(err, ErrModelPolicy) {
		t.Fatalf("revoked LOCAL handle reached HTTP: %v", err)
	}
	pool.SetAccountSource(nil)
	if len(pool.LocalAccountSources()) != 0 {
		t.Fatal("logout retained an extra catalog")
	}
	if _, err := pool.Resolve(extra.ID()); !errors.Is(err, ErrAccountLoginRequired) {
		t.Fatalf("logout redirected a stored source: %v", err)
	}
	if calls.Load() != 2 {
		t.Fatal("a refused request reached an upstream")
	}
}

func TestGatewayModeHidesLocalExtrasAndUnavailableSourcesStayUncallable(t *testing.T) {
	cfg := configFor(t, true)
	pool := New(cfg)
	primary := accountSourceFor(t, "https://preset.invalid/v1", "preset-session")
	pool.SetAccountSource(primary)
	extra := accountSourceFor(t, "https://extra.invalid/v1", "extra-session")
	extra.id = config.AccountSourcePrefix + strings.Repeat("b", 32)
	pool.SetLocalAccountSources([]*AccountSource{extra})
	if _, err := pool.Resolve(extra.ID()); !errors.Is(err, ErrAccountLoginRequired) {
		t.Fatalf("gateway mode resolved a LOCAL-only catalog: %v", err)
	}
	for _, row := range pool.List() {
		if row.ProviderID == extra.ID() {
			t.Fatal("gateway mode listed a LOCAL-only catalog")
		}
	}
	cfg.Product.Mode = config.ProductModeLocal
	refused := NewUnavailableAccountSource(extra.ID(), "Extra", "https://extra.invalid/v1", "extra-session", false)
	pool.SetLocalAccountSources([]*AccountSource{refused})
	if _, err := pool.Resolve(extra.ID()); !errors.Is(err, ErrAccountModelsUnavailable) {
		t.Fatalf("unavailable LOCAL catalog = %v", err)
	}
	if err := pool.validateModelSelection(extra.ID(), "same"); !errors.Is(err, ErrAccountModelsUnavailable) {
		t.Fatal(err)
	}
}

func TestAbsentCatalogOwnersAndOfflinePoolsDoNotPublishAccountSources(t *testing.T) {
	var absent *Pool
	if absent.localExecution() || absent.LocalAccountSources() != nil || absent.accountForExecution(accountTestID) != nil {
		t.Fatal("absent owner published an account")
	}
	absent.SetLocalAccountSources(nil)
	cfg := configFor(t, true)
	cfg.Product.Mode = config.ProductModeLocal
	pool := NewMock(cfg)
	pool.SetLocalAccountSources([]*AccountSource{accountSourceFor(t, "https://extra.invalid/v1", "fixture")})
	if pool.LocalAccountSources() != nil {
		t.Fatal("offline mode retained a real account source")
	}
}
