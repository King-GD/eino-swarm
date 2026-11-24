package mcpclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LubyRuffy/eino-swarm/internal/config"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The entry switch read can pass while discovery is still in flight. The
// post-connect read must stop execution if the user disables the remote tool.
func TestProducerDisableDuringDiscoveryPreventsToolExecution(t *testing.T) {
	listed, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	var initializes, lists, calls, switchReads atomic.Int32
	remote := mcp.NewServer(&mcp.Implementation{Name: "discovery-admission", Version: "test"}, nil)
	remote.AddTool(&mcp.Tool{Name: "lookup", InputSchema: map[string]any{"type": "object"}}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: `{"ok":true}`}}}, nil
	})
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return remote }, nil)
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			raw, err := io.ReadAll(r.Body)
			if err != nil {
				t.Error(err)
				return
			}
			// Inspect the real RPC, then restore the exact body for the SDK.
			r.Body = io.NopCloser(bytes.NewReader(raw))
			var message struct{ Method string }
			if err := json.Unmarshal(raw, &message); err != nil {
				t.Error(err)
				return
			}
			switch message.Method {
			case "initialize":
				initializes.Add(1)
			case "tools/list":
				if lists.Add(1) == 1 {
					close(listed)
				}
				select {
				case <-release:
				case <-r.Context().Done():
					return
				}
			case "tools/call":
				calls.Add(1)
			}
		}
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(endpoint.Close)
	server := config.MCPServer{ID: "discovery-admission", Transport: "streamable_http", URL: endpoint.URL, Enabled: true, TimeoutSeconds: 3}
	m := NewManager()
	t.Cleanup(m.Close)
	t.Cleanup(unblock)
	var disabled atomic.Bool
	m.UseRemoteSwitches(func() []config.MCPServer {
		switchReads.Add(1)
		current := cloneServer(server)
		if disabled.Load() {
			current.DisabledTools = []string{"lookup"}
		}
		return []config.MCPServer{current}
	})
	ctx, cancel := context.WithCancel(t.Context())
	result := make(chan error, 1)
	finished := make(chan struct{})
	workspace := t.TempDir()
	go func() {
		defer close(finished)
		_, err := m.CallTool(ctx, server, workspace, "lookup", nil)
		result <- err
	}()
	t.Cleanup(func() {
		cancel()
		unblock()
		select {
		case <-finished:
		case <-time.After(5 * time.Second):
			t.Error("the owned producer call did not exit during cleanup")
		}
	})
	select {
	case <-listed:
	case <-time.After(10 * time.Second):
		t.Fatal("the producer did not reach the actual MCP tools/list request")
	}
	if initializes.Load() != 1 || lists.Load() != 1 || switchReads.Load() != 1 {
		t.Fatalf("expected discovery after exactly one entry check: initialize=%d list=%d checks=%d", initializes.Load(), lists.Load(), switchReads.Load())
	}
	disabled.Store(true)
	current := cloneServer(server)
	current.DisabledTools = []string{"lookup"}
	m.Reload([]config.MCPServer{current})
	unblock()
	var err error
	select {
	case err = <-result:
	case <-time.After(10 * time.Second):
		t.Fatal("the producer did not settle after discovery was released")
	}
	if calls.Load() != 0 {
		t.Fatalf("the disabled producer reached tools/call after discovery: calls=%d err=%v", calls.Load(), err)
	}
	var failure *Error
	if !errors.As(err, &failure) || failure.Code != "tool_switched_off" || switchReads.Load() != 2 {
		t.Fatalf("post-discovery admission did not refuse the disabled tool: checks=%d err=%v", switchReads.Load(), err)
	}
}
