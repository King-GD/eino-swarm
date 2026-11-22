package engine

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LubyRuffy/eino-swarm/internal/config"
	"github.com/LubyRuffy/eino-swarm/internal/mcpclient"
	"github.com/LubyRuffy/eino-swarm/internal/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMCPMissingRequiredArgumentCanBeCorrectedInTheSameTurn(t *testing.T) {
	for _, mode := range []string{"direct", "proxy"} {
		t.Run(mode, func(t *testing.T) {
			var rejected, executed atomic.Int32
			releaseSibling := make(chan struct{})
			release := sync.OnceFunc(func() { close(releaseSibling) })
			defer release()
			server := mcp.NewServer(&mcp.Implementation{Name: "validation fixture", Version: "test"}, nil)
			server.AddTool(&mcp.Tool{Name: "lookup", InputSchema: map[string]any{
				"type": "object", "required": []string{"record_key"},
				"properties": map[string]any{"record_key": map[string]any{"type": "string", "minLength": 1}},
			}}, func(_ context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				var args struct {
					Key string `json:"record_key"`
				}
				if err := json.Unmarshal(req.Params.Arguments, &args); err != nil || args.Key == "" {
					rejected.Add(1)
					return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{
						&mcp.TextContent{Text: "Missing required parameter: record_key"},
					}}, nil
				}
				executed.Add(1)
				return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "record found"}}}, nil
			})
			server.AddTool(&mcp.Tool{Name: "check", InputSchema: map[string]any{"type": "object"}},
				func(ctx context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
					// The independent operation finishes after the missing-argument result.
					select {
					case <-ctx.Done():
						return nil, ctx.Err()
					case <-releaseSibling:
						return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "independent check finished"}}}, nil
					}
				})
			remote := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil))
			t.Cleanup(remote.Close)

			// These opaque arguments travel through the actual model HTTP adapter,
			// Registry and ADK before reaching either MCP mounting path.
			cfg := config.MCPServer{ID: "schema-fixture", Enabled: true, Transport: "streamable_http", URL: remote.URL, TimeoutSeconds: 10}
			_, proxy := mcpclient.ProxyNames(cfg.ID)
			lookup, sibling := proxy, proxy
			missing, corrected, other := `{"name":"lookup","arguments":{}}`, `{"name":"lookup","arguments":{"record_key":"fixture"}}`, `{"name":"check","arguments":{}}`
			if mode == "direct" {
				lookup, sibling = mcpclient.DirectToolName(cfg.ID, "lookup"), mcpclient.DirectToolName(cfg.ID, "check")
				missing, corrected, other = `{}`, `{"record_key":"fixture"}`, `{}`
			}
			e, requests := newScheduleCompletionHTTPEngine(t, func(w http.ResponseWriter, _ *http.Request, step int, request scheduleCompletionRequest) {
				switch step {
				case 1:
					writeContextFlowReply(w, request.Stream, map[string]any{"tool_calls": []any{
						map[string]any{"index": 0, "id": "missing-arg", "type": "function", "function": map[string]any{"name": lookup, "arguments": missing}},
						map[string]any{"index": 1, "id": "independent-check", "type": "function", "function": map[string]any{"name": sibling, "arguments": other}},
					}}, "tool_calls")
				case 2:
					if got := nuwaValidationReceipt(request, "missing-arg"); !strings.Contains(got, "record_key") || !strings.Contains(got, `"isError":true`) {
						t.Errorf("model did not receive the field error on the original call: %q", got)
					}
					if got := nuwaValidationReceipt(request, "independent-check"); !strings.Contains(got, "independent check finished") {
						t.Errorf("model did not receive the independent result: %q", got)
					}
					nuwaValidationTool(w, request, "corrected-call", lookup, corrected)
				default:
					if got := nuwaValidationReceipt(request, "corrected-call"); !strings.Contains(got, "record found") {
						t.Errorf("corrected call was not delivered to the model: %q", got)
					}
					writeContextFlowReply(w, request.Stream, map[string]any{"content": "Record verified."}, "stop")
				}
			})
			e.cfg.Memory.Enabled = false
			e.cfg.Swarm.ComposerPredictions = false
			e.cfg.MCP.DirectLoadPercent = 0
			e.cfg.MCP.Servers = []config.MCPServer{cfg}
			if mode == "direct" {
				e.cfg.MCP.DirectLoadPercent = 100
			}
			thread, err := e.CreateThread("MCP input validation", "", "")
			if err != nil {
				t.Fatal(err)
			}
			sub := e.Subscribe(thread.ID)
			defer sub.Close()
			turn, err := e.StartTurn(thread.ID, "Check the remote records.")
			if err != nil {
				t.Fatal(err)
			}
			timer := time.NewTimer(8 * time.Second)
			defer timer.Stop()
		waitForRejection:
			for {
				select {
				case event, ok := <-sub.C:
					if !ok {
						t.Fatal("subscription closed before the original call settled")
					}
					if event.Kind == "tool_result" && event.ToolCallID == "missing-arg" {
						release()
						break waitForRejection
					}
					if event.Kind == "error" {
						release()
						break waitForRejection
					}
				case <-timer.C:
					t.Fatal("the original call did not publish a result or fail the turn")
				}
			}
			finished := waitForTurn(t, e, turn.ID)
			waitSettled(t, e, thread.ID)
			if finished.Status != store.TurnDone || len(requests.snapshot()) != 3 || rejected.Load() != 0 || executed.Load() != 1 {
				t.Fatalf("turn=%s, model requests=%d, invalid remote calls=%d, executions=%d, error=%s",
					finished.Status, len(requests.snapshot()), rejected.Load(), executed.Load(), finished.Error)
			}
			if failure := e.restoredMCPFailure(turn.ID, nil); failure != nil {
				t.Fatalf("recovering the turn would reinstate a block: %v", failure)
			}
		})
	}
}
