package config

import "testing"

func TestFunctionalBindingsAreValidatedAndDetached(t *testing.T) {
	if Default().ProductSnapshot().Resolved() != ProductModeFOFA {
		t.Fatal("default execution mode")
	}
	for _, tc := range []struct {
		scope, role, provider, model string
		valid                        bool
	}{
		{"local", "embedding", "", "", true}, {"gateway:scope", "embedding", "", "", true},
		{"local", "embedding", "own", "", false}, {"local", "embedding", "", "embed", false},
		{"local", "title", "", "", false},
		{"local", "title", "own", "small", true}, {"gateway:scope", "speech", "gateway:account", "voice", true},
		{"bad", "title", "own", "small", false}, {"local", "unknown", "own", "small", false},
		{"local", "title", "", "small", false}, {"local", "title", "own", "", false},
		{"local", "title", "gateway:account", "small", false},
	} {
		t.Run(tc.scope+tc.role+tc.provider+tc.model, func(t *testing.T) {
			cfg := Default()
			cfg.Models.Bindings = map[string]map[string]ModelRef{tc.scope: {tc.role: {ProviderID: tc.provider, Model: tc.model}}}
			if err := cfg.validateModelBindings(); (err == nil) != tc.valid {
				t.Fatalf("validation=%v", err)
			}
			snap := cfg.ModelsSnapshot()
			snap.Bindings[tc.scope][tc.role] = ModelRef{Model: "changed"}
			if cfg.Models.Bindings[tc.scope][tc.role].Model != tc.model {
				t.Fatal("snapshot mutated live bindings")
			}
			copy := cfg.Snapshot()
			delete(copy.Models.Bindings, tc.scope)
			if len(cfg.Models.Bindings) != 1 {
				t.Fatal("clone mutated live scopes")
			}
		})
	}
	if err := Default().validateModelBindings(); err != nil {
		t.Fatal(err)
	}
	if KnownModelRole("unknown") || len(ModelRoles()) != 10 {
		t.Fatal("role validation")
	}
}
