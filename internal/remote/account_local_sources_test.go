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

	swarm "github.com/LubyRuffy/eino-swarm"
	"github.com/LubyRuffy/eino-swarm/internal/config"
	"github.com/LubyRuffy/eino-swarm/internal/provider"
	"github.com/cloudwego/eino/schema"
)

const localGatewaySourceID = "gateway:abcdefabcdefabcdefabcdefabcdefab"

func TestLocalModeListsAuthorizedServerCatalogs(t *testing.T) {
	g := newAccountGateway(t)
	var inferenceCalls atomic.Int32
	var gatewayStatus atomic.Int32
	gatewayStatus.Store(http.StatusOK)
	g.catalog = func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"object":"list","data":[]}`)
	}
	g.extra = func(w http.ResponseWriter, r *http.Request) bool {
		switch r.URL.Path {
		case "/client/desktop/v1/chat/completions", "/client/models/v1/gateway/chat/completions":
			inferenceCalls.Add(1)
			if r.Header.Get("Authorization") != "Bearer "+accountModelsToken {
				t.Error("wrong source credential")
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"source reply"},"finish_reason":"stop"}]}`)
			return true
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
				"api_access_enabled": true, "configurable": true, "secret": "strip-me",
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
		accountModelsSourceID: {"conversation": true},
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

	presetModel, err := pool.Get(t.Context(), accountModelsSourceID, "title")
	if err != nil {
		t.Fatal(err)
	}
	gatewayModel, err := pool.Get(t.Context(), localGatewaySourceID, "conversation")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := presetModel.Generate(t.Context(), []*schema.Message{schema.UserMessage("source fixture")}); err != nil {
		t.Fatal(err)
	}
	if _, err := gatewayModel.Generate(t.Context(), []*schema.Message{schema.UserMessage("source fixture")}); err != nil {
		t.Fatal(err)
	}
	if inferenceCalls.Load() != 2 {
		t.Fatal("LOCAL models did not reach both HTTP sources")
	}
	// A mode change affects new turns, not the authorized model already
	// bound to the running turn. Refresh must retain its hidden catalog.
	if err := cfg.Update(func(next *config.Config) error { next.Product.Mode = config.ProductModeFOFA; return nil }); err != nil {
		t.Fatal(err)
	}
	am.Refresh(t.Context())
	if listedAccount(pool, localGatewaySourceID, "original") {
		t.Fatal("gateway mode exposed the original catalog as a new choice")
	}
	if _, err := gatewayModel.Generate(t.Context(), []*schema.Message{schema.UserMessage("continue bound work")}); err != nil {
		t.Fatalf("mode switch interrupted an authorized running model: %v", err)
	}
	if inferenceCalls.Load() != 3 {
		t.Fatal("bound model did not reach its original HTTP source")
	}
	coldPool := provider.New(cfg)
	cold := NewAccountModels(cfg, coldPool, nil)
	if state := cold.Refresh(t.Context()); state.State != AccountReady {
		t.Fatalf("replacement lost the preset: %+v", state)
	}
	restored, err := coldPool.RestoreWorkerModel(t.Context(), swarm.ModelBinding{ProviderID: localGatewaySourceID, Model: "original"}, nil)
	if err != nil {
		t.Fatalf("replacement lost the still-authorized original catalog: %v", err)
	}
	if _, err := restored("worker", "restored-worker").Generate(t.Context(), []*schema.Message{schema.UserMessage("resume bound work")}); err != nil {
		t.Fatal(err)
	}
	if inferenceCalls.Load() != 4 {
		t.Fatal("replacement did not call the bound HTTP source")
	}
	if err := cfg.Update(func(next *config.Config) error { next.Product.Mode = config.ProductModeLocal; return nil }); err != nil {
		t.Fatal(err)
	}
	gatewayStatus.Store(http.StatusServiceUnavailable)
	kept := am.Refresh(context.Background())
	if kept.State != AccountReady || !listedAccount(pool, localGatewaySourceID, "original") || !listedAccount(pool, accountModelsSourceID, "conversation") || listedAccount(pool, accountModelsSourceID, "title") {
		t.Fatalf("503 dropped a catalog: %+v", kept)
	}
	gatewayStatus.Store(http.StatusForbidden)
	denied := am.Refresh(context.Background())
	if denied.State != AccountReady || listedAccount(pool, localGatewaySourceID, "original") || !listedAccount(pool, accountModelsSourceID, "conversation") || listedAccount(pool, accountModelsSourceID, "title") {
		t.Fatalf("403 removed the signed-in preset: %+v", denied)
	}
	if _, err := gatewayModel.Generate(t.Context(), []*schema.Message{schema.UserMessage("revoked fixture")}); err == nil {
		t.Fatal("revoked cached LOCAL model reached HTTP")
	}
	if inferenceCalls.Load() != 4 {
		t.Fatal("revoked source was still callable")
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

func TestLocalExtraGatewayAccessIsIndependentOfDesktopModelAccess(t *testing.T) {
	g := newAccountGateway(t)
	g.catalog = func(http.ResponseWriter, *http.Request) { t.Error("denied desktop catalog was requested") }
	g.extra = func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != "/client/models/v1/gateway/models" {
			return false
		}
		_, _ = io.WriteString(w, `{"data":[{"id":"extra-model","model_type":"chat"}]}`)
		return true
	}
	g.bootstrap = func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{
			"models":         map[string]any{"source_id": accountModelsSourceID, "source_kind": "gateway", "execution": "desktop", "api_access_enabled": false, "configurable": false},
			"gateway_models": map[string]any{"source_id": localGatewaySourceID, "source_kind": "gateway", "execution": "desktop", "label": "Extra scope", "api_base_url": g.server.URL + "/client/models/v1/gateway", "catalog_url": g.server.URL + "/client/models/v1/gateway/models", "api_access_enabled": true, "configurable": true},
		}})
	}
	am, pool, cfg := accountModelsFixture(t, g)
	if err := cfg.Update(func(next *config.Config) error { next.Product.Mode = config.ProductModeLocal; return nil }); err != nil {
		t.Fatal(err)
	}
	if state := am.Refresh(t.Context()); state.State != AccountReady || !listedAccount(pool, localGatewaySourceID, "extra-model") {
		t.Fatalf("independent LOCAL authorization = %+v", state)
	}
	if _, err := pool.Get(t.Context(), localGatewaySourceID, "extra-model"); err != nil {
		t.Fatal(err)
	}
}
