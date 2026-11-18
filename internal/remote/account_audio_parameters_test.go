package remote

import (
	"encoding/json"
	"github.com/LubyRuffy/eino-swarm/internal/provider"
	"net/http"
	"sync/atomic"
	"testing"
)

func TestFixedAudioRolesReadOnlyTheAuthorizedPrivateParameterCatalog(t *testing.T) {
	g := newAccountGateway(t)
	var privateReads atomic.Int32
	var unavailable atomic.Bool
	g.bootstrap = func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{
			"models": map[string]any{"source_id": accountModelsSourceID, "source_kind": "gateway", "execution": "desktop", "label": "Gateway", "api_base_url": g.server.URL + "/client/desktop/v1", "catalog_url": g.server.URL + "/client/desktop/v1/models", "client_catalog_url": g.server.URL + "/private/models", "api_access_enabled": true, "configurable": false, "roles": []map[string]any{{"id": "conversation", "enabled": true}, {"id": "speech", "enabled": true}}},
		}})
	}
	g.catalog = func(w http.ResponseWriter, r *http.Request) {
		t.Error("fixed audio read ordinary model catalog")
		w.WriteHeader(500)
	}
	g.extra = func(w http.ResponseWriter, r *http.Request) bool {
		if r.Header.Get("Authorization") != "Bearer "+accountModelsToken {
			t.Error("private request lost account authority")
		}
		switch r.URL.Path {
		case "/client/models/v1/capabilities":
			role := provider.RoleCapability{Supported: true, Available: true, Endpoints: []string{"audio_speech"}, AudioParametersVersion: 1}
			source := &provider.SourceCapabilities{Authorized: true, Available: true, Capabilities: map[string]provider.RoleCapability{"speech": role, "conversation": {Supported: true, Available: true, Endpoints: []string{"chat_completions"}}}}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": provider.ClientCapabilities{Version: 1, ModelRolesVersion: 3, Sources: map[string]*provider.SourceCapabilities{"preset": source, "gateway": {}}}})
			return true
		case "/private/models":
			privateReads.Add(1)
			if unavailable.Load() {
				w.WriteHeader(503)
				return true
			}
			_, _ = w.Write([]byte(`{"data":[{"id":"speech","model_type":"speech","audio":{"version":1,"revision":"r1","status":"configured","parameters":{"voice":{"values":["bright"],"default":"bright","configurable":true}}}}]}`))
			return true
		}
		return false
	}
	a, p, _ := accountModelsFixture(t, g)
	if state := a.Refresh(t.Context()); state.State != AccountReady {
		t.Fatal(state)
	}
	view := p.AudioParameters(accountModelsSourceID, "speech", "speech")
	if view == nil || view.Status != "configured" || view.Parameters.Voice.Values[0] != "bright" || privateReads.Load() != 1 {
		t.Fatalf("parameters=%+v private=%d", view, privateReads.Load())
	}
	unavailable.Store(true)
	a.Refresh(t.Context())
	if view = p.AudioParameters(accountModelsSourceID, "speech", "speech"); view == nil || view.Status != "unavailable" {
		t.Fatalf("stale declaration retained: %+v", view)
	}
	if privateReads.Load() != 2 {
		t.Fatalf("one refresh repeated its failed private catalog read: %d", privateReads.Load())
	}
}
