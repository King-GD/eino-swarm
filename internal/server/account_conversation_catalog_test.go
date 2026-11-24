package server_test

import (
	"net/http"
	"testing"

	"github.com/LubyRuffy/eino-swarm/internal/config"
	"github.com/LubyRuffy/eino-swarm/internal/provider"
	"github.com/LubyRuffy/eino-swarm/internal/remote"
)

func TestModelPickerHTTPListsOnlyThirdPartyConversationBinding(t *testing.T) {
	ah := newAccountHarness(t, false)
	ah.waitAccount(remote.AccountSignedOut)
	// This test publishes the catalog itself. Join the initial account
	// refresh before injecting it so a signed-out publication cannot erase it.
	ah.app.Remote.Stop()
	if err := ah.app.Config.Update(func(c *config.Config) error {
		c.Product.Mode = config.ProductModeLocal
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	src, err := provider.NewAccountSource(accountSourceID, "Partner", "https://gateway.invalid/v1", "fixture-token", provider.Catalog{
		Names: []string{"title", "conversation", "compact", "translation"},
		Types: map[string]string{"title": "chat", "conversation": "chat", "compact": "chat", "translation": "chat"},
	})
	if err != nil {
		t.Fatal(err)
	}
	src.SetModelPolicy(true, nil)
	src.SetCatalogGroup("third_party")
	ah.app.Engine.Providers().SetLocalAccountSources([]*provider.AccountSource{src})
	got := ah.json(http.MethodGet, "/api/models", nil, http.StatusOK)
	models, ok := got["models"].([]any)
	if !ok {
		t.Fatalf("expected the model picker to return a catalog, got %v", got)
	}
	var names []string
	for _, value := range models {
		row := value.(map[string]any)
		if row["provider_id"] != accountSourceID {
			continue
		}
		if row["catalog_group"] != "third_party" || row["ready"] != true {
			t.Fatalf("lost third-party row metadata: %v", row)
		}
		names = append(names, row["model"].(string))
	}
	if len(names) != 1 || names[0] != "conversation" {
		t.Fatalf("HTTP picker choices = %v", names)
	}
}
