package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cloudwego/eino/schema"
)

// This explicit native-test fixture never selects itself from user wording.
// It publishes synthetic data through the same owned tools as a real model.
func mockTaskPanels(msgs []*schema.Message) *schema.Message {
	if os.Getenv("ZWAI_MOCK_TASK_PANELS") != "1" {
		return nil
	}
	invoke := func(id, name string, args any) *schema.Message {
		raw, _ := json.Marshal(args)
		return schema.AssistantMessage("Synthetic map fixture: applying the next recorded step.", []schema.ToolCall{call(id, name, string(raw))})
	}
	fail := func() *schema.Message {
		return schema.AssistantMessage("Synthetic map fixture could not verify its tool receipt.", nil)
	}
	index, status := panelFixtureReceipt(msgs, "panel-fixture-index", "read_panel_state")
	if status == "absent" {
		return invoke("panel-fixture-index", "read_panel_state", map[string]any{})
	}
	var catalog struct {
		Plugins []struct {
			ID string `json:"id"`
		} `json:"plugins"`
	}
	if status != "ok" || json.Unmarshal(index, &catalog) != nil {
		return fail()
	}
	available := false
	for _, plugin := range catalog.Plugins {
		available = available || plugin.ID == "fofa-world-map"
	}
	if !available {
		return fail()
	}
	opened, status := panelFixtureReceipt(msgs, "panel-fixture-open", "open_task_panel")
	if status == "absent" {
		return invoke("panel-fixture-open", "open_task_panel", map[string]any{"plugin_id": "fofa-world-map"})
	}
	var owner struct {
		ID       string `json:"id"`
		PanelID  string `json:"panel_id"`
		Revision int64  `json:"revision"`
	}
	if status != "ok" || json.Unmarshal(opened, &owner) != nil {
		return fail()
	}
	if owner.ID == "" {
		owner.ID = owner.PanelID
	}
	if owner.ID == "" {
		return fail()
	}
	read, status := panelFixtureReceipt(msgs, "panel-fixture-read", "read_panel_state")
	if status == "absent" {
		return invoke("panel-fixture-read", "read_panel_state", map[string]any{"panel_id": owner.ID})
	}
	var current struct {
		ID       string `json:"id"`
		Revision int64  `json:"revision"`
	}
	if status != "ok" || json.Unmarshal(read, &current) != nil || current.ID != owner.ID {
		return fail()
	}
	first, status := panelFixtureReceipt(msgs, "panel-fixture-initial", "publish_panel_update")
	if status == "absent" {
		return invoke("panel-fixture-initial", "publish_panel_update", map[string]any{"protocol_version": 1, "panel_id": owner.ID, "operation_id": "native-fixture-initial", "base_revision": current.Revision, "kind": "snapshot", "state": panelFixtureState()})
	}
	var initialReceipt struct {
		ID       string `json:"id"`
		Revision int64  `json:"revision"`
	}
	if status != "ok" || json.Unmarshal(first, &initialReceipt) != nil || initialReceipt.ID != owner.ID || initialReceipt.Revision != current.Revision+1 {
		return fail()
	}
	last, status := panelFixtureReceipt(msgs, "panel-fixture-final", "publish_panel_update")
	if status == "absent" {
		ops := []map[string]any{}
		for _, id := range []string{"a", "b", "c"} {
			state := "responded"
			if id == "b" {
				state = "no_response"
			}
			ops = append(ops, map[string]any{"op": "replace", "path": "/assets_by_id/" + id + "/status", "value": state})
			result := map[string]any{"status": state, "finished_at": "2026-01-01T00:00:00Z", "elapsed_ms": 12}
			if id == "b" {
				result["error"] = "fixture_timeout"
			} else {
				result["status_code"] = 403
			}
			ops = append(ops, map[string]any{"op": "add", "path": "/results_by_id/" + id, "value": result})
		}
		ops = append(ops, map[string]any{"op": "replace", "path": "/collection/phase", "value": "finished"}, map[string]any{"op": "replace", "path": "/probe", "value": map[string]any{"pending": 0, "running": 0, "responded": 2, "no_response": 1, "skipped": 0, "interrupted": 0}})
		return invoke("panel-fixture-final", "publish_panel_update", map[string]any{"protocol_version": 1, "panel_id": owner.ID, "operation_id": "native-fixture-final", "base_revision": initialReceipt.Revision, "kind": "patch", "operations": ops})
	}
	var finalReceipt struct {
		ID       string `json:"id"`
		Revision int64  `json:"revision"`
	}
	if status != "ok" || json.Unmarshal(last, &finalReceipt) != nil || finalReceipt.ID != owner.ID || finalReceipt.Revision != initialReceipt.Revision+1 {
		return fail()
	}
	final, status := panelFixtureReceipt(msgs, "panel-fixture-verify", "read_panel_state")
	if status == "absent" {
		return invoke("panel-fixture-verify", "read_panel_state", map[string]any{"panel_id": owner.ID})
	}
	var verified struct {
		ID       string `json:"id"`
		Revision int64  `json:"revision"`
		State    struct {
			Probe struct {
				Responded  int `json:"responded"`
				NoResponse int `json:"no_response"`
			} `json:"probe"`
		} `json:"state"`
	}
	if status != "ok" || json.Unmarshal(final, &verified) != nil || verified.ID != owner.ID || verified.Revision != finalReceipt.Revision || verified.State.Probe.Responded != 2 || verified.State.Probe.NoResponse != 1 {
		return fail()
	}
	return schema.AssistantMessage("Synthetic map fixture complete; no external assets were contacted.", nil)
}

func panelFixtureReceipt(msgs []*schema.Message, id, name string) (json.RawMessage, string) {
	called := false
	for _, message := range msgs {
		if message == nil {
			continue
		}
		if message.Role == schema.Assistant {
			for _, candidate := range message.ToolCalls {
				if candidate.ID == id {
					if candidate.Function.Name != name {
						return nil, "error"
					}
					called = true
				}
			}
		}
		if called && message.Role == schema.Tool && message.ToolCallID == id {
			var body map[string]json.RawMessage
			if json.Unmarshal([]byte(message.Content), &body) != nil || body == nil || len(body["error"]) > 0 {
				return nil, "error"
			}
			return json.RawMessage(message.Content), "ok"
		}
	}
	if called {
		return nil, "pending"
	}
	return nil, "absent"
}

func waitMockTaskPanelGate(ctx context.Context, role string, msgs []*schema.Message) error {
	if os.Getenv("ZWAI_MOCK_TASK_PANELS") != "1" || role != managerRole {
		return nil
	}
	if _, status := panelFixtureReceipt(msgs, "panel-fixture-initial", "publish_panel_update"); status != "ok" {
		return nil
	}
	if _, status := panelFixtureReceipt(msgs, "panel-fixture-final", "publish_panel_update"); status != "absent" {
		return nil
	}
	gate := os.Getenv("ZWAI_MOCK_TASK_PANEL_GATE")
	if gate == "" {
		return nil
	}
	caseID := os.Getenv("ZWAI_NATIVE_CASE_ID")
	if caseID != "" {
		workspace := os.Getenv("ZWAI_NATIVE_WORKSPACE")
		if workspace == "" || gate != filepath.Join(workspace, "panel-continue") || !strings.HasPrefix(filepath.Base(filepath.Dir(workspace)), "zwai-native-"+caseID+"-") {
			return fmt.Errorf("native panel fixture gate is outside its case workspace")
		}
	}
	for {
		info, err := os.Lstat(gate)
		if err == nil {
			if !info.Mode().IsRegular() {
				return fmt.Errorf("native panel fixture gate must be a regular file")
			}
			if caseID != "" {
				content, readErr := os.ReadFile(gate)
				if readErr != nil || string(content) != caseID {
					return fmt.Errorf("native panel fixture gate has another case identity")
				}
			}
			return nil
		}
		if !os.IsNotExist(err) {
			return fmt.Errorf("native panel fixture gate: %w", err)
		}
		timer := time.NewTimer(25 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return context.Cause(ctx)
		case <-timer.C:
		}
	}
}

func panelFixtureState() map[string]any {
	assets := map[string]any{}
	for index, id := range []string{"a", "b", "c"} {
		country, longitude, latitude := "CN", 116.4+float64(index)*5, 39.9
		if id == "c" {
			country, longitude, latitude = "US", -74, 40.7
		}
		status := "pending"
		if id == "a" {
			status = "running"
		}
		assets[id] = map[string]any{"id": id, "ip": fmt.Sprintf("192.0.2.%d", index+1), "port": 443, "protocol": "https", "host": "synthetic-" + id + ".invalid", "country": country, "region": "", "city": "", "longitude": longitude, "latitude": latitude, "status": status}
	}
	return map[string]any{"collection": map[string]any{"phase": "collecting", "received": 3, "discovered": 3, "completeness": "partial", "reason": "synthetic_fixture"}, "assets_by_id": assets, "probe": map[string]any{"pending": 2, "running": 1, "responded": 0, "no_response": 0, "skipped": 0, "interrupted": 0}, "results_by_id": map[string]any{}}
}
