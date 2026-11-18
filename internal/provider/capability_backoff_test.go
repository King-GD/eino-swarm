package provider

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LubyRuffy/eino-swarm/internal/config"
)

// Account capability admission must share the actual request's gate and call
// recorder. Otherwise a recovering service panics, or bills a wait as a call.
func TestAccountModelRecoversThroughOneAdmissionAndRecorder(t *testing.T) {
	for _, api := range []string{"chat_completions", "responses"} {
		for _, streaming := range []bool{false, true} {
			t.Run(api+"/stream="+strconv.FormatBool(streaming), func(t *testing.T) {
				var hits atomic.Int32
				endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if hits.Add(1) == 1 {
						w.WriteHeader(http.StatusServiceUnavailable)
						_, _ = io.WriteString(w, `{"error":{"message":"temporarily unavailable"}}`)
						return
					}
					var body struct {
						Stream bool `json:"stream"`
					}
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
						return
					}
					if body.Stream {
						w.Header().Set("Content-Type", "text/event-stream")
						if api == "responses" {
							_, _ = io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[{\"type\":\"message\",\"content\":[{\"type\":\"output_text\",\"text\":\"ready\"}]}]}}\n\n")
						} else {
							_, _ = io.WriteString(w, backoffChunk+"data: [DONE]\n\n")
						}
					} else {
						w.Header().Set("Content-Type", "application/json")
						if api == "responses" {
							_, _ = io.WriteString(w, `{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"ready"}]}]}`)
						} else {
							_, _ = io.WriteString(w, backoffCompletion)
						}
					}
				}))
				defer endpoint.Close()
				pool := backoffPool(endpoint.URL)
				gate := newBackoffGate(200*time.Millisecond, time.Second)
				pool.backoffs = map[string]*backoffGate{backoffKey(endpoint.URL): gate}
				src := accountSourceFor(t, endpoint.URL, "test-token")
				src.SetCapabilities(roleManifest("conversation", api, true))
				pool.SetAccountSource(src)
				records := make(chan CallRecord, 8)
				build, err := pool.RoleModelBuilder(t.Context(), src.id, "server-model", "", func(r CallRecord) { records <- r }, "conversation")
				if err != nil {
					t.Fatal(err)
				}
				cm := build("manager", "manager")
				if _, err := cm.Generate(t.Context(), backoffMessages()); HTTPStatus(err) != http.StatusServiceUnavailable {
					t.Fatalf("initial failure: %v", err)
				}
				if streaming {
					sr, err := cm.Stream(t.Context(), backoffMessages())
					if err != nil {
						t.Fatal(err)
					}
					defer sr.Close()
					if err := finishBackoffStream(sr); err != nil {
						t.Fatal(err)
					}
				} else {
					out, err := cm.Generate(t.Context(), backoffMessages())
					if err != nil || out == nil || out.Content != "ready" {
						t.Fatalf("recovery: output=%v err=%v", out, err)
					}
				}
				if hits.Load() != 2 || len(records) != 3 {
					t.Fatalf("requests=%d records=%d", hits.Load(), len(records))
				}
				first, wait, last := <-records, <-records, <-records
				if HTTPStatus(first.Err) != http.StatusServiceUnavailable || first.Backoff != nil || last.Err != nil || last.Backoff != nil {
					t.Fatalf("call records: first=%+v last=%+v", first, last)
				}
				if wait.Backoff == nil || wait.Backoff.CooldownID != gate.id+":1" || wait.Backoff.WaitMS <= 0 || wait.AgentID != "manager" || wait.ProviderID != src.id {
					t.Fatalf("wait lost gate/caller identity: %+v", wait)
				}
				if gate.failures != 0 {
					t.Fatal("successful recovery did not clear shared outage")
				}
				release, ok := pool.TryHoldModelPolicy()
				if !ok {
					t.Fatal("completed request retained source policy hold")
				}
				_, err = cm.Generate(t.Context(), backoffMessages())
				release()
				if !errors.Is(err, ErrModelPolicy) || hits.Load() != 2 || len(records) != 0 {
					t.Fatalf("source freeze bypassed: err=%v requests=%d records=%d", err, hits.Load(), len(records))
				}
				pool.InvalidateCapabilities()
				if _, err := cm.Generate(t.Context(), backoffMessages()); !errors.Is(err, ErrModelPolicy) || hits.Load() != 2 {
					t.Fatalf("revoked grant sent HTTP: %v", err)
				}
			})
		}
	}
}

// Non-Pool library models still need capability checks without a policy hold.
func TestCapabilityGuardWithoutTrackedModelRetainsGrantChecks(t *testing.T) {
	pool := New(config.Default())
	src := accountSourceFor(t, "https://gateway.invalid", "test-token")
	src.SetCapabilities(roleManifest("conversation", "chat_completions", true))
	pool.SetAccountSource(src)
	guarded := pool.guardCapability(&fakeModel{}, src.id, "unknown-role")
	if _, err := guarded.Generate(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	pool.SetAccountSource(nil)
	if _, err := guarded.Generate(context.Background(), nil); !errors.Is(err, ErrModelPolicy) {
		t.Fatalf("missing source bypassed: %v", err)
	}
}
