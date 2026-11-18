package server

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LubyRuffy/eino-swarm/internal/config"
	"github.com/LubyRuffy/eino-swarm/internal/engine"
	"github.com/LubyRuffy/eino-swarm/internal/provider"
	"github.com/LubyRuffy/eino-swarm/internal/store"
)

func TestManagedSettingsHidePrivateModelsAndRejectEveryModelWrite(t *testing.T) {
	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cfg.Models.Providers[0].BaseURL = "https://personal.invalid/v1"
	cfg.Models.Providers[0].Model = "personal-model"
	cfg.Swarm.TitleModel = "private-title"
	cfg.Swarm.CompactModel = "private-compact"
	cfg.Search.EmbeddingModel = "private-embedding"
	cfg.Speech.Input.Source = config.SpeechSourceCustom
	cfg.Speech.Input.BaseURL = "https://voice.invalid/v1"
	cfg.Speech.Input.APIKey = "private-voice-key"
	st, err := store.Open(cfg.DBPath())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	pool := provider.New(cfg)
	eng := engine.New(cfg, st, pool, slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer eng.Shutdown()
	srv, err := New(Options{Engine: eng, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	src, err := provider.NewAccountProfile("gateway:0123456789abcdef0123456789abcdef", "https://gateway.invalid/v1", "private-session", provider.AccountProfile{Label: "Managed"})
	if err != nil {
		t.Fatal(err)
	}
	pool.SetAccountSource(src)
	view := srv.currentSettingsView()
	raw, _ := json.Marshal(view)
	for _, private := range []string{"personal-model", "personal.invalid", "private-title", "private-compact", "private-embedding", "voice.invalid", "private-voice-key", "private-session"} {
		if strings.Contains(string(raw), private) {
			t.Fatalf("settings exposed %s", private)
		}
	}
	next := cfg.Snapshot()
	next.Swarm.TitleModel = ""
	next.Swarm.CompactModel = ""
	next.Search.EmbeddingModel = ""
	next.Speech.Input.SpeechEndpoint = config.SpeechEndpoint{Source: config.SpeechSourceAccount}
	if err := validatePresetSettings(cfg, next, pool.ModelPolicy()); err != nil {
		t.Fatal(err)
	}
	if next.Swarm.TitleModel != cfg.Swarm.TitleModel || next.Speech.Input.APIKey != cfg.Speech.Input.APIKey {
		t.Fatal("sanitized unrelated save erased private selections")
	}
	for _, edit := range []func(*config.Config){
		func(c *config.Config) { c.Swarm.TitleModel = "new" }, func(c *config.Config) { c.Swarm.CompactModel = "new" }, func(c *config.Config) { c.Search.EmbeddingModel = "new" },
		func(c *config.Config) { c.Speech.Output.Model = "new" }, func(c *config.Config) {
			c.Models.Bindings = map[string]map[string]config.ModelRef{"local": {"title": {ProviderID: "default", Model: "new"}}}
		},
	} {
		next := cfg.Snapshot()
		edit(next)
		if !errors.Is(validatePresetSettings(cfg, next, pool.ModelPolicy()), errCustomModelsDisabled) {
			t.Fatal("hidden model write accepted")
		}
	}
	for _, body := range []string{`{"models":{"bindings":{"title":{"provider_id":"default","model":"new"}}}}`, `{"swarm":{"title_model":"new"}}`, `{"speech":{"output":{"model":"new"}}}`} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPut, "/api/settings", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		srv.Handler().ServeHTTP(w, req)
		if w.Code != http.StatusForbidden {
			t.Fatalf("managed mutation=%d", w.Code)
		}
	}
	next = cfg.Snapshot()
	next.Product.Mode = config.ProductModeLocal
	if err := validatePresetSettings(cfg, next, pool.ModelPolicy()); err != nil {
		t.Fatal("debug LOCAL could not recover settings", err)
	}
	if err := eng.UpdateConfigForMode(&next.Product, func(c *config.Config) error { c.Product = next.Product; return nil }); err != nil {
		t.Fatal(err)
	}
	if v := srv.currentSettingsView(); !v.ModelPolicy.Configurable || len(v.Models.Providers) == 0 {
		t.Fatal("LOCAL settings not restored")
	}
}

func TestOrdinaryGatewayBindingsCanBePatchedAndRestoredWithoutProviders(t *testing.T) {
	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(cfg.DBPath())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	pool := provider.New(cfg)
	eng := engine.New(cfg, st, pool, slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer eng.Shutdown()
	srv, err := New(Options{Engine: eng, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	src, err := provider.NewAccountSource("gateway:0123456789abcdef0123456789abcdef", "Gateway", "https://gateway.invalid/v1", "private", provider.Catalog{Names: []string{"chat"}})
	if err != nil {
		t.Fatal(err)
	}
	src.SetModelPolicy(true, map[string]string{"conversation": "chat", "title": "small"})
	pool.SetAccountSource(src)
	for _, body := range []string{`{"models":{"bindings":{"title":{"provider_id":"default","model":"chosen"}}}}`, `{"models":{"bindings":{}}}`} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPut, "/api/settings", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		srv.Handler().ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("binding patch=%d %s", w.Code, w.Body.String())
		}
	}
	if ref, _ := pool.RoleSelection("title", "", ""); ref.Model != "small" {
		t.Fatal("restore did not follow gateway default")
	}
	if v := srv.currentSettingsView(); !v.ModelPolicy.Configurable || len(v.Models.Providers) == 0 || len(v.Models.Bindings) != 0 {
		t.Fatal("ordinary gateway settings hidden or override retained")
	}
}

func TestImageBindingAcceptsTypedGatewayChoicesAndRetainsOldPinsOnGrantLoss(t *testing.T) {
	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(cfg.DBPath())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	const sourceID = "gateway:0123456789abcdef0123456789abcdef"
	pool := provider.New(cfg)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	eng := engine.New(cfg, st, pool, log)
	defer eng.Shutdown()
	srv, err := New(Options{Engine: eng, Logger: log})
	if err != nil {
		t.Fatal(err)
	}
	publish := func(image bool) {
		src, err := provider.NewAccountSource(sourceID, "Gateway", "https://gateway.invalid/v1", "fixture", provider.Catalog{
			Names: []string{"chat", "picture"},
			Types: map[string]string{"chat": "chat", "picture": "image"},
		})
		if err != nil {
			t.Fatal(err)
		}
		caps := map[string]provider.RoleCapability{
			"conversation": {Supported: true, Available: true, Configurable: true, Endpoints: []string{"chat_completions"}},
		}
		if image {
			caps["image"] = provider.RoleCapability{Supported: true, Available: true, Configurable: true, Endpoints: []string{"images"}}
		}
		src.SetCapabilities(&provider.SourceCapabilities{Authorized: true, Available: true, Configurable: true, Capabilities: caps})
		pool.SetAccountSource(src)
	}
	put := func(body string, status int) {
		t.Helper()
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPut, "/api/settings", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		srv.Handler().ServeHTTP(w, req)
		if w.Code != status {
			t.Fatalf("settings returned %d, want %d: %s", w.Code, status, w.Body.String())
		}
	}
	pin := func(providerID, model string) string {
		return `{"models":{"bindings":{"image":{"provider_id":"` + providerID + `","model":"` + model + `"}}}}`
	}
	publish(true)
	put(pin(sourceID, "chat"), http.StatusForbidden)
	put(pin("default", "picture"), http.StatusForbidden)
	put(pin(sourceID, "picture"), http.StatusOK)
	if !pool.ImageAvailable(sourceID) {
		t.Fatal("accepted pin did not admit image generation")
	}
	publish(false)
	put(pin(sourceID, "picture"), http.StatusOK)
	if pool.ImageAvailable(sourceID) || pool.ModelPolicy().Bindings["image"].Model != "picture" {
		t.Fatal("grant loss should close calls while keeping the old pin")
	}
	put(`{"models":{"bindings":{}}}`, http.StatusOK)
	if _, exists := pool.ModelPolicy().Bindings["image"]; exists {
		t.Fatal("disabled capability prevented clearing an old pin")
	}
}
