package remote

import (
	"encoding/json"
	"net/http"
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
