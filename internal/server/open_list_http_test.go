package server_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/LubyRuffy/eino-swarm/internal/store"
)

func TestOpeningTurnsOmitStoredAnswersAndProtocolPrompts(t *testing.T) {
	h := newHarness(t)
	id := h.newThread()
	human := "human-" + strings.Repeat("h", 64)
	protocol := "protocol-" + strings.Repeat("g", 4096)
	answer := "answer-" + strings.Repeat("a", 4096)
	for _, turn := range []store.Turn{
		{ID: "human", ThreadID: id, Status: store.TurnDone, UserText: human, Final: answer, DurationMS: 1200},
		{ID: "goal", ThreadID: id, Status: store.TurnDone, UserText: protocol, Final: answer, GoalContinue: true, DurationMS: 3400},
		{ID: "schedule", ThreadID: id, Status: store.TurnError, UserText: protocol, Final: answer, ScheduleContinue: true, Error: "check failed", DurationMS: 900},
	} {
		if err := h.app.Store.CreateTurn(&turn); err != nil {
			t.Fatal(err)
		}
	}

	got := h.json(http.MethodGet, "/api/threads/"+id+"/turns", nil, http.StatusOK)
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(protocol)) || bytes.Contains(raw, []byte(answer)) {
		t.Fatal("opening turns included a stored answer or a protocol prompt")
	}
	rows, _ := got["turns"].([]any)
	if len(rows) != 3 {
		t.Fatalf("turns=%v", got["turns"])
	}
	first := rows[0].(map[string]any)
	if first["user_text"] != human || first["final"] != "" || first["duration_ms"] != float64(1200) || first["status"] != store.TurnDone {
		t.Fatalf("human turn=%v", first)
	}
	second := rows[1].(map[string]any)
	if second["user_text"] != "" || second["final"] != "" || second["goal_continue"] != true || second["duration_ms"] != float64(3400) {
		t.Fatalf("goal turn=%v", second)
	}
	third := rows[2].(map[string]any)
	if third["user_text"] != "" || third["final"] != "" || third["schedule_continue"] != true || third["error"] != "check failed" {
		t.Fatalf("schedule turn=%v", third)
	}
	stored, err := h.app.Store.ListTurns(id)
	if err != nil || stored[1].UserText != protocol || stored[0].Final != answer {
		t.Fatalf("stored turns changed: %+v err=%v", stored, err)
	}
}
