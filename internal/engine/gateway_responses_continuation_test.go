package engine

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/LubyRuffy/eino-swarm/internal/config"
	"github.com/LubyRuffy/eino-swarm/internal/provider"
	"github.com/LubyRuffy/eino-swarm/internal/store"
)

func TestGatewayResponsesConversationContinuesAndRebuilds(t *testing.T) {
	type request struct {
		Store      bool            `json:"store"`
		Previous   string          `json:"previous_response_id"`
		Input      json.RawMessage `json:"input"`
		Stream     bool            `json:"stream"`
		ToolChoice string          `json:"tool_choice"`
		Bytes      int             `json:"-"`
	}
	var mu sync.Mutex
	var requests []request
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/client/models/v1/gateway/responses" || r.Header.Get("Authorization") != "Bearer fixture-key" {
			t.Errorf("wrong Gateway route: %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		raw, err := io.ReadAll(r.Body)
		var in request
		if err != nil || json.Unmarshal(raw, &in) != nil {
			t.Error("invalid Responses request")
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		if replyNoScheduleClaimsResponsesFixture(t, w, in.Input, in.Stream, in.ToolChoice) {
			if in.Store || in.Previous != "" {
				t.Error("auxiliary classification shared the manager's continuation")
			}
			return
		}
		in.Bytes = len(raw)
		mu.Lock()
		requests = append(requests, in)
		n := len(requests)
		mu.Unlock()
		if n == 3 && in.Previous == "resp_2" {
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"error":{"code":"not_found","message":"previous response was not found"}}`)
			return
		}
		answer := fmt.Sprintf("Answer %d.", n)
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("X-AIGateway-Responses-Mode", "native")
		w.Header().Set("X-AIGateway-Responses-Developer-History", "1")
		fmt.Fprintf(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":%q}\n\n", answer)
		fmt.Fprintf(w, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_%d\",\"status\":\"completed\",\"output\":[{\"id\":\"msg_%d\",\"type\":\"message\",\"role\":\"assistant\",\"status\":\"completed\",\"content\":[{\"type\":\"output_text\",\"text\":%q,\"annotations\":[]}]}],\"usage\":{\"input_tokens\":2000,\"output_tokens\":3,\"total_tokens\":2003,\"input_tokens_details\":{\"cached_tokens\":%d}}}}\n\n", n, n, answer, (n-1)*384)
	}))
	t.Cleanup(upstream.Close)
	e := newTestEngineAt(t, t.TempDir())
	e.cfg.Product.Mode = config.ProductModeLocal
	e.cfg.Swarm.AutoTitle, e.cfg.Swarm.ComposerPredictions = false, false
	e.cfg.Memory.Enabled, e.cfg.Memory.AutoReview, e.cfg.Memory.AutoDream = false, false, false
	e.cfg.Personality.Instructions = "Keep the original fixture rule."
	e.pool = provider.New(e.cfg)
	const sourceID, modelName = "gateway:responses-fixture", "native-model"
	source, err := provider.NewAccountSource(sourceID, "Gateway fixture", upstream.URL+"/client/models/v1/gateway", "fixture-key", provider.Catalog{Names: []string{modelName}})
	if err != nil {
		t.Fatal(err)
	}
	source.SetModelPolicy(true, map[string]string{"conversation": modelName})
	source.SetCatalogGroup("original")
	source.SetCapabilities(&provider.SourceCapabilities{Authorized: true, Available: true, Configurable: true,
		Capabilities: map[string]provider.RoleCapability{
			"conversation": {Supported: true, Available: true, Configurable: true, Endpoints: []string{"responses"}, ResponsesModes: []string{"native"}, ResponsesDeveloperHistory: 1},
		}})
	e.pool.SetLocalAccountSources([]*provider.AccountSource{source})
	thread, err := e.CreateThread("Gateway Responses fixture", sourceID, "")
	if err != nil {
		t.Fatal(err)
	}
	const firstText = "First append-only turn. "
	var completed *store.Turn
	for i, input := range []string{strings.Repeat(firstText, 450), "Second turn.", "Third turn.", "Fourth turn.", "Rules changed.", "Apply the revised rules."} {
		if i == 4 {
			if err := e.cfg.Update(func(c *config.Config) error {
				c.Personality.Instructions = "Use the revised fixture rule."
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		}
		turn, err := e.StartTurn(thread.ID, input)
		if err != nil {
			t.Fatal(err)
		}
		completed = waitForTurn(t, e, turn.ID)
		waitSettled(t, e, thread.ID)
		if completed.Status != store.TurnDone {
			t.Fatalf("turn failed: %+v", completed)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	for i, got := range requests {
		t.Logf("request %d store=%v parent=%q bytes=%d", i, got.Store, got.Previous, got.Bytes)
	}
	if len(requests) != 7 {
		t.Fatalf("got %d requests, want six turns and one retry", len(requests))
	}
	for i, parent := range []string{"", "resp_1", "resp_2", "", "resp_4", "", "resp_6"} {
		if got := requests[i]; !got.Store || got.Previous != parent {
			t.Fatalf("request %d: store=%v parent=%q, want true/%q", i, got.Store, got.Previous, parent)
		}
	}
	for _, index := range []int{0, 3, 5} {
		if !strings.Contains(string(requests[index].Input), `"role":"developer"`) {
			t.Fatalf("seed or rebuild %d did not publish the fixed rules as developer input", index)
		}
	}
	if updated := string(requests[5].Input); !strings.Contains(updated, "Use the revised fixture rule.") ||
		strings.Contains(updated, "Keep the original fixture rule.") || !strings.Contains(updated, firstText) ||
		strings.Contains(string(requests[6].Input), "Use the revised fixture rule.") {
		t.Fatal("a fixed rule edit must establish a new full chain before incrementing again")
	}
	if requests[1].Bytes >= requests[0].Bytes || requests[2].Bytes >= requests[3].Bytes || requests[4].Bytes >= requests[3].Bytes ||
		strings.Contains(string(requests[1].Input), firstText) || !strings.Contains(string(requests[3].Input), firstText) ||
		!strings.Contains(string(requests[3].Input), "Answer 2.") || !strings.Contains(string(requests[3].Input), "Third turn.") {
		t.Fatal("incremental/rebuilt requests did not preserve the local transcript")
	}
	calls, err := e.store.ListLLMCalls(completed.ID)
	if err != nil || len(calls) == 0 || calls[0].AgentID != "manager" || calls[0].CachedTokens != 2304 {
		t.Fatalf("Gateway cached_tokens did not reach the call trace: %+v (%v)", calls, err)
	}
	t.Logf("PC Gateway Responses request bytes: seed=%d delta=%d expired_delta=%d rebuild=%d next_delta=%d; reported cached tokens=%d",
		requests[0].Bytes, requests[1].Bytes, requests[2].Bytes, requests[3].Bytes, requests[4].Bytes, calls[0].CachedTokens)
}
