package store

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/LubyRuffy/eino-swarm/internal/taskpanel"
)

func TestTaskPanelInitialStateIsNotAPublishedObservation(t *testing.T) {
	f := newPanelFixture(t)
	p := f.open(t)
	check := func(want int) {
		t.Helper()
		panels, _, err := f.s.SnapshotTaskPanels(t.Context(), f.thread.ID)
		if err != nil || len(panels) != want {
			t.Fatalf("PC has %d panels, want %d: %v", len(panels), want, err)
		}
		phone, more, err := f.s.TaskPanelManifests(t.Context(), f.thread.ID, 5)
		if err != nil || more || len(phone) != want {
			t.Fatalf("phone has %d panels, want %d: %v", len(phone), want, err)
		}
	}
	check(0)
	bindings, err := f.s.TaskPanels(t.Context(), f.thread.ID)
	if err != nil || len(bindings) != 1 || bindings[0].ID != p.ID {
		t.Fatalf("runtime lost the binding before it could publish: %v, %v", bindings, err)
	}
	claim := taskpanel.Update{ProtocolVersion: 1, PanelID: p.ID, OperationID: f.src.ToolCallID + ":start", BaseRevision: p.Revision, Kind: "patch", Operations: []byte("[]")}
	if _, err := f.s.PublishTaskPanel(t.Context(), f.src, claim); err != nil {
		t.Fatal(err)
	}
	check(0)
	if _, err := f.s.StartTaskPanelSource(t.Context(), f.src, p.ID); err != nil {
		t.Fatal(err)
	}
	check(1)
	p, err = f.s.FinishTaskPanelSource(t.Context(), f.src, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	check(0)
	// Publishing the exact initial bytes can report an explicit zero result.
	zero := taskpanel.Update{ProtocolVersion: 1, PanelID: p.ID, OperationID: "observed-zero", BaseRevision: p.Revision, Kind: "snapshot", State: f.def.InitialState}
	if _, err := f.s.PublishTaskPanel(t.Context(), f.src, zero); err != nil {
		t.Fatal(err)
	}
	check(1)
}

func TestTaskPanelCloseSurvivesReopeningDatabaseAndRejectsLateWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "panels.sqlite")
	first, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = first.Close() })
	thread := Thread{Title: "Close panel"}
	if err = first.CreateThread(&thread); err != nil {
		t.Fatal(err)
	}
	turn := Turn{ThreadID: thread.ID}
	if err = first.CreateTurn(&turn); err != nil {
		t.Fatal(err)
	}
	source := taskpanel.Source{ThreadID: thread.ID, TurnID: turn.ID, AgentID: "manager", ToolCallID: "collect"}
	def := taskpanel.Definition{ID: "empty-fixture", Version: "1", Name: "Empty fixture", Entry: "fixture.html", Schema: []byte(`{"type":"object"}`), InitialState: []byte(`{}`)}
	panel, err := first.OpenTaskPanel(t.Context(), def, source)
	if err != nil {
		t.Fatal(err)
	}
	panel, err = first.StartTaskPanelSource(t.Context(), source, panel.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = first.CloseTaskPanel(t.Context(), thread.ID, panel.ID); err != nil {
		t.Fatal(err)
	}
	if err = first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Close() })
	if n, err := second.RecoverTaskPanelSources(t.Context()); err != nil || n != 0 {
		t.Fatalf("closed source was recovered: %d, %v", n, err)
	}
	if visible, err := second.TaskPanelVisible(t.Context(), thread.ID, panel.ID); err != nil || visible {
		t.Fatalf("closed instance became visible: %t, %v", visible, err)
	}
	if _, err = second.OpenTaskPanel(t.Context(), def, source); !errors.Is(err, taskpanel.ErrConflict) {
		t.Fatalf("repeated open bypassed close: %v", err)
	}
	if _, err = second.StartTaskPanelSource(t.Context(), source, panel.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("source restarted after close: %v", err)
	}
	update := taskpanel.Update{ProtocolVersion: 1, PanelID: panel.ID, OperationID: "late", BaseRevision: panel.Revision + 1, Kind: "snapshot", State: []byte(`{}`)}
	if _, err = second.PublishTaskPanel(t.Context(), source, update); !errors.Is(err, ErrNotFound) {
		t.Fatalf("late publication bypassed close: %v", err)
	}
	journal, err := second.TaskPanelChanges(t.Context(), thread.ID, 0)
	if err != nil || len(journal) != 2 || journal[1].Kind != "deleted" || journal[1].SourceStatus != "interrupted" {
		t.Fatalf("durable close result: %v, %v", journal, err)
	}
}

func TestTaskPanelCloseDeliveryFailureLeavesSourceRunning(t *testing.T) {
	f := newPanelFixture(t)
	panel := f.open(t)
	if _, err := f.s.StartTaskPanelSource(t.Context(), f.src, panel.ID); err != nil {
		t.Fatal(err)
	}
	injectPanelFault(t, f.s, "create", "task_panel_change_rows", "")
	if err := f.s.CloseTaskPanel(t.Context(), f.thread.ID, panel.ID); err == nil {
		t.Fatal("close succeeded without its delivery record")
	}
	current, err := f.s.VisibleTaskPanel(t.Context(), f.thread.ID, panel.ID)
	if err != nil || current.SourceStatus != "running" {
		t.Fatalf("failed close changed source: %v, %v", current, err)
	}
}

func TestTaskPanelLegacyObservationIndexDoesNotCountOpeningOrSourceClaim(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.sqlite")
	old, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = old.Close() })
	f := panelFixtureForStore(t, old)
	first := f.open(t)
	empty := taskpanel.Update{ProtocolVersion: 1, PanelID: first.ID, OperationID: "source:start", BaseRevision: first.Revision, Kind: "patch", Operations: []byte(" [ ] ")}
	if _, err = old.PublishTaskPanel(t.Context(), f.src, empty); err != nil {
		t.Fatal(err)
	}
	f.def.ID = "zero-results"
	second := f.open(t)
	zero := taskpanel.Update{ProtocolVersion: 1, PanelID: second.ID, OperationID: "observed-zero", BaseRevision: second.Revision, Kind: "snapshot", State: f.def.InitialState}
	if _, err = old.PublishTaskPanel(t.Context(), f.src, zero); err != nil {
		t.Fatal(err)
	}
	if err = old.db.Exec("DROP INDEX idx_panel_observation").Error; err != nil {
		t.Fatal(err)
	}
	if err = old.db.Exec("ALTER TABLE task_panel_mutations DROP COLUMN observation").Error; err != nil {
		t.Fatal(err)
	}
	if err = old.Close(); err != nil {
		t.Fatal(err)
	}
	migrated, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = migrated.Close() })
	panels, _, err := migrated.SnapshotTaskPanels(t.Context(), f.thread.ID)
	if err != nil || len(panels) != 1 || panels[0].ID != second.ID {
		t.Fatalf("migration classified legacy observation incorrectly: %v, %v", panels, err)
	}
}

func TestTaskPanelCloseRejectsCapturedPublicationAndPreservesHistoricalReceipt(t *testing.T) {
	f := newPanelFixture(t)
	panel := f.open(t)
	accepted := f.update(panel, "observed", 1)
	saved, err := f.s.PublishTaskPanel(t.Context(), f.src, accepted)
	if err != nil {
		t.Fatal(err)
	}
	pending := f.update(saved, "captured-before-close", 2)

	if err := f.s.CloseTaskPanel(t.Context(), f.thread.ID, panel.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.s.CloseTaskPanel(t.Context(), f.thread.ID, panel.ID); err != nil {
		t.Fatal(err)
	}
	for _, base := range []int64{saved.Revision + 1, pending.BaseRevision} {
		pending.BaseRevision = base
		if _, err := f.s.PublishTaskPanel(t.Context(), f.src, pending); !errors.Is(err, ErrNotFound) {
			t.Fatalf("closed identity accepted a new write with base %d: %v", base, err)
		}
	}
	replay, err := f.s.PublishTaskPanel(t.Context(), f.src, accepted)
	if err != nil || replay.Revision != saved.Revision || string(replay.State) != string(saved.State) {
		t.Fatalf("accepted operation lost its original receipt: %+v, %v", replay, err)
	}
	changes, err := f.s.TaskPanelChanges(t.Context(), f.thread.ID, 0)
	if err != nil || len(changes) != 2 || changes[1].Kind != "deleted" {
		t.Fatalf("close retry or late publication changed the journal: %v, %v", changes, err)
	}
}
