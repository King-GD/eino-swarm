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

func easmFixtureServer(t *testing.T) config.MCPServer {
	t.Helper()
	remote := mcp.NewServer(&mcp.Implementation{Name: "easm-fixture", Version: "test"}, nil)
	remote.AddTool(&mcp.Tool{Name: "org_lookup_fixture", InputSchema: map[string]any{"type": "object"}}, func(_ context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var args struct{ Names []string }
		_ = json.Unmarshal(request.Params.Arguments, &args)
		raw, _ := json.Marshal(map[string]any{"results": []any{map[string]any{"id": "41", "name": args.Names[0], "domains": []any{map[string]string{"domain": "subject.test"}}}}})
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(raw)}}}, nil
	})
	remote.AddTool(&mcp.Tool{Name: "stats_fixture", InputSchema: map[string]any{"type": "object"}}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "quota exhausted"}}}, nil
	})
	endpoint := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return remote }, nil))
	t.Cleanup(endpoint.Close)
	return config.MCPServer{ID: "easm-fixture", Name: "EASM fixture", Transport: "streamable_http", URL: endpoint.URL, Enabled: true, TimeoutSeconds: 3}
}

// Real model -> Registry tool -> EASM producer -> MCP HTTP -> durable panel.
// A refused lookup must stay inside its panel step: the agent's turn still
// finishes normally and the shared task execution is not blocked.
func TestEASMSourceRunsThroughActualManagerHTTP(t *testing.T) {
	server := easmFixtureServer(t)
	var receipt string
	e, _ := newScheduleCompletionHTTPEngine(t, func(w http.ResponseWriter, _ *http.Request, step int, request scheduleCompletionRequest) {
		switch step {
		case 1:
			writeScheduleCompletionTool(w, request, "open_task_panel", `{"plugin_id":"easm-asset-portrait"}`)
		case 2:
			var panel taskpanel.Panel
			if err := json.Unmarshal([]byte(request.Messages[len(request.Messages)-1].Content), &panel); err != nil {
				t.Error(err)
			}
			writeScheduleCompletionTool(w, request, "run_panel_source", fmt.Sprintf(`{"panel_id":%q,"input":{"subject":"Subject Co","query":"org=\"Subject Co\""}}`, panel.ID))
		default:
			receipt = request.Messages[len(request.Messages)-1].Content
			writeContextFlowReply(w, request.Stream, map[string]any{"content": "The portrait is in the panel."}, "stop")
		}
	})
	if err := e.cfg.Update(func(c *config.Config) error {
		c.MCP.Servers = []config.MCPServer{server}
		c.TaskPanels.EASM.ServerID = server.ID
		c.TaskPanels.EASM.Tools = map[string]string{"organizations": "org_lookup_fixture", "stats": "stats_fixture"}
		for role := range config.EASMToolRoles {
			if _, set := c.TaskPanels.EASM.Tools[role]; !set {
				c.TaskPanels.EASM.Tools[role] = ""
			}
		}
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
	if state.Title != "Subject Co" || state.Collection.Status != "done" || len(state.Edges) != 1 || state.Edges[0].Target != "domain:subject.test" {
		t.Fatalf("state = %s", panels[0].State)
	}
	statuses := map[easm.Step]easm.StepStatus{}
	for _, status := range state.Collection.Steps {
		statuses[status.ID] = status
	}
	if statuses[easm.StepOrganizations].Status != "done" || statuses[easm.StepSummary].Status != "failed" || statuses[easm.StepHeat].Status != "unavailable" {
		t.Fatalf("steps = %+v", state.Collection.Steps)
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
	if body, err := caller.Call(t.Context(), "org_lookup_fixture", map[string]any{"names": []string{"S"}}); err != nil || len(body) == 0 {
		t.Fatalf("body=%s err=%v", body, err)
	}
	for _, unresolved := range []*easmCaller{
		{manager: manager, servers: []config.MCPServer{server}, serverID: "other"},
		{manager: manager, servers: []config.MCPServer{server}},
		{servers: []config.MCPServer{server}, serverID: server.ID},
	} {
		if _, err := unresolved.Call(t.Context(), "org_lookup_fixture", nil); err != easm.ErrUnavailable {
			t.Fatalf("unresolved server err=%v", err)
		}
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := caller.Call(cancelled, "org_lookup_fixture", nil); err != context.Canceled {
		t.Fatalf("cancelled err=%v", err)
	}
}
