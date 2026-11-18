package server_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LubyRuffy/eino-swarm/internal/config"
	"github.com/LubyRuffy/eino-swarm/internal/server"
)

func TestHeadlessHTTPLoginPublishesOnlyValidatedPublicLink(t *testing.T) {
	for _, safe := range []bool{true, false} {
		t.Run(map[bool]string{true: "manual", false: "unsafe-response"}[safe], func(t *testing.T) {
			h := newHarness(t) // No native/system-browser launcher is installed.
			var origin string
			gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				uri := origin + "/login?desktop_code=PUBLIC"
				if !safe {
					uri += "&device_code=private-device"
				}
				w.WriteHeader(http.StatusCreated)
				_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"device_code": strings.Repeat("d", 64), "user_code": "PUBLIC", "verification_uri": uri, "interval": 5, "expires_at": time.Now().Add(time.Minute)}})
			}))
			origin = gateway.URL
			defer gateway.Close()
			h.app.Config.Remote.HubURL = origin
			h.app.Config.Remote.Enabled = false
			h.app.Config.Remote.AuthMode = "independent"
			h.json(http.MethodPost, "/api/remote/login", map[string]any{"enable_account": true}, http.StatusAccepted)
			deadline := time.Now().Add(3 * time.Second)
			var login map[string]any
			for time.Now().Before(deadline) {
				status := h.json(http.MethodGet, "/api/remote/status", nil, http.StatusOK)
				login, _ = status["login"].(map[string]any)
				if login["state"] != "starting" {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if safe {
				if login["state"] != "authorization_pending" || login["verification_uri"] != origin+"/login?desktop_code=PUBLIC" {
					t.Fatalf("headless login unavailable: %v", login)
				}
			} else if login["state"] != "error" || login["error_code"] != "unsafe_response" || login["verification_uri"] != nil || login["user_code"] != nil {
				t.Fatalf("unsafe link escaped validation: %v", login)
			}
			for _, private := range []string{"candidate_token", "device_code", "code_verifier"} {
				if login[private] != nil {
					t.Fatal("private proof escaped local HTTP boundary")
				}
			}
			cancelled := h.json(http.MethodDelete, "/api/remote/login", nil, http.StatusOK)
			if cancelled["login"].(map[string]any)["verification_uri"] != nil {
				t.Fatal("cancelled login retained authorization link")
			}
			h.json(http.MethodPut, "/api/remote/token", map[string]any{"token": "unapproved"}, http.StatusForbidden)
		})
	}
}

func TestSharedEngineLoginLaunchesHostBrowserOnlyOnExplicitRequest(t *testing.T) {
	for _, test := range []struct {
		name    string
		body    map[string]any
		launch  bool
		hasHook bool
	}{
		{name: "browser-enables-account", body: map[string]any{"enable_account": true}, hasHook: true},
		{name: "browser-preconfigured", body: map[string]any{"open_browser": false}, hasHook: true},
		{name: "desktop-enables-account", body: map[string]any{"enable_account": true, "open_browser": true}, launch: true, hasHook: true},
		{name: "desktop-preconfigured", body: map[string]any{"open_browser": true}, launch: true, hasHook: true},
		{name: "desktop-request-without-launcher", body: map[string]any{"open_browser": true}},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newHarness(t)
			defer h.app.Remote.Stop()
			var publicURL string
			gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/admin/api/v1/auth/desktop/start" {
					http.NotFound(w, r)
					return
				}
				w.WriteHeader(http.StatusCreated)
				_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{
					"device_code": strings.Repeat("d", 64), "user_code": "PUBLIC", "verification_uri": publicURL,
					"interval": 5, "expires_at": time.Now().Add(time.Minute),
				}})
			}))
			defer gateway.Close()
			publicURL = gateway.URL + "/login?desktop_code=PUBLIC"
			if err := h.app.Config.Update(func(cfg *config.Config) error {
				cfg.Remote.HubURL = gateway.URL
				cfg.Remote.KeepAwake = false
				if test.body["enable_account"] != true {
					cfg.Remote.AuthMode = config.RemoteAuthAccount
					cfg.Remote.Enabled = true
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			var opened []string
			opts := server.Options{Engine: h.app.Engine, Mode: server.ModeEngine}
			if test.hasHook {
				opts.OpenURL = func(target string) error {
					opened = append(opened, target)
					return nil
				}
			}
			service, err := server.New(opts)
			if err != nil {
				t.Fatal(err)
			}
			service.SetRemote(h.app.Remote)
			local := httptest.NewServer(service.Handler())
			defer local.Close()
			client := &harness{t: t, srv: local}
			client.json(http.MethodPost, "/api/remote/login", test.body, http.StatusAccepted)
			deadline := time.Now().Add(3 * time.Second)
			for {
				status := client.json(http.MethodGet, "/api/remote/status", nil, http.StatusOK)
				login := status["login"].(map[string]any)
				switch {
				case login["state"] == "authorization_pending":
					if login["verification_uri"] != publicURL || status["has_token"] != false {
						t.Fatal("manual link or unauthenticated status was lost")
					}
				case login["state"] == "starting" && time.Now().Before(deadline):
					time.Sleep(10 * time.Millisecond)
					continue
				default:
					t.Fatalf("expected a pending login, got %v", login["state"])
				}
				break
			}
			client.json(http.MethodDelete, "/api/remote/login", nil, http.StatusOK)
			if test.launch {
				if len(opened) != 1 || opened[0] != publicURL {
					t.Fatalf("native opt-in did not launch the validated link exactly once: %d calls", len(opened))
				}
			} else if len(opened) != 0 {
				t.Fatalf("manual sign-in launched a host browser: %d calls", len(opened))
			}
		})
	}
}
