package remote

import (
	"encoding/json"
	"io"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/LubyRuffy/eino-swarm/internal/config"
	"github.com/LubyRuffy/eino-swarm/internal/engine"
	"github.com/LubyRuffy/eino-swarm/internal/store"
)

// Exercise bootstrap, catalog publication, cache retention and the phone
// projection: transport type alone cannot distinguish a functional alias.
func TestThirdPartyAuxiliaryBindingsStayOutOfConversationCatalogs(t *testing.T) {
	g := newAccountGateway(t)
	var unavailable atomic.Bool
	g.bootstrap = func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{
			"models": map[string]any{
				"source_id": accountModelsSourceID, "source_kind": "gateway", "execution": "desktop", "label": "Partner",
				"api_base_url": g.server.URL + "/client/desktop/v1", "catalog_url": g.server.URL + "/client/desktop/v1/models",
				"client_catalog_url": g.server.URL + "/preset/models", "api_access_enabled": true, "configurable": true,
				"defaults": map[string]string{"conversation": "conversation", "title": "title", "compact": "compact", "translation": "translation"},
			},
			"gateway_models": map[string]any{
				"source_id": localGatewaySourceID, "source_kind": "gateway", "execution": "desktop", "label": "Original",
				"api_base_url": g.server.URL + "/original", "catalog_url": g.server.URL + "/original/models", "api_access_enabled": true, "configurable": true,
			},
		}})
	}
	g.extra = func(w http.ResponseWriter, r *http.Request) bool {
		switch r.URL.Path {
		case "/preset/models", "/original/models":
			if unavailable.Load() {
				http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
				return true
			}
			_, _ = io.WriteString(w, `{"data":[{"id":"title","model_type":"chat"},{"id":"conversation","model_type":"chat"},{"id":"compact","model_type":"chat"},{"id":"translation","model_type":"chat"}]}`)
			return true
		}
		return false
	}
	am, pool, cfg := accountModelsFixture(t, g)
	if err := cfg.Update(func(c *config.Config) error { c.Product.Mode = config.ProductModeLocal; return nil }); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(cfg.DBPath())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	eng := engine.New(cfg, st, pool, am.log)
	t.Cleanup(eng.Shutdown)
	for _, cached := range []bool{false, true} {
		unavailable.Store(cached)
		if state := am.Refresh(t.Context()); state.State != AccountReady {
			t.Fatalf("cached=%v state=%+v", cached, state)
		}
		for _, name := range []string{"conversation", "title", "compact", "translation"} {
			if got := listedAccount(pool, accountModelsSourceID, name); got != (name == "conversation") {
				t.Fatalf("cached=%v third-party choice %q listed=%v", cached, name, got)
			}
			if !listedAccount(pool, localGatewaySourceID, name) {
				t.Fatalf("original model %q was hidden", name)
			}
			// Hiding a picker row must not remove the auxiliary target.
			if target, err := pool.ResolveModel(accountModelsSourceID, name); err != nil || target.Model != name {
				t.Fatalf("functional target %q: %+v %v", name, target, err)
			}
		}
		var choices []string
		for _, row := range modelViews(eng) {
			if row.ProviderID == accountModelsSourceID {
				choices = append(choices, row.Model)
			}
		}
		if len(choices) != 1 || choices[0] != "conversation" {
			t.Fatalf("phone choices=%v", choices)
		}
	}
}
