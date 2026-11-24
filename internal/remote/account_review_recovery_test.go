package remote

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LubyRuffy/eino-swarm/internal/engine"
	"github.com/LubyRuffy/eino-swarm/internal/memory"
	"github.com/LubyRuffy/eino-swarm/internal/provider"
	"github.com/LubyRuffy/eino-swarm/internal/store"
)

// An account refresh interruption used to abort automatic memory review
// before its first HTTP request, even when discovery could already recover.
func TestAutomaticMemoryReviewRecoversAfterAccountDiscoveryOutage(t *testing.T) {
	for _, tc := range []struct {
		name     string
		recovery int
		failed   bool
	}{
		{name: "restored", recovery: http.StatusOK},
		{name: "failed-turn-restored", recovery: http.StatusOK, failed: true},
		{name: "revoked", recovery: http.StatusForbidden},
		{name: "still-unavailable", recovery: http.StatusServiceUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := newAccountGateway(t)
			var bootstrapStatus, bootstrapReads, reviewerCalls atomic.Int32
			bootstrapStatus.Store(http.StatusOK)
			g.bootstrap = func(w http.ResponseWriter, r *http.Request) {
				bootstrapReads.Add(1)
				if status := int(bootstrapStatus.Load()); status != http.StatusOK {
					w.WriteHeader(status)
					return
				}
				g.writeBootstrap(w, g.server.URL, true)
			}
			const requirement = "Use metric units in future reports."
			var policyFirst sync.Once
			beforeTerminal := make(chan struct{})
			releaseTerminal := make(chan struct{})
			g.extra = func(w http.ResponseWriter, r *http.Request) bool {
				if r.URL.Path == "/client/models/v1/capabilities" {
					source := &provider.SourceCapabilities{Authorized: true, Available: true, Configurable: true, Capabilities: map[string]provider.RoleCapability{
						"conversation": {Supported: true, Available: true, Configurable: true, Endpoints: []string{"chat_completions"}},
					}}
					_ = json.NewEncoder(w).Encode(map[string]any{"data": provider.ClientCapabilities{Version: 1, ModelRolesVersion: 3, Sources: map[string]*provider.SourceCapabilities{"preset": source, "gateway": source}}})
					return true
				}
				if r.URL.Path != "/client/desktop/v1/chat/completions" {
					return false
				}
				var req struct {
					Model  string `json:"model"`
					Stream bool   `json:"stream"`
					Tools  []struct {
						Function struct{ Name string } `json:"function"`
					} `json:"tools"`
				}
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Error(err)
				}
				if req.Model != "server-model" || r.Header.Get("Authorization") != "Bearer "+accountModelsToken {
					t.Error("model or credential changed")
				}
				review := false
				for _, tool := range req.Tools {
					review = review || tool.Function.Name == memory.ToolApplyReview
				}
				if (!req.Stream || tc.failed) && !review {
					policyFirst.Do(func() { close(beforeTerminal) })
					select {
					case <-releaseTerminal:
					case <-r.Context().Done():
						return true
					}
					if tc.failed {
						w.WriteHeader(http.StatusBadRequest)
						_, _ = fmt.Fprint(w, `{"error":{"message":"fixture manager failed"}}`)
						return true
					}
					w.Header().Set("Content-Type", "application/json")
					_, _ = fmt.Fprint(w, `{"choices":[{"index":0,"message":{"role":"assistant","content":"{\"claims\":[]}"},"finish_reason":"stop"}]}`)
					return true
				}
				w.Header().Set("Content-Type", "text/event-stream")
				delta := `{"content":"Finished."}`
				reason := "stop"
				if review {
					if tc.failed && len(req.Tools) != 1 {
						t.Error("a failed turn received review tools beyond the notes-only batch")
					}
					reviewerCalls.Add(1)
					arguments := fmt.Sprintf(`{"changes":[{"target":"memory","action":"add","content":%q,"evidence":[{"seq":1,"quote":%q}]}]}`, requirement, requirement)
					delta = fmt.Sprintf(`{"tool_calls":[{"index":0,"id":"review-batch","type":"function","function":{"name":%q,"arguments":%q}}]}`, memory.ToolApplyReview, arguments)
					reason = "tool_calls"
				}
				write := func(delta, finish string) {
					_, _ = fmt.Fprintf(w, "data: {\"id\":\"gateway-fixture\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":%s,\"finish_reason\":%s}]}\n\n", delta, finish)
					w.(http.Flusher).Flush()
				}
				write(`{"role":"assistant"}`, "null")
				write(delta, "null")
				write("{}", fmt.Sprintf("%q", reason))
				_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
				return true
			}

			account, pool, cfg := accountModelsFixture(t, g)
			account.Start()
			t.Cleanup(account.Stop)
			waitFor(t, func() bool { return account.State().State == AccountReady }, "initial discovery")
			st, err := store.Open(cfg.DBPath())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = st.Close() })
			e := engine.New(cfg, st, pool, account.log)
			t.Cleanup(e.Shutdown)
			project, err := e.CreateProject("Memory", "", t.TempDir(), true)
			if err != nil {
				t.Fatal(err)
			}
			th, err := e.CreateThread("Known title", accountModelsSourceID, project.ID)
			if err != nil {
				t.Fatal(err)
			}
			turn, err := e.StartTurn(th.ID, requirement)
			if err != nil {
				t.Fatal(err)
			}
			select {
			case <-beforeTerminal:
			case <-time.After(10 * time.Second):
				t.Fatal("the in-flight turn never reached its gateway gate")
			}
			bootstrapStatus.Store(http.StatusServiceUnavailable)
			if err := pool.RefreshCapabilities(t.Context()); err != nil {
				t.Fatal(err)
			}
			if pool.FeatureAvailable(accountModelsSourceID, "conversation") {
				t.Fatal("failed discovery still granted conversation access")
			}
			reads := bootstrapReads.Load()
			bootstrapStatus.Store(int32(tc.recovery))
			close(releaseTerminal)

			var receipt struct {
				Err        string `json:"err"`
				StopReason string `json:"stop_reason"`
				Attempts   int    `json:"attempt_count"`
			}
			deadline := time.Now().Add(10 * time.Second)
			var observed []string
			found := false
			for time.Now().Before(deadline) && !found {
				events, err := st.ListTurnEvents(turn.ID)
				if err != nil {
					t.Fatal(err)
				}
				observed = observed[:0]
				for _, event := range events {
					observed = append(observed, event.Kind+" "+event.Err)
					if event.Kind == engine.KindMemoryReview {
						if err := json.Unmarshal([]byte(event.Text), &receipt); err != nil {
							t.Fatal(err)
						}
						found = true
						break
					}
				}
				if !found {
					time.Sleep(10 * time.Millisecond)
				}
			}
			if !found {
				saved, err := st.GetTurn(turn.ID)
				calls, _ := st.ListLLMCalls(turn.ID)
				t.Fatalf("no review receipt: events=%v turn=%+v err=%v, requests=%v calls=%+v", observed, saved, err, g.seen(), calls)
			}
			if n := bootstrapReads.Load() - reads; n != 1 {
				t.Errorf("expected one owned discovery refresh, got %d: events=%v requests=%v", n, observed, g.seen())
			}
			wantStatus := store.TurnDone
			if tc.failed {
				wantStatus = store.TurnError
			}
			saved, err := st.GetTurn(turn.ID)
			if err != nil || saved.Status != wantStatus {
				t.Fatalf("turn status=%+v err=%v, want %s", saved, err, wantStatus)
			}
			notes, err := e.ProjectMemory(project.ID).Read()
			if err != nil {
				t.Fatal(err)
			}
			if tc.recovery == http.StatusOK {
				if len(notes.Entries) != 1 || notes.Entries[0] != requirement {
					t.Fatalf("restored review did not write its cited note: %+v", notes)
				}
				if receipt.Err != "" || receipt.StopReason != "completed" || receipt.Attempts != 1 || reviewerCalls.Load() != 1 {
					t.Fatalf("review did not complete after discovery recovered: %+v, model requests=%d", receipt, reviewerCalls.Load())
				}
			} else if receipt.Err == "" || receipt.StopReason != "error" || receipt.Attempts != 0 || reviewerCalls.Load() != 0 || len(notes.Entries) != 0 ||
				strings.Contains(receipt.Err, "model_selection_disabled") {
				t.Fatalf("review did not preserve the account failure: %+v, model requests=%d", receipt, reviewerCalls.Load())
			}
		})
	}
}
