package mcpclient

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LubyRuffy/eino-swarm/internal/config"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// A support trace distinguishes the timed-out request from later work on the
// same session without copying task data into the engine log.
func TestMCPDiagnosticsCorrelateTimeoutAndLaterSuccessWithoutSecrets(t *testing.T) {
	const (
		argumentSecret = "private-tool-argument"
		headerSecret   = "private-auth-header"
		endpointSecret = "private-endpoint-parameter"
	)
	var calls atomic.Int32
	var verifiedRequest atomic.Bool
	sessions := make(chan string, 4)
	release := make(chan struct{})
	server := mcp.NewServer(&mcp.Implementation{Name: "diagnostics", Version: "test"}, nil)
	server.AddTool(&mcp.Tool{Name: "lookup", InputSchema: map[string]any{"type": "object"}}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var args struct {
			Request     string
			Independent bool
		}
		verifiedRequest.Store(json.Unmarshal(req.Params.Arguments, &args) == nil && args.Request == argumentSecret)
		calls.Add(1)
		if args.Independent {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "verified"}}}, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-release:
			return &mcp.CallToolResult{}, nil
		}
	})
	var verifiedEndpoint atomic.Bool
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == headerSecret && r.URL.Query().Get("key") == endpointSecret {
			verifiedEndpoint.Store(true)
		}
		if r.Method == http.MethodPost {
			var request struct{ Method string }
			var raw bytes.Buffer
			if err := json.NewDecoder(io.TeeReader(r.Body, &raw)).Decode(&request); err != nil {
				t.Errorf("decode MCP request: %v", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			r.Body = io.NopCloser(io.MultiReader(&raw, r.Body))
			if request.Method == "tools/call" {
				sessions <- r.Header.Get("Mcp-Session-Id")
			}
		}
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(httpServer.Close)
	var output bytes.Buffer
	manager := NewManager(slog.New(slog.NewJSONHandler(&output, nil)))
	t.Cleanup(manager.Close)
	t.Cleanup(func() { close(release) })

	ctx, cancel := context.WithTimeout(t.Context(), 6*time.Second)
	defer cancel()
	configured := config.MCPServer{
		ID: "diagnostics", Transport: "streamable_http", Enabled: true, TimeoutSeconds: 1,
		URL: httpServer.URL + "?key=" + endpointSecret, Headers: map[string]string{"Authorization": headerSecret},
	}
	tools, err := manager.Tools(ctx, []config.MCPServer{configured}, t.TempDir(), 10000)
	if err != nil || len(tools) != 2 {
		t.Fatalf("tools = %d, %v", len(tools), err)
	}
	node, err := compose.NewToolNode(ctx, &compose.ToolsNodeConfig{Tools: tools})
	if err != nil {
		t.Fatal(err)
	}
	proxy := toolName(configured.ID, "call_tool")
	result := invokeDiagnosticTool(t, ctx, node, "call-timeout", proxy, `{"name":"lookup","arguments":{"request":"`+argumentSecret+`"}}`)
	if !strings.Contains(result, `"code":"timeout"`) || !strings.Contains(result, `"outcome":"unknown"`) {
		t.Errorf("missing timeout and unknown outcome: %q", result)
	}
	next := invokeDiagnosticTool(t, ctx, node, "call-verify", proxy, `{"name":"lookup","arguments":{"request":"`+argumentSecret+`","independent":true}}`)
	if !strings.Contains(next, "verified") {
		t.Errorf("session did not serve the next call: %q", next)
	}
	if calls.Load() != 2 || !verifiedRequest.Load() || !verifiedEndpoint.Load() || len(sessions) != 2 {
		t.Fatalf("wire calls = %d, argument delivered = %v, endpoint/header delivered = %v", calls.Load(), verifiedRequest.Load(), verifiedEndpoint.Load())
	}
	if first, second := <-sessions, <-sessions; first == "" || first != second {
		t.Fatal("the two calls did not use the same MCP session")
	}
	outcomes := readDiagnosticOutcomes(t, output.String(), argumentSecret, headerSecret, endpointSecret, httpServer.URL)
	for callID, code := range map[string]string{"call-timeout": "timeout", "call-verify": "ok"} {
		record := outcomes[callID]
		if record.ServerID != configured.ID || record.Transport != configured.Transport || record.Code != code {
			t.Errorf("call %s: missing correlated %s diagnostic, got %+v", callID, code, record)
		}
	}
	timedOut := outcomes["call-timeout"]
	if timedOut.Tool != "lookup" || timedOut.TimeoutMS != 1000 || timedOut.ElapsedMS <= 0 || timedOut.Phase != "call" {
		t.Errorf("missing measured tool timeout: %+v", timedOut)
	}
}

func TestMCPDiagnosticsClassifyActualCallOutcomesAndKeepCatalogFailureQuiet(t *testing.T) {
	const (
		resultBody     = "private-tool-result"
		remoteError    = "private-remote-tool-failure"
		httpFailure    = "private-http-failure"
		unverifiedTool = "private-unverified-tool-name"
		inputValue     = "private-input-value"
	)
	server := mcp.NewServer(&mcp.Implementation{Name: "diagnostics", Version: "test"}, nil)
	for name, body := range map[string]string{"lookup": resultBody, "refuse": remoteError} {
		server.AddTool(&mcp.Tool{Name: name, InputSchema: map[string]any{"type": "object"}}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{IsError: name == "refuse", Content: []mcp.Content{&mcp.TextContent{Text: body}}}, nil
		})
	}
	server.AddTool(&mcp.Tool{Name: "update", InputSchema: map[string]any{"type": "object", "required": []string{"record_id"}}},
		func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			t.Error("missing required arguments reached remote execution")
			return &mcp.CallToolResult{}, nil
		})
	httpServer := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil))
	t.Cleanup(httpServer.Close)
	deniedServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, httpFailure, http.StatusUnauthorized)
	}))
	t.Cleanup(deniedServer.Close)
	var output bytes.Buffer
	manager := NewManager(slog.New(slog.NewJSONHandler(&output, nil)))
	t.Cleanup(manager.Close)
	ctx, cancel := context.WithTimeout(t.Context(), 6*time.Second)
	defer cancel()
	servers := []config.MCPServer{
		{ID: "healthy", Transport: "streamable_http", Enabled: true, TimeoutSeconds: 1, URL: httpServer.URL},
		{ID: "denied", Transport: "streamable_http", Enabled: true, TimeoutSeconds: 1, URL: deniedServer.URL},
		{ID: "catalog", Transport: "streamable_http", Enabled: true, TimeoutSeconds: 1, URL: deniedServer.URL},
	}
	tools, err := manager.Tools(ctx, servers, t.TempDir(), 10000)
	if err != nil {
		t.Fatal(err)
	}
	node, err := compose.NewToolNode(ctx, &compose.ToolsNodeConfig{Tools: tools})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		id, server, proxy, arguments, want string
	}{
		{"ok-call", "healthy", "call_tool", `{"name":"lookup","arguments":{}}`, resultBody},
		{"missing-argument", "healthy", "call_tool", `{"name":"update","arguments":{"value":"` + inputValue + `"}}`, "record_id"},
		{"remote-error", "healthy", "call_tool", `{"name":"refuse","arguments":{}}`, `"isError":true`},
		{"bad-connection", "denied", "call_tool", `{"name":"` + unverifiedTool + `","arguments":{}}`, `"code":"connection_failed"`},
		{"catalog-check", "catalog", "list_tools", `{}`, `"code":"connection_failed"`},
	} {
		body := invokeDiagnosticTool(t, ctx, node, tc.id, toolName(tc.server, tc.proxy), tc.arguments)
		if !strings.Contains(body, tc.want) || tc.id == "remote-error" && !strings.Contains(body, remoteError) {
			t.Errorf("%s got %q, want %q", tc.id, body, tc.want)
		}
	}
	outcomes := readDiagnosticOutcomes(t, output.String(), resultBody, remoteError, httpFailure, unverifiedTool, inputValue, "record_id", httpServer.URL, deniedServer.URL)
	if len(outcomes) != 4 {
		t.Errorf("only tool invocations should be logged, got %+v", outcomes)
	}
	for _, want := range []diagnosticOutcome{
		{CallID: "ok-call", ServerID: "healthy", Phase: "call", Code: "ok", Tool: "lookup", Level: "INFO"},
		{CallID: "missing-argument", ServerID: "healthy", Phase: "validate", Code: "missing_required_arguments", Tool: "update", Level: "WARN"},
		{CallID: "remote-error", ServerID: "healthy", Phase: "call", Code: "tool_error", Tool: "refuse", Level: "WARN"},
		{CallID: "bad-connection", ServerID: "denied", Phase: "connect", Code: "connection_failed", Level: "WARN"},
	} {
		got := outcomes[want.CallID]
		if got.ServerID != want.ServerID || got.Phase != want.Phase || got.Code != want.Code || got.Tool != want.Tool || got.Level != want.Level {
			t.Errorf("call %s: want %+v, got %+v", want.CallID, want, got)
		}
	}
}

type diagnosticOutcome struct {
	Message   string `json:"msg"`
	Level     string `json:"level"`
	CallID    string `json:"tool_call_id"`
	ServerID  string `json:"server_id"`
	Transport string `json:"transport"`
	Phase     string `json:"phase"`
	Code      string `json:"code"`
	Tool      string `json:"tool"`
	TimeoutMS int64  `json:"timeout_ms"`
	ElapsedMS int64  `json:"elapsed_ms"`
}

func readDiagnosticOutcomes(t *testing.T, logged string, secrets ...string) map[string]diagnosticOutcome {
	t.Helper()
	for _, secret := range secrets {
		if strings.Contains(logged, secret) {
			t.Fatal("MCP diagnostics disclosed a transport or request secret")
		}
	}
	outcomes := make(map[string]diagnosticOutcome)
	for _, line := range strings.Split(strings.TrimSpace(logged), "\n") {
		if line == "" {
			continue
		}
		var record diagnosticOutcome
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatal(err)
		}
		if record.Message == "MCP tool call" {
			if _, duplicate := outcomes[record.CallID]; duplicate {
				t.Errorf("duplicate diagnostic for call %s", record.CallID)
			}
			outcomes[record.CallID] = record
		}
	}
	return outcomes
}

func invokeDiagnosticTool(t *testing.T, ctx context.Context, node *compose.ToolsNode, callID, tool, arguments string) string {
	t.Helper()
	messages, err := node.Invoke(ctx, schema.AssistantMessage("", []schema.ToolCall{{
		ID: callID, Type: "function", Function: schema.FunctionCall{Name: tool, Arguments: arguments},
	}}))
	if err != nil || len(messages) != 1 {
		t.Fatalf("call %s: messages = %d, %v", callID, len(messages), err)
	}
	return messages[0].Content
}
