package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/LubyRuffy/eino-swarm/internal/taskpanel"
)

func TestTaskPanelInterruptedProducerKeepsStateAndCannotTouchSuccessor(t *testing.T) {
	f := newPanelFixture(t)
	p := f.open(t)
	p, e := f.s.PublishTaskPanel(t.Context(), f.src, f.update(p, "data", 1))
	if e != nil {
		t.Fatal(e)
	}
	if e = f.s.FinishTurn(f.turn.ID, TurnCancelled, "", ""); e != nil {
		t.Fatal(e)
	}
	marked, e := f.s.InterruptTaskPanelSource(t.Context(), f.src, p.ID)
	if e != nil || marked.SourceStatus != "interrupted" || marked.Revision != p.Revision+1 || string(marked.State) != string(p.State) {
		t.Fatalf("marked=%+v %v", marked, e)
	}
	repeated, e := f.s.InterruptTaskPanelSource(t.Context(), f.src, p.ID)
	if e != nil || repeated.Revision != marked.Revision {
		t.Fatal(repeated, e)
	}
	changes, e := f.s.TaskPanelChanges(t.Context(), f.thread.ID, 0)
	if e != nil || changes[len(changes)-1].SourceStatus != "interrupted" {
		t.Fatal(changes, e)
	}
	next := Turn{ThreadID: f.thread.ID}
	if e = f.s.CreateTurn(&next); e != nil {
		t.Fatal(e)
	}
	successor := f.src
	successor.TurnID = next.ID
	successor.ToolCallID = "new-source"
	fresh, e := f.s.PublishTaskPanel(t.Context(), successor, f.update(marked, "new", 2))
	if e != nil || fresh.SourceStatus != "" {
		t.Fatal(fresh, e)
	}
	if _, e = f.s.InterruptTaskPanelSource(t.Context(), f.src, p.ID); !errors.Is(e, taskpanel.ErrConflict) {
		t.Fatal(e)
	}
	wrong := successor
	wrong.Generation = 1
	if _, e = f.s.InterruptTaskPanelSource(t.Context(), wrong, p.ID); !errors.Is(e, taskpanel.ErrConflict) {
		t.Fatal(e)
	}
	wrong = successor
	wrong.ToolCallID = "old-call"
	if _, e = f.s.InterruptTaskPanelSource(t.Context(), wrong, p.ID); !errors.Is(e, taskpanel.ErrConflict) {
		t.Fatal(e)
	}
	if e = f.s.DeleteThread(f.thread.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = f.s.InterruptTaskPanelSource(t.Context(), successor, p.ID); !errors.Is(e, ErrNotFound) {
		t.Fatal(e)
	}
}
func TestTaskPanelInterruptRefusesInvalidAndUnstoredOwners(t *testing.T) {
	f := newPanelFixture(t)
	p := f.open(t)
	if _, e := f.s.InterruptTaskPanelSource(t.Context(), taskpanel.Source{}, p.ID); !errors.Is(e, taskpanel.ErrInvalid) {
		t.Fatal(e)
	}
	if _, e := f.s.InterruptTaskPanelSource(t.Context(), f.src, "missing"); !errors.Is(e, ErrNotFound) {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, e := f.s.InterruptTaskPanelSource(ctx, f.src, p.ID); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	if e := f.s.db.Model(&TaskPanelRow{}).Where("id = ?", p.ID).UpdateColumn("source_json", []byte(`{`)).Error; e != nil {
		t.Fatal(e)
	}
	if _, e := f.s.InterruptTaskPanelSource(t.Context(), f.src, p.ID); e == nil {
		t.Fatal("corrupt source")
	}
}
func TestTaskPanelInterruptStorageFailureKeepsPendingState(t *testing.T) {
	for _, fault := range []struct{ stage, table string }{{"query", "turns"}, {"query", "task_panel_rows"}, {"update", "task_panel_rows"}, {"create", "task_panel_change_rows"}} {
		t.Run(fault.stage+fault.table, func(t *testing.T) {
			f := newPanelFixture(t)
			p := f.open(t)
			if _, e := f.s.StartTaskPanelSource(t.Context(), f.src, p.ID); e != nil {
				t.Fatal(e)
			}
			injectPanelFault(t, f.s, fault.stage, fault.table, "")
			if _, e := f.s.InterruptTaskPanelSource(t.Context(), f.src, p.ID); e == nil {
				t.Fatal("fault ignored")
			}
		})
	}
}

func TestTaskPanelSourceStatusSurvivesUpdatesAndRequiresExplicitNewOwner(t *testing.T) {
	f := newPanelFixture(t)
	p := f.open(t)
	if _, e := f.s.FinishTaskPanelSource(t.Context(), f.src, p.ID); !errors.Is(e, taskpanel.ErrConflict) {
		t.Fatal(e)
	}
	started, e := f.s.StartTaskPanelSource(t.Context(), f.src, p.ID)
	if e != nil || started.SourceStatus != "running" {
		t.Fatal(started, e)
	}
	next, e := f.s.PublishTaskPanel(t.Context(), f.src, f.update(started, "during-run", 1))
	if e != nil || next.SourceStatus != "running" {
		t.Fatal(next, e)
	}
	finished, e := f.s.FinishTaskPanelSource(t.Context(), f.src, p.ID)
	if e != nil || finished.SourceStatus != "finished" {
		t.Fatal(finished, e)
	}
	if _, e = f.s.StartTaskPanelSource(t.Context(), f.src, p.ID); !errors.Is(e, taskpanel.ErrConflict) {
		t.Fatal("same execution restarted", e)
	}
	if n, e := f.s.RecoverTaskPanelSources(t.Context()); e != nil || n != 0 {
		t.Fatal("completed source changed", n, e)
	}
	other := f.src
	other.ToolCallID = "next-call"
	claimed, e := f.s.PublishTaskPanel(t.Context(), other, f.update(finished, "new-run", 2))
	if e != nil || claimed.SourceStatus != "" {
		t.Fatal(claimed, e)
	}
	if _, e = f.s.StartTaskPanelSource(t.Context(), other, p.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = f.s.FinishTaskPanelSource(t.Context(), f.src, p.ID); !errors.Is(e, taskpanel.ErrConflict) {
		t.Fatal("old completion changed successor", e)
	}
	if e = f.s.FinishTurn(f.turn.ID, TurnCancelled, "", ""); e != nil {
		t.Fatal(e)
	}
	if _, e = f.s.FinishTaskPanelSource(t.Context(), other, p.ID); !errors.Is(e, taskpanel.ErrConflict) {
		t.Fatal("cancelled source completed", e)
	}
	if n, e := f.s.RecoverTaskPanelSources(t.Context()); e != nil || n != 1 {
		t.Fatal(n, e)
	}
}
func TestTaskPanelRestartMarksDurableOrphanWithoutRunningWork(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "source.db")
	first, e := Open(filename)
	if e != nil {
		t.Fatal(e)
	}
	th := Thread{Title: "durable panel"}
	if e = first.CreateThread(&th); e != nil {
		t.Fatal(e)
	}
	turn := Turn{ThreadID: th.ID}
	if e = first.CreateTurn(&turn); e != nil {
		t.Fatal(e)
	}
	def := taskpanel.Definition{ID: "durable", Version: "1", Name: "Durable", Entry: "durable.html", Schema: []byte(`{"type":"object"}`), InitialState: []byte(`{"retained":1}`)}
	src := taskpanel.Source{ThreadID: th.ID, TurnID: turn.ID, AgentID: "manager", ToolCallID: "source-call"}
	p, e := first.OpenTaskPanel(t.Context(), def, src)
	if e != nil {
		t.Fatal(e)
	}
	started, e := first.StartTaskPanelSource(t.Context(), src, p.ID)
	if e != nil {
		t.Fatal(e)
	}
	if e = first.Close(); e != nil {
		t.Fatal(e)
	}
	second, e := Open(filename)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = second.Close() })
	before, e := second.TaskPanel(t.Context(), th.ID, p.ID)
	if e != nil || before.SourceStatus != "running" {
		t.Fatal("fixture did not retain orphan", before, e)
	}
	n, e := second.RecoverTaskPanelSources(t.Context())
	if e != nil || n != 1 {
		t.Fatal(n, e)
	}
	recovered, e := second.TaskPanel(t.Context(), th.ID, p.ID)
	if e != nil || recovered.SourceStatus != "interrupted" || recovered.Revision != started.Revision+1 || string(recovered.State) != string(started.State) {
		t.Fatal(recovered, e)
	}
	if n, e = second.RecoverTaskPanelSources(t.Context()); e != nil || n != 0 {
		t.Fatal("repeated recovery wrote again", n, e)
	}
}
func TestTaskPanelRecoveryFailsClosedForCorruptOrMissingSource(t *testing.T) {
	for _, kind := range []string{"query", "corrupt", "missing-turn", "delivery"} {
		t.Run(kind, func(t *testing.T) {
			f := newPanelFixture(t)
			p := f.open(t)
			if _, e := f.s.StartTaskPanelSource(t.Context(), f.src, p.ID); e != nil {
				t.Fatal(e)
			}
			switch kind {
			case "query":
				injectPanelFault(t, f.s, "query", "task_panel_rows", "")
			case "corrupt":
				if e := f.s.db.Model(&TaskPanelRow{}).Where("id = ?", p.ID).UpdateColumn("source_json", []byte(`{`)).Error; e != nil {
					t.Fatal(e)
				}
			case "missing-turn":
				if e := f.s.db.Where("id = ?", f.turn.ID).Delete(&Turn{}).Error; e != nil {
					t.Fatal(e)
				}
			case "delivery":
				injectPanelFault(t, f.s, "create", "task_panel_change_rows", "")
			}
			if n, e := f.s.RecoverTaskPanelSources(t.Context()); e == nil || n != 0 {
				t.Fatal("recovery fabricated success", n, e)
			}
		})
	}
}
