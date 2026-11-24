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

func TestOpenedAccountChatRechecksDiscoveryBeforeAnyModelRequest(t *testing.T) {
	for _, tc := range []struct {
		name    string
		model   string
		api     string
		allowed bool
		cancel  bool
		want    error
	}{
		{name: "restored", model: "server-model", api: "chat_completions", allowed: true},
		{name: "endpoint changed", model: "server-model", api: "responses", allowed: true, want: ErrModelPolicy},
		{name: "role withdrawn", model: "server-model", api: "chat_completions", want: ErrModelPolicy},
		{name: "model withdrawn", model: "different-model", api: "chat_completions", allowed: true, want: ErrModelPolicy},
		{name: "cancelled", cancel: true, want: context.Canceled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var modelRequests, refreshes atomic.Int32
			endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				modelRequests.Add(1)
				if req.URL.Path != "/chat/completions" || req.Header.Get("Authorization") != "Bearer test-token" {
					t.Errorf("request changed endpoint or identity: %s", req.URL.Path)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, backoffCompletion)
			}))
			defer endpoint.Close()
			pool := backoffPool(endpoint.URL)
			src := accountSourceFor(t, endpoint.URL, "test-token")
			src.SetCapabilities(roleManifest("conversation", "chat_completions", true))
			pool.SetAccountSource(src)
			pool.SetLocalAccountSources([]*AccountSource{src})
			records := make(chan CallRecord, 2)
			build, err := pool.ModelBuilder(t.Context(), src.id, "server-model", "", func(r CallRecord) { records <- r })
			if err != nil {
				t.Fatal(err)
			}
			chat := build("manager", "manager")
			pool.InterruptCapabilityDiscovery()
			if pool.FeatureAvailable(src.id, "conversation") {
				t.Fatal("waiting for discovery retained an active grant")
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			pool.SetCapabilitiesRefresher(func(context.Context) error {
				refreshes.Add(1)
				if modelRequests.Load() != 0 || len(records) != 0 {
					t.Error("the model request was sent or recorded before renewal")
				}
				if tc.cancel {
					cancel()
					return ctx.Err()
				}
				fresh, err := NewAccountSource(src.id, "fixture", endpoint.URL, "test-token", Catalog{Names: []string{tc.model}})
				if err != nil {
					return err
				}
				capabilities := roleManifest("conversation", tc.api, true)
				if !tc.allowed {
					delete(capabilities.Capabilities, "conversation")
				}
				fresh.SetCapabilities(capabilities)
				pool.SetAccountSource(fresh)
				return nil
			})
			out, err := chat.Generate(ctx, backoffMessages())
			if !errors.Is(err, tc.want) || refreshes.Load() != 1 {
				t.Fatalf("refreshes=%d, error=%v, want %v", refreshes.Load(), err, tc.want)
			}
			if tc.want == nil {
				if out == nil || out.Content != "ready" || modelRequests.Load() != 1 || len(records) != 1 {
					t.Fatalf("restored result=%v calls=%d records=%d", out, modelRequests.Load(), len(records))
				}
			} else if modelRequests.Load() != 0 || len(records) != 0 {
				t.Fatalf("unauthorized or cancelled request reached the model: calls=%d records=%d", modelRequests.Load(), len(records))
			}
		})
	}
}

func TestBackgroundReviewPreservesModelAndAccountWhenDiscoveryIsInterrupted(t *testing.T) {
	for _, tc := range []struct {
		name  string
		fresh string
		want  error
	}{
		{"cancelled", "", context.Canceled},
		{"discovery owner stopped", "", errCapabilityDiscoveryInterrupted},
		{"account changed", "new-token", ErrAccountModelsUnavailable},
		{"model changed", "test-token", ErrModelPolicy},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pool := New(config.Default())
			const endpoint = "http://127.0.0.1:1"
			src := accountSourceFor(t, endpoint, "test-token")
			src.SetModelPolicy(false, nil)
			src.SetCapabilities(roleManifest("conversation", "chat_completions", true))
			pool.SetAccountSource(src)
			pool.InterruptCapabilityDiscovery()

			ctx, cancel := context.WithCancel(WithChatDiscoveryRecovery(t.Context()))
			defer cancel()
			pool.SetCapabilitiesRefresher(func(context.Context) error {
				if tc.fresh == "" {
					if errors.Is(tc.want, context.Canceled) {
						cancel()
					}
					return errors.New("discovery stopped")
				}
				replacement, err := NewAccountSource(src.id, "fixture", endpoint, tc.fresh, Catalog{Names: []string{"replacement"}})
				if err != nil {
					return err
				}
				replacement.SetModelPolicy(false, nil)
				replacement.SetCapabilities(roleManifest("conversation", "chat_completions", true))
				pool.SetAccountSource(replacement)
				return nil
			})

			build, err := pool.ReviewModelBuilder(ctx, src.id, "server-model", "", nil)
			if build != nil || !errors.Is(err, tc.want) {
				t.Fatalf("unexpected review admission: built=%t err=%v, want %v", build != nil, err, tc.want)
			}
		})
	}
}
