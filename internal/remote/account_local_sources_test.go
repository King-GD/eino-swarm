package remote

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/LubyRuffy/eino-swarm/internal/config"
	"github.com/LubyRuffy/eino-swarm/internal/provider"
)

const localGatewaySourceID = "gateway:abcdefabcdefabcdefabcdefabcdefab"

func TestLocalModeListsAuthorizedServerCatalogs(t *testing.T) {
	g := newAccountGateway(t)
	var gatewayStatus atomic.Int32
	gatewayStatus.Store(http.StatusOK)
	g.catalog = func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"object":"list","data":[]}`)
	}
	g.extra = func(w http.ResponseWriter, r *http.Request) bool {
		switch r.URL.Path {
		case "/client/models/v1/preset/models":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"object":"list","data":[{"id":"conversation","model_type":"chat"},{"id":"title","model_type":"chat"},{"id":"embedding","model_type":"embedding"}]}`)
			return true
		case "/client/models/v1/gateway/models":
			if code := int(gatewayStatus.Load()); code != http.StatusOK {
				http.Error(w, "no", code)
				return true
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"object":"list","data":[{"id":"conversation","model_type":"chat"},{"id":"original","model_type":"chat"}]}`)
			return true
		default:
			return false
		}
	}
	g.bootstrap = func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{
			"user": map[string]any{"id": strings.Repeat("2", 32), "username": "Account"},
			"models": map[string]any{
				"source_id": accountModelsSourceID, "source_kind": "gateway", "label": "Partner", "execution": "desktop",
				"api_base_url": g.server.URL + "/client/desktop/v1", "catalog_url": g.server.URL + "/client/desktop/v1/models",
				"client_catalog_url": g.server.URL + "/client/models/v1/preset/models", "api_access_enabled": true,
				"configurable": false,
				"roles":        []map[string]any{{"id": "conversation", "enabled": true, "label": "对话"}},
			},
			"gateway_models": map[string]any{
				"source_id": localGatewaySourceID, "source_kind": "gateway", "label": "服务器", "execution": "desktop",
				"api_base_url": g.server.URL + "/client/models/v1/gateway", "catalog_url": g.server.URL + "/client/models/v1/gateway/models",
				"api_access_enabled": true, "secret": "strip-me",
			},
		}})
	}
	am, pool, cfg := accountModelsFixture(t, g)
	fofa := am.Refresh(context.Background())
	if fofa.State != AccountReady || fofa.Models != 1 {
		t.Fatalf("gateway mode = %+v", fofa)
	}
	for _, row := range pool.List() {
		if row.ProviderID == localGatewaySourceID || row.Model == "title" {
			t.Fatalf("gateway mode listed a local catalog: %+v", row)
		}
	}
	if err := cfg.Update(func(next *config.Config) error {
		next.Product.Mode = config.ProductModeLocal
		if len(next.Models.Providers) == 0 {
			return errors.New("no provider")
		}
		next.Models.Providers[0].Model = "personal-model"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	local := am.Refresh(context.Background())
	if local.State != AccountReady || local.SourceID != accountModelsSourceID {
		t.Fatalf("local = %+v", local)
	}
	encoded, err := json.Marshal(local)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), accountModelsToken) || strings.Contains(string(encoded), "strip-me") || strings.Contains(string(encoded), g.server.URL) {
		t.Fatalf("state leaked a credential or endpoint: %s", encoded)
	}
	want := map[string]map[string]bool{
		accountModelsSourceID: {"conversation": true, "title": true},
		localGatewaySourceID:  {"conversation": true, "original": true},
	}
	var personal bool
	for _, row := range pool.List() {
		if row.Model == "embedding" {
			t.Fatalf("embedding listed: %+v", row)
		}
		if row.Model == "personal-model" && row.Source == provider.SourceProvider {
			personal = true
			continue
		}
		if row.Source != provider.SourceAccount {
			continue
		}
		names := want[row.ProviderID]
		if names == nil || !names[row.Model] {
			t.Fatalf("unexpected account row: %+v", row)
		}
		if row.ProviderID == accountModelsSourceID && row.ProviderLabel != "Partner" {
			t.Fatalf("preset label = %q", row.ProviderLabel)
		}
		if row.ProviderID == localGatewaySourceID && row.ProviderLabel != "服务器" {
			t.Fatalf("gateway label = %q", row.ProviderLabel)
		}
		delete(names, row.Model)
	}
	for id, names := range want {
		if len(names) != 0 {
			t.Fatalf("missing %s %v", id, names)
		}
	}
	if !personal {
		t.Fatal("personal provider missing")
	}
	preset, err := pool.ResolveModel(accountModelsSourceID, "title")
	if err != nil || preset.Model != "title" || preset.APIKey != accountModelsToken || !strings.HasSuffix(preset.BaseURL, "/client/desktop/v1") {
		t.Fatalf("preset resolve = %+v %v", preset, err)
	}
	original, err := pool.ResolveModel(localGatewaySourceID, "conversation")
	if err != nil || original.Model != "conversation" || original.APIKey != accountModelsToken || !strings.HasSuffix(original.BaseURL, "/client/models/v1/gateway") || original.BaseURL == preset.BaseURL {
		t.Fatalf("gateway resolve = %+v %v", original, err)
	}
	if _, err := pool.ResolveModel("gateway:"+strings.Repeat("e", 32), "conversation"); err != provider.ErrAccountLoginRequired {
		t.Fatalf("unknown gateway = %v", err)
	}

	gatewayStatus.Store(http.StatusServiceUnavailable)
	kept := am.Refresh(context.Background())
	if kept.State != AccountReady || !listedAccount(pool, localGatewaySourceID, "original") || !listedAccount(pool, accountModelsSourceID, "title") {
		t.Fatalf("503 dropped a catalog: %+v", kept)
	}
	gatewayStatus.Store(http.StatusForbidden)
	denied := am.Refresh(context.Background())
	if denied.State != AccountReady || listedAccount(pool, localGatewaySourceID, "original") || !listedAccount(pool, accountModelsSourceID, "title") {
		t.Fatalf("403 removed the signed-in preset: %+v", denied)
	}
	signOutAccount(t, cfg)
	if st := am.Refresh(context.Background()); st.State != AccountSignedOut {
		t.Fatalf("sign-out = %+v", st)
	}
	if _, err := pool.ResolveModel(accountModelsSourceID, "conversation"); err != provider.ErrAccountLoginRequired {
		t.Fatalf("signed-out resolve = %v", err)
	}
}

func TestDeniedGatewayAccessIsNotALocalCatalog(t *testing.T) {
	g := newAccountGateway(t)
	g.catalog = func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"object":"list","data":[]}`)
	}
	g.extra = func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path == "/client/models/v1/gateway/models" {
			t.Errorf("disabled gateway catalog was requested")
			return true
		}
		if r.URL.Path == "/client/models/v1/preset/models" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"object":"list","data":[{"id":"conversation","model_type":"chat"}]}`)
			return true
		}
		return false
	}
	g.bootstrap = func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{
			"user": map[string]any{"id": strings.Repeat("2", 32), "username": "Account"},
			"models": map[string]any{
				"source_id": accountModelsSourceID, "source_kind": "gateway", "label": "Partner", "execution": "desktop",
				"api_base_url": g.server.URL + "/client/desktop/v1", "catalog_url": g.server.URL + "/client/desktop/v1/models",
				"client_catalog_url": g.server.URL + "/client/models/v1/preset/models", "api_access_enabled": true,
			},
			"gateway_models": map[string]any{
				"source_id": localGatewaySourceID, "source_kind": "gateway", "label": "服务器", "execution": "desktop",
				"api_base_url": g.server.URL + "/client/models/v1/gateway", "catalog_url": g.server.URL + "/client/models/v1/gateway/models",
				"api_access_enabled": false,
			},
		}})
	}
	am, pool, cfg := accountModelsFixture(t, g)
	if err := cfg.Update(func(next *config.Config) error {
		next.Product.Mode = config.ProductModeLocal
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	st := am.Refresh(context.Background())
	if st.State != AccountReady || !listedAccount(pool, accountModelsSourceID, "conversation") || listedAccount(pool, localGatewaySourceID, "conversation") {
		t.Fatalf("disabled gateway = %+v", st)
	}
}

func TestMalformedGatewayCatalogIsNotPublished(t *testing.T) {
	g := newAccountGateway(t)
	g.bootstrap = func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{
			"user": map[string]any{"id": strings.Repeat("2", 32), "username": "Account"},
			"models": map[string]any{
				"source_id": accountModelsSourceID, "source_kind": "gateway", "label": "Partner", "execution": "desktop",
				"api_base_url": g.server.URL + "/client/desktop/v1", "catalog_url": g.server.URL + "/client/desktop/v1/models",
				"api_access_enabled": true,
			},
			"gateway_models": map[string]any{
				"source_id": accountModelsSourceID, "source_kind": "gateway", "label": "服务器", "execution": "desktop",
				"api_base_url": "https://evil.example/v1", "catalog_url": "https://evil.example/models",
				"api_access_enabled": true,
			},
		}})
	}
	am, pool, cfg := accountModelsFixture(t, g)
	if err := cfg.Update(func(next *config.Config) error {
		next.Product.Mode = config.ProductModeLocal
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	st := am.Refresh(context.Background())
	if st.State != AccountUnavailable || st.Reason != AccountReasonUnsafe || pool.AccountSourceID() != "" {
		t.Fatalf("malformed gateway = %+v source %q", st, pool.AccountSourceID())
	}
}

func listedAccount(pool *provider.Pool, id, model string) bool {
	for _, row := range pool.List() {
		if row.ProviderID == id && row.Model == model {
			return true
		}
	}
	return false
}
