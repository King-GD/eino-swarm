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

type panelSSEFrame struct{ kind, id, data string }

func nextPanelFrame(t *testing.T, scanner *bufio.Scanner) panelSSEFrame {
	t.Helper()
	var frame panelSSEFrame
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" && frame.kind != "" {
			return frame
		}
		if strings.HasPrefix(line, "event: ") {
			frame.kind = strings.TrimPrefix(line, "event: ")
		}
		if strings.HasPrefix(line, "id: ") {
			frame.id = strings.TrimPrefix(line, "id: ")
		}
		if strings.HasPrefix(line, "data: ") {
			frame.data = strings.TrimPrefix(line, "data: ")
		}
	}
	t.Fatalf("stream ended: %v", scanner.Err())
	return frame
}

// Panel frames share the chat transport but can never alter Last-Event-ID.
// Otherwise a panel cursor ahead of chat seq would discard chat after reconnect.
func TestTaskPanelMultiplexKeepsChatResumeCursorIndependent(t *testing.T) {
	h := newHarness(t)
	thread := h.newThread()
	turn := &store.Turn{ThreadID: thread}
	if err := h.app.Store.CreateTurn(turn); err != nil {
		t.Fatal(err)
	}
	chat := &store.Event{ThreadID: thread, TurnID: turn.ID, Kind: "user_message", Text: "fixture input"}
	if err := h.app.Store.AppendEvent(chat); err != nil {
		t.Fatal(err)
	}
	definitions, err := h.app.Engine.TaskPanelCatalog()
	if err != nil {
		t.Fatal(err)
	}
	source := taskpanel.Source{ThreadID: thread, TurnID: turn.ID, AgentID: "manager", ToolCallID: "fixture"}
	panel, err := h.app.Store.OpenTaskPanel(t.Context(), definitions[0], source)
	if err != nil {
		t.Fatal(err)
	}
	for i := range 10 {
		panel, err = h.app.Store.PublishTaskPanel(t.Context(), source, taskpanel.Update{ProtocolVersion: 1, PanelID: panel.ID, OperationID: string(rune('a' + i)), BaseRevision: panel.Revision, Kind: "patch", Operations: json.RawMessage(`[{"op":"replace","path":"/collection/phase","value":"collecting"}]`)})
		if err != nil {
			t.Fatal(err)
		}
	}
	connect := func(last string) (*http.Response, *bufio.Scanner) {
		t.Helper()
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		t.Cleanup(cancel)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.srv.URL+"/api/threads/"+thread+"/events?panels=1", nil)
		if err != nil {
			t.Fatal(err)
		}
		if last != "" {
			req.Header.Set("Last-Event-ID", last)
		}
		resp, err := h.srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != 200 {
			t.Fatal(resp.Status)
		}
		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 4096), 2<<20)
		return resp, scanner
	}
	resp, scanner := connect("")
	first := nextPanelFrame(t, scanner)
	if first.kind != "user_message" || first.id != "1" {
		t.Fatalf("first=%+v", first)
	}
	ready := nextPanelFrame(t, scanner)
	if ready.kind != "panel_ready" || ready.id != "" {
		t.Fatalf("panel ready changed chat cursor: %+v", ready)
	}
	var payload struct {
		Cursor int64 `json:"cursor"`
	}
	if err := json.Unmarshal([]byte(ready.data), &payload); err != nil || payload.Cursor <= chat.Seq {
		t.Fatalf("independent cursor=%+v %v", payload, err)
	}
	if frame := nextPanelFrame(t, scanner); frame.kind != "ready" || frame.id != "" {
		t.Fatalf("chat ready=%+v", frame)
	}
	if _, err := h.app.Store.PublishTaskPanel(t.Context(), source, taskpanel.Update{ProtocolVersion: 1, PanelID: panel.ID, OperationID: "live", BaseRevision: panel.Revision, Kind: "patch", Operations: json.RawMessage(`[{"op":"replace","path":"/collection/phase","value":"finished"}]`)}); err != nil {
		t.Fatal(err)
	}
	update := nextPanelFrame(t, scanner)
	if update.kind != "panel_update" || update.id != "" {
		t.Fatalf("panel update changed chat cursor: %+v", update)
	}
	_ = resp.Body.Close()
	second := &store.Event{ThreadID: thread, TurnID: turn.ID, Kind: "agent_message", Text: "fixture result"}
	if err := h.app.Store.AppendEvent(second); err != nil {
		t.Fatal(err)
	}
	resumed, resumeScanner := connect("1")
	defer resumed.Body.Close()
	frame := nextPanelFrame(t, resumeScanner)
	if frame.kind != "agent_message" || frame.id != "2" {
		t.Fatalf("chat lost after panel frames: %+v", frame)
	}
}
