package provider

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LubyRuffy/eino-swarm/frontend"
	"github.com/LubyRuffy/eino-swarm/internal/taskpanel"
	"github.com/cloudwego/eino/schema"
)

func TestTaskPanelNativeFixtureUsesPairedReceiptsAndCurrentSchema(t *testing.T) {
	t.Setenv("ZWAI_MOCK_TASK_PANELS", "")
	if mockTaskPanels(nil) != nil || managerScript(1, nil).ToolCalls[0].Function.Name != "spawn_agent" {
		t.Fatal("normal mock changed")
	}
	t.Setenv("ZWAI_MOCK_TASK_PANELS", "1")
	t.Setenv("ZWAI_MOCK_TASK_PANEL_GATE", "")
	var definitions []taskpanel.Definition
	if err := json.Unmarshal(frontend.TaskPanelCatalog(), &definitions); err != nil {
		t.Fatal(err)
	}
	d := definitions[0]
	state, revision := d.InitialState, int64(1)
	var messages []*schema.Message
	for step := 0; step < 10; step++ {
		out := managerScript(step+1, messages)
		if len(out.ToolCalls) == 0 {
			if !strings.Contains(out.Content, "Synthetic map fixture complete") {
				t.Fatal(out.Content)
			}
			break
		}
		c := out.ToolCalls[0]
		var response any
		switch c.Function.Name {
		case "read_panel_state":
			if c.ID == "panel-fixture-index" {
				response = map[string]any{"plugins": definitions}
			} else {
				response = map[string]any{"id": "receipt-panel", "revision": revision, "state": json.RawMessage(state), "definition": d}
			}
		case "open_task_panel":
			if c.Function.Arguments != `{"plugin_id":"fofa-world-map"}` {
				t.Fatal(c.Function.Arguments)
			}
			response = map[string]any{"id": "receipt-panel", "revision": revision}
		case "publish_panel_update":
			var update taskpanel.Update
			if err := json.Unmarshal([]byte(c.Function.Arguments), &update); err != nil {
				t.Fatal(err)
			}
			if update.PanelID != "receipt-panel" || update.BaseRevision != revision {
				t.Fatalf("untrusted identity/revision: %+v", update)
			}
			var err error
			state, err = taskpanel.Apply(d, state, update)
			if err != nil {
				t.Fatal(err)
			}
			revision++
			response = map[string]any{"id": "receipt-panel", "revision": revision}
		default:
			t.Fatalf("unexpected tool %s", c.Function.Name)
		}
		raw, _ := json.Marshal(response)
		messages = append(messages, out, schema.ToolMessage(string(raw), c.ID))
	}
	if revision != 3 || len(messages) != 12 {
		t.Fatalf("fixture did not finish exactly two updates: revision=%d messages=%d", revision, len(messages))
	}
	// A call intention, corrupt receipt or foreign identity never advances a step.
	for index, message := range messages {
		if message.Role != schema.Tool {
			continue
		}
		for _, body := range []string{`{`, `{"error":"refused"}`, `{}`} {
			broken := append([]*schema.Message(nil), messages[:index+1]...)
			broken[index] = schema.ToolMessage(body, message.ToolCallID)
			if got := mockTaskPanels(broken); len(got.ToolCalls) != 0 || !strings.Contains(got.Content, "could not verify") {
				t.Fatalf("advanced past %s %s: %+v", message.ToolCallID, body, got)
			}
		}
		if got := mockTaskPanels(messages[:index]); len(got.ToolCalls) != 0 {
			t.Fatal("replayed an unsettled call")
		}
	}
	if _, status := panelFixtureReceipt([]*schema.Message{nil, schema.ToolMessage(`{}`, "id")}, "id", "read_panel_state"); status != "absent" {
		t.Fatal(status)
	}
	wrong := schema.AssistantMessage("", []schema.ToolCall{call("id", "other_tool", `{}`)})
	if _, status := panelFixtureReceipt([]*schema.Message{wrong}, "id", "read_panel_state"); status != "error" {
		t.Fatal(status)
	}
}

func TestTaskPanelFixtureGateCancelsAndAcceptsOnlyARegularReleaseFile(t *testing.T) {
	t.Setenv("ZWAI_NATIVE_CASE_ID", "")
	messages := []*schema.Message{schema.AssistantMessage("", []schema.ToolCall{call("panel-fixture-initial", "publish_panel_update", `{}`)}), schema.ToolMessage(`{"id":"p","revision":2}`, "panel-fixture-initial")}
	t.Setenv("ZWAI_MOCK_TASK_PANELS", "")
	if err := waitMockTaskPanelGate(t.Context(), managerRole, messages); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ZWAI_MOCK_TASK_PANELS", "1")
	t.Setenv("ZWAI_MOCK_TASK_PANEL_GATE", "")
	t.Setenv("ZWAI_MOCK_TASK_PANEL_GATE", filepath.Join(t.TempDir(), strings.Repeat("x", 300)))
	if err := waitMockTaskPanelGate(t.Context(), managerRole, messages); err == nil {
		t.Fatal("accepted invalid gate path")
	}
	t.Setenv("ZWAI_MOCK_TASK_PANEL_GATE", "")
	if err := waitMockTaskPanelGate(t.Context(), managerRole, messages); err != nil {
		t.Fatal(err)
	}
	if err := waitMockTaskPanelGate(t.Context(), "worker", messages); err != nil {
		t.Fatal(err)
	}
	if err := waitMockTaskPanelGate(t.Context(), managerRole, nil); err != nil {
		t.Fatal(err)
	}
	gate := filepath.Join(t.TempDir(), "continue")
	t.Setenv("ZWAI_MOCK_TASK_PANEL_GATE", gate)
	for _, stream := range []bool{false, true} {
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Millisecond)
		model := newMockModel(managerRole)
		var err error
		if stream {
			_, err = model.Stream(ctx, messages)
		} else {
			_, err = model.Generate(ctx, messages)
		}
		cancel()
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(gate, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := waitMockTaskPanelGate(t.Context(), managerRole, messages); err == nil {
		t.Fatal("accepted a directory")
	}
	if err := os.Remove(gate); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(gate, []byte("continue"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := waitMockTaskPanelGate(t.Context(), managerRole, messages); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(gate); err != nil {
		t.Fatal(err)
	}
	messages = append(messages, schema.AssistantMessage("", []schema.ToolCall{call("panel-fixture-final", "publish_panel_update", `{}`)}))
	if err := waitMockTaskPanelGate(t.Context(), managerRole, messages); err != nil {
		t.Fatal(err)
	}
}

func TestTaskPanelNativeGateIsBoundToItsPrivateCase(t *testing.T) {
	t.Setenv("ZWAI_MOCK_TASK_PANELS", "1")
	t.Setenv("ZWAI_NATIVE_CASE_ID", "case-one")
	root, err := os.MkdirTemp(t.TempDir(), "zwai-native-case-one-")
	if err != nil {
		t.Fatal(err)
	}
	workspace := filepath.Join(root, "workspace")
	if err := os.Mkdir(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ZWAI_NATIVE_WORKSPACE", workspace)
	messages := []*schema.Message{schema.AssistantMessage("", []schema.ToolCall{call("panel-fixture-initial", "publish_panel_update", `{}`)}), schema.ToolMessage(`{"id":"p","revision":2}`, "panel-fixture-initial")}
	t.Setenv("ZWAI_MOCK_TASK_PANEL_GATE", filepath.Join(root, "foreign"))
	if waitMockTaskPanelGate(t.Context(), managerRole, messages) == nil {
		t.Fatal("accepted a foreign gate path")
	}
	gate := filepath.Join(workspace, "panel-continue")
	t.Setenv("ZWAI_MOCK_TASK_PANEL_GATE", gate)
	if err := os.WriteFile(gate, []byte("another-case"), 0o600); err != nil {
		t.Fatal(err)
	}
	if waitMockTaskPanelGate(t.Context(), managerRole, messages) == nil {
		t.Fatal("accepted foreign gate content")
	}
	if err := os.WriteFile(gate, []byte("case-one"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := waitMockTaskPanelGate(t.Context(), managerRole, messages); err != nil {
		t.Fatal(err)
	}
}
