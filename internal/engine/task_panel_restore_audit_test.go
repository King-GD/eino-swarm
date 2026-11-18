package engine

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/LubyRuffy/eino-swarm/internal/store"
	"github.com/LubyRuffy/eino-swarm/internal/taskpanel"
)

// Panels are optional observations. A malformed panel source must be reported,
// but cannot skip restoration of an unrelated executable conversation.
func TestTaskPanelAuditCorruptSourceDoesNotSkipOtherConversationRestore(t *testing.T) {
	e := newTestEngine(t)
	defer e.Shutdown()
	bad, err := e.CreateThread("Corrupt panel observation", "", "")
	if err != nil {
		t.Fatal(err)
	}
	badTurn := plantUnfinishedTurn(t, e, bad.ID, "retain interrupted observation")
	definition := taskpanel.Definition{ID: "restore-audit", Version: "1", Name: "Restore audit", Entry: "audit.html", Schema: json.RawMessage(`{"type":"object"}`), InitialState: json.RawMessage(`{}`)}
	source := taskpanel.Source{ThreadID: bad.ID, TurnID: badTurn.ID, AgentID: "manager", ToolCallID: "observed-source"}
	panel, err := e.store.OpenTaskPanel(t.Context(), definition, source)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.store.StartTaskPanelSource(t.Context(), source, panel.ID); err != nil {
		t.Fatal(err)
	}
	if err = e.store.DB().Model(&store.TaskPanelRow{}).Where("id = ?", panel.ID).UpdateColumn("source_json", []byte(`{`)).Error; err != nil {
		t.Fatal(err)
	}
	good, err := e.CreateThread("Independent executable work", "", "")
	if err != nil {
		t.Fatal(err)
	}
	goodTurn := plantUnfinishedTurn(t, e, good.ID, "continue the independent supplied work")
	progressCalls := 0
	restoreErr := e.Restore(t.Context(), func(done, total int) { progressCalls++ })
	if restoreErr == nil {
		t.Fatal("malformed durable source was not reported")
	}
	if progressCalls == 0 {
		t.Fatalf("one malformed panel prevented all conversation recovery: error=%v good_turn=%s status=%+v", restoreErr, goodTurn.ID, e.Status(good.ID))
	}
	var failures *RestoreFailures
	if !errors.As(restoreErr, &failures) || failures.Failed != 1 {
		t.Fatalf("panel restore failure was not isolated: %v", restoreErr)
	}
	if got := waitForTurn(t, e, goodTurn.ID); got.Status != store.TurnDone {
		t.Fatalf("independent turn did not finish after recovery: %+v", got)
	}
	waitSettled(t, e, good.ID)
	if e.Status(bad.ID).Running {
		t.Fatal("conversation with unverified source ownership was resumed")
	}
}
