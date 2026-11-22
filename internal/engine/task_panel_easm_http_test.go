package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LubyRuffy/eino-swarm/internal/config"
	"github.com/LubyRuffy/eino-swarm/internal/mcpclient"
	"github.com/LubyRuffy/eino-swarm/internal/panelplugins/easm"
	"github.com/LubyRuffy/eino-swarm/internal/store"
	"github.com/LubyRuffy/eino-swarm/internal/taskpanel"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const easmFixtureQuery = `org="Subject Co"`

// easmFixtureServer offers the aggregations the producer owns. The heat tool
// answers; the stats tool refuses, so one failing aggregation cannot hide the
// other steps' results.
func easmFixtureServer(t *testing.T) config.MCPServer {
	t.Helper()
	remote := mcp.NewServer(&mcp.Implementation{Name: "easm-fixture", Version: "test"}, nil)
	remote.AddTool(&mcp.Tool{Name: "heat_fixture", InputSchema: map[string]any{"type": "object"}}, func(_ context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var args struct{ Query string }
		_ = json.Unmarshal(request.Params.Arguments, &args)
		raw, _ := json.Marshal(map[string]any{
			"query": args.Query,
			"hot_trend": []any{
				map[string]any{"date": "2026-01-02", "value": 3},
				map[string]any{"date": "2026-01-09", "value": 8},
			},
		})
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(raw)}}}, nil
	})
	remote.AddTool(&mcp.Tool{Name: "stats_fixture", InputSchema: map[string]any{"type": "object"}}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "quota exhausted"}}}, nil
	})
	endpoint := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return remote }, nil))
	t.Cleanup(endpoint.Close)
	return config.MCPServer{ID: "easm-fixture", Name: "EASM fixture", Transport: "streamable_http", URL: endpoint.URL, Enabled: true, TimeoutSeconds: 3}
}

// easmToolCall writes one tool call with its own id: a later call reusing an id
// would be the same operation, and the store refuses to apply it twice.
func easmToolCall(w http.ResponseWriter, request scheduleCompletionRequest, id, name, args string) {
	writeContextFlowReply(w, request.Stream, map[string]any{"tool_calls": []any{map[string]any{
		"index": 0, "id": id, "type": "function",
		"function": map[string]any{"name": name, "arguments": args},
	}}}, "tool_calls")
}

// Real model → Registry tool → EASM producer → MCP HTTP → durable panel. The
// agent pushes two batches of its own findings and asks for the aggregations on
// the second; a refused aggregation must stay inside its panel step so the
// turn still finishes and the graph the agent found is kept.
func TestEASMSourceMergesPushedBatchesThroughActualManagerHTTP(t *testing.T) {
	server := easmFixtureServer(t)
	var receipt string
	var panelID string
	e, _ := newScheduleCompletionHTTPEngine(t, func(w http.ResponseWriter, _ *http.Request, step int, request scheduleCompletionRequest) {
		switch step {
		case 1:
			easmToolCall(w, request, "easm-open", "open_task_panel", `{"plugin_id":"easm-asset-portrait"}`)
		case 2:
			var panel taskpanel.Panel
			if err := json.Unmarshal([]byte(request.Messages[len(request.Messages)-1].Content), &panel); err != nil {
				t.Error(err)
			}
			panelID = panel.ID
			input := map[string]any{
				"subject": "Subject Co",
				"companies": []any{
					map[string]any{"name": "Parent Co", "registry_id": "9", "relation": "investment", "confirmed": true, "source": "relation_tool"},
				},
				"domains": []any{
					map[string]any{"domain": "subject.test", "parent": "Parent Co", "confirmed": true, "source": "org_tool"},
				},
				"certificates": []any{
					map[string]any{"serial": "0a1b", "domain": "subject.test", "issuer": "CA", "subject_cn": "subject.test", "valid": true, "match": true, "source": "org_tool"},
				},
			}
			raw, err := json.Marshal(map[string]any{"panel_id": panel.ID, "input": input})
			if err != nil {
				t.Error(err)
			}
			easmToolCall(w, request, fmt.Sprintf("easm-batch-%d", step), "run_panel_source", string(raw))
		case 3:
			input := map[string]any{
				"subject": "Subject Co",
				"domains": []any{
					map[string]any{"domain": "child.test", "confirmed": false, "source": "branch_tool"},
				},
				"query": easmFixtureQuery,
			}
			raw, err := json.Marshal(map[string]any{"panel_id": panelID, "input": input})
			if err != nil {
				t.Error(err)
			}
			easmToolCall(w, request, fmt.Sprintf("easm-batch-%d", step), "run_panel_source", string(raw))
		default:
			receipt = request.Messages[len(request.Messages)-1].Content
			writeContextFlowReply(w, request.Stream, map[string]any{"content": "The portrait is in the panel."}, "stop")
		}
	})
	if err := e.cfg.Update(func(c *config.Config) error {
		c.MCP.Servers = []config.MCPServer{server}
		c.TaskPanels.EASM.ServerID = server.ID
		c.TaskPanels.EASM.Tools = map[string]string{"stats": "stats_fixture", "heat": "heat_fixture", "product_rules": ""}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	thread, err := e.CreateThread("Portrait fixture", "", "")
	if err != nil {
		t.Fatal(err)
	}
	turn, err := e.StartTurn(thread.ID, "Build the portrait of the confirmed subject.")
	if err != nil {
		t.Fatal(err)
	}
	if got := waitForTurn(t, e, turn.ID); got.Status != store.TurnDone {
		t.Fatalf("turn = %+v", got)
	}
	waitSettled(t, e, thread.ID)
	panels, err := e.store.TaskPanels(t.Context(), thread.ID)
	if err != nil || len(panels) != 1 {
		t.Fatalf("panels=%v err=%v", panels, err)
	}
	var state easm.State
	if err := json.Unmarshal(panels[0].State, &state); err != nil {
		t.Fatal(err)
	}
	if state.Title != "Subject Co" || state.Query != easmFixtureQuery || state.Collection.Status != "done" {
		t.Fatalf("state = %s", panels[0].State)
	}
	// Both batches reached the durable panel, not just the last one.
	for _, id := range []string{"company:reg:9", "domain:subject.test", "domain:child.test", "feature:cert:0a1b@CA"} {
		found := false
		for _, node := range state.Nodes {
			found = found || node.ID == id
		}
		if !found {
			t.Fatalf("%s is missing from %s", id, panels[0].State)
		}
	}
	statuses := map[easm.Step]easm.StepStatus{}
	for _, status := range state.Collection.Steps {
		statuses[status.ID] = status
	}
	if statuses[easm.StepHeat] != (easm.StepStatus{ID: easm.StepHeat, Status: "done"}) || len(state.Heat) != 2 {
		t.Fatalf("heat = %+v %+v", statuses[easm.StepHeat], state.Heat)
	}
	for _, step := range []easm.Step{easm.StepSummary, easm.StepComponents} {
		if got := statuses[step]; got.Status != "failed" || got.Code != "tool_error" {
			t.Fatalf("%s = %+v", step, got)
		}
	}
	var result struct {
		OK bool `json:"ok"`
	}
	if json.Unmarshal([]byte(receipt), &result) != nil || !result.OK {
		t.Fatalf("receipt = %s", receipt)
	}
}

// Transport outcomes map to step outcomes: a missing tool is unavailable, a
// remote refusal keeps only its code, and no server means unavailable.
func TestEASMCallerMapsTransportOutcomes(t *testing.T) {
	server := easmFixtureServer(t)
	manager := mcpclient.NewManager()
	defer manager.Close()
	caller := &easmCaller{manager: manager, servers: []config.MCPServer{server}, serverID: server.ID, workspace: t.TempDir()}
	if _, err := caller.Call(t.Context(), "absent_tool", nil); err != easm.ErrUnavailable {
		t.Fatalf("missing tool err=%v", err)
	}
	_, err := caller.Call(t.Context(), "stats_fixture", nil)
	if failure, ok := err.(*easm.ToolError); !ok || failure.Code != "tool_error" {
		t.Fatalf("refusal err=%v", err)
	}
	if body, err := caller.Call(t.Context(), "heat_fixture", map[string]any{"query": easmFixtureQuery}); err != nil || len(body) == 0 {
		t.Fatalf("body=%s err=%v", body, err)
	}
	for _, unresolved := range []*easmCaller{
		{manager: manager, servers: []config.MCPServer{server}, serverID: "other"},
		{manager: manager, servers: []config.MCPServer{server}},
		{servers: []config.MCPServer{server}, serverID: server.ID},
	} {
		if _, err := unresolved.Call(t.Context(), "heat_fixture", nil); err != easm.ErrUnavailable {
			t.Fatalf("unresolved server err=%v", err)
		}
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := caller.Call(cancelled, "heat_fixture", nil); err != context.Canceled {
		t.Fatalf("cancelled err=%v", err)
	}
}
