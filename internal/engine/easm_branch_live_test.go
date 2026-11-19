package engine

import (
	"context"
	"encoding/json"
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
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The legal owner is bound before dispatch and remains stable while the graph
// changes. A slow sibling cannot postpone the returned branch evidence.
func TestEASMCompanyBranchesUseTheirFrozenLegalOwnerBeforeModelPublication(t *testing.T) {
	for _, mode := range []string{"direct", "progressive", "late-parent-direct", "late-parent-progressive"} {
		t.Run(mode, func(t *testing.T) {
			progressive := strings.HasSuffix(mode, "progressive")
			lateParent := strings.HasPrefix(mode, "late-parent")
			started := make(chan struct{}, 1)
			branchDispatched := make(chan struct{})
			releases, closers := make(map[string]chan struct{}), make(map[string]*sync.Once)
			for _, key := range []string{"branches", "parent", "slow", "final"} {
				releases[key], closers[key] = make(chan struct{}), new(sync.Once)
			}
			release := func(key string) { closers[key].Do(func() { close(releases[key]) }) }
			defer func() {
				for key := range releases {
					release(key)
				}
			}()
			remote := mcp.NewServer(&mcp.Implementation{Name: "branch-fixture", Version: "test"}, nil)
			remote.AddTool(&mcp.Tool{Name: "mcp_fofa_org_lookup", InputSchema: map[string]any{"type": "object"}}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return easmOrganizationResult(`{"results":[{"id":"parent-source","name":"Fixture Legal Parent","domains":[]},{"id":"first-branch","name":"Fixture North Branch","domains":[]}]}`), nil
			})
			remote.AddTool(&mcp.Tool{Name: "mcp_icp_query", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"query_type": map[string]any{"type": "string"}, "value": map[string]any{"type": "string"}, "fuzzy_match": map[string]any{"type": "boolean"}, "size": map[string]any{"type": "integer"}}, "required": []string{"query_type", "value"}}}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				var args struct {
					QueryType string `json:"query_type"`
					Value     string
				}
				if json.Unmarshal(req.Params.Arguments, &args) != nil || args.QueryType != "org" || args.Value != "Fixture Legal Parent" {
					t.Errorf("parent discovery lost its actual source query: %s", req.Params.Arguments)
				}
				select {
				case <-branchDispatched:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
				select {
				case <-releases["parent"]:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
				return easmOrganizationResult(`{"records":[{"org":"Fixture Legal Parent","icp":"Fixture Filing 77","domain":"parent.example.test"}]}`), nil
			})
			remote.AddTool(&mcp.Tool{Name: "mcp_company_branch", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"keyword": map[string]any{"type": "string"}, "page_size": map[string]any{"type": "integer"}}, "required": []string{"keyword"}}}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				var args struct {
					Keyword  string
					PageSize int `json:"page_size"`
				}
				if json.Unmarshal(req.Params.Arguments, &args) != nil || args.Keyword != "Fixture Legal Parent" || args.PageSize != 20 {
					t.Errorf("branch request lost its actual owner: %s", req.Params.Arguments)
				}
				started <- struct{}{}
				close(branchDispatched)
				select {
				case <-releases["branches"]:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
				body := map[string]any{"total": 2, "size": 20, "page": 1, "items": []any{map[string]any{"id": 17, "name": "Fixture North Branch", "reg_status": "Active"}, map[string]any{"id": json.Number("9007199254740993"), "name": "Fixture South Branch", "reg_status": "Dissolved"}}, "padding": strings.Repeat("branch registry metadata ", 200)}
				raw, _ := json.Marshal(body)
				result := easmOrganizationResult(string(raw))
				if progressive {
					result.StructuredContent = body
				}
				return result, nil
			})
			remote.AddTool(&mcp.Tool{Name: "slow_branch_observation", InputSchema: map[string]any{"type": "object"}}, func(ctx context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				select {
				case <-releases["slow"]:
					return easmOrganizationResult(`{"complete":true}`), nil
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			})
			endpoint := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return remote }, nil))
			t.Cleanup(endpoint.Close)
			server := config.MCPServer{ID: "branch-observations", Name: "Branch observations", Transport: "streamable_http", URL: endpoint.URL, Enabled: true, TimeoutSeconds: 15}
			call := func(index int, id, name string, args map[string]any) map[string]any {
				modelName := mcpclient.DirectToolName(server.ID, name)
				var input any = args
				if progressive {
					_, modelName = mcpclient.ProxyNames(server.ID)
					input = map[string]any{"name": name, "arguments": args}
				}
				raw, _ := json.Marshal(input)
				return map[string]any{"index": index, "id": id, "type": "function", "function": map[string]any{"name": modelName, "arguments": string(raw)}}
			}
			var e *Engine
			var threadID, panelID string
			ready := make(chan struct{}, 1)
			e, fixture := newScheduleCompletionHTTPEngine(t, func(w http.ResponseWriter, r *http.Request, step int, request scheduleCompletionRequest) {
				switch step {
				case 1:
					panelAdmissionCall(w, request, "branch-open", "open_task_panel", `{"plugin_id":"easm-asset-portrait","subject":"Fixture Brand"}`)
				case 2:
					var panel taskpanel.Panel
					_ = json.Unmarshal([]byte(nuwaValidationReceipt(request, "branch-open")), &panel)
					panelID = panel.ID
					if lateParent {
						writeContextFlowReply(w, request.Stream, map[string]any{"tool_calls": []any{call(0, "branch-query", "mcp_company_branch", map[string]any{"keyword": "Fixture Legal Parent", "page_size": 20}), call(1, "late-parent-icp", "mcp_icp_query", map[string]any{"query_type": "org", "value": "Fixture Legal Parent", "fuzzy_match": false, "size": 100}), call(2, "branch-slow", "slow_branch_observation", map[string]any{})}}, "tool_calls")
						return
					}
					writeContextFlowReply(w, request.Stream, map[string]any{"tool_calls": []any{call(0, "branch-org", "mcp_fofa_org_lookup", map[string]any{})}}, "tool_calls")
				case 3:
					if !lateParent {
						writeContextFlowReply(w, request.Stream, map[string]any{"tool_calls": []any{call(0, "branch-query", "mcp_company_branch", map[string]any{"keyword": "Fixture Legal Parent", "page_size": 20}), call(1, "branch-slow", "slow_branch_observation", map[string]any{})}}, "tool_calls")
						return
					}
					fallthrough
				case 4:
					var capped struct{ Truncated bool }
					if json.Unmarshal([]byte(nuwaValidationReceipt(request, "branch-query")), &capped) != nil || !capped.Truncated {
						t.Error("branch fixture did not cross the real result-budget boundary")
					}
					ready <- struct{}{}
					select {
					case <-releases["final"]:
						writeContextFlowReply(w, request.Stream, map[string]any{"content": "Branch observations are available."}, "stop")
					case <-r.Context().Done():
					}
				default:
					t.Errorf("unexpected branch model request %d", step)
					writeContextFlowReply(w, request.Stream, map[string]any{"content": "Unexpected request."}, "stop")
				}
			})
			e.cfg.Memory.Enabled, e.cfg.Memory.AutoReview, e.cfg.Swarm.ComposerPredictions = false, false, false
			e.cfg.Tools.ResultMaxChars = 1024
			e.cfg.MCP.Servers = []config.MCPServer{server}
			e.cfg.MCP.DirectLoadPercent = 100
			if progressive {
				e.cfg.MCP.DirectLoadPercent = 0
			}
			thread, err := e.CreateThread("Branch source ownership", "", "")
			if err != nil {
				t.Fatal(err)
			}
			threadID = thread.ID
			turn, err := e.StartTurn(threadID, "Collect the supplied company branches and preserve their legal ownership.")
			if err != nil {
				t.Fatal(err)
			}
			select {
			case <-started:
			case <-time.After(10 * time.Second):
				t.Fatal("branch query did not start")
			}
			panel, err := e.store.TaskPanel(t.Context(), threadID, panelID)
			if err != nil {
				t.Fatal(err)
			}
			ownerID, firstChildID := "company:fofa-org:parent-source", "company:fofa-org:first-branch"
			expectedNodes, expectedEdges, expectedCalls := 4, 4, 3
			if lateParent {
				if len(fixture.snapshot()) != 2 || strings.Contains(string(panel.State), "Fixture Legal Parent") {
					t.Fatal("branch query was not dispatched before the legal parent existed")
				}
				release("parent")
				ordinaryEventually(t, func() bool {
					panel, err = e.store.TaskPanel(t.Context(), threadID, panelID)
					return err == nil && strings.Contains(string(panel.State), "company:icp-org:Fixture Legal Parent")
				}, "the earlier ICP result did not publish the legal parent")
				ownerID, firstChildID = "company:icp-org:Fixture Legal Parent", "company:registry:17"
				expectedNodes, expectedEdges, expectedCalls = 6, 5, 2
			} else {
				rename := taskpanel.Update{ProtocolVersion: 1, PanelID: panel.ID, BaseRevision: panel.Revision, OperationID: "concurrent-label-observation", Kind: "patch", Operations: json.RawMessage(`[{"op":"replace","path":"/nodes/1/label","value":"Observed display alias"}]`)}
				source := taskpanel.Source{ThreadID: threadID, TurnID: turn.ID, AgentID: "manager", ToolCallID: "concurrent-label-observation"}
				panel, err = e.publishTaskPanel(t.Context(), source, rename)
				if err != nil {
					t.Fatal(err)
				}
			}
			release("branches")
			var current taskpanel.Panel
			ordinaryEventually(t, func() bool {
				current, err = e.store.TaskPanel(t.Context(), threadID, panelID)
				return err == nil && current.Revision == panel.Revision+1
			}, "branches waited for a slow sibling or another model publication")
			var graph struct {
				Nodes []struct {
					ID, Kind, Label string
					Detail          struct{ Reason string }
				}
				Edges []struct {
					Source, Target string
					Confirmed      bool
				}
			}
			_ = json.Unmarshal(current.State, &graph)
			if len(graph.Nodes) != expectedNodes || len(graph.Edges) != expectedEdges || len(fixture.snapshot()) != expectedCalls {
				t.Fatalf("branch evidence was duplicated or delayed: %s", current.State)
			}
			children := map[string]bool{firstChildID: false, "company:registry:9007199254740993": false}
			for _, edge := range graph.Edges {
				if _, expected := children[edge.Target]; expected && edge.Confirmed {
					if edge.Source != ownerID {
						t.Fatalf("branch attached to a brand or changed display name: %+v", edge)
					}
					children[edge.Target] = true
				}
			}
			for id, found := range children {
				if !found {
					t.Errorf("parent source lost branch %s", id)
				}
			}
			for _, node := range graph.Nodes {
				if _, child := children[node.ID]; child && (!strings.Contains(node.Detail.Reason, "mcp_company_branch") || !strings.Contains(node.Detail.Reason, "Fixture Legal Parent")) {
					t.Errorf("branch lost its source fact: %+v", node)
				}
			}
			if pending, err := e.store.GetTurn(turn.ID); err != nil || pending.Status != store.TurnRunning {
				t.Fatalf("branch publication required task completion: %+v %v", pending, err)
			}
			release("slow")
			select {
			case <-ready:
			case <-time.After(10 * time.Second):
				t.Fatal("model did not collect completed branch tools")
			}
			release("final")
			if finished := waitForTurn(t, e, turn.ID); finished.Status != store.TurnDone {
				t.Fatal(finished.Error)
			}
			waitSettled(t, e, threadID)
		})
	}
}
