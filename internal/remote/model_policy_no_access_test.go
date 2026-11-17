package remote

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/LubyRuffy/eino-swarm/internal/config"
)

// Losing API access is not permission to unlock a managed preset, and it must
// not remove a configurable account's right to use its own service.
func TestModelPermissionSurvivesUnavailableAccountCatalog(t *testing.T) {
	for _, configurable := range []bool{false, true} {
		t.Run(map[bool]string{false: "managed", true: "ordinary"}[configurable], func(t *testing.T) {
			g := newAccountGateway(t)
			g.bootstrap = func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"models": map[string]any{
					"source_id": accountModelsSourceID, "source_kind": "gateway", "execution": "desktop", "label": "Scope fixture", "api_access_enabled": false, "configurable": configurable,
				}}})
			}
			account, pool, cfg := accountModelsFixture(t, g)
			if err := cfg.Update(func(next *config.Config) error { next.Product.Mode = config.ProductModeFOFA; return nil }); err != nil {
				t.Fatal(err)
			}
			if state := account.Refresh(t.Context()); state.State != AccountNoAccess {
				t.Fatal(state)
			}
			policy := pool.ModelPolicy()
			if !policy.Ready || policy.Configurable != configurable || policy.Scope == "" {
				t.Fatalf("catalog refusal lost model permissions: %+v", policy)
			}
			own := cfg.ModelsSnapshot().Default
			if _, err := pool.ResolveModel(own, ""); configurable && err != nil || !configurable && err == nil {
				t.Fatalf("configurable=%t resolved personal model: %v", configurable, err)
			}
			if pool.AccountSourceID() != "" {
				t.Fatal("an unavailable account became a callable catalog")
			}
			if _, err := pool.ResolveModel(accountModelsSourceID, "old-account-model"); err == nil {
				t.Fatal("unavailable source stayed callable")
			}
		})
	}
}

func TestUnavailableBootstrapCannotExecuteAPersonalModelInGatewayMode(t *testing.T) {
	gateway := newAccountGateway(t)
	gateway.bootstrap = func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"models":{"source_kind":"untrusted"}}}`))
	}
	account, pool, cfg := accountModelsFixture(t, gateway)
	if err := cfg.Update(func(next *config.Config) error { next.Product.Mode = config.ProductModeFOFA; return nil }); err != nil {
		t.Fatal(err)
	}
	if state := account.Refresh(t.Context()); state.State != AccountUnavailable {
		t.Fatal(state)
	}
	if err := pool.ValidateConversationSelection(cfg.ModelsSnapshot().Default, "personal-model"); err == nil {
		t.Fatal("missing Gateway policy executed the retained personal selection")
	}
}
