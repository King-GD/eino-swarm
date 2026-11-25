package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/LubyRuffy/eino-swarm/internal/taskpanel"
	"gorm.io/gorm"
)

type panelFixture struct {
	s      *Store
	thread Thread
	turn   Turn
	src    taskpanel.Source
	def    taskpanel.Definition
}

func newPanelFixture(t *testing.T) panelFixture {
	t.Helper()
	return panelFixtureForStore(t, open(t))
}

func panelFixtureForStore(t *testing.T, s *Store) panelFixture {
	t.Helper()
	f := panelFixture{s: s, thread: Thread{Title: "panel source"}, def: taskpanel.Definition{ID: "chart", Version: "1", Name: "Chart", Entry: "chart/index.html", Schema: json.RawMessage(`{"type":"object","properties":{"count":{"type":"integer"}}}`), InitialState: json.RawMessage(`{"count":0}`)}}
	if err := f.s.CreateThread(&f.thread); err != nil {
		t.Fatal(err)
	}
	f.turn = Turn{ThreadID: f.thread.ID}
	if err := f.s.CreateTurn(&f.turn); err != nil {
		t.Fatal(err)
	}
	f.src = taskpanel.Source{ThreadID: f.thread.ID, TurnID: f.turn.ID, AgentID: "manager", ToolCallID: "call-open"}
	return f
}
func (f panelFixture) open(t *testing.T) taskpanel.Panel {
	t.Helper()
	p, e := f.s.OpenTaskPanel(t.Context(), f.def, f.src)
	if e != nil {
		t.Fatal(e)
	}
	return p
}
func (f panelFixture) update(p taskpanel.Panel, id string, n int) taskpanel.Update {
	return taskpanel.Update{ProtocolVersion: 1, PanelID: p.ID, OperationID: id, BaseRevision: p.Revision, Kind: "patch", Operations: json.RawMessage(fmt.Sprintf(`[{"op":"add","path":"/count","value":%d}]`, n))}
}
func TestTaskPanelReplayAndDuplicateKeepOriginalReceipt(t *testing.T) {
	f := newPanelFixture(t)
	p := f.open(t)
	if p.Revision != 1 || p.State == nil {
		t.Fatalf("open=%+v", p)
	}
	changed := f.def
	changed.Version = "2"
	again, e := f.s.OpenTaskPanel(t.Context(), changed, f.src)
	if e != nil || again.PluginVersion != "1" || again.ID != p.ID {
		t.Fatalf("pinned=%+v %v", again, e)
	}
	u := f.update(p, "update-1", 1)
	p1, e := f.s.PublishTaskPanel(t.Context(), f.src, u)
	if e != nil {
		t.Fatal(e)
	}
	p2, e := f.s.PublishTaskPanel(t.Context(), f.src, f.update(p1, "update-2", 2))
	if e != nil {
		t.Fatal(e)
	}
	repeat, e := f.s.PublishTaskPanel(t.Context(), f.src, u)
	if e != nil || repeat.Revision != p1.Revision || string(repeat.State) != string(p1.State) {
		t.Fatalf("duplicate=%+v %v", repeat, e)
	}
	bad := u
	bad.Operations = []byte(`[{"op":"replace","path":"/count","value":8}]`)
	if _, e = f.s.PublishTaskPanel(t.Context(), f.src, bad); !errors.Is(e, taskpanel.ErrConflict) {
		t.Fatal(e)
	}
	stale := f.update(p, "new-operation", 4)
	if _, e = f.s.PublishTaskPanel(t.Context(), f.src, stale); !errors.Is(e, taskpanel.ErrConflict) {
		t.Fatal(e)
	}
	list, cursor, e := f.s.SnapshotTaskPanels(t.Context(), f.thread.ID)
	if e != nil || len(list) != 1 || list[0].Revision != p2.Revision || cursor == 0 {
		t.Fatalf("snapshot=%v %d %v", list, cursor, e)
	}
	got, e := f.s.TaskPanel(t.Context(), f.thread.ID, p.ID)
	if e != nil || got.Revision != p2.Revision {
		t.Fatal(e)
	}
	got.State[0] = '!'
	got, e = f.s.TaskPanel(t.Context(), f.thread.ID, p.ID)
	if e != nil || got.State[0] != '{' {
		t.Fatal("state alias")
	}
	changes, e := f.s.TaskPanelChanges(t.Context(), f.thread.ID, 0)
	if e != nil || len(changes) != 2 || changes[0].Kind != "patch" || changes[1].Kind != "patch" {
		t.Fatalf("changes=%v %v", changes, e)
	}
	c, e := f.s.TaskPanelCursor(t.Context(), f.thread.ID)
	if e != nil || c != cursor {
		t.Fatal(c, e)
	}
	list, e = f.s.TaskPanels(t.Context(), f.thread.ID)
	if e != nil || len(list) != 1 {
		t.Fatal(e)
	}
}
func TestTaskPanelOwnershipAndCancellationRefuseLateWrites(t *testing.T) {
	f := newPanelFixture(t)
	p := f.open(t)
	other := Thread{Title: "other"}
	if e := f.s.CreateThread(&other); e != nil {
		t.Fatal(e)
	}
	if _, e := f.s.TaskPanel(t.Context(), other.ID, p.ID); !errors.Is(e, ErrNotFound) {
		t.Fatal(e)
	}
	for _, src := range []taskpanel.Source{{}, {ThreadID: other.ID, TurnID: f.turn.ID, AgentID: "manager", ToolCallID: "x"}, {ThreadID: f.thread.ID, TurnID: "missing", AgentID: "manager", ToolCallID: "x"}} {
		if _, e := f.s.OpenTaskPanel(t.Context(), f.def, src); e == nil {
			t.Fatal("source accepted")
		}
		if _, e := f.s.PublishTaskPanel(t.Context(), src, f.update(p, "x", 1)); e == nil {
			t.Fatal("source published")
		}
	}
	if _, _, e := f.s.SnapshotTaskPanels(t.Context(), "missing"); e == nil {
		t.Fatal("missing thread")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, e := f.s.PublishTaskPanel(ctx, f.src, f.update(p, "cancelled", 1)); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	if e := f.s.FinishTurn(f.turn.ID, TurnCancelled, "", ""); e != nil {
		t.Fatal(e)
	}
	if _, e := f.s.PublishTaskPanel(t.Context(), f.src, f.update(p, "stopped", 1)); !errors.Is(e, taskpanel.ErrConflict) {
		t.Fatal(e)
	}
	if e := f.s.DeleteThread(f.thread.ID); e != nil {
		t.Fatal(e)
	}
	if _, e := f.s.OpenTaskPanel(t.Context(), f.def, f.src); !errors.Is(e, ErrNotFound) {
		t.Fatal(e)
	}
	for _, model := range []any{&TaskPanelRow{}, &TaskPanelMutation{}, &TaskPanelChangeRow{}} {
		var n int64
		if e := f.s.db.Model(model).Count(&n).Error; e != nil || n != 0 {
			t.Fatal(n, e)
		}
	}
}
func TestTaskPanelConcurrentCASAllowsOneProducer(t *testing.T) {
	f := newPanelFixture(t)
	p := f.open(t)
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := range 2 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, e := f.s.PublishTaskPanel(t.Context(), f.src, f.update(p, fmt.Sprintf("op-%d", i), i+1))
			results <- e
		}(i)
	}
	wg.Wait()
	close(results)
	var ok, conflict int
	for e := range results {
		if e == nil {
			ok++
		} else if errors.Is(e, taskpanel.ErrConflict) {
			conflict++
		} else {
			t.Fatal(e)
		}
	}
	if ok != 1 || conflict != 1 {
		t.Fatal(ok, conflict)
	}
}
func TestTaskPanelRewindRestoresStateWithoutReusingRevision(t *testing.T) {
	f := newPanelFixture(t)
	p := f.open(t)
	first, e := f.s.PublishTaskPanel(t.Context(), f.src, f.update(p, "first", 1))
	if e != nil {
		t.Fatal(e)
	}
	next := Turn{ThreadID: f.thread.ID}
	if e = f.s.CreateTurn(&next); e != nil {
		t.Fatal(e)
	}
	cut := Event{ThreadID: f.thread.ID, TurnID: next.ID, Kind: "user_message", Text: "edit this"}
	if e = f.s.AppendEvent(&cut); e != nil {
		t.Fatal(e)
	}
	src := f.src
	src.TurnID = next.ID
	src.ToolCallID = "next"
	second, e := f.s.PublishTaskPanel(t.Context(), src, f.update(first, "second", 2))
	if e != nil {
		t.Fatal(e)
	}
	cursor, _ := f.s.TaskPanelCursor(t.Context(), f.thread.ID)
	if e = f.s.TruncateFromEventSeq(f.thread.ID, cut.Seq); e != nil {
		t.Fatal(e)
	}
	restored, e := f.s.TaskPanel(t.Context(), f.thread.ID, p.ID)
	if e != nil || restored.Revision <= second.Revision || string(restored.State) != string(first.State) {
		t.Fatalf("restored=%+v %v", restored, e)
	}
	changes, e := f.s.TaskPanelChanges(t.Context(), f.thread.ID, cursor)
	if e != nil || len(changes) != 1 || changes[0].Kind != "snapshot" {
		t.Fatalf("changes=%v %v", changes, e)
	}
	if _, e = f.s.PublishTaskPanel(t.Context(), src, f.update(restored, "late", 3)); !errors.Is(e, ErrNotFound) {
		t.Fatal(e)
	}
}
func TestTaskPanelRewindHidesNewBindingAndReopenAdvancesVersion(t *testing.T) {
	f := newPanelFixture(t)
	cut := Event{ThreadID: f.thread.ID, TurnID: f.turn.ID, Kind: "user_message", Text: "replace"}
	if e := f.s.AppendEvent(&cut); e != nil {
		t.Fatal(e)
	}
	p := f.open(t)
	cursor, _ := f.s.TaskPanelCursor(t.Context(), f.thread.ID)
	if e := f.s.TruncateFromEventSeq(f.thread.ID, cut.Seq); e != nil {
		t.Fatal(e)
	}
	if _, e := f.s.TaskPanel(t.Context(), f.thread.ID, p.ID); !errors.Is(e, ErrNotFound) {
		t.Fatal(e)
	}
	list, e := f.s.TaskPanels(t.Context(), f.thread.ID)
	if e != nil || len(list) != 0 {
		t.Fatal(e)
	}
	changes, e := f.s.TaskPanelChanges(t.Context(), f.thread.ID, cursor)
	if e != nil || len(changes) != 1 || changes[0].Kind != "deleted" {
		t.Fatal(changes, e)
	}
	next := Turn{ThreadID: f.thread.ID}
	if e = f.s.CreateTurn(&next); e != nil {
		t.Fatal(e)
	}
	f.src.TurnID = next.ID
	reopened := f.open(t)
	if reopened.ID != p.ID || reopened.Revision <= p.Revision+1 {
		t.Fatal(reopened)
	}
}
func TestTaskPanelPublicationFailureRollsBackStateAndDelivery(t *testing.T) {
	f := newPanelFixture(t)
	p := f.open(t)
	if e := f.s.db.Callback().Create().Before("gorm:create").Register("panel-fault", func(tx *gorm.DB) {
		if tx.Statement.Table == "task_panel_change_rows" {
			tx.AddError(errors.New("injected delivery failure"))
		}
	}); e != nil {
		t.Fatal(e)
	}
	if _, e := f.s.PublishTaskPanel(t.Context(), f.src, f.update(p, "failed", 1)); e == nil {
		t.Fatal("fault ignored")
	}
	got, e := f.s.TaskPanel(t.Context(), f.thread.ID, p.ID)
	if e != nil || got.Revision != p.Revision || string(got.State) != string(p.State) {
		t.Fatal(got, e)
	}
	changes, e := f.s.TaskPanelChanges(t.Context(), f.thread.ID, 0)
	if e != nil || len(changes) != 0 {
		t.Fatal(changes, e)
	}
}

// Budgets guard new work inside the same transaction as CAS, but cannot make a
// previously accepted remove fail by reapplying it to a newer document.
func TestTaskPanelDuplicateRemoveReturnsReceiptAfterBudgetShrinks(t *testing.T) {
	f := newPanelFixture(t)
	p := f.open(t)
	remove := taskpanel.Update{ProtocolVersion: 1, PanelID: p.ID, OperationID: "remove-count", BaseRevision: p.Revision, Kind: "patch", Operations: []byte(`[{"op":"remove","path":"/count"}]`)}
	accepted, e := f.s.PublishTaskPanel(t.Context(), f.src, remove)
	if e != nil {
		t.Fatal(e)
	}
	later := f.update(accepted, "restore-count", 2)
	if _, e = f.s.PublishTaskPanel(t.Context(), f.src, later); e != nil {
		t.Fatal(e)
	}
	repeat, e := f.s.PublishTaskPanel(t.Context(), f.src, remove, taskpanel.Limits{MaxStateBytes: 1, MaxUpdateBytes: 1})
	if e != nil || repeat.Revision != accepted.Revision || string(repeat.State) != "{}" {
		t.Fatal(repeat, e)
	}
	current, e := f.s.TaskPanel(t.Context(), f.thread.ID, p.ID)
	if e != nil {
		t.Fatal(e)
	}
	for _, limits := range []taskpanel.Limits{{MaxStateBytes: 1}, {MaxUpdateBytes: 1}} {
		if _, e = f.s.PublishTaskPanel(t.Context(), f.src, f.update(current, "oversize", 3), limits); !errors.Is(e, taskpanel.ErrInvalid) {
			t.Fatal(e)
		}
	}
	if _, e = f.s.PublishTaskPanel(t.Context(), f.src, f.update(current, "ambiguous", 3), taskpanel.Limits{}, taskpanel.Limits{}); !errors.Is(e, taskpanel.ErrInvalid) {
		t.Fatal(e)
	}
}
