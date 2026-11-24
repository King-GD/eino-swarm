package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/LubyRuffy/eino-swarm/internal/config"
	experiment "github.com/LubyRuffy/eino-swarm/internal/memoryexperiment"
	"gopkg.in/yaml.v3"
)

// One Generate callback can otherwise hide two actual POSTs separated by the
// provider's header reconnect/backoff. A measurement must own that recovery.
func TestDreamHeaderDeadlineDoesNotReplayOrEnterSharedProviderBackoff(t *testing.T) {
	var requests atomic.Int32
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if _, err := io.Copy(io.Discard, r.Body); err != nil {
			t.Error(err)
		}
		<-r.Context().Done()
	}))
	defer endpoint.Close()
	configPath := writeTestConfig(t, config.Provider{ID: "fixture", BaseURL: endpoint.URL + "/v1", Model: "fixture", FirstByteTimeoutSeconds: 1, TimeoutSeconds: 5})
	dir := t.TempDir()
	inputPath, out := filepath.Join(dir, "input.json"), filepath.Join(dir, "out")
	writeTestJSON(t, inputPath, fixtureInput())
	if err := runDream(context.Background(), options{input: inputPath, out: out, config: configPath, provider: "fixture", timeout: 8}); err == nil {
		t.Fatal("header timeout unexpectedly completed")
	}
	result := readDreamResult(t, out)
	if requests.Load() != 1 {
		t.Errorf("one logical callback concealed %d actual HTTP requests; want exactly one", requests.Load())
	}
	if len(result.Calls) != 1 || result.CostComplete || result.ModelOutput != nil || result.Proposal.Revision != "" || result.FailureKind != "input_or_provider_error" {
		t.Fatalf("timeout evidence incomplete: calls=%d complete=%v kind=%s", len(result.Calls), result.CostComplete, result.FailureKind)
	}
	if !strings.Contains(result.Error, "no first byte within 1s") || result.DurationMS >= 4000 {
		t.Fatalf("first header cause obscured by reconnect/backoff: duration=%d", result.DurationMS)
	}
}

// A rejected configured effort is a measured error. The experiment must not
// silently remove that effort and spend a second request on compatibility.
func TestDreamConfiguredReasoningRejectionDoesNotRetryWithoutItsEffort(t *testing.T) {
	var requests atomic.Int32
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempt := requests.Add(1)
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if attempt == 1 {
			if body["reasoning_effort"] != "low" {
				t.Error("configured effort did not reach initial request")
			}
			w.WriteHeader(http.StatusBadRequest)
			if _, err := io.WriteString(w, `{"error":{"message":"unknown parameter: reasoning_effort","type":"invalid_request_error"}}`); err != nil {
				t.Error(err)
			}
			return
		}
		if body["reasoning_effort"] != nil {
			t.Error("fallback did not remove optional effort")
		}
		snapshot := requestFrozenNotes(t, body)
		proposal := experiment.DreamProposal{Revision: snapshot.Rev, Changes: []experiment.DreamChange{}}
		raw, err := json.Marshal(proposal)
		if err != nil {
			t.Error(err)
			return
		}
		response := map[string]any{"id": "response", "object": "chat.completion", "choices": []any{map[string]any{"index": 0, "message": map[string]any{"role": "assistant", "content": string(raw)}, "finish_reason": "stop"}}, "usage": map[string]int{"prompt_tokens": 7, "completion_tokens": 5, "total_tokens": 12}}
		if err := json.NewEncoder(w).Encode(response); err != nil {
			t.Error(err)
		}
	}))
	defer endpoint.Close()
	configPath := writeTestConfig(t, config.Provider{ID: "fixture", BaseURL: endpoint.URL + "/v1", Model: "fixture"})
	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	if err := yaml.Unmarshal(raw, cfg); err != nil {
		t.Fatal(err)
	}
	cfg.Swarm.DefaultReasoningEffort = config.ReasoningLow
	raw, err = yaml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	inputPath, out := filepath.Join(dir, "input.json"), filepath.Join(dir, "out")
	writeTestJSON(t, inputPath, fixtureInput())
	if err := runDream(context.Background(), options{input: inputPath, out: out, config: configPath, provider: "fixture", timeout: 3}); err == nil {
		t.Error("configured effort rejection was silently repaired")
	}
	result := readDreamResult(t, out)
	if requests.Load() != 1 || len(result.Calls) != 1 || result.Calls[0].Error == "" || result.Error == "" || result.Proposal.Revision != "" || result.CostComplete {
		t.Fatalf("configured effort rejection was hidden or retried: HTTP=%d callrows=%d complete=%v", requests.Load(), len(result.Calls), result.CostComplete)
	}
}
