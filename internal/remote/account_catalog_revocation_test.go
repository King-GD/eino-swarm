package remote

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/LubyRuffy/eino-swarm/internal/config"
)

// A sibling outage cannot preserve a source the latest bootstrap revoked.
func TestLocalCatalogRevocationSurvivesSiblingOutage(t *testing.T) {
	for _, tc := range []struct {
		name                                  string
		revokePreset, defaultOutage, denyHTTP bool
	}{
		{name: "bootstrap revocation during preset outage"},
		{name: "HTTP revocation during preset outage", denyHTTP: true},
		{name: "preset revocation during gateway outage", revokePreset: true},
		{name: "revocation during default catalog outage", defaultOutage: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := newAccountGateway(t)
			var revoked, outage atomic.Bool
			original := g.catalog
			g.catalog = func(w http.ResponseWriter, r *http.Request) {
				if tc.defaultOutage && outage.Load() {
					http.Error(w, "temporary", 503)
					return
				}
				original(w, r)
			}
			g.bootstrap = func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{
					"user": map[string]any{"id": strings.Repeat("2", 32), "username": "Account"},
					"models": map[string]any{
						"source_id": accountModelsSourceID, "source_kind": "gateway", "label": "Preset", "execution": "desktop",
						"api_base_url": g.server.URL + "/client/desktop/v1", "catalog_url": g.server.URL + "/client/desktop/v1/models",
						"client_catalog_url": g.server.URL + "/preset/models", "api_access_enabled": !(tc.revokePreset && revoked.Load()),
					},
					"gateway_models": map[string]any{
						"source_id": localGatewaySourceID, "source_kind": "gateway", "label": "Gateway", "execution": "desktop",
						"api_base_url": g.server.URL + "/global", "catalog_url": g.server.URL + "/global/models", "api_access_enabled": !(revoked.Load() && !tc.revokePreset && !tc.denyHTTP),
					},
				}})
			}
			g.extra = func(w http.ResponseWriter, r *http.Request) bool {
				switch r.URL.Path {
				case "/preset/models":
					if outage.Load() && !tc.revokePreset && !tc.defaultOutage {
						http.Error(w, "temporary", http.StatusServiceUnavailable)
						return true
					}
					_, _ = io.WriteString(w, `{"data":[{"id":"preset-model"}]}`)
					return true
				case "/global/models":
					if outage.Load() && tc.revokePreset {
						http.Error(w, "temporary", 503)
						return true
					}
					if revoked.Load() && tc.denyHTTP {
						http.Error(w, "denied", 403)
						return true
					}
					_, _ = io.WriteString(w, `{"data":[{"id":"global-model"}]}`)
					return true
				}
				return false
			}
			am, pool, cfg := accountModelsFixture(t, g)
			if err := cfg.Update(func(next *config.Config) error { next.Product.Mode = config.ProductModeLocal; return nil }); err != nil {
				t.Fatal(err)
			}
			if st := am.Refresh(t.Context()); st.State != AccountReady || !listedAccount(pool, localGatewaySourceID, "global-model") || !listedAccount(pool, accountModelsSourceID, "preset-model") {
				t.Fatalf("initial catalogs = %+v", st)
			}
			revoked.Store(true)
			outage.Store(true)
			st := am.Refresh(t.Context())
			revokedID, revokedName, keptID, keptName := localGatewaySourceID, "global-model", accountModelsSourceID, "preset-model"
			if tc.revokePreset {
				revokedID, revokedName, keptID, keptName = keptID, keptName, revokedID, revokedName
			}
			if listedAccount(pool, revokedID, revokedName) {
				t.Fatal("revoked catalog survived a sibling outage")
			}
			if _, err := pool.ResolveModel(revokedID, revokedName); err == nil {
				t.Fatal("revoked model remained callable")
			}
			if st.State != AccountReady || !listedAccount(pool, keptID, keptName) {
				t.Fatalf("authorized catalog was lost during transient outage: %+v", st)
			}
		})
	}
}

func TestSwitchableCatalogValidationAndErrorBoundaries(t *testing.T) {
	g := newAccountGateway(t)
	am, _, _ := accountModelsFixture(t, g)
	for _, tc := range []struct {
		name, body, token, base string
		want                    int
	}{
		{"malformed", "{", accountModelsToken, g.server.URL, catalogOmit},
		{"empty", `{"data":[]}`, accountModelsToken, g.server.URL, catalogOmit},
		{"missing credential", `{"data":[{"id":"chat"}]}`, "", g.server.URL, catalogOmit},
		{"missing endpoint", `{"data":[{"id":"chat"}]}`, accountModelsToken, "", catalogOmit},
		{"valid", `{"data":[{"id":"chat"}]}`, accountModelsToken, g.server.URL, catalogReady},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g.mu.Lock()
			g.catalog = func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, tc.body) }
			g.mu.Unlock()
			_, status := am.oneCatalog(t.Context(), tc.token, accountCatalogRef{sourceID: accountModelsSourceID, apiBaseURL: tc.base, catalogURL: g.server.URL + "/client/desktop/v1/models"})
			if status != tc.want {
				t.Fatalf("status=%d", status)
			}
		})
	}
	for _, err := range []error{errAccountResponse, &accountStatusError{code: 401}, &accountStatusError{code: 403}, &accountStatusError{code: 404}} {
		if catalogStatus(err) != catalogOmit {
			t.Fatal("unsafe or denied source retained")
		}
	}
	for _, err := range []error{io.ErrUnexpectedEOF, &accountStatusError{code: 503}} {
		if catalogStatus(err) != catalogKeep {
			t.Fatal("transient source discarded")
		}
	}
	origin := g.server.URL
	valid := map[string]any{"source_id": localGatewaySourceID, "source_kind": "gateway", "execution": "desktop", "label": "Gateway", "api_base_url": origin + "/v1", "catalog_url": origin + "/v1/models", "api_access_enabled": true}
	for _, field := range []string{"source_id", "source_kind", "execution", "label", "api_base_url", "catalog_url"} {
		body := map[string]any{}
		for k, v := range valid {
			body[k] = v
		}
		body[field] = "invalid\nvalue"
		raw, _ := json.Marshal(body)
		var boot accountBootstrap
		boot.sourceID = accountModelsSourceID
		if boot.parseSwitchableCatalogs("", raw, origin) == nil {
			t.Fatalf("unsafe %s accepted", field)
		}
	}
	for _, raw := range []string{"{", "null", ""} {
		var boot accountBootstrap
		err := boot.parseSwitchableCatalogs("", json.RawMessage(raw), origin)
		if (raw == "{") != (err != nil) {
			t.Fatalf("raw validation %q: %v", raw, err)
		}
	}
	var boot accountBootstrap
	if boot.parseSwitchableCatalogs("https://elsewhere.invalid/models", nil, origin) == nil {
		t.Fatal("cross-origin client catalog accepted")
	}
}
