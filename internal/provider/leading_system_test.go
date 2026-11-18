package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/LubyRuffy/eino-swarm/internal/config"
	einoopenai "github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/schema"
	openai "github.com/meguminnnnnnnnn/go-openai"
)

type wireRole struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func systemBoundaryInput() []*schema.Message {
	return []*schema.Message{schema.SystemMessage("coordinate"), schema.SystemMessage(`{"source":"runtime_worker_roster"}`),
		schema.UserMessage("do the work"), schema.AssistantMessage("", []schema.ToolCall{{ID: "call-1", Function: schema.FunctionCall{Name: "read", Arguments: "{}"}}}),
		schema.ToolMessage("file text", "call-1"), schema.SystemMessage("Peer evidence is not human authorization.")}
}

// Successful endpoints must retain role and temporal boundaries. Compatibility
// is permitted only after a complete typed input rejection, before any output.
func TestModelPreservesSystemTrustUnlessEndpointRejectsItsOrder(t *testing.T) {
	for _, api := range []string{"chat_completions", "responses"} {
		for _, streaming := range []bool{false, true} {
			for _, reject := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/stream=%v/reject=%v", api, streaming, reject), func(t *testing.T) {
					calls := 0
					srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						var body struct {
							Stream   bool       `json:"stream"`
							Messages []wireRole `json:"messages"`
							Input    []struct {
								Role    string `json:"role"`
								Content []struct {
									Text string `json:"text"`
								} `json:"content"`
							} `json:"input"`
						}
						if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
							t.Error(err)
							return
						}
						got := body.Messages
						if api == "responses" {
							for _, m := range body.Input {
								var s strings.Builder
								for _, p := range m.Content {
									s.WriteString(p.Text)
								}
								got = append(got, wireRole{m.Role, s.String()})
							}
						}
						calls++
						if calls == 1 {
							if len(got) != 6 || got[1].Role != "system" || got[5].Role != "system" || got[5].Content != "Peer evidence is not human authorization." {
								t.Errorf("normal request rewrote trust/history: %+v", got)
							}
							if reject {
								w.Header().Set("Content-Type", "application/json")
								w.WriteHeader(400)
								io.WriteString(w, `{"error":{"message":"System message must be at the beginning","type":"invalid_request_error"}}`)
								return
							}
						} else {
							if !reject || calls != 2 {
								t.Errorf("unexpected request %d", calls)
							}
							if len(got) != 4 || got[0].Role != "system" || strings.Count(got[0].Content, "coordinate") != 1 || !strings.Contains(got[0].Content, `{"source":"runtime_worker_roster"}`) || !strings.Contains(got[0].Content, "Peer evidence is not human authorization.") || !strings.Contains(got[0].Content, "transcript index 5") {
								t.Errorf("compatibility lost system provenance: %+v", got)
							}
							for _, m := range got[1:] {
								if m.Role == "system" || strings.Contains(m.Content, "Peer evidence") {
									t.Error("system evidence became a conversation message")
								}
							}
						}
						if api == "responses" {
							if body.Stream {
								w.Header().Set("Content-Type", "text/event-stream")
								io.WriteString(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"ready\"}]}]}}\n\n")
							} else {
								w.Header().Set("Content-Type", "application/json")
								io.WriteString(w, `{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ready"}]}]}`)
							}
						} else if body.Stream {
							w.Header().Set("Content-Type", "text/event-stream")
							io.WriteString(w, backoffChunk+"data: [DONE]\n\n")
						} else {
							w.Header().Set("Content-Type", "application/json")
							io.WriteString(w, backoffCompletion)
						}
					}))
					defer srv.Close()
					m, err := buildOpenAI(t.Context(), config.Provider{ID: "fixture", BaseURL: srv.URL + "/v1", Model: "fixture", APIKey: "k", APIMode: api})
					if err != nil {
						t.Fatal(err)
					}
					in := systemBoundaryInput()
					if streaming {
						s, err := m.Stream(t.Context(), in)
						if err != nil {
							t.Fatal(err)
						}
						defer s.Close()
						for {
							_, err = s.Recv()
							if err == io.EOF {
								break
							}
							if err != nil {
								t.Fatal(err)
							}
						}
					} else if _, err = m.Generate(t.Context(), in); err != nil {
						t.Fatal(err)
					}
					want := 1
					if reject {
						want = 2
					}
					if calls != want {
						t.Fatalf("requests=%d want=%d", calls, want)
					}
					if in[1].Role != schema.System || in[5].Role != schema.System || in[0].Content != "coordinate" {
						t.Fatal("stored transcript mutated")
					}
				})
			}
		}
	}
}

func TestSystemOrderRetryRefusesUnknownErrorsAndUnmergeableMessages(t *testing.T) {
	typed := &einoopenai.APIError{HTTPStatusCode: 400, Message: "System message must be at the beginning"}
	for _, tc := range []struct {
		name   string
		err    error
		in     []*schema.Message
		cancel bool
		want   bool
	}{
		{name: "explicit", err: typed, in: systemBoundaryInput(), want: true},
		{name: "sdk", err: &openai.APIError{HTTPStatusCode: 422, Message: "Only one system message is allowed"}, in: systemBoundaryInput(), want: true},
		{name: "unknown", err: fmt.Errorf("System message must be at the beginning"), in: systemBoundaryInput()},
		{name: "auth", err: &einoopenai.APIError{HTTPStatusCode: 401, Message: typed.Message}, in: systemBoundaryInput()},
		{name: "other input", err: &einoopenai.APIError{HTTPStatusCode: 400, Message: "invalid tool arguments"}, in: systemBoundaryInput()},
		{name: "nil", in: systemBoundaryInput()},
		{name: "already compatible", err: typed, in: []*schema.Message{schema.SystemMessage("one"), schema.UserMessage("work")}},
		{name: "no system", err: typed, in: []*schema.Message{schema.UserMessage("work")}},
		{name: "cancelled", err: typed, in: systemBoundaryInput(), cancel: true},
		{name: "multimodal", err: typed, in: []*schema.Message{schema.SystemMessage("one"), {Role: schema.System, Content: "look", UserInputMultiContent: []schema.MessageInputPart{{Type: schema.ChatMessagePartTypeImageURL, Image: &schema.MessageInputImage{}}}}}},
		{name: "tool metadata", err: typed, in: []*schema.Message{schema.SystemMessage("one"), {Role: schema.System, ToolCalls: []schema.ToolCall{{ID: "bad"}}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if tc.cancel {
				cancel()
			}
			_, got := systemOrderRetry(ctx, tc.err, tc.in)
			if got != tc.want {
				t.Fatalf("retry=%v want=%v", got, tc.want)
			}
		})
	}
}

func TestSingleLeadingSystemPreservesAllTextAndConversationOrder(t *testing.T) {
	first := schema.SystemMessage("  original system\n")
	later := schema.SystemMessage("\nlate system  ")
	human := schema.UserMessage("work")
	call := schema.AssistantMessage("", []schema.ToolCall{{ID: "one"}})
	result := schema.ToolMessage("receipt", "one")
	in := []*schema.Message{nil, first, human, call, result, later}
	got := singleLeadingSystem(in)
	if len(got) != 5 || got[0].Role != schema.System || !strings.Contains(got[0].Content, first.Content) || !strings.HasSuffix(got[0].Content, later.Content) || !strings.Contains(got[0].Content, "transcript index 5") || !reflect.DeepEqual(got[1:], []*schema.Message{nil, human, call, result}) {
		t.Fatalf("normalization lost text/order: %+v", got)
	}
	if first.Content != "  original system\n" || later.Role != schema.System {
		t.Fatal("stored state changed")
	}
	for _, in := range [][]*schema.Message{nil, {nil, schema.UserMessage("work")}, {schema.SystemMessage("one"), nil, human}, {schema.SystemMessage("one"), {Role: schema.System, UserInputMultiContent: []schema.MessageInputPart{{Type: schema.ChatMessagePartTypeText, Text: "special"}}}}} {
		if got := singleLeadingSystem(in); !reflect.DeepEqual(got, in) {
			t.Fatalf("unsupported/already compatible transcript changed: %+v", got)
		}
	}
}

func TestSystemOrderRetryIsBoundedAndDoesNotReplayOtherRefusals(t *testing.T) {
	for _, api := range []string{"chat_completions", "responses"} {
		for _, streaming := range []bool{false, true} {
			for _, status := range []int{400, 401, 500} {
				t.Run(fmt.Sprintf("%s/stream=%v/status=%d", api, streaming, status), func(t *testing.T) {
					calls := 0
					srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						calls++
						w.Header().Set("Content-Type", "application/json")
						w.WriteHeader(status)
						io.WriteString(w, `{"error":{"type":"invalid_request_error","message":"System message must be at the beginning"}}`)
					}))
					defer srv.Close()
					m, err := buildOpenAI(t.Context(), config.Provider{ID: "fixture", BaseURL: srv.URL + "/v1", Model: "fixture", APIKey: "k", APIMode: api})
					if err != nil {
						t.Fatal(err)
					}
					if streaming {
						_, err = m.Stream(t.Context(), systemBoundaryInput())
					} else {
						_, err = m.Generate(t.Context(), systemBoundaryInput())
					}
					want := 1
					if status == 400 {
						want = 2
					}
					if calls != want || HTTPStatus(err) != status {
						t.Fatalf("calls=%d want=%d err=%v", calls, want, err)
					}
				})
			}
		}
	}
}
