package server_test

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/LubyRuffy/eino-swarm/internal/store"
	"github.com/LubyRuffy/eino-swarm/internal/taskpanel"
)

func TestTaskPanelHTTPSettingsAndConversationIsolation(t *testing.T) {
	h := newHarness(t)
	catalog := h.json(http.MethodGet, "/api/task-panels/catalog", nil, http.StatusOK)
	if len(catalog["plugins"].([]any)) == 0 {
		t.Fatal("bundled plugins missing")
	}
	before := h.json(http.MethodGet, "/api/task-panels/settings", nil, http.StatusOK)
	if before["concurrency"] != float64(16) {
		t.Fatalf("defaults=%v", before)
	}
	after := h.json(http.MethodPut, "/api/task-panels/settings", map[string]any{"concurrency": 3}, http.StatusOK)
	if after["concurrency"] != float64(3) || after["fofa_client"] != before["fofa_client"] {
		t.Fatalf("partial save=%v", after)
	}
	for _, bad := range []any{map[string]any{}, map[string]any{"unknown": 1}, map[string]any{"enabled": nil}, map[string]any{"concurrency": 0}, map[string]any{"timeout_seconds": "bad"}} {
		h.json(http.MethodPut, "/api/task-panels/settings", bad, http.StatusBadRequest)
	}
	thread := h.newThread()
	other := h.newThread()
	initial := h.json(http.MethodGet, "/api/threads/"+thread+"/panels", nil, http.StatusOK)
	if len(initial["panels"].([]any)) != 0 {
		t.Fatal("fresh conversation has a panel")
	}
	turn := &store.Turn{ThreadID: thread}
	if err := h.app.Store.CreateTurn(turn); err != nil {
		t.Fatal(err)
	}
	definitions, err := h.app.Engine.TaskPanelCatalog()
	if err != nil {
		t.Fatal(err)
	}
	panel, err := h.app.Store.OpenTaskPanel(t.Context(), definitions[0], taskpanel.Source{ThreadID: thread, TurnID: turn.ID, AgentID: "manager", ToolCallID: "fixture-open"})
	if err != nil {
		t.Fatal(err)
	}
	h.json(http.MethodGet, "/api/threads/"+thread+"/panels/"+panel.ID, nil, http.StatusOK)
	h.json(http.MethodGet, "/api/threads/"+other+"/panels/"+panel.ID, nil, http.StatusNotFound)
	h.json(http.MethodGet, "/api/threads/missing/panels", nil, http.StatusNotFound)
}

// The EASM bindings save as one object: clearing one role keeps the user's
// other bindings, and an unknown role is refused instead of silently ignored.
func TestTaskPanelHTTPSettingsSaveEASMBindings(t *testing.T) {
	h := newHarness(t)
	before := h.json(http.MethodGet, "/api/task-panels/settings", nil, http.StatusOK)
	easm := before["easm"].(map[string]any)
	tools := easm["tools"].(map[string]any)
	tools["heat"] = ""
	tools["stats"] = "custom_stats"
	easm["server_id"] = "fixture-server"
	after := h.json(http.MethodPut, "/api/task-panels/settings", map[string]any{"easm": easm}, http.StatusOK)
	saved := after["easm"].(map[string]any)
	savedTools := saved["tools"].(map[string]any)
	if saved["server_id"] != "fixture-server" || savedTools["heat"] != "" || savedTools["stats"] != "custom_stats" || savedTools["organizations"] != tools["organizations"] || after["concurrency"] != before["concurrency"] {
		t.Fatalf("saved=%v", after)
	}
	for _, bad := range []map[string]any{{"tools": map[string]any{"unknown": "x"}, "max_domains": 20, "max_components": 20}, {"tools": tools, "max_domains": 0, "max_components": 20}} {
		h.json(http.MethodPut, "/api/task-panels/settings", map[string]any{"easm": bad}, http.StatusBadRequest)
	}
}

func TestTaskPanelSSEDeliversCommittedPatchWithoutChatSequence(t *testing.T) {
	h := newHarness(t)
	thread := h.newThread()
	turn := &store.Turn{ThreadID: thread}
	if err := h.app.Store.CreateTurn(turn); err != nil {
		t.Fatal(err)
	}
	definitions, err := h.app.Engine.TaskPanelCatalog()
	if err != nil {
		t.Fatal(err)
	}
	source := taskpanel.Source{ThreadID: thread, TurnID: turn.ID, AgentID: "manager", ToolCallID: "open-stream"}
	panel, err := h.app.Store.OpenTaskPanel(t.Context(), definitions[0], source)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, h.srv.URL+"/api/threads/"+thread+"/panels/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := h.srv.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", response.StatusCode)
	}
	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 4096), 2<<20)
	next := func() string {
		t.Helper()
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "data: ") {
				return strings.TrimPrefix(line, "data: ")
			}
		}
		t.Fatalf("stream ended: %v", scanner.Err())
		return ""
	}
	if !strings.Contains(next(), panel.ID) {
		t.Fatal("ready snapshot omitted panel")
	}
	changed, err := h.app.Store.PublishTaskPanel(t.Context(), source, taskpanel.Update{ProtocolVersion: 1, PanelID: panel.ID, OperationID: "live-patch", BaseRevision: panel.Revision, Kind: "patch", Operations: json.RawMessage(`[{"op":"replace","path":"/collection/phase","value":"finished"}]`)})
	if err != nil {
		t.Fatal(err)
	}
	var update taskpanel.Change
	if err := json.Unmarshal([]byte(next()), &update); err != nil {
		t.Fatal(err)
	}
	if update.Kind != "patch" || update.Revision != changed.Revision || update.BaseRevision != panel.Revision || update.Cursor <= 0 {
		t.Fatalf("change=%+v", update)
	}
	events, err := h.app.Store.ListEvents(thread, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatal("panel transport polluted transcript")
	}
}
