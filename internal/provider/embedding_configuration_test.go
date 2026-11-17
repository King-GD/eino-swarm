package provider

import (
	"testing"

	"github.com/LubyRuffy/eino-swarm/internal/config"
)

// Clearing an editable binding must override a preset, while restoring defaults
// must continue to follow the server instead of saving a snapshot of its model.
func TestEmbeddingConfigurationFollowsPresetAndCanBeRemoved(t *testing.T) {
	cfg := config.Default()
	pool := New(cfg)
	src := accountSourceFor(t, "https://example.invalid/v1", "private")
	src.SetModelPolicy(true, map[string]string{"embedding": "preset-embed"})
	pool.SetAccountSource(src)
	scope := pool.ModelPolicy().Scope
	for _, tc := range []struct {
		ref     *config.ModelRef
		want    string
		enabled bool
	}{
		{nil, "preset-embed", true},
		{&config.ModelRef{}, "", false},
		{&config.ModelRef{ProviderID: src.id, Model: "chosen-embed"}, "chosen-embed", true},
		{nil, "preset-embed", true},
	} {
		cfg.Models.Bindings = map[string]map[string]config.ModelRef{scope: {}}
		if tc.ref != nil {
			cfg.Models.Bindings[scope]["embedding"] = *tc.ref
		}
		id, name, on, configured := pool.AccountEmbedding()
		if !configured || on != tc.enabled || name != tc.want || on && id != src.id {
			t.Fatalf("binding=%+v: target=(%q,%q,%v,%v)", tc.ref, id, name, on, configured)
		}
	}
}
