package provider

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/LubyRuffy/eino-swarm/internal/config"
	"github.com/cloudwego/eino/schema"
)

func TestSingleLeadingSystemLeavesANormalTranscriptUntouched(t *testing.T) {
	in := []*schema.Message{schema.SystemMessage("coordinate"), nil, schema.UserMessage("do the work")}
	if got := singleLeadingSystem(in); len(got) != len(in) || got[0] != in[0] || got[1] != nil || got[2] != in[2] {
		t.Fatalf("one leading system must keep the caller's slice: %+v", got)
	}
	image := &schema.Message{Role: schema.System, UserInputMultiContent: []schema.MessageInputPart{{Type: schema.ChatMessagePartTypeImageURL, Image: &schema.MessageInputImage{}}}}
	alone := []*schema.Message{image, schema.UserMessage("look")}
	if got := singleLeadingSystem(alone); got[0] != image {
		t.Fatal("a single non-text system at the start is already acceptable")
	}
	leadingNil := []*schema.Message{nil, schema.SystemMessage("coordinate"), schema.SystemMessage("worker roster")}
	got := singleLeadingSystem(leadingNil)
	if len(got) != 2 || got[0].Role != schema.System || got[0].Content != "coordinate\n\nworker roster" || got[1] != nil {
		t.Fatalf("a blank slot pushed the system message off the start: %+v", got)
	}
}

func TestSingleLeadingSystemJoinsPrefixAndKeepsLaterNoticeInPlace(t *testing.T) {
	first := schema.SystemMessage("coordinate\n")
	roster := schema.SystemMessage("  worker roster  ")
	blank := schema.SystemMessage(" \n")
	human := schema.UserMessage("do the work")
	call := schema.AssistantMessage("checking", []schema.ToolCall{{ID: "call-1", Function: schema.FunctionCall{Name: "read", Arguments: "{}"}}})
	result := schema.ToolMessage("file text", "call-1")
	notice := schema.SystemMessage("A peer task message arrived.")
	image := &schema.Message{
		Role:    schema.System,
		Content: "pixels",
		UserInputMultiContent: []schema.MessageInputPart{
			{Type: schema.ChatMessagePartTypeImageURL, Image: &schema.MessageInputImage{}},
		},
	}
	in := []*schema.Message{first, roster, blank, nil, human, call, result, notice, image}
	got := singleLeadingSystem(in)

	if first.Content != "coordinate\n" || roster.Role != schema.System || notice.Role != schema.System || image.Role != schema.System {
		t.Fatal("collapsing a request mutated the stored messages")
	}
	if len(got) != 7 || got[0].Role != schema.System || got[0] == first {
		t.Fatalf("leading systems were not one new message: %+v", got)
	}
	if got[0].Content != "coordinate\n\nworker roster" {
		t.Fatalf("leading sections = %q", got[0].Content)
	}
	if got[1] != nil || got[2] != human || got[3] != call || got[4] != result {
		t.Fatal("conversation order or tool pairing changed")
	}
	if got[5].Role != schema.User || got[5].Content != notice.Content || got[5] == notice {
		t.Fatalf("notice after tools = %+v", got[5])
	}
	if got[6].Role != schema.User || got[6].Content != "pixels" || len(got[6].UserInputMultiContent) != 1 || got[6] == image {
		t.Fatalf("non-text system after the conversation = %+v", got[6])
	}
}

func TestSingleLeadingSystemKeepsOneNonTextSystemAtTheStart(t *testing.T) {
	image := &schema.Message{Role: schema.System, Content: "pixels", UserInputMultiContent: []schema.MessageInputPart{{Type: schema.ChatMessagePartTypeImageURL, Image: &schema.MessageInputImage{}}}}
	later := schema.SystemMessage("follow-up instruction")
	got := singleLeadingSystem([]*schema.Message{image, later, schema.UserMessage("go")})
	if len(got) != 3 || got[0] != image || got[1].Role != schema.User || got[1].Content != later.Content || got[2].Role != schema.User {
		t.Fatalf("non-text system must stay first: %+v", got)
	}
	if later.Role != schema.System {
		t.Fatal("stored follow-up changed role")
	}
}

func TestProviderMessagesKeepsACaptionWhenJoiningANonTextSystem(t *testing.T) {
	got := providerMessages([]*schema.Message{
		schema.SystemMessage("coordinate"),
		{
			Role:    schema.System,
			Content: "look",
			UserInputMultiContent: []schema.MessageInputPart{
				{Type: schema.ChatMessagePartTypeImageURL, Image: &schema.MessageInputImage{}},
			},
		},
	})
	if len(got) != 2 || got[0].Role != schema.System || got[0].Content != "coordinate" || got[1].Role != schema.User || got[1].Content != "" {
		t.Fatalf("joined request = %+v", got)
	}
	if len(got[1].UserInputMultiContent) != 2 || got[1].UserInputMultiContent[0].Text != "look" {
		t.Fatalf("caption dropped: %+v", got[1].UserInputMultiContent)
	}
}

func TestChatModelSendsOneLeadingSystemOnGenerateAndStream(t *testing.T) {
	var mu sync.Mutex
	var bodies [][]wireRole
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("path %s", r.URL.Path)
		}
		var request struct {
			Stream   bool       `json:"stream"`
			Messages []wireRole `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			http.Error(w, "bad", http.StatusBadRequest)
			return
		}
		mu.Lock()
		bodies = append(bodies, request.Messages)
		mu.Unlock()
		if request.Stream {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, backoffChunk+"data: [DONE]\n\n")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, backoffCompletion)
	}))
	defer srv.Close()
	inner, err := buildOpenAI(t.Context(), config.Provider{ID: "fixture", BaseURL: srv.URL + "/v1", Model: "fixture", APIKey: "k"})
	if err != nil {
		t.Fatal(err)
	}
	input := []*schema.Message{
		schema.SystemMessage("coordinate"),
		schema.SystemMessage(`{"source":"runtime_worker_roster"}`),
		schema.UserMessage("do the work"),
		schema.AssistantMessage("", []schema.ToolCall{{ID: "call-1", Function: schema.FunctionCall{Name: "read", Arguments: "{}"}}}),
		schema.ToolMessage("file text", "call-1"),
		schema.SystemMessage("A peer task message arrived."),
	}
	if _, err = inner.Generate(t.Context(), input); err != nil {
		t.Fatal(err)
	}
	stream, err := inner.Stream(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	for {
		_, err = stream.Recv()
		if err != nil {
			break
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 2 {
		t.Fatalf("requests = %d", len(bodies))
	}
	for i, messages := range bodies {
		assertOneLeadingSystem(t, i, messages)
	}
	if input[1].Role != schema.System || input[5].Role != schema.System {
		t.Fatal("the chat request rewrote stored roles")
	}
}

func TestResponsesSendsOneLeadingSystem(t *testing.T) {
	var messages []wireRole
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Input []struct {
				Role    string `json:"role"`
				Content []struct {
					Text string `json:"text"`
				} `json:"content"`
			} `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		for _, item := range body.Input {
			if item.Role == "" {
				continue
			}
			var text strings.Builder
			for _, part := range item.Content {
				text.WriteString(part.Text)
			}
			messages = append(messages, wireRole{Role: item.Role, Content: text.String()})
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ready"}]}]}`)
	}))
	defer srv.Close()
	m := testResponsesModel(t, config.Provider{BaseURL: srv.URL, Model: "fixture", APIKey: "k"})
	_, err := m.Generate(t.Context(), []*schema.Message{
		schema.SystemMessage("coordinate"),
		schema.SystemMessage("worker roster"),
		schema.UserMessage("do the work"),
		schema.SystemMessage("A peer task message arrived."),
	})
	if err != nil {
		t.Fatal(err)
	}
	assertOneLeadingSystem(t, 0, messages)
}

type wireRole struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func assertOneLeadingSystem(t *testing.T, n int, messages []wireRole) {
	t.Helper()
	if len(messages) < 3 || messages[0].Role != "system" || !strings.Contains(messages[0].Content, "coordinate") || !strings.Contains(messages[0].Content, "roster") {
		t.Errorf("request %d system = %+v", n, messages)
	}
	systems := 0
	for _, message := range messages {
		if message.Role == "system" {
			systems++
		}
	}
	if systems != 1 {
		t.Errorf("request %d has %d system messages: %+v", n, systems, messages)
	}
	last := messages[len(messages)-1]
	if last.Role != "user" || last.Content != "A peer task message arrived." {
		t.Errorf("request %d moved the later notice: %+v", n, messages)
	}
}
