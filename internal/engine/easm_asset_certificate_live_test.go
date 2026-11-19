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

// A TLS lookup is already structured evidence. Both tool surfaces must publish
// it before the next model input, without waiting for a model-authored patch.
func TestEASMAssetCertificateBatchesAppearBeforeModelPublication(t *testing.T) {
	for _, mode := range []string{"direct", "progressive"} {
		t.Run(mode, func(t *testing.T) {
			var calls atomic.Int32
			remote := mcp.NewServer(&mcp.Implementation{Name: "tls-fixture", Version: "test"}, nil)
			remote.AddTool(&mcp.Tool{Name: "mcp_fofa_asset_lookup", InputSchema: map[string]any{"type": "object"}}, func(_ context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				var args struct{ Hosts, Include []string }
				if json.Unmarshal(request.Params.Arguments, &args) != nil || len(args.Hosts) != 4 || len(args.Include) != 1 || args.Include[0] != "tls" {
					t.Error("asset lookup lost its explicit TLS request")
				}
				rows := []json.RawMessage{json.RawMessage(easmAssetCertificateDNSRow)}
				if calls.Add(1) > 1 {
					mixed := strings.ReplaceAll(easmAssetCertificateDNSRow, `"host":"https://service.example.test"`, `"host":"https://alternate.example.test"`)
					mixed = strings.ReplaceAll(mixed, `"is_match":false`, `"is_match":true`)
					rows = append(rows, json.RawMessage(mixed))
					rows = append(rows, json.RawMessage(easmAssetCertificateIPRow))
					third := strings.ReplaceAll(easmAssetCertificateIPRow, "serial-ip", "serial-third")
					third = strings.ReplaceAll(third, "203.0.113.7", "203.0.113.8")
					rows = append(rows, json.RawMessage(third))
				}
				body := map[string]any{"results": rows, "size": len(rows), "total": len(rows), "page": 1, "next_cursor": nil, "padding": strings.Repeat("TLS source evidence ", 300)}
				raw, _ := json.Marshal(body)
				result := &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(raw)}}}
				if mode == "progressive" {
					result.StructuredContent = body
				}
				return result, nil
			})
			endpoint := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return remote }, nil))
			t.Cleanup(endpoint.Close)
			server := config.MCPServer{ID: "tls-observations", Name: "TLS observations", Transport: "streamable_http", URL: endpoint.URL, Enabled: true, TimeoutSeconds: 5}
			name := mcpclient.DirectToolName(server.ID, "mcp_fofa_asset_lookup")
			var args any = map[string]any{"hosts": []string{"https://service.example.test", "https://alternate.example.test", "https://203.0.113.7:443", "https://203.0.113.8:443"}, "include": []string{"tls"}, "size": 4}
			if mode == "progressive" {
				_, name = mcpclient.ProxyNames(server.ID)
				args = map[string]any{"name": "mcp_fofa_asset_lookup", "arguments": args}
			}
			arguments, _ := json.Marshal(args)
			var e *Engine
			var threadID, panelID string
			var rootRevision int64
			ready, release := make(chan struct{}, 1), make(chan struct{})
			var once sync.Once
			defer once.Do(func() { close(release) })
			check := func(batch int, revision int64) {
				panel, err := e.store.TaskPanel(t.Context(), threadID, panelID)
				if err != nil {
					t.Error(err)
					return
				}
				var graph easmAssetCertificateGraph
				_ = json.Unmarshal(panel.State, &graph)
				wantNodes, wantEdges := 4, 3
				if batch == 2 {
					wantNodes, wantEdges = 8, 9
				}
				if panel.Revision != revision || len(graph.Nodes) != wantNodes || len(graph.Edges) != wantEdges {
					t.Errorf("TLS batch waited for model publication: revision=%d state=%s", panel.Revision, panel.State)
				}
				certificates := 0
				for _, node := range graph.Nodes {
					if node.FeatureKind == "cert" {
						certificates++
						wantValid := "true"
						wantMatch, wantObservations := "false", 1
						if node.Detail.Serial != "serial-dns" {
							wantValid = "false"
						} else if batch == 2 {
							wantMatch, wantObservations = "", 2
						}
						if string(node.Detail.IsValid) != wantValid || string(node.Detail.IsMatch) != wantMatch || len(node.Detail.Observations) != wantObservations || !strings.Contains(node.Detail.Reason, "mcp_fofa_asset_lookup") {
							t.Errorf("TLS source flags were inferred or dropped: %+v", node)
						}
						for _, observed := range node.Detail.Observations {
							expectedMatch := "false"
							if observed.Host == "https://alternate.example.test" {
								expectedMatch = "true"
							}
							if observed.Source != "mcp_fofa_asset_lookup" || string(observed.IsValid) != wantValid || string(observed.IsMatch) != expectedMatch || string(observed.IsExpired) != "false" {
								t.Errorf("TLS service observation lost a source flag: %+v", observed)
							}
						}
					}
				}
				if certificates != 2*batch-1 {
					t.Errorf("TLS response did not paint all certificate observations: %s", panel.State)
				}
				for _, edge := range graph.Edges {
					if edge.Confirmed || edge.Source == "root" && strings.HasPrefix(edge.Target, "domain:") {
						t.Error("TLS evidence fabricated confirmed deployment or target ownership")
					}
				}
			}
			e, _ = newScheduleCompletionHTTPEngine(t, func(w http.ResponseWriter, request *http.Request, step int, input scheduleCompletionRequest) {
				switch step {
				case 1:
					panelAdmissionCall(w, input, "tls-open", "open_task_panel", `{"plugin_id":"easm-asset-portrait","subject":"Fixture"}`)
				case 2:
					var opened taskpanel.Panel
					if err := json.Unmarshal([]byte(nuwaValidationReceipt(input, "tls-open")), &opened); err != nil || opened.ID == "" {
						t.Errorf("open TLS panel: %v", err)
					}
					panelID, rootRevision = opened.ID, opened.Revision
					panelAdmissionCall(w, input, "tls-1", name, string(arguments))
				case 3:
					check(1, rootRevision+1)
					var capped struct{ Truncated bool }
					if json.Unmarshal([]byte(nuwaValidationReceipt(input, "tls-1")), &capped) != nil || !capped.Truncated {
						t.Error("TLS fixture did not cross model result capping")
					}
					panelAdmissionCall(w, input, "tls-2", name, string(arguments))
				case 4:
					check(2, rootRevision+2)
					panelAdmissionCall(w, input, "tls-repeat", name, string(arguments))
				case 5:
					check(2, rootRevision+2)
					ready <- struct{}{}
					select {
					case <-release:
						writeContextFlowReply(w, input.Stream, map[string]any{"content": "TLS observations are available."}, "stop")
					case <-request.Context().Done():
					}
				default:
					t.Errorf("unexpected TLS model request %d", step)
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
			thread, err := e.CreateThread("TLS source observations", "", "")
			if err != nil {
				t.Fatal(err)
			}
			threadID = thread.ID
			turn, err := e.StartTurn(thread.ID, "Collect the supplied TLS evidence and retain its source status.")
			if err != nil {
				t.Fatal(err)
			}
			select {
			case <-ready:
			case <-time.After(10 * time.Second):
				t.Fatal("TLS observations did not reach the final-response gate")
			}
			if pending, err := e.store.GetTurn(turn.ID); err != nil || pending.Status != store.TurnRunning || calls.Load() != 3 {
				t.Fatalf("TLS projection depended on task completion: turn=%+v error=%v calls=%d", pending, err, calls.Load())
			}
			once.Do(func() { close(release) })
			if done := waitForTurn(t, e, turn.ID); done.Status != store.TurnDone {
				t.Fatal(done.Error)
			}
			waitSettled(t, e, thread.ID)
		})
	}
}
