package remote

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LubyRuffy/eino-swarm/internal/config"
)

func TestManualLoginCompletesWithoutAHostBrowser(t *testing.T) {
	for _, browser := range []string{"missing", "failed", "working", "configuration-changed"} {
		t.Run(browser, func(t *testing.T) {
			var origin string
			var polls atomic.Int32
			advance := make(chan struct{})
			gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/start") {
					w.WriteHeader(http.StatusCreated)
					_ = json.NewEncoder(w).Encode(map[string]any{"data": loginStartResponse{DeviceCode: strings.Repeat("d", 64), UserCode: "PUBLIC", VerificationURI: origin + "/login?desktop_code=PUBLIC", ExpiresAt: time.Now().Add(time.Minute), Interval: 5}})
					return
				}
				switch polls.Add(1) {
				case 1:
					http.Error(w, "temporary", http.StatusServiceUnavailable)
				case 2, 3:
					_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]string{"status": "account_pending"}})
				default:
					_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"status": "authorized", "host_id": strings.Repeat("a", 32), "user": map[string]string{"id": strings.Repeat("b", 32), "username": "approved"}}})
				}
			}))
			origin = gateway.URL
			defer gateway.Close()
			e := testEngine(t)
			cfg := e.Config()
			cfg.Remote.Enabled, cfg.Remote.AuthMode, cfg.Remote.HubURL = true, "account", origin
			host := New(e, cfg, nil)
			defer host.Stop()
			host.loginWait = func(ctx context.Context, _ time.Duration) error {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-advance:
					return nil
				}
			}
			var opened atomic.Int32
			openerDone := make(chan struct{})
			var opener func(string) error
			if browser != "missing" {
				opener = func(uri string) error {
					defer close(openerDone)
					opened.Add(1)
					if uri != origin+"/login?desktop_code=PUBLIC" {
						t.Error("opener received an unvalidated URL")
					}
					if browser == "failed" {
						return errors.New("private launcher detail")
					}
					if browser == "configuration-changed" {
						if err := cfg.Update(func(next *config.Config) error { next.Remote.Enabled = false; return nil }); err != nil {
							t.Error(err)
						}
						return errors.New("launcher result after configuration changed")
					}
					return nil
				}
			}
			if err := host.StartLogin(opener, false); err != nil {
				t.Fatal(err)
			}
			if browser == "configuration-changed" {
				<-openerDone
				waitAccountLogin(t, func() bool {
					host.mu.Lock()
					defer host.mu.Unlock()
					return host.loginActive == nil
				})
				host.Reload()
				if polls.Load() != 0 || cfg.HasHostToken() || host.Status().Login.VerificationURI != "" {
					t.Fatal("late browser failure revived an obsolete login")
				}
				return
			}
			for step := int32(0); step < 4; step++ {
				waitAccountLogin(t, func() bool {
					status := host.Status().Login
					return status != nil && (step == 0 && status.State == "authorization_pending" || step > 0 && polls.Load() >= step && (status.ErrorCode == "network_unavailable" || status.State == "account_pending"))
				})
				raw, _ := json.Marshal(host.Status().Login)
				var status map[string]any
				_ = json.Unmarshal(raw, &status)
				if status["verification_uri"] != origin+"/login?desktop_code=PUBLIC" || status["user_code"] != "PUBLIC" {
					t.Fatalf("manual authorization link missing at poll %d: %s", step, raw)
				}
				for _, private := range []string{"device_code", "candidate_token", "code_verifier", "private launcher detail"} {
					if strings.Contains(string(raw), private) {
						t.Fatal("private authorization material reached status")
					}
				}
				advance <- struct{}{}
			}
			waitAccountLogin(t, func() bool { return host.Status().Login.State == "authorized" })
			if !cfg.HasHostToken() {
				t.Fatal("approved manual authorization did not activate its credential")
			}
			raw, _ := json.Marshal(host.Status().Login)
			if strings.Contains(string(raw), "verification_uri") || browser == "missing" && opened.Load() != 0 || browser != "missing" && opened.Load() != 1 {
				t.Fatal("terminal link retained or browser launched repeatedly")
			}
		})
	}
}
