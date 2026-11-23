package provider

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/LubyRuffy/eino-swarm/internal/config"
	"github.com/cloudwego/eino/schema"
)

func TestThinkingOffRejectsReportedReasoningWithoutRetry(t *testing.T) {
	for _, tc := range []struct {
		name, message, usage string
	}{
		{name: "reasoning content", message: `{"reasoning_content":"already thinking"}`},
		{name: "billed reasoning", message: `{}`, usage: `,"usage":{"completion_tokens":3,"total_tokens":4,"completion_tokens_details":{"reasoning_tokens":2}}`},
	} {
		for _, streamed := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", tc.name, streamed), func(t *testing.T) {
				var calls atomic.Int32
				endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					var body defaultReasoningRequest
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Effort == nil || *body.Effort != config.ReasoningNone || body.Stream != streamed {
						t.Errorf("request did not explicitly disable thinking: %+v, %v", body, err)
					}
					if streamed {
						w.Header().Set("Content-Type", "text/event-stream")
						_, _ = fmt.Fprintf(w, "data: {\"choices\":[{\"index\":0,\"delta\":%s}]%s}\n\ndata: [DONE]\n\n", tc.message, tc.usage)
						return
					}
					w.Header().Set("Content-Type", "application/json")
					_, _ = fmt.Fprintf(w, `{"choices":[{"index":0,"message":%s,"finish_reason":"stop"}]%s}`, tc.message, tc.usage)
				}))
				t.Cleanup(endpoint.Close)
				cfg := loadDefaultReasoningFixture(t, endpoint.URL, config.APIModeChatCompletions, "", true)
				recorded := make(chan CallRecord, 1)
				build, err := New(cfg).ModelBuilder(t.Context(), "", "", config.ReasoningNone, func(call CallRecord) { recorded <- call })
				if err != nil {
					t.Fatal(err)
				}
				m := build("manager", "manager")
				messages := []*schema.Message{schema.UserMessage("Return a result.")}
				if streamed {
					stream, err := m.Stream(t.Context(), messages)
					if err != nil {
						t.Fatal(err)
					}
					defer stream.Close()
					_, err = stream.Recv()
					if !errors.Is(err, errThinkingOffIgnored) {
						t.Fatalf("stream accepted reasoning with thinking off: %v", err)
					}
					if _, err := stream.Recv(); !errors.Is(err, io.EOF) {
						t.Fatalf("stream did not terminate: %v", err)
					}
				} else if output, err := m.Generate(t.Context(), messages); output != nil || !errors.Is(err, errThinkingOffIgnored) {
					t.Fatalf("response accepted reasoning with thinking off: %v, %v", output, err)
				}
				select {
				case call := <-recorded:
					if !errors.Is(call.Err, errThinkingOffIgnored) || (tc.usage != "" && call.ReasoningTokens != 2) {
						t.Fatalf("incomplete failure receipt: %+v", call)
					}
				default:
					t.Fatal("failure reached the consumer before recording")
				}
				if calls.Load() != 1 {
					t.Fatalf("thinking off was retried: %d calls", calls.Load())
				}
			})
		}
	}
}

func TestOfflineThinkingOffStillOffersToolCalls(t *testing.T) {
	pool := NewMock(config.Default())
	build, err := pool.ModelBuilder(t.Context(), "", "", config.ReasoningNone, nil)
	if err != nil {
		t.Fatal(err)
	}
	source := []*schema.Message{schema.UserMessage("Collect the current results.")}
	generated, err := build("manager", "manager").Generate(t.Context(), source)
	if err != nil || generated.ReasoningContent != "" || len(generated.ToolCalls) != 2 {
		t.Fatalf("offline reply with thinking off: %v, %v", generated, err)
	}
	stream, err := build("manager", "manager").Stream(t.Context(), source)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	calls := 0
	for {
		msg, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil || msg.ReasoningContent != "" {
			t.Fatalf("offline stream with thinking off: %v, %v", msg, err)
		}
		calls += len(msg.ToolCalls)
	}
	if calls != 2 {
		t.Fatalf("offline stream lost tool calls: %d", calls)
	}
}
