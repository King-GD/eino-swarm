package mcpclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LubyRuffy/eino-swarm/internal/config"
	"github.com/cloudwego/eino/components/tool"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestExpiredBindingRefreshesSavedHeadersWithoutRedirecting(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "key fixture", Version: "test"}, nil)
	server.AddTool(&mcp.Tool{Name: "lookup", InputSchema: map[string]any{"type": "object"}}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "key accepted"}}}, nil
	})
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	var mu sync.Mutex
	var credentials []string
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			mu.Lock()
			credentials = append(credentials, r.Header.Get("Authorization"))
			mu.Unlock()
		}
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(remote.Close)

	m := NewManager()
	t.Cleanup(m.Close)
	current := config.MCPServer{ID: "current", URL: remote.URL, Enabled: true, Transport: "streamable_http", Headers: map[string]string{"Authorization": "old-test-key"}}
	m.Reload([]config.MCPServer{current})
	set, err := m.Tools(t.Context(), []config.MCPServer{current}, t.TempDir(), 10000)
	if err != nil {
		t.Fatal(err)
	}
	args := `{"name":"lookup","arguments":{}}`
	remoteCall := set[1].(tool.InvokableTool)
	checkCall := func(ctx context.Context) {
		t.Helper()
		out, err := remoteCall.InvokableRun(ctx, args)
		if err != nil || !strings.Contains(out, "key accepted") || ExecutionFailure(ctx) != nil {
			t.Fatalf("tool did not succeed: %s, %v", out, err)
		}
	}
	ctx := WithExecution(t.Context(), NewExecution(nil))
	checkCall(ctx)
	mu.Lock()
	firstCount := len(credentials)
	mu.Unlock()
	current.Headers = map[string]string{"Authorization": "refreshed-test-key"}
	m.Reload([]config.MCPServer{current})
	checkCall(ctx)
	mu.Lock()
	defer mu.Unlock()
	if firstCount == 0 || len(credentials) <= firstCount {
		t.Fatal("no observable request on the renewed binding")
	}
	for _, header := range credentials[firstCount:] {
		if header != "refreshed-test-key" {
			t.Error("a new POST used anything other than the refreshed header")
		}
	}
	// Changing the configured endpoint does not grant the old call access to it.
	current.URL += "/different"
	m.Reload([]config.MCPServer{current})
	out, err := remoteCall.InvokableRun(ctx, args)
	if err != nil || !strings.Contains(out, "session_expired") {
		t.Fatalf("retargeted result: %s, %v", out, err)
	}
	if failure := ExecutionFailure(ctx); failure == nil || failure.Code != "session_expired" {
		t.Fatalf("the failed renewal did not block the task: %v", failure)
	}
}

func TestMCPFailureBlocksAdmissionBeforeCancellingTheManager(t *testing.T) {
	writing, release, settled := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	ctx := WithExecution(t.Context(), NewExecution(func(context.Context, *ExecutionError) {
		close(writing)
		<-release
	}))
	manager, stopManager := WatchExecution(ctx)
	defer stopManager()
	worker := InheritExecution(ctx, t.Context())
	go func() {
		defer close(settled)
		markExecutionFailed(worker, "timeout", "remote")
	}()
	select {
	case <-writing:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not begin recording the failure")
	}
	failure := ExecutionFailure(manager)
	if failure == nil || failure.Code != "timeout" || failure.ServerID != "remote" {
		t.Fatalf("next work was not blocked by the original failure: %+v", failure)
	}
	if manager.Err() != nil {
		t.Fatal("manager cancelled before the receipt attempt completed")
	}
	markExecutionFailed(manager, "closed", "another")
	if ExecutionFailure(manager) != failure {
		t.Fatal("a competing failure replaced the original receipt")
	}
	if !strings.Contains(failure.Error(), "Verify the remote operation's outcome") {
		t.Fatal("a timeout omitted the observed-outcome guidance")
	}
	stopped, stop := context.WithCancel(ctx)
	stop()
	if got := AwaitExecutionFailure(stopped); got != nil {
		t.Fatal("human Stop was delayed by an unfinished receipt")
	}
	once.Do(func() { close(release) })
	select {
	case <-manager.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the active manager did not receive cancellation")
	}
	if context.Cause(manager) != failure || AwaitExecutionFailure(ctx) != failure {
		t.Fatal("manager cancellation lost the original failure")
	}
	<-settled
}

func TestUntrackedMCPWorkKeepsItsOriginalContext(t *testing.T) {
	ctx := t.Context()
	watched, stop := WatchExecution(ctx)
	defer stop()
	if watched != ctx || InheritExecution(ctx, ctx) != ctx || AwaitExecutionFailure(ctx) != nil {
		t.Fatal("a standalone tool acquired a task failure scope")
	}
	tracked := WithExecution(ctx, NewExecution(nil))
	if AwaitExecutionFailure(tracked) != nil {
		t.Fatal("a fresh task reported a failure")
	}
	markExecutionFailed(tracked, "cancelled", "remote")
	if ExecutionFailure(tracked) != nil {
		t.Fatal("caller cancellation appeared as an MCP failure")
	}
}
