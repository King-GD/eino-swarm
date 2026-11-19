package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
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

// Each real MCP result must reach the graph before the next model request.
// Holding the final response proves updates do not depend on task completion.
func TestEASMDrawsKnownAssetsBeforeTheTaskFinishes(t *testing.T) {
	for _, mode := range []string{"direct", "progressive", "shorthand-direct", "same-name-direct"} {
		t.Run(mode, func(t *testing.T) {
			const subject = "Fixture research institution"
			candidates := mode == "shorthand-direct" || mode == "same-name-direct"
			firstNodes, firstEdges, secondNodes, secondEdges := 2, 1, 3, 2
			if candidates {
				firstNodes, firstEdges, secondNodes, secondEdges = 5, 5, 6, 6
			}
			var lookups atomic.Int32
			siblingStarted := make(chan struct{})
			releaseSibling := make(chan struct{})
			var siblingOnce sync.Once
			defer siblingOnce.Do(func() { close(releaseSibling) })
			remote := mcp.NewServer(&mcp.Implementation{Name: "asset-fixture", Version: "test"}, nil)
			remote.AddTool(&mcp.Tool{
				Name: "mcp_fofa_org_lookup", Description: "Find observed organization domains",
				InputSchema: map[string]any{"type": "object", "properties": map[string]any{"keyword": map[string]any{"type": "string"}}, "required": []string{"keyword"}},
			}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				domains := []map[string]string{{"domain": "first.example.test"}}
				if lookups.Add(1) > 1 {
					domains = append(domains, map[string]string{"domain": "second.example.test"})
				}
				data := map[string]any{"padding": strings.Repeat("unrelated metadata ", 200), "results": []any{
					map[string]any{"id": "fixture-subject", "name": subject, "domains": domains},
				}}
				if candidates {
					north, south := "Fixture North Legal Entity", "Fixture South Legal Entity"
					if mode == "same-name-direct" {
						north, south = subject, subject
					}
					data["results"] = []any{
						map[string]any{"id": "north", "name": north, "domains": append(domains, map[string]string{"domain": "shared.example.test"})},
						map[string]any{"id": "south", "name": south, "domains": []map[string]string{{"domain": "shared.example.test"}}},
					}
				}
				raw, _ := json.Marshal(data)
				result := &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(raw)}}}
				if mode == "progressive" {
					result.StructuredContent = data
				}
				return result, nil
			})
			remote.AddTool(&mcp.Tool{Name: "hold_observation", InputSchema: map[string]any{"type": "object"}}, func(ctx context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				close(siblingStarted)
				select {
				case <-releaseSibling:
					return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "The independent observation finished."}}}, nil
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			})
			endpoint := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return remote }, nil))
			t.Cleanup(endpoint.Close)
			server := config.MCPServer{ID: "asset-observations", Name: "Asset observations", Transport: "streamable_http", URL: endpoint.URL, Enabled: true, TimeoutSeconds: 3}
			_, proxy := mcpclient.ProxyNames(server.ID)
			lookup := mcpclient.DirectToolName(server.ID, "mcp_fofa_org_lookup")
			lookupArgs := `{"keyword":"` + subject + `"}`
			if mode == "progressive" {
				lookup = proxy
				lookupArgs = `{"name":"mcp_fofa_org_lookup","arguments":` + lookupArgs + `}`
			}

			var e *Engine
			var panelID, threadID string
			var rootRevision int64
			ready := make(chan taskpanel.Panel, 1)
			release := make(chan struct{})
			var releaseOnce sync.Once
			defer releaseOnce.Do(func() { close(release) })
			checkGraph := func(nodes, edges int, revision int64) taskpanel.Panel {
				panel, err := e.store.TaskPanel(t.Context(), threadID, panelID)
				if err != nil {
					t.Errorf("read committed graph: %v", err)
					return panel
				}
				var state struct {
					Nodes []struct{ ID, Kind, Label string }
					Edges []struct {
						Source, Target string
						Confirmed      bool
					}
					Summary struct {
						Clues int `json:"clues"`
					}
				}
				if err := json.Unmarshal(panel.State, &state); err != nil {
					t.Errorf("decode committed graph: %v", err)
				}
				if panel.Revision != revision || len(state.Nodes) != nodes || len(state.Edges) != edges {
					t.Errorf("committed graph revision=%d nodes=%d edges=%d, want %d/%d/%d", panel.Revision, len(state.Nodes), len(state.Edges), revision, nodes, edges)
				}
				if len(state.Nodes) == 0 || state.Nodes[0].Kind != "root" || state.Nodes[0].Label != subject {
					t.Errorf("known subject missing from graph: %s", panel.State)
				}
				sharedOwners, companies := 0, 0
				for _, node := range state.Nodes {
					if node.Kind == "company" {
						companies++
					}
				}
				for _, edge := range state.Edges {
					if candidates && edge.Source == state.Nodes[0].ID {
						if edge.Confirmed {
							t.Error("a candidate legal entity was confirmed as belonging to the target")
						}
					} else if !edge.Confirmed {
						t.Error("direct organization domain evidence lost its confirmation")
					}
					if edge.Target == "domain:shared.example.test" {
						sharedOwners++
					}
				}
				if candidates && nodes > 1 && (companies != 2 || sharedOwners != 2) {
					t.Errorf("candidate identities or shared-domain associations were lost: companies=%d shared=%d", companies, sharedOwners)
				}
				if revision > rootRevision && state.Summary.Clues != 7 {
					t.Error("automatic graph update erased the separately published summary")
				}
				return panel
			}
			e, _ = newScheduleCompletionHTTPEngine(t, func(w http.ResponseWriter, r *http.Request, step int, request scheduleCompletionRequest) {
				switch step {
				case 1:
					panelAdmissionCall(w, request, "easm-open", "open_task_panel", `{"plugin_id":"easm-asset-portrait","subject":"`+subject+`"}`)
				case 2:
					var opened taskpanel.Panel
					if err := json.Unmarshal([]byte(nuwaValidationReceipt(request, "easm-open")), &opened); err != nil || opened.ID == "" {
						t.Errorf("open did not return a seeded panel: %v", err)
						writeContextFlowReply(w, request.Stream, map[string]any{"content": "Fixture failed."}, "stop")
						return
					}
					panelID, rootRevision = opened.ID, opened.Revision
					checkGraph(1, 0, rootRevision)
					panelAdmissionCall(w, request, "easm-summary", "publish_panel_update", fmt.Sprintf(`{"protocol_version":1,"panel_id":%q,"operation_id":"summary","base_revision":%d,"kind":"patch","operations":[{"op":"add","path":"/summary","value":{"clues":7}}]}`, panelID, rootRevision))
				case 3:
					checkGraph(1, 0, rootRevision+1)
					if mode != "progressive" {
						writeContextFlowReply(w, request.Stream, map[string]any{"tool_calls": []any{
							map[string]any{"index": 0, "id": "easm-lookup-1", "type": "function", "function": map[string]any{"name": lookup, "arguments": lookupArgs}},
							map[string]any{"index": 1, "id": "easm-slow-observation", "type": "function", "function": map[string]any{"name": mcpclient.DirectToolName(server.ID, "hold_observation"), "arguments": `{}`}},
						}}, "tool_calls")
					} else {
						panelAdmissionCall(w, request, "easm-lookup-1", lookup, lookupArgs)
					}
				case 4:
					checkGraph(firstNodes, firstEdges, rootRevision+2)
					var capped struct{ Truncated bool }
					if json.Unmarshal([]byte(nuwaValidationReceipt(request, "easm-lookup-1")), &capped) != nil || !capped.Truncated {
						t.Error("the fixture did not exercise publication before MCP result truncation")
					}
					panelAdmissionCall(w, request, "easm-lookup-2", lookup, lookupArgs)
				case 5:
					checkGraph(secondNodes, secondEdges, rootRevision+3)
					panelAdmissionCall(w, request, "easm-lookup-duplicate", lookup, lookupArgs)
				case 6:
					ready <- checkGraph(secondNodes, secondEdges, rootRevision+3)
					select {
					case <-release:
						writeContextFlowReply(w, request.Stream, map[string]any{"content": "The observations are available."}, "stop")
					case <-r.Context().Done():
					}
				default:
					t.Errorf("unexpected model request %d", step)
					writeContextFlowReply(w, request.Stream, map[string]any{"content": "Fixture failed."}, "stop")
				}
			})
			e.cfg.Memory.Enabled, e.cfg.Memory.AutoReview = false, false
			e.cfg.Swarm.ComposerPredictions = false
			e.cfg.Tools.ResultMaxChars = 1024
			e.cfg.MCP.Servers = []config.MCPServer{server}
			e.cfg.MCP.DirectLoadPercent = 100
			if mode == "progressive" {
				e.cfg.MCP.DirectLoadPercent = 0
			}
			thread, err := e.CreateThread("Live asset observations", "", "")
			if err != nil {
				t.Fatal(err)
			}
			threadID = thread.ID
			turn, err := e.StartTurn(thread.ID, "Inspect the supplied institution and keep the observed asset graph current.")
			if err != nil {
				t.Fatal(err)
			}
			if mode != "progressive" {
				select {
				case <-siblingStarted:
				case <-time.After(10 * time.Second):
					t.Fatal("the independent tool did not start alongside the organization lookup")
				}
				ordinaryEventually(t, func() bool {
					panel, err := e.store.TaskPanel(t.Context(), thread.ID, panelID)
					return err == nil && panel.Revision == rootRevision+2
				}, "the first graph batch waited for an unrelated slow tool")
				checkGraph(firstNodes, firstEdges, rootRevision+2)
				siblingOnce.Do(func() { close(releaseSibling) })
			}
			select {
			case panel := <-ready:
				if lookups.Load() != 3 || panel.Source.ToolCallID != "easm-lookup-2" {
					t.Fatalf("duplicate evidence changed the committed publisher: calls=%d source=%+v", lookups.Load(), panel.Source)
				}
			case <-time.After(15 * time.Second):
				t.Fatal("the graph did not receive the MCP batches while the final answer was pending")
			}
			pending, err := e.store.GetTurn(turn.ID)
			if err != nil || pending.Status != store.TurnRunning {
				t.Fatalf("updates required task completion: turn=%+v err=%v", pending, err)
			}
			releaseOnce.Do(func() { close(release) })
			finished := waitForTurn(t, e, turn.ID)
			waitSettled(t, e, thread.ID)
			if finished.Status != store.TurnDone {
				t.Fatalf("asset task failed: %s", finished.Error)
			}
			checkGraph(secondNodes, secondEdges, rootRevision+3)
		})
	}
}

// The subject can become known after other sections already have observations.
// Reopening must add only the missing root and retain an existing root identity.
func TestEASMAddsTheLateSubjectWithoutErasingObservedSections(t *testing.T) {
	const subject = "Fixture organization"
	var e *Engine
	var panelID, threadID string
	var openedRevision int64
	check := func(wantNodes int, wantRevision int64) taskpanel.Panel {
		panel, err := e.store.TaskPanel(t.Context(), threadID, panelID)
		if err != nil {
			t.Errorf("read panel: %v", err)
			return panel
		}
		var state struct {
			Title string
			Nodes []struct{ ID, Kind, Label string }
			Heat  []struct {
				Date  string
				Value int
			}
		}
		if err := json.Unmarshal(panel.State, &state); err != nil {
			t.Error(err)
		}
		if panel.Revision != wantRevision || len(state.Nodes) != wantNodes {
			t.Errorf("revision=%d nodes=%d, want %d/%d", panel.Revision, len(state.Nodes), wantRevision, wantNodes)
		}
		if len(state.Heat) != 1 || state.Heat[0].Date != "2026-01-01" || state.Heat[0].Value != 4 {
			t.Error("reopening lost the existing heat observation")
		}
		if wantNodes == 1 && (state.Title != subject || state.Nodes[0].Kind != "root" || state.Nodes[0].ID == "" || state.Nodes[0].Label != subject) {
			t.Errorf("reopening changed the known subject: %s", panel.State)
		}
		return panel
	}
	e, _ = newScheduleCompletionHTTPEngine(t, func(w http.ResponseWriter, _ *http.Request, step int, request scheduleCompletionRequest) {
		switch step {
		case 1:
			panelAdmissionCall(w, request, "open-empty", "open_task_panel", `{"plugin_id":"easm-asset-portrait"}`)
		case 2:
			var opened taskpanel.Panel
			if err := json.Unmarshal([]byte(nuwaValidationReceipt(request, "open-empty")), &opened); err != nil || opened.ID == "" {
				t.Errorf("open empty panel: %v", err)
				writeContextFlowReply(w, request.Stream, map[string]any{"content": "Fixture failed."}, "stop")
				return
			}
			panelID, openedRevision = opened.ID, opened.Revision
			panelAdmissionCall(w, request, "publish-heat", "publish_panel_update", fmt.Sprintf(`{"protocol_version":1,"panel_id":%q,"operation_id":"heat","base_revision":%d,"kind":"patch","operations":[{"op":"replace","path":"/heat","value":[{"date":"2026-01-01","value":4}]}]}`, panelID, openedRevision))
		case 3:
			check(0, openedRevision+1)
			panelAdmissionCall(w, request, "open-with-subject", "open_task_panel", `{"plugin_id":"easm-asset-portrait","subject":"`+subject+`"}`)
		case 4:
			check(1, openedRevision+2)
			panelAdmissionCall(w, request, "reopen-other-subject", "open_task_panel", `{"plugin_id":"easm-asset-portrait","subject":"Different organization"}`)
		case 5:
			check(1, openedRevision+2)
			panelAdmissionCall(w, request, "invalid-subject", "open_task_panel", `{"plugin_id":"easm-asset-portrait","subject":"invalid\nsubject"}`)
		default:
			if !strings.Contains(nuwaValidationReceipt(request, "invalid-subject"), "EASM subject") {
				t.Error("invalid subject did not return a validation error")
			}
			check(1, openedRevision+2)
			writeContextFlowReply(w, request.Stream, map[string]any{"content": "The subject is available in the existing graph."}, "stop")
		}
	})
	e.cfg.Memory.Enabled, e.cfg.Memory.AutoReview = false, false
	e.cfg.Swarm.ComposerPredictions = false
	thread, err := e.CreateThread("Late asset subject", "", "")
	if err != nil {
		t.Fatal(err)
	}
	threadID = thread.ID
	turn, err := e.StartTurn(thread.ID, "Keep the available observations while establishing the subject.")
	if err != nil {
		t.Fatal(err)
	}
	finished := waitForTurn(t, e, turn.ID)
	waitSettled(t, e, thread.ID)
	if finished.Status != store.TurnDone {
		t.Fatalf("subject update failed: %s", finished.Error)
	}
	check(1, openedRevision+2)
}

// Continuing a known subject grants the new turn publication ownership without
// replacing the previous turn's graph, statistics or other observed sections.
func TestEASMContinuesTheSameSubjectInALaterTurn(t *testing.T) {
	const subject = "Fixture institute"
	remote := mcp.NewServer(&mcp.Implementation{Name: "continued-asset-fixture", Version: "test"}, nil)
	remote.AddTool(&mcp.Tool{Name: "mcp_fofa_org_lookup", InputSchema: map[string]any{"type": "object"}}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: `{"results":[{"name":"` + subject + `","domains":[{"domain":"old.example.test"},{"domain":"new.example.test"}]}]}`}}}, nil
	})
	endpoint := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return remote }, nil))
	t.Cleanup(endpoint.Close)
	server := config.MCPServer{ID: "continued-assets", Name: "Continued assets", Transport: "streamable_http", URL: endpoint.URL, Enabled: true, TimeoutSeconds: 3}
	var e *Engine
	var panelID, threadID string
	var previous taskpanel.Panel
	ready := make(chan taskpanel.Panel, 1)
	release := make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	e, _ = newScheduleCompletionHTTPEngine(t, func(w http.ResponseWriter, r *http.Request, step int, request scheduleCompletionRequest) {
		switch step {
		case 1:
			panelAdmissionCall(w, request, "initial-open", "open_task_panel", `{"plugin_id":"easm-asset-portrait","subject":"`+subject+`"}`)
		case 2:
			var opened taskpanel.Panel
			if err := json.Unmarshal([]byte(nuwaValidationReceipt(request, "initial-open")), &opened); err != nil || opened.ID == "" {
				t.Errorf("initial open receipt: %v", err)
				writeContextFlowReply(w, request.Stream, map[string]any{"content": "Fixture failed."}, "stop")
				return
			}
			panelID = opened.ID
			state := `{"schema_version":1,"title":"` + subject + `","nodes":[{"id":"root","kind":"root","label":"` + subject + `"},{"id":"original-domain","kind":"domain","label":"old.example.test","detail":{"reason":"Originally observed evidence"}}],"edges":[{"source":"root","target":"original-domain","confirmed":false}],"summary":{"clues":8},"heat":[{"date":"2026-01-01","value":5}],"components":[{"name":"fixture-component","count":3}]}`
			panelAdmissionCall(w, request, "initial-data", "publish_panel_update", fmt.Sprintf(`{"protocol_version":1,"panel_id":%q,"operation_id":"initial-data","base_revision":%d,"kind":"snapshot","state":%s}`, panelID, opened.Revision, state))
		case 3:
			var published taskpanel.Panel
			receipt := nuwaValidationReceipt(request, "initial-data")
			if err := json.Unmarshal([]byte(receipt), &published); err != nil || published.ID != panelID || published.Revision < 2 {
				t.Errorf("initial publication failed: %v receipt=%s", err, receipt)
				writeContextFlowReply(w, request.Stream, map[string]any{"content": "Fixture failed."}, "stop")
				return
			}
			var err error
			previous, err = e.store.TaskPanel(t.Context(), threadID, panelID)
			if err != nil || previous.Revision != published.Revision {
				t.Errorf("initial publication was not persisted: %v revision=%d receipt=%d", err, previous.Revision, published.Revision)
			}
			writeContextFlowReply(w, request.Stream, map[string]any{"content": "The initial observations are available."}, "stop")
		case 4:
			panelAdmissionCall(w, request, "continue-subject", "open_task_panel", `{"plugin_id":"easm-asset-portrait","subject":"`+subject+`"}`)
		case 5:
			panel, err := e.store.TaskPanel(t.Context(), threadID, panelID)
			var before, after any
			if err != nil || json.Unmarshal(previous.State, &before) != nil || json.Unmarshal(panel.State, &after) != nil || !reflect.DeepEqual(before, after) {
				t.Errorf("reopening the same subject changed retained observations: %v", err)
			}
			if panel.Revision != previous.Revision+1 || panel.Source.TurnID == previous.Source.TurnID || panel.Source.ToolCallID != "continue-subject" {
				t.Errorf("later turn did not claim the existing subject: previous=%+v current=%+v", previous.Source, panel.Source)
			}
			panelAdmissionCall(w, request, "continue-lookup", mcpclient.DirectToolName(server.ID, "mcp_fofa_org_lookup"), `{}`)
		case 6:
			panel, err := e.store.TaskPanel(t.Context(), threadID, panelID)
			if err != nil {
				t.Error(err)
			}
			ready <- panel
			select {
			case <-release:
				writeContextFlowReply(w, request.Stream, map[string]any{"content": "The graph includes the new observation."}, "stop")
			case <-r.Context().Done():
			}
		default:
			t.Errorf("unexpected continuation request %d", step)
			writeContextFlowReply(w, request.Stream, map[string]any{"content": "Fixture failed."}, "stop")
		}
	})
	e.cfg.Memory.Enabled, e.cfg.Memory.AutoReview = false, false
	e.cfg.Swarm.ComposerPredictions = false
	e.cfg.MCP.Servers = []config.MCPServer{server}
	e.cfg.MCP.DirectLoadPercent = 100
	thread, err := e.CreateThread("Continued asset subject", "", "")
	if err != nil {
		t.Fatal(err)
	}
	threadID = thread.ID
	first, err := e.StartTurn(thread.ID, "Record the initial observations for the subject.")
	if err != nil {
		t.Fatal(err)
	}
	if got := waitForTurn(t, e, first.ID); got.Status != store.TurnDone {
		t.Fatalf("initial turn failed: %s", got.Error)
	}
	waitSettled(t, e, thread.ID)
	second, err := e.StartTurn(thread.ID, "Continue observing the same subject.")
	if err != nil {
		t.Fatal(err)
	}
	select {
	case panel := <-ready:
		var state, oldState map[string]json.RawMessage
		if json.Unmarshal(panel.State, &state) != nil || json.Unmarshal(previous.State, &oldState) != nil {
			t.Fatal("invalid saved portrait")
		}
		for _, section := range []string{"title", "summary", "heat", "components"} {
			var current, old any
			if json.Unmarshal(state[section], &current) != nil || json.Unmarshal(oldState[section], &old) != nil || !reflect.DeepEqual(current, old) {
				t.Errorf("continued lookup erased section %s", section)
			}
		}
		var nodes []struct{ ID, Kind, Label string }
		var edges []struct{ Source, Target string }
		if json.Unmarshal(state["nodes"], &nodes) != nil || json.Unmarshal(state["edges"], &edges) != nil || len(nodes) != 3 || len(edges) != 2 || nodes[1].ID != "original-domain" || nodes[2].Label != "new.example.test" {
			t.Errorf("later lookup did not extend the retained graph: %s", panel.State)
		}
		for _, section := range []string{"nodes", "edges"} {
			var current, old []any
			if json.Unmarshal(state[section], &current) != nil || json.Unmarshal(oldState[section], &old) != nil || len(current) < len(old) || !reflect.DeepEqual(current[:len(old)], old) {
				t.Errorf("continued lookup changed retained %s", section)
			}
		}
		if panel.Revision != previous.Revision+2 || panel.Source.TurnID != second.ID || panel.Source.ToolCallID != "continue-lookup" {
			t.Errorf("later lookup publication lost ownership: revision=%d source=%+v", panel.Revision, panel.Source)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the continued subject did not receive its lookup before the final response")
	}
	if pending, err := e.store.GetTurn(second.ID); err != nil || pending.Status != store.TurnRunning {
		t.Fatalf("continued graph waited for task completion: turn=%+v error=%v", pending, err)
	}
	releaseOnce.Do(func() { close(release) })
	if got := waitForTurn(t, e, second.ID); got.Status != store.TurnDone {
		t.Fatalf("continued turn failed: %s", got.Error)
	}
	waitSettled(t, e, thread.ID)
}

func TestEASMRejectsUpdatesWithoutRuntimeOwnership(t *testing.T) {
	e := newTestEngine(t)
	thread, err := e.CreateThread("Unowned asset observations", "", "")
	if err != nil {
		t.Fatal(err)
	}
	result := &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: `{"results":[{"name":"Fixture","domains":[{"domain":"example.test"}]}]}`}}}
	for _, ctx := range []context.Context{t.Context(), withHistoryReadOwner(t.Context(), thread.ID, "unowned-turn")} {
		e.updateEASMFromMCP(ctx, mcpclient.ToolCall{Name: "mcp_fofa_org_lookup"}, result)
	}
	e.updateEASMFromMCP(t.Context(), mcpclient.ToolCall{Name: "unrelated-tool"}, result)
	panels, err := e.store.TaskPanels(t.Context(), thread.ID)
	if err != nil || len(panels) != 0 {
		t.Fatalf("unowned evidence created a panel: panels=%v error=%v", panels, err)
	}
}

// A refused automatic publication retains the remote observation for the model
// and preserves the last accepted graph. Absence of a panel creates no binding.
func TestEASMUnavailablePublicationPreservesTheAcceptedGraph(t *testing.T) {
	for _, scenario := range []string{"no-panel", "update-budget"} {
		t.Run(scenario, func(t *testing.T) {
			const subject = "Fixture institution"
			remote := mcp.NewServer(&mcp.Implementation{Name: "refused-asset-fixture", Version: "test"}, nil)
			remote.AddTool(&mcp.Tool{Name: "mcp_fofa_org_lookup", InputSchema: map[string]any{"type": "object"}}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				domains := make([]map[string]string, 12)
				for i := range domains {
					domains[i] = map[string]string{"domain": fmt.Sprintf("domain-%d.example.test", i)}
				}
				raw, _ := json.Marshal(map[string]any{"results": []any{map[string]any{"name": subject, "domains": domains}}})
				return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(raw)}}}, nil
			})
			endpoint := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return remote }, nil))
			t.Cleanup(endpoint.Close)
			server := config.MCPServer{ID: "refused-assets", Name: "Refused assets", Transport: "streamable_http", URL: endpoint.URL, Enabled: true, TimeoutSeconds: 3}
			lookup := mcpclient.DirectToolName(server.ID, "mcp_fofa_org_lookup")
			var e *Engine
			var before taskpanel.Panel
			var threadID string
			e, _ = newScheduleCompletionHTTPEngine(t, func(w http.ResponseWriter, _ *http.Request, step int, request scheduleCompletionRequest) {
				if scenario == "no-panel" {
					step++
				}
				switch step {
				case 1:
					panelAdmissionCall(w, request, "refusal-open", "open_task_panel", `{"plugin_id":"easm-asset-portrait","subject":"`+subject+`"}`)
				case 2:
					if scenario == "update-budget" {
						var opened taskpanel.Panel
						if err := json.Unmarshal([]byte(nuwaValidationReceipt(request, "refusal-open")), &opened); err != nil || opened.ID == "" {
							t.Errorf("open receipt: %v", err)
						}
						var err error
						before, err = e.store.TaskPanel(t.Context(), threadID, opened.ID)
						if err != nil {
							t.Error(err)
						}
						if err := e.cfg.Update(func(cfg *config.Config) error {
							cfg.TaskPanels.MaxUpdateBytes, cfg.TaskPanels.MaxStateBytes = 1024, 1024
							return nil
						}); err != nil {
							t.Error(err)
						}
					}
					panelAdmissionCall(w, request, "refusal-lookup", lookup, `{}`)
				default:
					if !strings.Contains(nuwaValidationReceipt(request, "refusal-lookup"), "domain-11.example.test") {
						t.Error("a refused graph update removed the MCP evidence from the model")
					}
					writeContextFlowReply(w, request.Stream, map[string]any{"content": "The observation remains available."}, "stop")
				}
			})
			e.cfg.Memory.Enabled, e.cfg.Memory.AutoReview = false, false
			e.cfg.Swarm.ComposerPredictions = false
			e.cfg.MCP.Servers = []config.MCPServer{server}
			e.cfg.MCP.DirectLoadPercent = 100
			var logs bytes.Buffer
			e.log = slog.New(slog.NewTextHandler(&logs, nil))
			thread, err := e.CreateThread("Refused asset publication", "", "")
			if err != nil {
				t.Fatal(err)
			}
			threadID = thread.ID
			turn, err := e.StartTurn(thread.ID, "Inspect the available subject observations.")
			if err != nil {
				t.Fatal(err)
			}
			if got := waitForTurn(t, e, turn.ID); got.Status != store.TurnDone {
				t.Fatalf("the remote observation failed with its panel update: %s", got.Error)
			}
			waitSettled(t, e, thread.ID)
			panels, err := e.store.TaskPanels(t.Context(), thread.ID)
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "no-panel" {
				if len(panels) != 0 {
					t.Fatalf("an unbound lookup created a panel: %v", panels)
				}
			} else if len(panels) != 1 || panels[0].Revision != before.Revision || !bytes.Equal(panels[0].State, before.State) || panels[0].Source != before.Source {
				t.Fatalf("refused publication changed accepted graph: %v", panels)
			}
			e.Shutdown()
			warned := strings.Contains(logs.String(), "level=WARN msg=\"could not apply EASM graph update\"")
			if warned != (scenario == "update-budget") {
				t.Fatalf("publication failure warning=%t, scenario=%s", warned, scenario)
			}
		})
	}
}
