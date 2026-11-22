package mcpclient

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LubyRuffy/eino-swarm/internal/config"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func producerServer(t *testing.T) config.MCPServer {
	t.Helper()
	s := mcp.NewServer(&mcp.Implementation{Name: "producer", Version: "test"}, nil)
	s.AddTool(&mcp.Tool{Name: "lookup", InputSchema: map[string]any{"type": "object"}}, func(_ context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var args map[string]any
		_ = json.Unmarshal(request.Params.Arguments, &args)
		raw, _ := json.Marshal(map[string]any{"id": json.Number("9007199254740993"), "echo": args["q"]})
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(raw)}}}, nil
	})
	s.AddTool(&mcp.Tool{Name: "structured", InputSchema: map[string]any{"type": "object"}}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "not json"}}, StructuredContent: map[string]any{"ok": true}}, nil
	})
	s.AddTool(&mcp.Tool{Name: "refuses", InputSchema: map[string]any{"type": "object"}}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "quota"}}}, nil
	})
	h := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s }, nil))
	t.Cleanup(h.Close)
	return config.MCPServer{ID: "producer", Transport: "streamable_http", URL: h.URL, Enabled: true, TimeoutSeconds: 2}
}

// A panel producer needs the exact reply: large organization IDs must survive
// without the float rounding of structured content.
func TestProducerCallKeepsExactTextReplies(t *testing.T) {
	server := producerServer(t)
	m := NewManager()
	defer m.Close()
	body, err := m.CallTool(t.Context(), server, t.TempDir(), "lookup", map[string]any{"q": "subject"})
	if err != nil || string(body) != `{"echo":"subject","id":9007199254740993}` {
		t.Fatalf("body=%s err=%v", body, err)
	}
	body, err = m.CallTool(t.Context(), server, t.TempDir(), "structured", nil)
	if err != nil || string(body) != `{"ok":true}` {
		t.Fatalf("structured body=%s err=%v", body, err)
	}
}

// A refused panel lookup must not block the agent's own task: the failure is
// returned to the producer and never marks the shared execution failed.
func TestProducerToolErrorDoesNotBlockTheTask(t *testing.T) {
	server := producerServer(t)
	m := NewManager()
	defer m.Close()
	state := NewExecution(nil)
	ctx := WithExecution(t.Context(), state)
	for _, name := range []string{"refuses", "missing"} {
		_, err := m.CallTool(ctx, server, t.TempDir(), name, nil)
		var failure *Error
		if !errors.As(err, &failure) {
			t.Fatalf("%s err=%v", name, err)
		}
	}
	if failure := ExecutionFailure(ctx); failure != nil {
		t.Fatalf("panel lookup blocked the task: %+v", failure)
	}
	if _, err := m.CallTool(ctx, server, t.TempDir(), "lookup", map[string]any{"bad": func() {}}); err == nil {
		t.Fatal("unencodable arguments were sent")
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := m.CallTool(cancelled, server, t.TempDir(), "lookup", nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled err=%v", err)
	}
}

// Only one enabled server may own a tool name; two candidates are ambiguous.
func TestServerOfferingNeedsOneEnabledServer(t *testing.T) {
	dir := t.TempDir()
	m := NewManager()
	defer m.Close()
	m.SetCatalogDir(dir)
	one := config.MCPServer{ID: "one", Transport: "stdio", Command: "one", Enabled: true}
	two := config.MCPServer{ID: "two", Transport: "stdio", Command: "two", Enabled: true}
	off := config.MCPServer{ID: "off", Transport: "stdio", Command: "off", Enabled: false}
	for _, server := range []config.MCPServer{one, two, off} {
		if err := SaveCatalog(dir, server, Catalog{ServerID: server.ID, Tools: []ToolDescriptor{{Name: server.ID + "_x", OriginalName: "lookup"}}}); err != nil {
			t.Fatal(err)
		}
	}
	if got, ok := m.ServerOffering([]config.MCPServer{one, off}, "lookup"); !ok || got.ID != "one" {
		t.Fatalf("single owner = %v %v", got.ID, ok)
	}
	if _, ok := m.ServerOffering([]config.MCPServer{one, two}, "lookup"); ok {
		t.Fatal("ambiguous owner was chosen")
	}
	one.DisabledTools = []string{"lookup"}
	if _, ok := m.ServerOffering([]config.MCPServer{one}, "lookup"); ok {
		t.Fatal("switched-off tool was offered")
	}
	if _, ok := m.ServerOffering([]config.MCPServer{two}, "other"); ok {
		t.Fatal("unknown tool was offered")
	}
	if replyBody(nil) != nil || replyBody(&mcp.CallToolResult{}) != nil {
		t.Fatal("empty result produced a body")
	}
}
