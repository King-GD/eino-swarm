package engine

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/LubyRuffy/eino-swarm/internal/store"
)

// Before occupying guest GUI, prove the native fixture crosses real model,
// Registry tools, owner injection and durable panel receipts without stubs.
func TestNativeTaskPanelFixtureCompletesThroughActualRegistry(t *testing.T) {
	t.Setenv("ZWAI_MOCK_TASK_PANELS", "1")
	t.Setenv("ZWAI_MOCK_TASK_PANEL_GATE", "")
	e := newTestEngine(t)
	thread, err := e.CreateThread("Synthetic map acceptance fixture", "", "")
	if err != nil {
		t.Fatal(err)
	}
	turn, err := e.StartTurn(thread.ID, "Show the synthetic map fixture.")
	if err != nil {
		t.Fatal(err)
	}
	got := waitForTurn(t, e, turn.ID)
	waitSettled(t, e, thread.ID)
	if got.Status != store.TurnDone || !strings.Contains(got.Final, "Synthetic map fixture complete") {
		t.Fatalf("turn=%+v", got)
	}
	panels, err := e.store.TaskPanels(t.Context(), thread.ID)
	if err != nil || len(panels) != 1 || panels[0].Revision != 3 {
		t.Fatalf("panels=%+v err=%v", panels, err)
	}
	var state struct {
		Probe struct {
			Responded  int `json:"responded"`
			NoResponse int `json:"no_response"`
		} `json:"probe"`
	}
	if err := json.Unmarshal(panels[0].State, &state); err != nil {
		t.Fatal(err)
	}
	if state.Probe.Responded != 2 || state.Probe.NoResponse != 1 {
		t.Fatalf("final state=%s", panels[0].State)
	}
}
