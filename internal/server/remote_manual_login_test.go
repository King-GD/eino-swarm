package server_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
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
