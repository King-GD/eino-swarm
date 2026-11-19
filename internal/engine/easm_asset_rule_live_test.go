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
	"github.com/LubyRuffy/eino-swarm/internal/taskpanel"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Rule names are typed only by prior organization evidence. The actual MCP
// boundary must keep product labels as evidence while adding known ownership.
func TestEASMAssetRulesAppearAtTheRealMCPBoundaryBeforeModelPublication(t *testing.T) {
	for _, mode := range []string{"direct", "progressive"} {
		t.Run(mode, func(t *testing.T) {
			var calls atomic.Int32
			remote := mcp.NewServer(&mcp.Implementation{Name: "asset-rule-fixture", Version: "test"}, nil)
			remote.AddTool(&mcp.Tool{Name: "mcp_fofa_org_lookup", InputSchema: map[string]any{"type": "object"}}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return easmOrganizationResult(`{"results":[{"id":"north","name":"Fixture North Company","domains":[]},{"id":"south","name":"Fixture South Company","domains":[]}]}`), nil
			})
			remote.AddTool(&mcp.Tool{Name: "mcp_company_asset", InputSchema: map[string]any{"type": "object"}}, func(_ context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				var args struct{ Domains []string }
				if json.Unmarshal(request.Params.Arguments, &args) != nil || len(args.Domains) != 4 {
					t.Error("asset rule request lost its explicit domain batch")
				}
				rows := []any{
					map[string]any{"domain": "north.example.test", "names": []map[string]string{{"name": "Fixture North Company", "time": "2026.10.31"}}},
					map[string]any{"domain": "product.example.test", "names": []map[string]string{{"name": "Fixture Product", "time": "2026.11.04"}}},
					map[string]any{"domain": "unmatched.example.test", "names": []any{}},
				}
				if calls.Add(1) > 1 {
					rows = append(rows, map[string]any{"domain": "south.example.test", "names": []map[string]string{{"name": "Fixture South Company", "time": "2026.11.19"}}})
				}
				body := map[string]any{"error": false, "errmsg": "", "results": rows, "padding": strings.Repeat("asset rule source evidence ", 200)}
				raw, _ := json.Marshal(body)
				result := &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(raw)}}}
				if mode == "progressive" {
					result.StructuredContent = body
				}
				return result, nil
			})
			endpoint := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return remote }, nil))
			t.Cleanup(endpoint.Close)
			server := config.MCPServer{ID: "asset-rule-observations", Name: "Asset rule observations", Transport: "streamable_http", URL: endpoint.URL, Enabled: true, TimeoutSeconds: 5}
			call := func(w http.ResponseWriter, input scheduleCompletionRequest, id, remoteName string, args any) {
				name := mcpclient.DirectToolName(server.ID, remoteName)
				if mode == "progressive" {
					_, name = mcpclient.ProxyNames(server.ID)
					args = map[string]any{"name": remoteName, "arguments": args}
				}
				raw, _ := json.Marshal(args)
				panelAdmissionCall(w, input, id, name, string(raw))
			}
			var e *Engine
			var threadID, panelID string
			var initialRevision int64
			ready, release := make(chan struct{}, 1), make(chan struct{})
			var once sync.Once
			defer once.Do(func() { close(release) })
			check := func(domainCount int, revision int64) {
				panel, err := e.store.TaskPanel(t.Context(), threadID, panelID)
				if err != nil {
					t.Error(err)
					return
				}
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
				_ = json.Unmarshal(panel.State, &graph)
				if panel.Revision != revision || len(graph.Nodes) != 3+domainCount || len(graph.Edges) != 1+domainCount {
					t.Errorf("asset rules did not publish at the MCP boundary: revision=%d state=%s", panel.Revision, panel.State)
				}
				for _, node := range graph.Nodes {
					if node.Kind == "feature" || node.Kind == "company" && node.Label == "Fixture Product" {
						t.Error("the asset rule manufactured a legal entity, investment or ICP feature")
					}
					if node.Kind == "domain" && !strings.Contains(node.Detail.Reason, "mcp_company_asset") {
						t.Error("the domain lost the actual asset-rule source")
					}
				}
				for _, edge := range graph.Edges {
					if edge.Target == "domain:product.example.test" || edge.Source == "root" && edge.Confirmed {
						t.Error("an untyped product rule fabricated root/company ownership")
					}
				}
			}
			domainArgs := map[string]any{"domains": []string{"north.example.test", "south.example.test", "product.example.test", "unmatched.example.test"}}
			e, _ = newScheduleCompletionHTTPEngine(t, func(w http.ResponseWriter, request *http.Request, step int, input scheduleCompletionRequest) {
				switch step {
				case 1:
					panelAdmissionCall(w, input, "asset-open", "open_task_panel", `{"plugin_id":"easm-asset-portrait","subject":"Fixture"}`)
				case 2:
					var opened taskpanel.Panel
					if err := json.Unmarshal([]byte(nuwaValidationReceipt(input, "asset-open")), &opened); err != nil || opened.ID == "" {
						t.Errorf("open asset rule panel: %v", err)
					}
					panelID, initialRevision = opened.ID, opened.Revision
					call(w, input, "asset-organizations", "mcp_fofa_org_lookup", map[string]any{"keyword": "Fixture"})
				case 3:
					call(w, input, "asset-rules-1", "mcp_company_asset", domainArgs)
				case 4:
					check(2, initialRevision+2)
					var capped struct{ Truncated bool }
					if json.Unmarshal([]byte(nuwaValidationReceipt(input, "asset-rules-1")), &capped) != nil || !capped.Truncated {
						t.Error("the asset-rule fixture did not cross model result capping")
					}
					call(w, input, "asset-rules-2", "mcp_company_asset", domainArgs)
				case 5:
					check(3, initialRevision+3)
					call(w, input, "asset-rules-repeat", "mcp_company_asset", domainArgs)
				case 6:
					check(3, initialRevision+3)
					ready <- struct{}{}
					select {
					case <-release:
						writeContextFlowReply(w, input.Stream, map[string]any{"content": "Asset rule observations are available."}, "stop")
					case <-request.Context().Done():
					}
				default:
					t.Errorf("unexpected asset-rule model request %d", step)
					writeContextFlowReply(w, input.Stream, map[string]any{"content": "Fixture failed."}, "stop")
				}
			})
			e.cfg.Memory.Enabled, e.cfg.Memory.AutoReview, e.cfg.Swarm.ComposerPredictions = false, false, false
			e.cfg.Tools.ResultMaxChars = 1024
			e.cfg.MCP.Servers = []config.MCPServer{server}
			e.cfg.MCP.DirectLoadPercent = 100
			if mode == "progressive" {
				e.cfg.MCP.DirectLoadPercent = 0
			}
			thread, err := e.CreateThread("Asset rule source observations", "", "")
			if err != nil {
				t.Fatal(err)
			}
			threadID = thread.ID
			turn, err := e.StartTurn(thread.ID, "Collect the supplied domain rule observations while retaining their reported sources.")
			if err != nil {
				t.Fatal(err)
			}
			select {
			case <-ready:
			case <-time.After(10 * time.Second):
				t.Fatal("asset rule observations did not reach the final-response gate")
			}
			if pending, err := e.store.GetTurn(turn.ID); err != nil || pending.Status != store.TurnRunning || calls.Load() != 3 {
				t.Fatalf("asset rules depended on task completion or reran discovery: turn=%+v error=%v calls=%d", pending, err, calls.Load())
			}
			once.Do(func() { close(release) })
			if done := waitForTurn(t, e, turn.ID); done.Status != store.TurnDone {
				t.Fatal(done.Error)
			}
			waitSettled(t, e, thread.ID)
		})
	}
}
