package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	swarm "github.com/LubyRuffy/eino-swarm"
	"github.com/LubyRuffy/eino-swarm/internal/config"
	"github.com/LubyRuffy/eino-swarm/internal/mcpclient"
	"github.com/LubyRuffy/eino-swarm/internal/panelplugins/easm"
	"github.com/LubyRuffy/eino-swarm/internal/store"
	"github.com/LubyRuffy/eino-swarm/internal/taskpanel"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// A shared panel tool can still publish a worker's findings, but may aggregate
// only with the execution tools present in that worker's actual model request.
func TestEASMWorkerSourceUsesItsRegisteredMCPScope(t *testing.T) {
	const serverID, heatTool, statsTool = "portrait-fixture", "fixture_heat", "fixture_stats"
	const managerModel, workerModel = "portrait-planner", "portrait-worker"
	const openCall, collectCall = "portrait-open", "portrait-collect"
	const spawnCall, waitCall = "portrait-spawn", "portrait-wait"
	list, call := mcpclient.ProxyNames(serverID)
	direct := mcpclient.DirectToolName(serverID, heatTool)
	for _, test := range []struct {
		name   string
		tools  []string
		load   int
		allows bool
	}{
		{name: "empty", tools: []string{}},
		{name: "directory only", tools: []string{list}},
		{name: "same server call proxy", tools: []string{call}, allows: true},
		{name: "exact direct tool", tools: []string{direct}, load: 100, allows: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var httpRequests, toolCalls atomic.Int32
			remote := mcp.NewServer(&mcp.Implementation{Name: serverID, Version: "test"}, nil)
			schema := map[string]any{"type": "object", "properties": map[string]any{"query": map[string]any{"type": "string"}}}
			remote.AddTool(&mcp.Tool{Name: heatTool, InputSchema: schema}, func(_ context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				toolCalls.Add(1)
				var args struct{ Query string }
				if err := json.Unmarshal(request.Params.Arguments, &args); err != nil || args.Query != easmFixtureQuery {
					t.Errorf("aggregation query = %q, err = %v", args.Query, err)
				}
				return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: `{"hot_trend":[{"date":"2026-01-02","value":3}]}`}}}, nil
			})
			remote.AddTool(&mcp.Tool{Name: statsTool, InputSchema: schema}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				t.Error("worker used an unselected statistics binding")
				return nil, fmt.Errorf("unexpected statistics request")
			})
			mcpHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return remote }, nil)
			endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				httpRequests.Add(1)
				mcpHandler.ServeHTTP(w, r)
			}))
			t.Cleanup(endpoint.Close)
			server := config.MCPServer{ID: serverID, Transport: "streamable_http", URL: endpoint.URL, Enabled: true, TimeoutSeconds: 3}

			var providerID string
			e, _ := newScheduleCompletionHTTPEngine(t, func(w http.ResponseWriter, _ *http.Request, _ int, request scheduleCompletionRequest) {
				result := func(id string) string { return nuwaValidationReceipt(request, id) }
				reply := func(id, name, args string) { easmToolCall(w, request, id, name, args) }
				switch request.Model {
				case workerModel:
					if result(openCall) == "" {
						var mounted []string
						panelTool := false
						for _, spec := range request.Tools {
							name := spec.Function.Name
							panelTool = panelTool || name == "run_panel_source"
							if strings.HasPrefix(name, "mcp_") {
								mounted = append(mounted, name)
							}
						}
						if !panelTool || !slices.Equal(mounted, test.tools) {
							t.Errorf("actual worker tools: MCP=%v want=%v panel=%v", mounted, test.tools, panelTool)
						}
						reply(openCall, "open_task_panel", `{"plugin_id":"easm-asset-portrait"}`)
						return
					}
					if result(collectCall) == "" {
						var panel taskpanel.Panel
						if err := json.Unmarshal([]byte(result(openCall)), &panel); err != nil || panel.ID == "" {
							t.Errorf("worker could not open its panel: %s (%v)", result(openCall), err)
						}
						reply(collectCall, "run_panel_source", fmt.Sprintf(`{"panel_id":%q,"input":{"subject":"Subject Co","query":%q,"domains":[{"domain":"observed.test","source":"fixture"}]}}`, panel.ID, easmFixtureQuery))
						return
					}
					var receipt struct{ OK bool }
					if err := json.Unmarshal([]byte(result(collectCall)), &receipt); err != nil || !receipt.OK {
						t.Errorf("worker source did not accept its findings: %s (%v)", result(collectCall), err)
					}
				case managerModel:
					if result(spawnCall) == "" {
						selected, err := json.Marshal(test.tools)
						if err != nil {
							t.Error(err)
						}
						reply(spawnCall, "spawn_agent", fmt.Sprintf(`{"role":"observer","task":"Record the observed evidence.","model_ref":%q,"tool_names":%s}`, nuwaModelRef(providerID, workerModel), selected))
						return
					}
					if result(waitCall) == "" {
						var worker toolScopeWorkerID
						if err := json.Unmarshal([]byte(result(spawnCall)), &worker); err != nil || worker.Generation != 1 || worker.AgentID == "" {
							t.Errorf("could not spawn fixture worker: %s (%v)", result(spawnCall), err)
						}
						reply(waitCall, "wait_agents", fmt.Sprintf(`{"agent_ids":[%q],"timeout_s":5}`, worker.AgentID))
						return
					}
					var wait toolScopeWait
					if err := json.Unmarshal([]byte(result(waitCall)), &wait); err != nil || wait.TimedOut || len(wait.Agents) != 1 || wait.Agents[0].Status != "done" {
						t.Errorf("could not collect fixture worker: %s (%v)", result(waitCall), err)
					}
				default:
					t.Errorf("unexpected request for model %q", request.Model)
				}
				writeContextFlowReply(w, request.Stream, map[string]any{"content": "The worker's findings were saved."}, "stop")
			})
			providerID = e.cfg.Snapshot().Models.Providers[0].ID
			if err := e.UpdateConfig(func(c *config.Config) error {
				c.Models.Providers[0].Model = managerModel
				c.Models.Providers[0].Catalog = []string{managerModel, workerModel}
				c.MCP.Servers = []config.MCPServer{server}
				c.MCP.DirectLoadPercent = test.load
				c.TaskPanels.EASM.ServerID = server.ID
				c.TaskPanels.EASM.Tools = map[string]string{easm.RoleStats: "", easm.RoleHeat: heatTool, easm.RoleProductRules: ""}
				if test.load != 0 {
					c.TaskPanels.EASM.Tools[easm.RoleStats] = statsTool
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if test.load != 0 {
				// Direct mounting also stays offline until an admitted call.
				catalog := mcpclient.Catalog{ServerID: server.ID, Tools: []mcpclient.ToolDescriptor{
					{Name: heatTool, OriginalName: heatTool, InputSchema: schema},
					{Name: statsTool, OriginalName: statsTool, InputSchema: schema},
				}}
				if err := mcpclient.SaveCatalog(e.cfg.DataDir(), server, catalog); err != nil {
					t.Fatal(err)
				}
			}
			project, err := e.CreateProject("Portrait worker scope", "", t.TempDir(), false, true)
			if err != nil {
				t.Fatal(err)
			}
			thread, err := e.CreateThread("Record scoped findings", "", project.ID)
			if err != nil {
				t.Fatal(err)
			}
			turn, err := e.StartTurn(thread.ID, "Delegate recording the local finding.")
			if err != nil {
				t.Fatal(err)
			}
			finished := waitForTurn(t, e, turn.ID)
			waitSettled(t, e, thread.ID)
			if finished.Status != store.TurnDone {
				t.Fatalf("worker turn did not finish: %s (%s)", finished.Status, finished.Error)
			}
			panels, err := e.store.TaskPanels(t.Context(), thread.ID)
			if err != nil || len(panels) != 1 {
				t.Fatalf("expected one saved portrait, got %d (%v)", len(panels), err)
			}
			var state easm.State
			panel := panels[0]
			if err := json.Unmarshal(panel.State, &state); err != nil {
				t.Fatal(err)
			}
			if panel.Source.AgentID == "" || panel.Source.AgentID == swarm.DefaultManagerID || panel.Source.Generation != 1 ||
				!slices.ContainsFunc(state.Nodes, func(node easm.Node) bool { return node.ID == "domain:observed.test" }) {
				t.Errorf("saved panel lost worker evidence or scope provenance: %+v, %s", panel.Source, panel.State)
			}
			wantStatus, wantCalls := "unavailable", int32(0)
			if test.allows {
				wantStatus, wantCalls = "done", 1
			}
			if !slices.ContainsFunc(state.Collection.Steps, func(step easm.StepStatus) bool { return step.ID == easm.StepHeat && step.Status == wantStatus }) ||
				len(state.Heat) != int(wantCalls) || toolCalls.Load() != wantCalls || (httpRequests.Load() > 0) != test.allows {
				t.Errorf("heat status want %s, calls want %d: steps=%+v heat=%v MCP HTTP=%d tool calls=%d",
					wantStatus, wantCalls, state.Collection.Steps, state.Heat, httpRequests.Load(), toolCalls.Load())
			}
			if test.load != 0 && !slices.ContainsFunc(state.Collection.Steps, func(step easm.StepStatus) bool {
				return step.ID == easm.StepSummary && step.Status == "unavailable"
			}) {
				t.Errorf("unselected statistics binding did not settle as unavailable: %+v", state.Collection.Steps)
			}
		})
	}
}
