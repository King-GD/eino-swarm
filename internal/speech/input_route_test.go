package speech

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/LubyRuffy/eino-swarm/internal/config"
	"github.com/LubyRuffy/eino-swarm/internal/provider"
)

func TestDefaultGatewayRecognitionGoesDirectWhilePersonalOverrideStaysOnPC(t *testing.T) {
	cfg, svc := configuredSpeech(t, "https://account.invalid/v1")
	if err := cfg.Update(func(c *config.Config) error { c.Product.Mode = config.ProductModeFOFA; return nil }); err != nil {
		t.Fatal(err)
	}
	source, err := provider.NewAccountSource("gateway:voice", "Gateway", "https://account.invalid/client/desktop/v1", "private-account-token", provider.Catalog{Names: []string{"default-recognizer", "default-speaker"}})
	if err != nil {
		t.Fatal(err)
	}
	source.SetModelPolicy(true, map[string]string{"transcription": "default-recognizer", "speech": "default-speaker"})
	svc.pool.SetAccountSource(source)
	view := svc.Snapshot()
	if !view.Input.Ready || view.Input.Execution != "gateway" || view.Input.GatewayOrigin != "https://account.invalid" || view.Input.Model != "default-recognizer" || view.Output.Execution != "gateway" {
		t.Fatalf("default voice did not bypass PC audio RPC: %+v", view)
	}
	scope := svc.pool.ModelPolicy().Scope
	if err := cfg.Update(func(c *config.Config) error {
		c.Models.Providers = append(c.Models.Providers, config.Provider{ID: "personal", BaseURL: "https://account.invalid/v1", APIKey: "private-personal-token", Model: "default-recognizer"})
		c.Models.Bindings = map[string]map[string]config.ModelRef{scope: {"transcription": {ProviderID: "personal", Model: "default-recognizer"}}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	view = svc.Snapshot()
	if !view.Input.Ready || view.Input.Execution != "pc_local" || view.Input.GatewayOrigin != "" || view.Output.Execution != "gateway" {
		t.Fatalf("same-name personal recognition inherited gateway authority: %+v", view)
	}
	raw, err := json.Marshal(view)
	if err != nil || strings.Contains(string(raw), "private-") || strings.Contains(string(raw), "/client/desktop") {
		t.Fatalf("unsafe phone snapshot: %s %v", raw, err)
	}
}
