package server

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/LubyRuffy/eino-swarm/internal/config"
	"github.com/LubyRuffy/eino-swarm/internal/engine"
	"github.com/LubyRuffy/eino-swarm/internal/provider"
	"github.com/LubyRuffy/eino-swarm/internal/store"
)

func TestManagedSpeechSwitchesRoundTripWithoutChangingPrivateModelSelections(t *testing.T) {
	for _, private := range []bool{false, true} {
		t.Run(map[bool]string{false: "account", true: "retained private endpoints"}[private], func(t *testing.T) {
			cfg, err := config.Load(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if private {
				cfg.Speech.Input.SpeechEndpoint = config.SpeechEndpoint{Source: config.SpeechSourceCustom, BaseURL: "https://input.invalid/v1", Model: "private-input", APIKey: "input-secret"}
				cfg.Speech.Output.SpeechEndpoint = config.SpeechEndpoint{Source: config.SpeechSourceProvider, ProviderID: cfg.Models.Providers[0].ID, Model: "private-output", APIKey: "output-secret"}
			}
			original := cfg.Snapshot()
			st, err := store.Open(cfg.DBPath())
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			pool := provider.New(cfg)
			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			eng := engine.New(cfg, st, pool, logger)
			defer eng.Shutdown()
			srv, err := New(Options{Engine: eng, Logger: logger})
			if err != nil {
				t.Fatal(err)
			}
			source, err := provider.NewAccountProfile("gateway:0123456789abcdef0123456789abcdef", "https://gateway.invalid/v1", "session-secret", provider.AccountProfile{Label: "Managed"})
			if err != nil {
				t.Fatal(err)
			}
			pool.SetAccountSource(source)
			request := func(method string, body any, status int) map[string]any {
				t.Helper()
				raw, err := json.Marshal(body)
				if err != nil {
					t.Fatal(err)
				}
				w := httptest.NewRecorder()
				r := httptest.NewRequest(method, "/api/settings", bytes.NewReader(raw))
				r.Header.Set("Content-Type", "application/json")
				srv.Handler().ServeHTTP(w, r)
				if w.Code != status {
					t.Fatalf("%s /api/settings: got %d, want %d: %s", method, w.Code, status, w.Body.String())
				}
				for _, secret := range []string{"input-secret", "output-secret", "session-secret", "private-input", "private-output"} {
					if bytes.Contains(w.Body.Bytes(), []byte(secret)) {
						t.Fatal("settings response exposed private selection or credential")
					}
				}
				var result map[string]any
				if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
					t.Fatal(err)
				}
				return result
			}
			// Use the actual sanitized GET body, exactly as the desktop autosave does.
			for _, enabled := range []bool{true, false} {
				view := request(http.MethodGet, nil, http.StatusOK)["settings"].(map[string]any)
				speech := view["speech"].(map[string]any)
				for _, kind := range []string{"input", "output"} {
					speech[kind].(map[string]any)["enabled"] = enabled
					speech[kind].(map[string]any)["gateway_disabled"] = enabled
				}
				speech["input"].(map[string]any)["device_id"] = "selected-microphone"
				speech["output"].(map[string]any)["timeout_seconds"] = float64(90)
				request(http.MethodPut, map[string]any{"speech": speech}, http.StatusOK)
				saved := cfg.Snapshot()
				if saved.Speech.Input.Enabled != enabled || saved.Speech.Output.Enabled != enabled {
					t.Fatal("switch change was discarded")
				}
				if saved.Speech.Input.DeviceID != "selected-microphone" || saved.Speech.Output.TimeoutSeconds != 90 {
					t.Fatal("personal speech settings were discarded")
				}
				saved.Speech.Input.Enabled, saved.Speech.Output.Enabled = false, false
				if saved.Speech.Input.SpeechEndpoint != original.Speech.Input.SpeechEndpoint || saved.Speech.Output.SpeechEndpoint != original.Speech.Output.SpeechEndpoint || !reflect.DeepEqual(saved.Models, original.Models) {
					t.Fatal("speech save changed a private model selection")
				}
				loaded, err := config.Load(cfg.DataDir())
				if err != nil {
					t.Fatal(err)
				}
				if loaded.Speech.Input.Enabled != enabled || loaded.Speech.Output.Enabled != enabled || loaded.Speech.Input.GatewayDisabled != enabled || loaded.Speech.Output.GatewayDisabled != enabled {
					t.Fatal("speech switches were not durable")
				}
				readback := request(http.MethodGet, nil, http.StatusOK)["settings"].(map[string]any)["speech"].(map[string]any)
				for _, kind := range []string{"input", "output"} {
					row := readback[kind].(map[string]any)
					if row["enabled"] != enabled || row["gateway_disabled"] != enabled {
						t.Fatal("managed settings projection lost the saved switch")
					}
				}
				for _, kind := range []string{"input", "output"} {
					delete(readback[kind].(map[string]any), "gateway_disabled")
				}
				request(http.MethodPut, map[string]any{"speech": readback}, http.StatusOK)
				if now := cfg.Snapshot().Speech; now.Input.GatewayDisabled != enabled || now.Output.GatewayDisabled != enabled {
					t.Fatal("omitting gateway opt-out reset the saved value")
				}
			}
			// Toggling cannot become a way to change hidden selection/credential fields.
			for _, kind := range []string{"input", "output"} {
				for field, value := range map[string]any{"source": "custom", "model": "changed", "base_url": "https://changed.invalid/v1", "provider_id": "changed", "api_key": "changed-secret"} {
					before := cfg.Snapshot()
					speech := request(http.MethodGet, nil, http.StatusOK)["settings"].(map[string]any)["speech"].(map[string]any)
					row := speech[kind].(map[string]any)
					row["enabled"], row[field] = true, value
					request(http.MethodPut, map[string]any{"speech": speech}, http.StatusForbidden)
					if !reflect.DeepEqual(cfg.Snapshot().Speech, before.Speech) {
						t.Fatalf("rejected %s.%s edit still changed speech settings", kind, field)
					}
				}
			}
		})
	}
}
