package mcpclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LubyRuffy/eino-swarm/internal/config"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestToolResultObserverOnlyReceivesLiveSuccessfulResults(t *testing.T) {
	manager := NewManager()
	t.Cleanup(manager.Close)
	result := &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "observed"}}}
	calls := 0
	manager.ObserveToolCalls(func(_ context.Context, call ToolCall) func(*mcp.CallToolResult) {
		if call.Name != "lookup" || call.ServerID != "source" || string(call.Arguments) != `{"key":"observed"}` {
			t.Fatalf("unexpected invocation %+v", call)
		}
		return func(got *mcp.CallToolResult) {
			calls++
			if got != result {
				t.Fatalf("unexpected observation: %v", got)
			}
		}
	})
	arguments := map[string]json.RawMessage{"key": json.RawMessage(`"observed"`)}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if manager.startToolCall(cancelled, "source", "lookup", arguments) != nil {
		t.Fatal("an already cancelled call acquired an observer")
	}
	complete := manager.startToolCall(t.Context(), "source", "lookup", arguments)
	complete(nil)
	complete(&mcp.CallToolResult{IsError: true})
	complete(result)
	live, stop := context.WithCancel(t.Context())
	late := manager.startToolCall(live, "source", "lookup", arguments)
	stop()
	late(result)
	manager.ObserveToolCalls(nil)
	if manager.startToolCall(t.Context(), "source", "lookup", arguments) != nil {
		t.Fatal("a detached observer received another call")
	}
	manager.ObserveToolCalls(func(context.Context, ToolCall) func(*mcp.CallToolResult) { return nil })
	if manager.startToolCall(t.Context(), "source", "lookup", arguments) != nil {
		t.Fatal("an ignored invocation acquired a completion callback")
	}
	if calls != 1 {
		t.Fatalf("got %d observations, want one", calls)
	}
}

// The real executor supplies call identity, while the configured adapter
// supplies server/name/arguments. Parallel remote completions retain that binding.
func TestToolCallObserversKeepTheirOriginalIdentityAcrossParallelRemoteResults(t *testing.T) {
	for _, mode := range []string{"direct", "progressive"} {
		t.Run(mode, func(t *testing.T) {
			var mu sync.Mutex
			starts := make(map[string]bool)
			started := make(chan string, 2)
			releases := map[string]chan struct{}{"first": make(chan struct{}), "second": make(chan struct{})}
			var firstOnce, secondOnce sync.Once
			release := func(key string) {
				if key == "first" {
					firstOnce.Do(func() { close(releases[key]) })
				} else {
					secondOnce.Do(func() { close(releases[key]) })
				}
			}
			var workers sync.WaitGroup
			defer func() {
				release("first")
				release("second")
				workers.Wait()
			}()
			remote := mcp.NewServer(&mcp.Implementation{Name: "observer-fixture", Version: "test"}, nil)
			remote.AddTool(&mcp.Tool{Name: "lookup", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"key": map[string]any{"type": "string"}}}}, func(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				var arguments struct{ Key string }
				if err := json.Unmarshal(request.Params.Arguments, &arguments); err != nil || releases[arguments.Key] == nil {
					return nil, fmt.Errorf("the observer changed the remote arguments")
				}
				mu.Lock()
				observed := starts[arguments.Key]
				mu.Unlock()
				if !observed {
					t.Error("remote dispatch preceded its observer binding")
				}
				started <- arguments.Key
				select {
				case <-releases[arguments.Key]:
					return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: arguments.Key + strings.Repeat(" complete remote evidence", 40)}}}, nil
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			})
			endpoint := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return remote }, nil))
			t.Cleanup(endpoint.Close)
			server := config.MCPServer{ID: "actual-source", Transport: "streamable_http", URL: endpoint.URL, Enabled: true, TimeoutSeconds: 5}
			manager := NewManager()
			t.Cleanup(manager.Close)
			var tools []tool.BaseTool
			name := DirectToolName(server.ID, "lookup")
			if mode == "direct" {
				tools, _ = manager.DirectTools(server, t.TempDir(), 128, []ToolDescriptor{{Name: "lookup", OriginalName: "lookup", InputSchema: map[string]any{"type": "object"}}})
			} else {
				var err error
				tools, err = manager.Tools(t.Context(), []config.MCPServer{server}, t.TempDir(), 128)
				if err != nil {
					t.Fatal(err)
				}
				_, name = ProxyNames(server.ID)
			}
			node, err := compose.NewToolNode(t.Context(), &compose.ToolsNodeConfig{Tools: tools})
			if err != nil {
				t.Fatal(err)
			}
			type observation struct {
				key, callID string
				observer    int
			}
			observations := make(chan observation, 2)
			observer := func(version int) func(context.Context, ToolCall) func(*mcp.CallToolResult) {
				return func(_ context.Context, call ToolCall) func(*mcp.CallToolResult) {
					var arguments struct{ Key string }
					if err := json.Unmarshal(call.Arguments, &arguments); err != nil || call.ServerID != server.ID || call.Name != "lookup" || call.CallID != "call-"+arguments.Key {
						t.Errorf("trusted invocation was replaced by model fields: %+v error=%v", call, err)
					}
					mu.Lock()
					starts[arguments.Key] = true
					mu.Unlock()
					for i := range call.Arguments {
						call.Arguments[i] = 'x'
					}
					return func(result *mcp.CallToolResult) {
						if len(result.Content) != 1 || result.Content[0].(*mcp.TextContent).Text != arguments.Key+strings.Repeat(" complete remote evidence", 40) {
							t.Error("the result was capped before observer completion")
						}
						observations <- observation{arguments.Key, call.CallID, version}
					}
				}
			}
			type outcome struct {
				messages []*schema.Message
				err      error
			}
			outcomes := make(chan outcome, 2)
			invoke := func(key string) {
				workers.Add(1)
				go func() {
					defer workers.Done()
					arguments := `{"key":"` + key + `","server_id":"forged-source","name":"forged-tool"}`
					if mode == "progressive" {
						arguments = `{"name":"lookup","arguments":` + arguments + `}`
					}
					messages, err := node.Invoke(t.Context(), &schema.Message{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{ID: "call-" + key, Type: "function", Function: schema.FunctionCall{Name: name, Arguments: arguments}}}})
					outcomes <- outcome{messages, err}
				}()
				select {
				case got := <-started:
					if got != key {
						t.Fatalf("remote dispatch=%q, want %q", got, key)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("the bound remote call did not start")
				}
			}
			manager.ObserveToolCalls(observer(1))
			invoke("first")
			manager.ObserveToolCalls(observer(2))
			invoke("second")
			for _, expected := range []observation{{"second", "call-second", 2}, {"first", "call-first", 1}} {
				release(expected.key)
				select {
				case got := <-observations:
					if got != expected {
						t.Fatalf("late result changed its observer binding: got=%+v want=%+v", got, expected)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("the bound completion did not arrive")
				}
				got := <-outcomes
				var capped struct{ Truncated bool }
				if got.err != nil || len(got.messages) != 1 || json.Unmarshal([]byte(got.messages[0].Content), &capped) != nil || !capped.Truncated {
					t.Fatalf("normal model-facing result cap changed: messages=%v error=%v", got.messages, got.err)
				}
			}
		})
	}
}
