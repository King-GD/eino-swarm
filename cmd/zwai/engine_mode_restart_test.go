//go:build !windows

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LubyRuffy/eino-swarm/internal/config"
	"github.com/LubyRuffy/eino-swarm/internal/provider"
	"github.com/LubyRuffy/eino-swarm/internal/store"
)

// Exercise discovery and restoration in separate real engine processes. An
// in-memory pool populated before Restore misses startup's pending catalog.
func TestGatewayModeRestartResumesOriginalModelBeforeApplyingPreset(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs real engine processes")
	}
	const originalID = "gateway:abcdefabcdefabcdefabcdefabcdefab"
	const presetID = "gateway:0123456789abcdef0123456789abcdef"
	const token = "mode-restart-private-fixture"
	var recovering atomic.Bool
	var originalCalls atomic.Int32
	var presetCalls atomic.Int32
	var base string
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/client/desktop/v1/bootstrap":
			if recovering.Load() {
				select {
				case <-time.After(200 * time.Millisecond):
				case <-r.Context().Done():
					return
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{
				"models":           map[string]any{"source_id": presetID, "source_kind": "gateway", "execution": "desktop", "label": "Partner", "api_base_url": base + "/client/desktop/v1", "catalog_url": base + "/client/desktop/v1/models", "client_catalog_url": base + "/client/models/v1/preset/models", "api_access_enabled": true, "configurable": false, "defaults": map[string]string{"conversation": "conversation"}, "roles": []map[string]any{{"id": "conversation", "enabled": true}}},
				"gateway_models":   map[string]any{"source_id": originalID, "source_kind": "gateway", "execution": "desktop", "label": "Gateway", "api_base_url": base + "/client/models/v1/gateway", "catalog_url": base + "/client/models/v1/gateway/models", "api_access_enabled": true, "configurable": true},
				"capabilities_url": base + "/client/models/v1/capabilities",
			}})
		case "/client/models/v1/capabilities":
			// This restart fixture serves Chat Completions for both sources.
			roles := map[string]provider.RoleCapability{
				"conversation": {Supported: true, Available: true, Configurable: true, Endpoints: []string{config.APIModeChatCompletions}},
			}
			source := &provider.SourceCapabilities{Authorized: true, Available: true, Configurable: false, Capabilities: roles}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": provider.ClientCapabilities{Version: 1, ModelRolesVersion: 3, Sources: map[string]*provider.SourceCapabilities{"preset": source, "gateway": source}}})
		case "/client/models/v1/preset/models", "/client/desktop/v1/models":
			_, _ = fmt.Fprint(w, `{"data":[{"id":"conversation","model_type":"chat"}]}`)
		case "/client/models/v1/gateway/models":
			_, _ = fmt.Fprint(w, `{"data":[{"id":"original-model","model_type":"chat"}]}`)
		case "/client/models/v1/gateway/chat/completions", "/client/desktop/v1/chat/completions":
			var body struct {
				Model    string          `json:"model"`
				Stream   bool            `json:"stream"`
				Messages json.RawMessage `json:"messages"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
				return
			}
			if r.Header.Get("Authorization") != "Bearer "+token {
				t.Error("model request lost its private device credential")
			}
			original := r.URL.Path == "/client/models/v1/gateway/chat/completions"
			if original {
				originalCalls.Add(1)
				if body.Model != "original-model" {
					t.Error("original binding was renamed")
				}
			} else {
				presetCalls.Add(1)
			}
			if original && !recovering.Load() {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"Working\"}}]}\n\n")
				w.(http.Flusher).Flush()
				<-r.Context().Done()
				return
			}
			content := "Complete."
			if strings.Contains(string(body.Messages), "Schedule answer claims") {
				content = `{"claims":[]}`
			}
			encoded, _ := json.Marshal(content)
			if body.Stream {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = fmt.Fprintf(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":%s},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n", encoded)
			} else {
				_, _ = fmt.Fprintf(w, `{"choices":[{"message":{"role":"assistant","content":%s},"finish_reason":"stop"}]}`, encoded)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	base = gateway.URL
	defer gateway.Close()
	dir := t.TempDir()
	binary := filepath.Join(dir, "mode-zwai")
	buildCtx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()
	if out, err := exec.CommandContext(buildCtx, "go", "build", "-race", "-trimpath", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	data := filepath.Join(dir, "data")
	cfg, err := config.Load(data)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Product.Mode = config.ProductModeLocal
	cfg.Remote.Enabled = false
	cfg.Remote.AuthMode, cfg.Remote.HubURL = config.RemoteAuthAccount, base
	cfg.Swarm.AutoTitle, cfg.Swarm.ComposerPredictions = false, false
	cfg.Memory.Enabled, cfg.Memory.AutoReview = false, false
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	credential, err := cfg.StageRemoteToken(token, base, "fixture-key")
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.ActivateRemoteToken(credential, config.RemoteCredential{Token: token, Origin: base, Authorized: true, HostID: strings.Repeat("1", 32), UserID: strings.Repeat("2", 32)}); err != nil {
		t.Fatal(err)
	}
	old := startRestartProcess(t, dir, binary, "engine", "--data-dir", data, "--addr", "127.0.0.1:0")
	record := waitRestartRecord(t, data, 0, 15*time.Second, old)
	waitRestartReady(t, record.URL, 15*time.Second)
	waitRestartCondition(t, 10*time.Second, "authorized original catalog", func() bool {
		var listed struct {
			Models []provider.Info `json:"models"`
		}
		restartJSON(t, http.MethodGet, record.URL+"/api/models", nil, http.StatusOK, &listed)
		for _, row := range listed.Models {
			if row.ProviderID == originalID {
				return true
			}
		}
		return false
	})
	var created struct {
		Thread store.Thread `json:"thread"`
	}
	restartJSON(t, http.MethodPost, record.URL+"/api/threads", map[string]string{"title": "Mode restart", "provider_id": originalID}, http.StatusCreated, &created)
	var started struct {
		Turn store.Turn `json:"turn"`
	}
	restartJSON(t, http.MethodPost, record.URL+"/api/threads/"+created.Thread.ID+"/turns", map[string]string{"text": "Finish the current task."}, http.StatusAccepted, &started)
	waitRestartCondition(t, 5*time.Second, "original model request", func() bool { return originalCalls.Load() == 1 })
	restartJSON(t, http.MethodPut, record.URL+"/api/settings", map[string]any{"product": map[string]string{"mode": "fofa"}}, http.StatusOK, nil)
	restartJSON(t, http.MethodPost, record.URL+"/api/account/capabilities/refresh", nil, http.StatusOK, nil)
	if err := old.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	<-old.done
	recovering.Store(true)
	replacement := startRestartProcess(t, dir, binary, "engine", "--data-dir", data, "--addr", "127.0.0.1:0")
	nextRecord := waitRestartRecord(t, data, record.PID, 15*time.Second, replacement)
	waitRestartReady(t, nextRecord.URL, 15*time.Second)
	var final restartProcessTrace
	waitRestartCondition(t, 10*time.Second, "the original recovered turn", func() bool {
		final = restartTrace(t, nextRecord.URL, started.Turn.ID)
		return final.Turn.Status != store.TurnRunning
	})
	if final.Turn.Status != store.TurnDone || final.Turn.ProviderID != originalID || originalCalls.Load() < 2 {
		t.Fatalf("restart lost original model: turn=%+v calls=%d", final.Turn, originalCalls.Load())
	}
	var next struct {
		Turn store.Turn `json:"turn"`
	}
	beforeNext := presetCalls.Load()
	restartJSON(t, http.MethodPost, nextRecord.URL+"/api/threads/"+created.Thread.ID+"/turns", map[string]string{"text": "Start the next request."}, http.StatusAccepted, &next)
	if next.Turn.ProviderID != presetID || next.Turn.Model != "conversation" {
		t.Fatalf("next turn did not apply preset: %+v", next.Turn)
	}
	waitRestartCondition(t, 10*time.Second, "the next preset turn to finish", func() bool {
		final = restartTrace(t, nextRecord.URL, next.Turn.ID)
		return final.Turn.Status != store.TurnRunning
	})
	if final.Turn.Status != store.TurnDone || presetCalls.Load() <= beforeNext {
		t.Fatalf("next turn did not reach the preset endpoint: %+v calls=%d", final.Turn, presetCalls.Load())
	}
}
