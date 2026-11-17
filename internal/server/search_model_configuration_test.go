package server_test

import (
	"net/http"
	"testing"

	"github.com/LubyRuffy/eino-swarm/internal/config"
)

// Settings, the provider role resolver and search must agree without a second switch.
func TestSearchFollowsConfiguredModelAndExplicitRemovalThroughHTTP(t *testing.T) {
	h := newHarness(t)
	h.json(http.MethodPut, "/api/settings", map[string]any{
		"search": map[string]any{"embedding": false, "embedding_model": "legacy-embed"},
	}, http.StatusOK)
	for _, tc := range []struct {
		name     string
		bindings map[string]config.ModelRef
		on       bool
	}{
		{"legacy model enables automatically", nil, true},
		{"empty binding overrides legacy pin", map[string]config.ModelRef{"embedding": {}}, false},
		{"configured binding enables automatically", map[string]config.ModelRef{"embedding": {ProviderID: "default", Model: "new-embed"}}, true},
		{"removing configuration disables", map[string]config.ModelRef{"embedding": {}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.bindings != nil {
				h.json(http.MethodPut, "/api/settings", map[string]any{"models": map[string]any{"bindings": tc.bindings}}, http.StatusOK)
				disk, err := config.Load(h.app.Config.DataDir())
				if err != nil {
					t.Fatal(err)
				}
				ref, exists := disk.ModelsSnapshot().Bindings[config.ProductModeLocal]["embedding"]
				if !exists || ref != tc.bindings["embedding"] {
					t.Fatalf("binding not durable: %+v, %v", ref, exists)
				}
			}
			out := h.json(http.MethodGet, "/api/search?q=configuration-probe", nil, http.StatusOK)
			if out["embedding"] != tc.on {
				t.Fatalf("semantic ranking = %v, want %v", out, tc.on)
			}
		})
	}
	h.json(http.MethodPut, "/api/settings", map[string]any{
		"models": map[string]any{"bindings": map[string]any{}},
		"search": map[string]any{"embedding": true, "embedding_model": ""},
	}, http.StatusOK)
	out := h.json(http.MethodGet, "/api/search?q=configuration-probe", nil, http.StatusOK)
	if out["embedding"] != false {
		t.Fatalf("an old true switch cannot enable search without a model: %v", out)
	}
}
