package remote

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/LubyRuffy/eino-swarm/internal/config"
	"github.com/LubyRuffy/eino-swarm/internal/engine"
	"github.com/LubyRuffy/eino-swarm/internal/provider"
)

func TestLocalModeListsAuthorizedServerCatalogs(t *testing.T) {
	g := newAccountGateway(t)
	const gatewayID = "gateway:abcdefabcdefabcdefabcdefabcdefab"
	const presetPath = "/client/models/v1/preset/models"
	const gatewayPath = "/client/models/v1/gateway/models"
	var gatewayStatus atomic.Int32
	gatewayStatus.Store(http.StatusOK)
	g.extra = func(w http.ResponseWriter, r *http.Request) bool {
		if r.Header.Get("Authorization") != "Bearer "+accountModelsToken {
			t.Errorf("catalog request lost the signed-in credential")
		}
		if r.Header.Get("X-AIGateway-Model-Roles") != "" {
			t.Errorf("role negotiation leaked onto a catalog request")
		}
		switch r.URL.Path {
		case presetPath:
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"data":[{"id":"conversation","model_type":"chat"},{"id":"title","model_type":"chat"},{"id":"embedding","model_type":"embedding"}]}`)
			return true
		case gatewayPath:
			if code := int(gatewayStatus.Load()); code != http.StatusOK {
				http.Error(w, `{"error":{"message":"unavailable"}}`, code)
				return true
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"data":[{"id":"conversation","model_type":"chat"},{"id":"original","model_type":"chat"}]}`)
			return true
		default:
			return false
		}
	}
	g.bootstrap = func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{
			"user":    map[string]any{"id": strings.Repeat("2", 32), "username": "Account", "display_name": "Account Name"},
			"host_id": strings.Repeat("1", 32),
			"models": map[string]any{
				"source_id": accountModelsSourceID, "source_kind": "gateway", "label": "Partner", "execution": "desktop",
				"api_base_url": g.server.URL + "/client/desktop/v1", "catalog_url": g.server.URL + "/client/desktop/v1/models",
				"client_catalog_url": g.server.URL + presetPath, "api_access_enabled": true, "configurable": false,
				"roles": []map[string]any{{"id": "conversation", "enabled": true, "label": "对话"}},
			},
			"gateway_models": map[string]any{
				"source_id": gatewayID, "source_kind": "gateway", "label": "服务器", "execution": "desktop",
				"api_base_url": g.server.URL + "/client/models/v1/gateway", "catalog_url": g.server.URL + gatewayPath,
				"api_access_enabled": true, "secret": "strip-me",
			},
		}})
	}
	am, pool, cfg := accountModelsFixture(t, g)
	if st := am.Refresh(context.Background()); st.State != AccountReady {
		t.Fatalf("refresh: %+v", st)
	}
	var gatewayMode []string
	for _, row := range pool.List() {
		if row.Source == provider.SourceAccount {
			gatewayMode = append(gatewayMode, row.Model)
		}
	}
	if strings.Join(gatewayMode, ",") != "conversation" {
		t.Fatalf("gateway mode listed the switchable catalogs: %v", gatewayMode)
	}
	base := testEngine(t)
	e := engine.New(cfg, base.Store(), pool, nil)
	t.Cleanup(e.Shutdown)
	var switched Response
	pump := &LinkPump{eng: e, allow: func() (bool, bool) { return true, false }, send: func(raw []byte) error { return json.Unmarshal(raw, &switched) }}
	pump.Dispatch([]byte(`{"op":"set_product_mode","product_mode":"local"}`))
	if !switched.OK || switched.ProductMode != "local" {
		t.Fatalf("PC switch failed: %+v", switched)
	}
	if err := cfg.Update(func(c *config.Config) error {
		if len(c.Models.Providers) == 0 {
			t.Fatal("fixture has no personal provider")
		}
		c.Models.Providers[0].Model = "personal-model"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	personal := 0
	for _, row := range pool.List() {
		if row.Model == "embedding" {
			t.Fatal("a non-conversation catalog row was selectable")
		}
		if row.Source == provider.SourceAccount {
			got[row.ProviderID+"/"+row.Model] = row.ProviderLabel
			continue
		}
		personal++
	}
	if personal == 0 || got[accountModelsSourceID+"/conversation"] != "Partner" || got[accountModelsSourceID+"/title"] != "Partner" || got[gatewayID+"/conversation"] != "服务器" || got[gatewayID+"/original"] != "服务器" {
		t.Fatalf("LOCAL list personal=%d rows=%v", personal, got)
	}
	cat := Handle(e, config.RemoteConfig{}, Request{Op: OpCatalog}, "relay", "session")
	groups := map[string]string{}
	for _, row := range cat.Models {
		groups[row.ProviderID] = row.CatalogGroup
	}
	if !cat.OK || groups[accountModelsSourceID] != "third_party" || groups[gatewayID] != "original" {
		t.Fatalf("RPC lost catalog groups: %+v", cat)
	}
	// Model selection crosses the actual phone dispatch and persists its source.
	th, err := e.CreateThread("routing", accountModelsSourceID, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{accountModelsSourceID, gatewayID} {
		var tuned Response
		pump.allow = func() (bool, bool) { return true, e.Providers().ModelPolicy().Configurable }
		pump.send = func(raw []byte) error { return json.Unmarshal(raw, &tuned) }
		body, _ := json.Marshal(Request{Op: OpTune, ThreadID: th.ID, ProviderID: id, Model: "conversation"})
		pump.Dispatch(body)
		stored, readErr := e.Store().GetThread(th.ID)
		if !tuned.OK || readErr != nil || stored.ProviderID != id {
			t.Fatalf("grouped model selection failed: %+v %v", tuned, readErr)
		}
	}
	preset, err := pool.ResolveModel(accountModelsSourceID, "title")
	if err != nil || preset.Model != "title" || preset.APIKey != accountModelsToken || !strings.HasSuffix(preset.BaseURL, "/client/desktop/v1") {
		t.Fatalf("preset route: %+v %v", preset, err)
	}
	original, err := pool.ResolveModel(gatewayID, "conversation")
	if err != nil || original.Model != "conversation" || original.APIKey != accountModelsToken || !strings.HasSuffix(original.BaseURL, "/client/models/v1/gateway") || original.BaseURL == preset.BaseURL {
		t.Fatalf("original route: %+v %v", original, err)
	}
	raw, _ := json.Marshal(am.State())
	if strings.Contains(string(raw), accountModelsToken) || strings.Contains(string(raw), "strip-me") || strings.Contains(string(raw), g.server.URL) {
		t.Fatalf("account state leaked a credential or endpoint: %s", raw)
	}

	gatewayStatus.Store(http.StatusServiceUnavailable)
	if st := am.Refresh(context.Background()); st.State != AccountReady {
		t.Fatalf("transient catalog failure changed the account: %+v", st)
	}
	if _, ok := listedAccount(pool, gatewayID, "original"); !ok {
		t.Fatal("a transient catalog failure dropped the original models")
	}
	if _, ok := listedAccount(pool, accountModelsSourceID, "title"); !ok {
		t.Fatal("a transient original refresh hid the third-party catalog")
	}

	gatewayStatus.Store(http.StatusForbidden)
	if st := am.Refresh(context.Background()); st.State != AccountReady {
		t.Fatalf("a denied catalog signed the account out: %+v", st)
	}
	for _, row := range pool.List() {
		if row.ProviderID == gatewayID {
			t.Fatalf("denied original catalog stayed listed: %+v", row)
		}
	}
	if _, ok := listedAccount(pool, accountModelsSourceID, "title"); !ok {
		t.Fatal("denying the original catalog hid the third-party catalog")
	}
}

func TestDeniedGatewayAccessIsNotALocalCatalog(t *testing.T) {
	g := newAccountGateway(t)
	const gatewayPath = "/client/models/v1/gateway/models"
	g.bootstrap = func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{
			"user":    map[string]any{"id": strings.Repeat("2", 32), "username": "Account"},
			"host_id": strings.Repeat("1", 32),
			"models": map[string]any{
				"source_id": accountModelsSourceID, "source_kind": "gateway", "label": "Partner", "execution": "desktop",
				"api_base_url": g.server.URL + "/client/desktop/v1", "catalog_url": g.server.URL + "/client/desktop/v1/models",
				"api_access_enabled": true,
			},
			"gateway_models": map[string]any{
				"source_id": "gateway:abcdefabcdefabcdefabcdefabcdefab", "source_kind": "gateway", "label": "服务器", "execution": "desktop",
				"api_base_url": g.server.URL + "/client/models/v1/gateway", "catalog_url": g.server.URL + gatewayPath,
				"api_access_enabled": false,
			},
		}})
	}
	am, _, _ := accountModelsFixture(t, g)
	if st := am.Refresh(context.Background()); st.State != AccountReady {
		t.Fatalf("disabled original access failed the login: %+v", st)
	}
	for _, uri := range g.seen() {
		if uri == gatewayPath {
			t.Fatal("disabled original access was fetched")
		}
	}
}

func TestMalformedGatewayCatalogIsNotPublished(t *testing.T) {
	g := newAccountGateway(t)
	g.bootstrap = func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{
			"user":    map[string]any{"id": strings.Repeat("2", 32), "username": "Account"},
			"host_id": strings.Repeat("1", 32),
			"models": map[string]any{
				"source_id": accountModelsSourceID, "source_kind": "gateway", "label": "Partner", "execution": "desktop",
				"api_base_url": g.server.URL + "/client/desktop/v1", "catalog_url": g.server.URL + "/client/desktop/v1/models",
				"api_access_enabled": true,
			},
			"gateway_models": map[string]any{
				"source_id": accountModelsSourceID, "source_kind": "gateway", "label": "服务器", "execution": "desktop",
				"api_base_url": "https://foreign.invalid/client/models/v1/gateway", "catalog_url": g.server.URL + "/client/models/v1/gateway/models",
				"api_access_enabled": true,
			},
		}})
	}
	am, pool, _ := accountModelsFixture(t, g)
	st := am.Refresh(context.Background())
	if st.State != AccountUnavailable || st.Reason != AccountReasonUnsafe || pool.AccountSourceID() != "" {
		t.Fatalf("malformed catalog was published: %+v", st)
	}
}

func listedAccount(pool *provider.Pool, id, model string) (provider.Info, bool) {
	for _, row := range pool.List() {
		if row.ProviderID == id && row.Model == model {
			return row, true
		}
	}
	return provider.Info{}, false
}
