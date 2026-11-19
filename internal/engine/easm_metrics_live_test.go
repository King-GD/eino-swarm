package engine

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
	"github.com/LubyRuffy/eino-swarm/internal/mcpclient"
	"github.com/LubyRuffy/eino-swarm/internal/store"
	"github.com/LubyRuffy/eino-swarm/internal/taskpanel"
	jsonpatch "github.com/evanphx/json-patch"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Each section must publish as its own real MCP call returns, while a sibling
// call still prevents the model from receiving or republishing those results.
func TestEASMMetricsAppearIndependentlyBeforeAnotherModelPublication(t *testing.T) {
	for _, mode := range []string{"direct", "progressive"} {
		t.Run(mode, func(t *testing.T) {
			const query = `domain=="metrics.example.test"`
			started := make(chan string, 4)
			releases := make(map[string]chan struct{})
			closers := make(map[string]*sync.Once)
			for _, key := range []string{"heat", "products", "slow", "rules", "final"} {
				releases[key], closers[key] = make(chan struct{}), new(sync.Once)
			}
			release := func(key string) { closers[key].Do(func() { close(releases[key]) }) }
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
			remote := mcp.NewServer(&mcp.Implementation{Name: "metrics-fixture", Version: "test"}, nil)
			remote.AddTool(&mcp.Tool{Name: "mcp_fofa_asset_heat", InputSchema: map[string]any{"type": "object"}}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				var args struct{ Query string }
				if err := json.Unmarshal(req.Params.Arguments, &args); err != nil || args.Query != query {
					t.Errorf("heat request lost the selected query: %s error=%v", req.Params.Arguments, err)
				}
				if err := wait(ctx, "heat"); err != nil {
					return nil, err
				}
				points := make([]map[string]any, 12)
				for i := range points {
					points[i] = map[string]any{"date": fmt.Sprintf("2026-02-%02d", i+1), "value": i * 3}
				}
				body := map[string]any{"hot_trend": points, "padding": strings.Repeat("raw metric evidence ", 200)}
				raw, _ := json.Marshal(body)
				result := &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(raw)}}}
				if mode == "progressive" {
					result.StructuredContent = body
				}
				return result, nil
			})
			remote.AddTool(&mcp.Tool{Name: "mcp_fofa_stats", InputSchema: map[string]any{"type": "object"}}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				var args struct{ Query, Fields, Tool string }
				if err := json.Unmarshal(req.Params.Arguments, &args); err != nil || args.Query != query || args.Fields != "product1,product5" || args.Tool != "fofa" {
					t.Errorf("product request lost its query or field scope: %s error=%v", req.Params.Arguments, err)
				}
				if err := wait(ctx, "products"); err != nil {
					return nil, err
				}
				body := map[string]any{
					"aggs":    map[string]any{"product": []map[string]int{{"name": 901, "count": 9}, {"name": 902, "count": 6}, {"name": 903, "count": 4}}},
					"padding": strings.Repeat("raw product evidence ", 200),
				}
				raw, _ := json.Marshal(body)
				return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(raw)}}}, nil
			})
			remote.AddTool(&mcp.Tool{Name: "mcp_fofa_rule_lookup", InputSchema: map[string]any{"type": "object"}}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				var args struct {
					RuleIDs []int `json:"rule_ids"`
				}
				if err := json.Unmarshal(req.Params.Arguments, &args); err != nil || len(args.RuleIDs) != 3 || args.RuleIDs[0] != 901 || args.RuleIDs[2] != 903 {
					t.Errorf("rule request lost its observed product identities: %s error=%v", req.Params.Arguments, err)
				}
				if err := wait(ctx, "rules"); err != nil {
					return nil, err
				}
				return &mcp.CallToolResult{StructuredContent: map[string]any{"results": []map[string]any{{"rule_id": 901, "product": "Fixture gateway"}, {"rule_id": 902, "product": "Fixture server"}, {"rule_id": 903, "product": "Fixture library"}}}}, nil
			})
			remote.AddTool(&mcp.Tool{Name: "slow_observation", InputSchema: map[string]any{"type": "object"}}, func(ctx context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				if err := wait(ctx, "slow"); err != nil {
					return nil, err
				}
				return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "The independent observation finished."}}}, nil
			})
			endpoint := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return remote }, nil))
			t.Cleanup(endpoint.Close)
			server := config.MCPServer{ID: "metric-observations", Name: "Metric observations", Transport: "streamable_http", URL: endpoint.URL, Enabled: true, TimeoutSeconds: 15}
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
			var baseline taskpanel.Panel
			var cursor int64
			selected := make(chan struct{}, 1)
			finalReady := make(chan struct{}, 1)
			e, fixture := newScheduleCompletionHTTPEngine(t, func(w http.ResponseWriter, r *http.Request, step int, request scheduleCompletionRequest) {
				switch step {
				case 1:
					panelAdmissionCall(w, request, "metric-open", "open_task_panel", `{"plugin_id":"easm-asset-portrait","subject":"Fixture institution"}`)
				case 2:
					var panel taskpanel.Panel
					if err := json.Unmarshal([]byte(nuwaValidationReceipt(request, "metric-open")), &panel); err != nil || panel.ID == "" {
						t.Errorf("root open receipt: %v", err)
					}
					panelID = panel.ID
					raw, _ := json.Marshal(map[string]string{"plugin_id": "easm-asset-portrait", "query": query})
					panelAdmissionCall(w, request, "metric-query", "open_task_panel", string(raw))
				case 3:
					var err error
					baseline, err = e.store.TaskPanel(t.Context(), threadID, panelID)
					if err != nil {
						t.Error(err)
					}
					cursor, err = e.store.TaskPanelCursor(t.Context(), threadID)
					if err != nil {
						t.Error(err)
					}
					selected <- struct{}{}
					writeContextFlowReply(w, request.Stream, map[string]any{"tool_calls": []any{
						call(0, "metric-heat", "mcp_fofa_asset_heat", map[string]any{"query": query}),
						call(1, "metric-products", "mcp_fofa_stats", map[string]any{"query": query, "fields": "product1,product5", "tool": "fofa"}),
						call(2, "metric-slow", "slow_observation", map[string]any{}),
					}}, "tool_calls")
				case 4:
					for _, id := range []string{"metric-heat", "metric-products"} {
						var capped struct{ Truncated bool }
						if json.Unmarshal([]byte(nuwaValidationReceipt(request, id)), &capped) != nil || !capped.Truncated {
							t.Errorf("%s did not cross the configured result-cap boundary", id)
						}
					}
					writeContextFlowReply(w, request.Stream, map[string]any{"tool_calls": []any{call(0, "metric-rules", "mcp_fofa_rule_lookup", map[string]any{"rule_ids": []int{901, 902, 903}})}}, "tool_calls")
				case 5:
					finalReady <- struct{}{}
					select {
					case <-releases["final"]:
						writeContextFlowReply(w, request.Stream, map[string]any{"content": "The observed sections are available."}, "stop")
					case <-r.Context().Done():
					}
				default:
					t.Errorf("unexpected metric model request %d", step)
					writeContextFlowReply(w, request.Stream, map[string]any{"content": "Fixture failed."}, "stop")
				}
			})
			e.cfg.Memory.Enabled, e.cfg.Memory.AutoReview, e.cfg.Swarm.ComposerPredictions = false, false, false
			e.cfg.Tools.ResultMaxChars = 1024
			e.cfg.MCP.Servers = []config.MCPServer{server}
			e.cfg.MCP.DirectLoadPercent = 100
			if mode == "progressive" {
				e.cfg.MCP.DirectLoadPercent = 0
			}
			thread, err := e.CreateThread("Live metric observations", "", "")
			if err != nil {
				t.Fatal(err)
			}
			threadID = thread.ID
			turn, err := e.StartTurn(thread.ID, "Collect the supplied observations and keep each available section current.")
			if err != nil {
				t.Fatal(err)
			}
			select {
			case <-selected:
			case <-time.After(10 * time.Second):
				t.Fatal("the model did not select a metric query before collection")
			}
			seen := make(map[string]bool)
			for len(seen) < 3 {
				select {
				case key := <-started:
					seen[key] = true
				case <-time.After(10 * time.Second):
					t.Fatal("independent metric calls did not start in one model round")
				}
			}
			if !seen["heat"] || !seen["products"] || !seen["slow"] {
				t.Fatalf("unexpected metric dispatches: %v", seen)
			}
			var graph struct {
				Nodes []struct{ Kind, Label string }
				Edges []any
				Scope struct{ ID, Query string } `json:"query_scope"`
				Heat  []struct {
					Date  string
					Value int
				}
				Components []struct {
					Name        string
					DisplayName string `json:"display_name"`
					Count       int
				}
			}
			read := func() taskpanel.Panel {
				panel, err := e.store.TaskPanel(t.Context(), threadID, panelID)
				if err != nil || json.Unmarshal(panel.State, &graph) != nil {
					t.Fatalf("read committed metrics: %v", err)
				}
				return panel
			}
			read()
			if len(graph.Nodes) != 1 || graph.Nodes[0].Kind != "root" || graph.Nodes[0].Label != "Fixture institution" || graph.Scope.ID == "" || graph.Scope.Query != query {
				t.Fatalf("metrics lost the subject or selected query: %+v", graph)
			}
			release("heat")
			ordinaryEventually(t, func() bool { read(); return len(graph.Heat) == 12 }, "the heat section waited for a slower tool or another model call")
			if len(graph.Components) != 0 || graph.Heat[0].Value != 0 || graph.Heat[11].Date != "2026-02-12" || graph.Heat[11].Value != 33 || len(fixture.snapshot()) != 3 {
				t.Fatalf("heat evidence was truncated or depended on model publication: %+v", graph)
			}
			release("products")
			ordinaryEventually(t, func() bool { read(); return len(graph.Components) == 3 }, "the product section waited for a slower tool or another model call")
			panel := read()
			if len(graph.Heat) != 12 || len(graph.Nodes) != 1 || len(graph.Edges) != 0 || panel.Revision != baseline.Revision+2 || graph.Components[0].Name != "901" || graph.Components[2].Count != 4 || len(fixture.snapshot()) != 3 {
				t.Fatalf("independent section updates changed evidence or lost a batch: %s", panel.State)
			}
			changes, err := e.store.TaskPanelChanges(t.Context(), threadID, cursor)
			if err != nil || len(changes) != 2 {
				t.Fatalf("independent section delivery journal: changes=%d error=%v", len(changes), err)
			}
			replayed := baseline.State
			for _, change := range changes {
				patch, err := jsonpatch.DecodePatch(change.Operations)
				if err != nil || change.Kind != "patch" {
					t.Fatalf("section update was not deliverable as a patch: %+v error=%v", change, err)
				}
				replayed, err = patch.Apply(replayed)
				if err != nil {
					t.Fatal(err)
				}
			}
			if !jsonpatch.Equal(replayed, panel.State) {
				t.Fatal("SSE journal patches did not preserve both independently arrived sections")
			}
			release("slow")
			select {
			case key := <-started:
				if key != "rules" {
					t.Fatalf("unexpected rule lookup: %s", key)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("the actual model did not request the observed product labels")
			}
			release("rules")
			ordinaryEventually(t, func() bool { read(); return graph.Components[0].DisplayName == "Fixture gateway" }, "rule lookup did not update the observed component names")
			if graph.Components[2].DisplayName != "Fixture library" || graph.Components[2].Count != 4 || len(graph.Heat) != 12 {
				t.Fatalf("rule labels changed the retained metric evidence: %+v", graph)
			}
			select {
			case <-finalReady:
			case <-time.After(10 * time.Second):
				t.Fatal("the final model response did not reach its fixture gate")
			}
			if pending, err := e.store.GetTurn(turn.ID); err != nil || pending.Status != store.TurnRunning {
				t.Fatalf("metrics required turn completion: turn=%+v error=%v", pending, err)
			}
			events, err := e.store.ListTurnEvents(turn.ID)
			if err != nil {
				t.Fatal(err)
			}
			for _, event := range events {
				if event.Kind == "tool_call" && strings.Contains(event.Text, "publish_panel_update") {
					t.Fatal("the fixture republished metrics through the model")
				}
			}
			release("final")
			if done := waitForTurn(t, e, turn.ID); done.Status != store.TurnDone {
				t.Fatal(done.Error)
			}
			waitSettled(t, e, thread.ID)
		})
	}
}
