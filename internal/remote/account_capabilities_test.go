package remote

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
)

func TestDiscoveryFailureClosesFeaturesWithoutSigningOutOrClearingPreferences(t *testing.T) {
	g := newAccountGateway(t)
	var rejected atomic.Bool
	g.bootstrap = func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-AIGateway-Model-Roles") != "3" {
			t.Error("missing ten-role negotiation")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{
			"capabilities_url": g.server.URL + "/client/models/v1/capabilities",
			"models":           map[string]any{"source_id": accountModelsSourceID, "source_kind": "gateway", "execution": "desktop", "label": "Account", "api_base_url": g.server.URL + "/client/desktop/v1", "catalog_url": g.server.URL + "/client/desktop/v1/models", "api_access_enabled": true, "configurable": true},
		}})
	}
	g.extra = func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != "/client/models/v1/capabilities" {
			return false
		}
		if r.Header.Get("Authorization") != "Bearer "+accountModelsToken {
			t.Error("capability credential changed")
		}
		if rejected.Load() {
			w.WriteHeader(503)
			return true
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"version": 1, "model_roles_version": 3, "sources": map[string]any{"preset": map[string]any{"authorized": true, "available": true, "configurable": true, "capabilities": map[string]any{"conversation": map[string]any{"supported": true, "available": true, "configurable": true, "endpoints": []string{"chat_completions"}}}}, "gateway": map[string]any{"authorized": false, "available": false, "configurable": false, "capabilities": map[string]any{}}}}})
		return true
	}
	a, p, cfg := accountModelsFixture(t, g)
	if a.Refresh(t.Context()).State != AccountReady || !p.ModelPolicy().Ready {
		t.Fatal("negotiation did not enable declared chat")
	}
	before := cfg.ModelsSnapshot().AccountModel
	rejected.Store(true)
	if a.Refresh(t.Context()).State == AccountSignedOut {
		t.Fatal("discovery outage logged the account out")
	}
	if p.ModelPolicy().Ready || p.ModelPolicy().Configurable || p.ModelPolicy().CapabilityStatus != "unavailable" {
		t.Fatal("failed discovery retained permission")
	}
	if cfg.ModelsSnapshot().AccountModel != before {
		t.Fatal("outage changed preference")
	}
	rejected.Store(false)
	if a.Refresh(t.Context()).State != AccountReady || !p.ModelPolicy().Ready {
		t.Fatal("retry did not restore capability")
	}
}

func TestAccountRefreshSharesInFlightDiscoveryWithIndependentCancellation(t *testing.T) {
	g := newAccountGateway(t)
	var reads atomic.Int32
	entered, release := make(chan struct{}), make(chan struct{})
	g.bootstrap = func(w http.ResponseWriter, r *http.Request) {
		if reads.Add(1) == 2 {
			close(entered)
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		}
		g.writeBootstrap(w, g.server.URL, true)
	}
	account, pool, _ := accountModelsFixture(t, g)
	account.Start()
	t.Cleanup(account.Stop)
	waitFor(t, func() bool { return account.State().State == AccountReady }, "initial discovery")

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	first, second := make(chan error, 1), make(chan error, 1)
	go func() { first <- pool.RefreshCapabilities(ctx) }()
	select {
	case <-entered:
	case <-t.Context().Done():
		t.Fatal("refresh never reached bootstrap")
	}
	waiter := &accountRefreshWaitContext{Context: t.Context(), waiting: make(chan struct{})}
	go func() { second <- pool.RefreshCapabilities(waiter) }()
	select {
	case <-waiter.waiting:
	case <-t.Context().Done():
		t.Fatal("second caller never waited for discovery")
	}
	cancel()
	if err := <-first; !errors.Is(err, context.Canceled) {
		t.Fatalf("first caller's cancellation was lost: %v", err)
	}
	close(release)
	if err := <-second; err != nil || reads.Load() != 2 {
		t.Fatalf("second caller did not share the running refresh: err=%v bootstrap requests=%d", err, reads.Load())
	}
	if err := pool.RefreshCapabilities(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("an already canceled caller queued discovery: %v", err)
	}
	account.Stop()
	if err := pool.RefreshCapabilities(t.Context()); err == nil {
		t.Fatal("the stopped discovery owner accepted a request")
	}
}

type accountRefreshWaitContext struct {
	context.Context
	once    sync.Once
	waiting chan struct{}
}

func (c *accountRefreshWaitContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.waiting) })
	return c.Context.Done()
}
