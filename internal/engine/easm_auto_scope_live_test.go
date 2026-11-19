package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LubyRuffy/eino-swarm/internal/config"
	"github.com/LubyRuffy/eino-swarm/internal/mcpclient"
	"github.com/LubyRuffy/eino-swarm/internal/store"
	"github.com/LubyRuffy/eino-swarm/internal/taskpanel"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The collection request itself establishes its range. The model never selects
// a query through another tool or republishes any returned metric in this case.
func TestEASMMetricRequestsSelectTheirRangeAndDrawWithoutModelSetup(t *testing.T) {
	for _, mode := range []string{"direct", "progressive"} {
		t.Run(mode, func(t *testing.T) {
			const queryA, queryB = `domain=="range-a.example.test"`, `domain=="range-b.example.test"`
			started := make(chan string, 8)
			releases, once := make(map[string]chan struct{}), make(map[string]*sync.Once)
			for _, key := range []string{"summary", "heat", "components", "slow", "late-a", "late-b", "final"} {
				releases[key], once[key] = make(chan struct{}), new(sync.Once)
			}
			release := func(key string) { once[key].Do(func() { close(releases[key]) }) }
			defer func() {
				for key := range releases {
					release(key)
				}
			}()
			wait := func(ctx context.Context, key string) error {
				started <- key
				select {
				case <-releases[key]:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			var callsA atomic.Int32
			remote := mcp.NewServer(&mcp.Implementation{Name: "automatic-range-fixture", Version: "test"}, nil)
			remote.AddTool(&mcp.Tool{Name: "mcp_fofa_asset_heat", InputSchema: map[string]any{"type": "object"}}, func(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				var args struct{ Query string }
				_ = json.Unmarshal(request.Params.Arguments, &args)
				key, value := "late-b", 22
				if args.Query == queryA {
					key, value = "late-a", 21
					if callsA.Add(1) == 1 {
						key, value = "heat", 11
					}
				} else if args.Query != queryB {
					t.Errorf("unexpected heat range: %s", request.Params.Arguments)
				}
				if err := wait(ctx, key); err != nil {
					return nil, err
				}
				return easmMetricResult(fmt.Sprintf(`{"hot_trend":[{"date":"2026-01-01","value":%d}]}`, value)), nil
			})
			remote.AddTool(&mcp.Tool{Name: "mcp_fofa_stats", InputSchema: map[string]any{"type": "object"}}, func(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				var args struct{ Query, Tool, Fields string }
				_ = json.Unmarshal(request.Params.Arguments, &args)
				if args.Query != queryA || args.Tool != "fofa" {
					t.Errorf("stats lost its actual range or backend: %s", request.Params.Arguments)
				}
				key, body := "components", `{"aggs":{"product":[{"name":701,"count":4}]}}`
				if args.Fields == "ip" {
					key, body = "summary", `{"size":17,"distinct":{"ip":3}}`
				}
				if err := wait(ctx, key); err != nil {
					return nil, err
				}
				return easmMetricResult(body), nil
			})
			remote.AddTool(&mcp.Tool{Name: "slow_observation", InputSchema: map[string]any{"type": "object"}}, func(ctx context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				if err := wait(ctx, "slow"); err != nil {
					return nil, err
				}
				return easmMetricResult(`{}`), nil
			})
			endpoint := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return remote }, nil))
			t.Cleanup(endpoint.Close)
			server := config.MCPServer{ID: "automatic-ranges", Name: "Automatic ranges", Transport: "streamable_http", URL: endpoint.URL, Enabled: true, TimeoutSeconds: 15}
			call := func(index int, id, name string, arguments map[string]any) map[string]any {
				modelName := mcpclient.DirectToolName(server.ID, name)
				var input any = arguments
				if mode == "progressive" {
					_, modelName = mcpclient.ProxyNames(server.ID)
					input = map[string]any{"name": name, "arguments": arguments}
				}
				raw, _ := json.Marshal(input)
				return map[string]any{"index": index, "id": id, "type": "function", "function": map[string]any{"name": modelName, "arguments": string(raw)}}
			}
			var e *Engine
			var panelID, threadID string
			finalReady := make(chan struct{}, 1)
			e, fixture := newScheduleCompletionHTTPEngine(t, func(w http.ResponseWriter, request *http.Request, step int, input scheduleCompletionRequest) {
				switch step {
				case 1:
					panelAdmissionCall(w, input, "auto-root", "open_task_panel", `{"plugin_id":"easm-asset-portrait","subject":"Fixture entity"}`)
				case 2:
					var panel taskpanel.Panel
					_ = json.Unmarshal([]byte(nuwaValidationReceipt(input, "auto-root")), &panel)
					panelID = panel.ID
					writeContextFlowReply(w, input.Stream, map[string]any{"tool_calls": []any{
						call(0, "auto-summary", "mcp_fofa_stats", map[string]any{"query": queryA, "tool": "fofa", "fields": "ip", "uniq_count": "ip"}),
						call(1, "auto-heat", "mcp_fofa_asset_heat", map[string]any{"query": queryA}),
						call(2, "auto-products", "mcp_fofa_stats", map[string]any{"query": queryA, "tool": "fofa", "fields": "product1,product5"}),
						call(3, "auto-slow", "slow_observation", map[string]any{}),
					}}, "tool_calls")
				case 3:
					writeContextFlowReply(w, input.Stream, map[string]any{"tool_calls": []any{
						call(0, "later-a", "mcp_fofa_asset_heat", map[string]any{"query": queryA}),
						call(1, "later-b", "mcp_fofa_asset_heat", map[string]any{"query": queryB}),
					}}, "tool_calls")
				case 4:
					finalReady <- struct{}{}
					select {
					case <-releases["final"]:
						writeContextFlowReply(w, input.Stream, map[string]any{"content": "Observed ranges are available."}, "stop")
					case <-request.Context().Done():
					}
				default:
					t.Errorf("unexpected automatic range model request %d", step)
					writeContextFlowReply(w, input.Stream, map[string]any{"content": "Fixture failed."}, "stop")
				}
			})
			e.cfg.Memory.Enabled, e.cfg.Memory.AutoReview, e.cfg.Swarm.ComposerPredictions = false, false, false
			e.cfg.MCP.Servers = []config.MCPServer{server}
			e.cfg.MCP.DirectLoadPercent = 100
			if mode == "progressive" {
				e.cfg.MCP.DirectLoadPercent = 0
			}
			thread, err := e.CreateThread("Automatic metric ranges", "", "")
			if err != nil {
				t.Fatal(err)
			}
			threadID = thread.ID
			turn, err := e.StartTurn(threadID, "Collect the supplied asset observations and display each available batch.")
			if err != nil {
				t.Fatal(err)
			}
			waitStarted := func(count int) {
				for range count {
					select {
					case <-started:
					case <-time.After(10 * time.Second):
						t.Fatal("metric collection did not dispatch")
					}
				}
			}
			var state struct {
				Scope      easmQueryScope `json:"query_scope"`
				Nodes      []struct{ ID string }
				Heat       []struct{ Value int }
				Components []any
				Summary    map[string]int
			}
			read := func() taskpanel.Panel {
				panel, err := e.store.TaskPanel(t.Context(), threadID, panelID)
				state.Scope, state.Summary = easmQueryScope{}, nil
				if err != nil || json.Unmarshal(panel.State, &state) != nil {
					t.Fatalf("read automatic query state: %v", err)
				}
				return panel
			}
			waitStarted(4)
			baseline := read()
			if state.Scope.ID == "" || state.Scope.Query != queryA || len(state.Nodes) != 1 {
				t.Fatalf("MCP requests did not select their own range before returning: %s", baseline.State)
			}
			initialScope := state.Scope.ID
			release("summary")
			ordinaryEventually(t, func() bool { read(); return state.Summary["fofa_assets"] == 17 && state.Summary["fofa_ips"] == 3 }, "IP statistics waited for an explicit query-selection tool")
			release("heat")
			ordinaryEventually(t, func() bool { read(); return len(state.Heat) == 1 && state.Heat[0].Value == 11 }, "heat waited for another model call")
			release("components")
			ordinaryEventually(t, func() bool { read(); return len(state.Components) == 1 }, "products waited for another model call")
			if len(fixture.snapshot()) != 2 || read().Revision != baseline.Revision+3 || state.Summary["fofa_assets"] != 17 {
				t.Fatal("same-query sections erased one another or required model publication")
			}
			release("slow")
			waitStarted(2)
			changed := read()
			if state.Scope.ID == initialScope || len(state.Heat) != 0 || len(state.Components) != 0 || len(state.Summary) != 0 || len(state.Nodes) != 1 {
				t.Fatalf("automatic range changes retained prior metrics or lost the graph: %s", changed.State)
			}
			current, old, expected := "late-a", "late-b", 21
			if state.Scope.Query == queryB {
				current, old, expected = "late-b", "late-a", 22
			}
			release(current)
			ordinaryEventually(t, func() bool { read(); return len(state.Heat) == 1 && state.Heat[0].Value == expected }, "current range did not draw its returned batch")
			accepted := read()
			release(old)
			select {
			case <-finalReady:
			case <-time.After(10 * time.Second):
				t.Fatal("model did not collect changed-range requests")
			}
			if saved := read(); saved.Revision != accepted.Revision || len(state.Heat) != 1 || state.Heat[0].Value != expected {
				t.Fatal("an older range's late response replaced the current range")
			}
			release("final")
			if got := waitForTurn(t, e, turn.ID); got.Status != store.TurnDone {
				t.Fatal(got.Error)
			}
			waitSettled(t, e, threadID)
		})
	}
}
