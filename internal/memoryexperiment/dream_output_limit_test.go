package memoryexperiment

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/LubyRuffy/eino-swarm/internal/config"
	"github.com/LubyRuffy/eino-swarm/internal/provider"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

type dreamResponseObservation struct {
	model.BaseChatModel
	response *schema.Message
}

func (m *dreamResponseObservation) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	response, err := m.BaseChatModel.Generate(ctx, input, opts...)
	m.response = response
	return response, err
}

// A reasoning-only or partial answer that exhausts the model's output limit
// must retain that cause; treating it as malformed JSON conceals the failure.
func TestDreamOutputLimitKeepsItsCauseAcrossTheActualHTTPModelBoundary(t *testing.T) {
	for _, test := range []struct{ name, content string }{{"empty", ""}, {"partial", `{"revision":`}, {"complete", ""}} {
		t.Run(test.name, func(t *testing.T) {
			store, notes, sources, options, _ := dreamFixture(t)
			content := test.content
			if test.name == "complete" {
				body, err := json.Marshal(DreamProposal{Revision: notes.Rev, Changes: []DreamChange{}})
				if err != nil {
					t.Fatal(err)
				}
				content = string(body)
			}
			var requests atomic.Int32
			endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if body["tools"] != nil || body["model"] != "fixture" {
					t.Errorf("unexpected model/tools: %+v", body)
				}
				w.Header().Set("Content-Type", "application/json")
				response := map[string]any{"id": "response", "object": "chat.completion", "choices": []any{map[string]any{
					"index": 0, "message": map[string]any{"role": "assistant", "content": content, "reasoning_content": "unfinished reasoning"}, "finish_reason": "length",
				}}, "usage": map[string]int{"prompt_tokens": 7, "completion_tokens": 11, "total_tokens": 18}}
				if err := json.NewEncoder(w).Encode(response); err != nil {
					t.Error(err)
				}
			}))
			defer endpoint.Close()
			cfg := config.Default()
			cfg.Models.Default = "fixture"
			cfg.Models.Providers = []config.Provider{{ID: "fixture", BaseURL: endpoint.URL + "/v1", Model: "fixture", APIMode: config.APIModeChatCompletions}}
			var calls []provider.CallRecord
			build, err := provider.New(cfg).AuxiliaryModelBuilder(context.Background(), "fixture", "", "", func(call provider.CallRecord) { calls = append(calls, call) })
			if err != nil {
				t.Fatal(err)
			}
			observation := &dreamResponseObservation{BaseChatModel: build("dream", "dream")}
			proposal, err := GenerateDream(context.Background(), observation, notes, sources, options)
			if !errors.Is(err, ErrDreamOutputLimit) {
				t.Fatalf("lost output-limit cause: proposal=%+v error=%v", proposal, err)
			}
			if proposal.Revision != "" || proposal.Changes != nil || requests.Load() != 1 || len(calls) != 1 {
				t.Fatalf("partial proposal or retry: %+v requests=%d calls=%d", proposal, requests.Load(), len(calls))
			}
			if observation.response == nil || observation.response.Content != content || observation.response.ReasoningContent != "unfinished reasoning" || observation.response.ResponseMeta == nil || observation.response.ResponseMeta.FinishReason != "length" {
				t.Fatalf("original output metadata lost: %+v", observation.response)
			}
			if calls[0].TotalTokens != 18 || calls[0].CompletionTokens != 11 {
				t.Fatalf("original usage lost: %+v", calls[0])
			}
			after, err := store.Read()
			if err != nil || after.Rev != notes.Rev {
				t.Fatalf("limited output wrote notes: %+v %v", after, err)
			}
		})
	}
}

func TestDreamPolicyFrontloadsSalienceWithoutForbiddingUsefulNewFacts(t *testing.T) {
	first := strings.SplitN(dreamInstruction, "Return exactly", 2)[0]
	for _, rule := range []string{"First identify", "most inputs justify no update", "return an empty changes array immediately", "max_changes is a cap, not a target", "Do not exhaustively extract", "validated new findings"} {
		if !strings.Contains(first, rule) {
			t.Errorf("missing frontloaded policy %q", rule)
		}
	}
	for _, target := range []string{"R05", "8192", "spawn_agent", "AGENTS.md"} {
		if strings.Contains(dreamInstruction, target) {
			t.Errorf("target-specific policy leaked: %q", target)
		}
	}
}

// Cancellation after a completed response must not conceal the already observed
// finish reason. It still returns no proposal and makes no second request.
func TestDreamLateCancellationDoesNotReplaceAnObservedOutputLimit(t *testing.T) {
	_, notes, sources, options, _ := dreamFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fake := &dreamTestModel{generate: func(context.Context) (*schema.Message, error) {
		cancel()
		return &schema.Message{Role: schema.Assistant, ResponseMeta: &schema.ResponseMeta{FinishReason: "length"}}, nil
	}}
	proposal, err := GenerateDream(ctx, fake, notes, sources, options)
	if !errors.Is(err, ErrDreamOutputLimit) || proposal.Revision != "" || proposal.Changes != nil || fake.calls != 1 {
		t.Fatalf("cause replaced or request retried: proposal=%+v err=%v calls=%d", proposal, err, fake.calls)
	}
}
