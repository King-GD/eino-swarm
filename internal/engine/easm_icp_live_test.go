package engine

import (
	"context"
	"encoding/json"
	"fmt"
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

// ICP evidence must paint its actual source path before another model request.
func TestEASMICPDomainsAppearThroughTheirRegistrationBeforeModelPublication(t *testing.T) {
	for _, mode := range []string{"direct", "progressive"} {
		t.Run(mode, func(t *testing.T) {
			var lookups atomic.Int32
			remote := mcp.NewServer(&mcp.Implementation{Name: "registration-fixture", Version: "test"}, nil)
			remote.AddTool(&mcp.Tool{Name: "mcp_icp_query", InputSchema: map[string]any{"type": "object"}}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				records := []any{map[string]any{"org": "Fixture Legal Entity", "icp": "Fixture ICP 42", "domain": "first.example.test"}}
				if lookups.Add(1) > 1 {
					records = append(records, map[string]any{"org": "Fixture Legal Entity", "icp": "Fixture ICP 42", "domain": "second.example.test"})
				}
				body := map[string]any{"records": records, "padding": strings.Repeat("source metadata ", 200)}
				raw, _ := json.Marshal(body)
				result := &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(raw)}}}
				if mode == "progressive" {
					result.StructuredContent = body
				}
				return result, nil
			})
			endpoint := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return remote }, nil))
			t.Cleanup(endpoint.Close)
			server := config.MCPServer{ID: "registration-observations", Name: "Registration observations", Transport: "streamable_http", URL: endpoint.URL, Enabled: true, TimeoutSeconds: 3}
			lookup, arguments := mcpclient.DirectToolName(server.ID, "mcp_icp_query"), `{}`
			if mode == "progressive" {
				_, lookup = mcpclient.ProxyNames(server.ID)
				arguments = `{"name":"mcp_icp_query","arguments":{}}`
			}
			var e *Engine
			var threadID, panelID string
			var rootRevision int64
			ready, release := make(chan struct{}, 1), make(chan struct{})
			var releaseOnce sync.Once
			defer releaseOnce.Do(func() { close(release) })
			check := func(domainCount int, revision int64) {
				panel, err := e.store.TaskPanel(t.Context(), threadID, panelID)
				if err != nil {
					t.Errorf("read committed registration path: %v", err)
					return
				}
				var graph struct {
					Nodes []struct {
						ID, Kind, Label, FeatureKind string
						Detail                       struct {
							SubjectName string `json:"subject_name"`
							Reason      string
							Features    []struct {
								Kind, Value string
								NodeID      string `json:"node_id"`
							}
						}
					}
					Edges []struct {
						Source, Target string
						Confirmed      bool
					}
				}
				_ = json.Unmarshal(panel.State, &graph)
				if panel.Revision != revision || len(graph.Nodes) != 3+domainCount || len(graph.Edges) != 2+domainCount {
					t.Errorf("ICP path not committed before model input: revision=%d nodes=%d edges=%d", panel.Revision, len(graph.Nodes), len(graph.Edges))
				}
				kinds := make(map[string]string)
				for _, node := range graph.Nodes {
					kinds[node.ID] = node.Kind
					if node.Kind == "domain" && (len(node.Detail.Features) != 1 || node.Detail.Features[0].Kind != "icp" || node.Detail.Features[0].NodeID != "feature:icp:Fixture ICP 42") {
						t.Errorf("domain card cannot return to the source registration: %+v", node)
					}
					if node.Kind == "feature" && (node.FeatureKind != "icp" || node.Label != "Fixture ICP 42" || node.Detail.SubjectName != "Fixture Legal Entity" || !strings.Contains(node.Detail.Reason, "ICP 查询记录")) {
						t.Errorf("registration identity changed: %+v", node)
					}
				}
				for _, edge := range graph.Edges {
					from, to := kinds[edge.Source], kinds[edge.Target]
					if from == "root" && (to != "company" || edge.Confirmed) || from == "company" && (to != "feature" || !edge.Confirmed) || from == "feature" && (to != "domain" || !edge.Confirmed) {
						t.Errorf("ICP evidence was flattened into the wrong relationship: %+v", edge)
					}
				}
			}
			e, _ = newScheduleCompletionHTTPEngine(t, func(w http.ResponseWriter, r *http.Request, step int, request scheduleCompletionRequest) {
				switch step {
				case 1:
					panelAdmissionCall(w, request, "registration-open", "open_task_panel", `{"plugin_id":"easm-asset-portrait","subject":"Fixture"}`)
				case 2:
					var panel taskpanel.Panel
					if err := json.Unmarshal([]byte(nuwaValidationReceipt(request, "registration-open")), &panel); err != nil || panel.ID == "" {
						t.Errorf("open registration panel: %v", err)
					}
					panelID, rootRevision = panel.ID, panel.Revision
					panelAdmissionCall(w, request, "registration-1", lookup, arguments)
				case 3:
					check(1, rootRevision+1)
					panelAdmissionCall(w, request, "registration-2", lookup, arguments)
				case 4:
					check(2, rootRevision+2)
					panelAdmissionCall(w, request, "registration-duplicate", lookup, arguments)
				case 5:
					check(2, rootRevision+2)
					ready <- struct{}{}
					select {
					case <-release:
						writeContextFlowReply(w, request.Stream, map[string]any{"content": "Registration observations are available."}, "stop")
					case <-r.Context().Done():
					}
				default:
					t.Errorf("unexpected model request %d", step)
					writeContextFlowReply(w, request.Stream, map[string]any{"content": fmt.Sprint(step)}, "stop")
				}
			})
			e.cfg.Memory.Enabled, e.cfg.Memory.AutoReview, e.cfg.Swarm.ComposerPredictions = false, false, false
			e.cfg.Tools.ResultMaxChars = 1024
			e.cfg.MCP.Servers = []config.MCPServer{server}
			e.cfg.MCP.DirectLoadPercent = 100
			if mode == "progressive" {
				e.cfg.MCP.DirectLoadPercent = 0
			}
			thread, err := e.CreateThread("Registration evidence", "", "")
			if err != nil {
				t.Fatal(err)
			}
			threadID = thread.ID
			turn, err := e.StartTurn(thread.ID, "Collect the supplied registration evidence and display its source relationships.")
			if err != nil {
				t.Fatal(err)
			}
			select {
			case <-ready:
			case <-time.After(15 * time.Second):
				t.Fatal("ICP batches did not reach the graph")
			}
			pending, _ := e.store.GetTurn(turn.ID)
			if pending.Status != store.TurnRunning || lookups.Load() != 3 {
				t.Fatalf("ICP rendering required task completion: status=%s calls=%d", pending.Status, lookups.Load())
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
