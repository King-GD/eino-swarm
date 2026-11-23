package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LubyRuffy/eino-swarm/internal/desktop"
)

func TestRecoveryTimeoutSamplesOriginalAndSuccessorLocally(t *testing.T) {
	engine := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodPost && strings.Contains(request.Header.Get("Content-Type"), "json") {
			var event desktop.RecoveryDiagnostic
			if err := json.NewDecoder(request.Body).Decode(&event); err != nil || event.Phase == "timeout_sample" {
				t.Errorf("unexpected Trace event: %+v, %v", event, err)
			}
		}
		response.WriteHeader(http.StatusNoContent)
	}))
	defer engine.Close()
	for generation, phase := range []string{"reloading", "probe_failed"} {
		path := filepath.Join(t.TempDir(), "journal.jsonl")
		spec := desktopRecoverySpec{Journal: path, Generation: generation}
		spec.Engine.URL = engine.URL
		report, closeReporter := desktopRecoveryReporter(spec)
		event := desktop.RecoveryDiagnostic{Phase: phase, Generation: generation, Navigation: "finished"}
		report(event)
		report(event)
		closeReporter()
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		count := 0
		for _, line := range strings.Split(strings.TrimSpace(string(body)), "\n") {
			var row desktop.RecoveryDiagnostic
			if err := json.Unmarshal([]byte(line), &row); err != nil {
				t.Fatal(err)
			}
			if row.Phase == "timeout_sample" {
				count++
				if row.Generation != generation || row.Navigation != "finished" || row.Resources == nil ||
					row.Resources.EngineStatus != http.StatusNoContent || row.Resources.HostCPU == "" {
					t.Fatalf("incomplete timeout sample: %+v", row)
				}
			}
		}
		if count != 1 {
			t.Fatalf("generation %d: got %d samples, want one", generation, count)
		}
	}
}

func TestRecoveryEngineProbeDistinguishesResponseFromTimeout(t *testing.T) {
	for _, test := range []struct {
		name   string
		status int
		want   string
	}{
		{"ready", http.StatusOK, "responded"},
		{"unhealthy", http.StatusServiceUnavailable, "responded"},
		{"blocked", 0, "timeout"},
	} {
		t.Run(test.name, func(t *testing.T) {
			engine := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				if request.Method != http.MethodGet || request.URL.Path != "/api/meta" {
					t.Error("expected read-only engine metadata probe")
				}
				if test.status == 0 {
					<-request.Context().Done()
					return
				}
				response.WriteHeader(test.status)
			}))
			defer engine.Close()
			got := probeDesktopRecoveryEngine(context.Background(), engine.URL)
			if got.EngineHTTP != test.want || got.EngineStatus != test.status {
				t.Fatalf("unexpected evidence: %+v", got)
			}
		})
	}
}
