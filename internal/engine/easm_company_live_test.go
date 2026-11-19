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

func TestEASMCompanyHierarchyAppearsThroughTheRealMCPBoundary(t *testing.T) {
	for _, mode := range []string{"direct", "progressive", "legal-subject-direct", "legal-subject-progressive"} {
		t.Run(mode, func(t *testing.T) {
			progressive := strings.HasSuffix(mode, "progressive")
			legalSubject := strings.HasPrefix(mode, "legal-subject")
			subject, expectedNodes, expectedEdges := "Fixture Brand", 4, 4
			if legalSubject {
				subject, expectedNodes, expectedEdges = "Fixture Parent", 3, 2
			}
			var relations atomic.Int32
			remote := mcp.NewServer(&mcp.Implementation{Name: "company-fixture", Version: "test"}, nil)
			schema := map[string]any{"type": "object", "properties": map[string]any{"keyword": map[string]any{"type": "string"}}, "required": []string{"keyword"}}
			remote.AddTool(&mcp.Tool{Name: "mcp_fofa_org_lookup", InputSchema: schema}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return easmOrganizationResult(`{"results":[{"id":"fofa-parent","name":"Fixture Parent","domains":[]},{"id":"fofa-child","name":"Fixture Subsidiary","domains":[]}]}`), nil
			})
			remote.AddTool(&mcp.Tool{Name: "mcp_company_relation", InputSchema: schema}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				relations.Add(1)
				var body map[string]any
				_ = json.Unmarshal([]byte(easmCompanyReply), &body)
				body["padding"] = strings.Repeat("registry metadata ", 200)
				raw, _ := json.Marshal(body)
				result := easmOrganizationResult(string(raw))
				if progressive {
					result.StructuredContent = body
				}
				return result, nil
			})
			endpoint := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return remote }, nil))
			t.Cleanup(endpoint.Close)
			server := config.MCPServer{ID: "company-observations", Name: "Company observations", Transport: "streamable_http", URL: endpoint.URL, Enabled: true, TimeoutSeconds: 3}
			call := func(remoteName string) (string, string) {
				if progressive {
					_, proxy := mcpclient.ProxyNames(server.ID)
					return proxy, `{"name":"` + remoteName + `","arguments":` + easmCompanyArguments + `}`
				}
				return mcpclient.DirectToolName(server.ID, remoteName), easmCompanyArguments
			}
			organizationTool, organizationArguments := call("mcp_fofa_org_lookup")
			relationTool, relationArguments := call("mcp_company_relation")
			var e *Engine
			var threadID, panelID string
			var initialRevision int64
			ready, release := make(chan struct{}, 1), make(chan struct{})
			var releaseOnce sync.Once
			defer releaseOnce.Do(func() { close(release) })
			check := func() {
				panel, err := e.store.TaskPanel(t.Context(), threadID, panelID)
				if err != nil {
					t.Errorf("read committed company hierarchy: %v", err)
					return
				}
				var graph struct {
					Nodes []struct{ ID, Kind, Label string }
					Edges []struct {
						Source, Target string
						Confirmed      bool
					}
				}
				_ = json.Unmarshal(panel.State, &graph)
				if panel.Revision != initialRevision+2 || len(graph.Nodes) != expectedNodes || len(graph.Edges) != expectedEdges {
					t.Errorf("company hierarchy was not committed before model input: revision=%d nodes=%d edges=%d", panel.Revision, len(graph.Nodes), len(graph.Edges))
				}
				if strings.Contains(string(panel.State), "Outside Investor") || graph.Nodes[0].Kind != "root" || graph.Nodes[0].Label != subject {
					t.Error("a registry result changed the brand identity or admitted an investor")
				}
				parent := "company:fofa-org:fofa-parent"
				if legalSubject {
					parent = "root"
				}
				children := map[string]bool{"company:fofa-org:fofa-child": false, "company:registry:registry-other-id": false}
				for _, edge := range graph.Edges {
					if edge.Source == "root" && edge.Confirmed && !legalSubject {
						t.Error("the brand association became a confirmed parent company")
					}
					if _, expected := children[edge.Target]; edge.Source == parent && expected && edge.Confirmed {
						children[edge.Target] = true
					}
				}
				for child, found := range children {
					if !found {
						t.Errorf("legal parent lost its direct investment in %s", child)
					}
				}
			}
			e, _ = newScheduleCompletionHTTPEngine(t, func(w http.ResponseWriter, r *http.Request, step int, request scheduleCompletionRequest) {
				switch step {
				case 1:
					panelAdmissionCall(w, request, "company-open", "open_task_panel", `{"plugin_id":"easm-asset-portrait","subject":"`+subject+`"}`)
				case 2:
					var opened taskpanel.Panel
					_ = json.Unmarshal([]byte(nuwaValidationReceipt(request, "company-open")), &opened)
					panelID, initialRevision = opened.ID, opened.Revision
					panelAdmissionCall(w, request, "company-org", organizationTool, organizationArguments)
				case 3:
					panelAdmissionCall(w, request, "company-relation", relationTool, relationArguments)
				case 4:
					check()
					panelAdmissionCall(w, request, "company-relation-duplicate", relationTool, relationArguments)
				case 5:
					check()
					ready <- struct{}{}
					select {
					case <-release:
						writeContextFlowReply(w, request.Stream, map[string]any{"content": "The observed company hierarchy is available."}, "stop")
					case <-r.Context().Done():
					}
				default:
					t.Errorf("unexpected company model request %d", step)
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
			thread, err := e.CreateThread("Company evidence", "", "")
			if err != nil {
				t.Fatal(err)
			}
			threadID = thread.ID
			turn, err := e.StartTurn(thread.ID, "Display the supplied legal entities and their recorded relationships.")
			if err != nil {
				t.Fatal(err)
			}
			select {
			case <-ready:
			case <-time.After(15 * time.Second):
				t.Fatal("the company hierarchy waited for another model publication")
			}
			pending, _ := e.store.GetTurn(turn.ID)
			if pending.Status != store.TurnRunning || relations.Load() != 2 {
				t.Fatalf("registry observations depended on task completion: status=%s calls=%d", pending.Status, relations.Load())
			}
			releaseOnce.Do(func() { close(release) })
			finished := waitForTurn(t, e, turn.ID)
			waitSettled(t, e, thread.ID)
			if finished.Status != store.TurnDone {
				t.Fatal(finished.Error)
			}
		})
	}
}
