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
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

func TestNativeResponsesReasoningTextMatchesTheCompletedItem(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"type\":\"response.reasoning_text.delta\",\"delta\":\"Checking.\"}\n\n")
		fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"OK\"}\n\n")
		fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_fixture\",\"status\":\"completed\",\"output\":[{\"type\":\"reasoning\",\"summary\":[],\"content\":[{\"type\":\"reasoning_text\",\"text\":\"Checking.\"}]},{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"OK\"}]}]}}\n\n")
	}))
	defer server.Close()
	m := testResponsesModel(t, config.Provider{BaseURL: server.URL, Model: "fixture"})
	stream, err := m.Stream(t.Context(), []*schema.Message{schema.UserMessage("question")})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	var chunks []*schema.Message
	for {
		msg, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		chunks = append(chunks, msg)
	}
	out, err := schema.ConcatMessages(chunks)
	if err != nil || out.Content != "OK" || out.ReasoningContent != "Checking." || out.ResponseMeta.FinishReason != "stop" {
		t.Fatalf("terminal reasoning must verify the existing deltas: %+v (%v)", out, err)
	}
}

func TestResponsesContinuationMatchesTheActualReplay(t *testing.T) {
	for _, scenario := range []struct {
		name   string
		alter  func(*schema.Message) *schema.Message
		opts   []model.Option
		parent bool
		late   bool
	}{
		{name: "same opaque items and paired tool result", parent: true},
		{name: "removed calls and reasoning", alter: func(out *schema.Message) *schema.Message {
			return schema.AssistantMessage(out.Content, nil)
		}},
		{name: "rewritten arguments", alter: func(out *schema.Message) *schema.Message {
			out.ToolCalls[0].Function.Arguments = `{"path":"other"}`
			return out
		}},
		{name: "changed model", opts: []model.Option{model.WithModel("other")}},
		{name: "changed tool policy", opts: []model.Option{model.WithToolChoice(schema.ToolChoiceForbidden)}},
		{name: "later System message requires a stateless replay", late: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			requests := make(chan map[string]any, 2)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				requests <- body
				w.Header().Set("X-AIGateway-Responses-Mode", "native")
				w.Header().Set("X-AIGateway-Responses-Developer-History", "1")
				fmt.Fprint(w, responsesFixture)
			}))
			defer server.Close()
			m := testResponsesModel(t, config.Provider{BaseURL: server.URL, Model: "fixture"}).withContinuation(&ResponsesContinuation{})
			fixed := schema.SystemMessage("fixed instruction")
			seed := schema.UserMessage("question")
			out, err := m.Generate(t.Context(), []*schema.Message{fixed, seed})
			if err != nil {
				t.Fatal(err)
			}
			if initial := <-requests; initial["store"] != true || initial["previous_response_id"] != nil {
				t.Fatalf("invalid seed: %v", initial)
			}
			if scenario.alter != nil {
				out = scenario.alter(out)
			}
			history := []*schema.Message{fixed, seed, out, schema.ToolMessage("result", "fc_1")}
			if scenario.late {
				history = append(history, schema.SystemMessage("later instruction"))
			}
			if _, err := m.Generate(t.Context(), history, scenario.opts...); err != nil {
				t.Fatal(err)
			}
			body := <-requests
			items := body["input"].([]any)
			if scenario.parent {
				if body["previous_response_id"] != "resp_1" || len(items) != 1 || items[0].(map[string]any)["call_id"] != "fc_1" {
					t.Fatalf("increment lost the paired tool result: %v", body)
				}
			} else if body["previous_response_id"] != nil || len(items) < 3 {
				t.Fatalf("changed input or policy still reused a parent: %v", body)
			}
			if scenario.late && (body["store"] != false || items[0].(map[string]any)["role"] != "system" ||
				items[len(items)-1].(map[string]any)["role"] != "system") {
				t.Fatalf("stateless replay changed the System order or stored it: %v", body)
			}
		})
	}
}

func TestResponsesContinuationNeverRetriesAnAdmittedStream(t *testing.T) {
	const exhausted = `{"type":"error","code":"previous_response_not_found","message":"previous response was not found"}`
	calls := make(chan map[string]any, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		calls <- body
		w.Header().Set("X-AIGateway-Responses-Mode", "native")
		w.Header().Set("X-AIGateway-Responses-Developer-History", "1")
		if body["stream"] == true {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprintf(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"observed\"}\n\ndata: %s\n\n", exhausted)
			return
		}
		fmt.Fprint(w, responsesFixture)
	}))
	defer server.Close()
	m := testResponsesModel(t, config.Provider{BaseURL: server.URL, Model: "fixture"}).withContinuation(&ResponsesContinuation{})
	seed := schema.UserMessage("question")
	out, err := m.Generate(t.Context(), []*schema.Message{seed})
	if err != nil {
		t.Fatal(err)
	}
	<-calls
	stream, err := m.Stream(t.Context(), []*schema.Message{seed, out, schema.ToolMessage("result", "fc_1")})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	delta, err := stream.Recv()
	if err != nil || delta.Content != "observed" {
		t.Fatalf("lost observed output: %v %v", delta, err)
	}
	if _, err := stream.Recv(); err == nil || err == io.EOF {
		t.Fatalf("admitted stream lost the provider failure: %v", err)
	}
	if _, err := stream.Recv(); err != io.EOF {
		t.Fatalf("failed stream did not close: %v", err)
	}
	if body := <-calls; body["previous_response_id"] != "resp_1" || len(calls) != 0 {
		t.Fatalf("admitted stream retried unexpectedly: %v (extra %d)", body, len(calls))
	}
}

func TestResponsesUnavailableStorageUsesFullStatelessRequest(t *testing.T) {
	calls := make(chan bool, 3)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Store bool  `json:"store"`
			Input []any `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Input) != 1 {
			t.Errorf("stateless recovery must send the complete input: %v (%v)", body, err)
		}
		calls <- body.Store
		if body.Store {
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprint(w, `{"error":{"code":"response_state_unavailable","message":"response state storage is unavailable"}}`)
			return
		}
		fmt.Fprint(w, responsesFixture)
	}))
	defer server.Close()
	cursor := &ResponsesContinuation{}
	m := testResponsesModel(t, config.Provider{BaseURL: server.URL, Model: "fixture"}).withContinuation(cursor)
	if _, err := m.Generate(t.Context(), []*schema.Message{schema.UserMessage("question")}); err != nil {
		t.Fatal(err)
	}
	first, second := <-calls, <-calls
	if !first || second || len(calls) != 0 || cursor.last != nil {
		t.Fatalf("expected one stateless fallback without a cursor: store %v then %v", first, second)
	}
}

func TestResponsesRejectedPinRebuildsOnceOnAnUnpinnedRoute(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Previous string `json:"previous_response_id"`
			Store    bool   `json:"store"`
			Input    []any  `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil || !request.Store {
			t.Errorf("invalid stored Responses request: %v", err)
		}
		switch attempts.Add(1) {
		case 2:
			if request.Previous != "resp_1" || len(request.Input) != 1 {
				t.Error("the initial retry target was not the incremental request")
			}
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprint(w, `{"error":{"code":"provider_unavailable","message":"no healthy provider can serve this model"}}`)
		case 3:
			if request.Previous != "" || len(request.Input) != 6 {
				t.Error("rebuild did not replay the full input on an unpinned route")
			}
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"error":{"code":"unauthorized","message":"authentication revoked"}}`)
		default:
			w.Header().Set("X-AIGateway-Responses-Mode", "native")
			w.Header().Set("X-AIGateway-Responses-Developer-History", "1")
			fmt.Fprint(w, responsesFixture)
		}
	}))
	defer server.Close()
	cursor := &ResponsesContinuation{}
	m := testResponsesModel(t, config.Provider{BaseURL: server.URL, Model: "fixture"}).withContinuation(cursor)
	seed := schema.UserMessage("question")
	out, err := m.Generate(t.Context(), []*schema.Message{seed})
	if err != nil {
		t.Fatal(err)
	}
	ctx, route := observeRouteRejection(t.Context())
	_, err = m.Generate(ctx, []*schema.Message{seed, out, schema.ToolMessage("result", "fc_1")})
	if HTTPStatus(err) != http.StatusUnauthorized || isRouteUnavailable(route.wrap(err)) || attempts.Load() != 3 || cursor.last != nil {
		t.Fatalf("the full request must preserve its own refusal: %v (attempts=%d)", err, attempts.Load())
	}
}

func TestResponsesRebuildRequiresAnExplicitAdmissionRefusal(t *testing.T) {
	for _, tc := range []struct {
		name         string
		status       int
		wire         string
		hasParent    bool
		rebuild      bool
		withoutStore bool
	}{
		{"missing Gateway parent", 404, `{"error":{"code":"not_found","message":"previous response was not found"}}`, true, true, false},
		{"missing vLLM parent", 404, `{"error":{"code":404,"type":"invalid_request_error","param":"response_id","message":"Response with id 'upstream' not found."}}`, true, true, false},
		{"vLLM unknown resource", 404, `{"error":{"code":404,"type":"invalid_request_error","param":"file_id","message":"File with id 'upstream' not found."}}`, true, false, false},
		{"invalid parent", 400, `{"error":{"code":"invalid_previous_response_id","param":"previous_response_id","message":"Invalid previous response ID"}}`, true, true, false},
		{"unavailable pin", 503, `{"error":{"code":"provider_unavailable","message":"no healthy provider can serve this model"}}`, true, true, false},
		{"whole model unavailable", 503, `{"error":{"code":"provider_unavailable","message":"no healthy provider can serve this model"}}`, false, false, false},
		{"incomplete locator", 503, `{"error":{"code":"response_state_unavailable","message":"previous response state is incomplete"}}`, true, true, false},
		{"preflight storage failure", 503, `{"error":{"code":"response_state_unavailable","message":"response state storage is unavailable"}}`, true, true, true},
		{"terminal storage failure", 503, `{"error":{"code":"response_state_unavailable","message":"response state storage is temporarily unavailable"}}`, true, false, false},
		{"denied", 403, `{"error":{"code":"previous_response_not_found"}}`, true, false, false},
		{"model removed", 404, `{"error":{"code":"model_not_found"}}`, true, false, false},
		{"malformed error", 404, `<html>Not found</html>`, true, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original := responsesHTTPError(tc.status, http.StatusText(tc.status), []byte(tc.wire))
			rebuild, withoutStore := responsesRebuildReason(original, tc.hasParent)
			if rebuild != tc.rebuild || withoutStore != tc.withoutStore {
				t.Fatalf("retry=%v withoutStore=%v", rebuild, withoutStore)
			}
		})
	}
	if rebuild, _ := responsesRebuildReason(errors.New("transport failed"), true); rebuild {
		t.Fatal("an unknown transport outcome must not trigger a full-history retry")
	}
}

func TestResponsesContinuationRequiresNegotiatedNativeGatewayMode(t *testing.T) {
	for _, tc := range []struct {
		name, catalog, offered, served, acknowledged string
		version                                      int
		store, remember                              bool
	}{
		{"native original", "original", "native", "native", "1", 1, true, true},
		{"native managed preset", "third_party", "native", "native", "1", 1, true, true},
		{"legacy catalog", "original", "native", "native", "", 0, false, false},
		{"unknown contract version", "original", "native", "native", "2", 2, false, false},
		{"compat catalog", "original", "compat", "compat", "", 0, false, false},
		{"selected model uses compatibility", "original", "native", "compat", "", 1, true, false},
		{"missing response acknowledgment", "original", "native", "native", "", 1, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stores := make(chan bool, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request struct{ Store bool }
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
				}
				stores <- request.Store
				w.Header().Set("X-AIGateway-Responses-Mode", tc.served)
				w.Header().Set("X-AIGateway-Responses-Developer-History", tc.acknowledged)
				fmt.Fprint(w, responsesFixture)
			}))
			defer server.Close()
			m := testResponsesModel(t, config.Provider{BaseURL: server.URL, Model: "fixture"})
			source := &AccountSource{catalogGroup: tc.catalog}
			source.SetCapabilities(&SourceCapabilities{Authorized: true, Available: true, Capabilities: map[string]RoleCapability{
				"conversation": {Supported: true, Available: true, Endpoints: []string{"responses"}, ResponsesModes: []string{tc.offered}, ResponsesDeveloperHistory: tc.version},
			}})
			tracked := &recordingModel{inner: m, roleSource: func() *AccountSource { return source }}
			cursor := &ResponsesContinuation{}
			if _, err := WithResponsesContinuation(tracked, cursor).Generate(t.Context(), []*schema.Message{schema.UserMessage("question")}); err != nil {
				t.Fatal(err)
			}
			if store := <-stores; store != tc.store || (cursor.last != nil) != tc.remember {
				t.Fatalf("store=%v saved=%v, want %v/%v", store, cursor.last != nil, tc.store, tc.remember)
			}
		})
	}
}
