package store

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/LubyRuffy/eino-swarm/internal/taskpanel"
	"gorm.io/gorm"
)

func injectPanelFault(t *testing.T, s *Store, stage, table, contains string) {
	t.Helper()
	callback := func(tx *gorm.DB) {
		if tx.Statement.Table == table && (contains == "" || strings.Contains(tx.Statement.SQL.String(), contains)) {
			tx.AddError(errors.New("panel storage fault"))
		}
	}
	var err error
	switch stage {
	case "query":
		err = s.db.Callback().Query().After("gorm:query").Register("panel-storage-fault", callback)
	case "create":
		err = s.db.Callback().Create().Before("gorm:create").Register("panel-storage-fault", callback)
	case "update":
		err = s.db.Callback().Update().Before("gorm:update").Register("panel-storage-fault", callback)
	case "delete":
		err = s.db.Callback().Delete().Before("gorm:delete").Register("panel-storage-fault", callback)
	case "row":
		err = s.db.Callback().Row().Before("gorm:row").Register("panel-storage-fault", callback)
	}
	if err != nil {
		t.Fatal(err)
	}
}
func TestTaskPanelStoreFailuresRemainErrors(t *testing.T) {
	t.Run("bad-definition", func(t *testing.T) {
		f := newPanelFixture(t)
		d := f.def
		d.Schema = []byte(`{`)
		if _, e := f.s.OpenTaskPanel(t.Context(), d, f.src); e == nil {
			t.Fatal("definition accepted")
		}
	})
	t.Run("owner-query", func(t *testing.T) {
		f := newPanelFixture(t)
		injectPanelFault(t, f.s, "query", "turns", "")
		if _, e := f.s.OpenTaskPanel(t.Context(), f.def, f.src); e == nil {
			t.Fatal("owner query ignored")
		}
	})
	t.Run("row-query", func(t *testing.T) {
		f := newPanelFixture(t)
		p := f.open(t)
		injectPanelFault(t, f.s, "query", "task_panel_rows", "")
		if _, e := f.s.OpenTaskPanel(t.Context(), f.def, f.src); e == nil {
			t.Fatal("open query")
		}
		if _, e := f.s.TaskPanel(t.Context(), f.thread.ID, p.ID); e == nil {
			t.Fatal("get query")
		}
		if _, _, e := f.s.SnapshotTaskPanels(t.Context(), f.thread.ID); e == nil {
			t.Fatal("list query")
		}
		if _, e := f.s.PublishTaskPanel(t.Context(), f.src, f.update(p, "x", 1)); e == nil {
			t.Fatal("publish query")
		}
	})
	t.Run("thread-query", func(t *testing.T) {
		f := newPanelFixture(t)
		injectPanelFault(t, f.s, "query", "threads", "")
		if _, _, e := f.s.SnapshotTaskPanels(t.Context(), f.thread.ID); e == nil {
			t.Fatal("thread query")
		}
	})
	t.Run("open-create", func(t *testing.T) {
		f := newPanelFixture(t)
		injectPanelFault(t, f.s, "create", "task_panel_rows", "")
		if _, e := f.s.OpenTaskPanel(t.Context(), f.def, f.src); e == nil {
			t.Fatal("create")
		}
	})
	t.Run("open-mutation", func(t *testing.T) {
		f := newPanelFixture(t)
		injectPanelFault(t, f.s, "create", "task_panel_mutations", "")
		if _, e := f.s.OpenTaskPanel(t.Context(), f.def, f.src); e == nil {
			t.Fatal("mutation")
		}
	})
	t.Run("anchor", func(t *testing.T) {
		f := newPanelFixture(t)
		injectPanelFault(t, f.s, "row", "events", "")
		if _, e := f.s.OpenTaskPanel(t.Context(), f.def, f.src); e == nil {
			t.Fatal("anchor")
		}
	})
	for _, stage := range []string{"update", "create"} {
		t.Run("publish-"+stage, func(t *testing.T) {
			f := newPanelFixture(t)
			p := f.open(t)
			table := "task_panel_rows"
			if stage == "create" {
				table = "task_panel_mutations"
			}
			injectPanelFault(t, f.s, stage, table, "")
			if _, e := f.s.PublishTaskPanel(t.Context(), f.src, f.update(p, "x", 1)); e == nil {
				t.Fatal("write")
			}
		})
	}
	t.Run("operation-query", func(t *testing.T) {
		f := newPanelFixture(t)
		p := f.open(t)
		injectPanelFault(t, f.s, "query", "task_panel_mutations", "")
		if _, e := f.s.PublishTaskPanel(t.Context(), f.src, f.update(p, "x", 1)); e == nil {
			t.Fatal("operation query")
		}
	})
	t.Run("delivery-query", func(t *testing.T) {
		f := newPanelFixture(t)
		injectPanelFault(t, f.s, "query", "task_panel_change_rows", "")
		if _, e := f.s.TaskPanelChanges(t.Context(), f.thread.ID, 0); e == nil {
			t.Fatal("delivery query")
		}
	})
	t.Run("cursor-query", func(t *testing.T) {
		f := newPanelFixture(t)
		injectPanelFault(t, f.s, "row", "task_panel_change_rows", "")
		if _, _, e := f.s.SnapshotTaskPanels(t.Context(), f.thread.ID); e == nil {
			t.Fatal("cursor query")
		}
	})
	t.Run("missing-panel", func(t *testing.T) {
		f := newPanelFixture(t)
		p := f.open(t)
		u := f.update(p, "x", 1)
		u.PanelID = "missing"
		if _, e := f.s.PublishTaskPanel(t.Context(), f.src, u); !errors.Is(e, ErrNotFound) {
			t.Fatal(e)
		}
	})
	t.Run("invalid-update", func(t *testing.T) {
		f := newPanelFixture(t)
		p := f.open(t)
		u := f.update(p, "x", 1)
		u.Operations = []byte(`{`)
		if _, e := f.s.PublishTaskPanel(t.Context(), f.src, u); !errors.Is(e, taskpanel.ErrInvalid) {
			t.Fatal(e)
		}
		u.Operations = []byte(`[{"op":"replace","path":"/count","value":"bad"}]`)
		if _, e := f.s.PublishTaskPanel(t.Context(), f.src, u); !errors.Is(e, taskpanel.ErrInvalid) {
			t.Fatal(e)
		}
	})
}
func TestTaskPanelCorruptDurableInputsFailClosed(t *testing.T) {
	for _, column := range []string{"definition_json", "source_json"} {
		t.Run(column, func(t *testing.T) {
			f := newPanelFixture(t)
			p := f.open(t)
			if _, e := f.s.StartTaskPanelSource(t.Context(), f.src, p.ID); e != nil {
				t.Fatal(e)
			}
			if e := f.s.db.Model(&TaskPanelRow{}).Where("id = ?", p.ID).UpdateColumn(column, []byte(`{`)).Error; e != nil {
				t.Fatal(e)
			}
			if _, e := f.s.TaskPanel(t.Context(), f.thread.ID, p.ID); e == nil {
				t.Fatal("get corrupt")
			}
			if _, e := f.s.OpenTaskPanel(t.Context(), f.def, f.src); e == nil {
				t.Fatal("open corrupt")
			}
			if _, _, e := f.s.SnapshotTaskPanels(t.Context(), f.thread.ID); e == nil {
				t.Fatal("snapshot corrupt")
			}
			if _, e := f.s.PublishTaskPanel(t.Context(), f.src, f.update(p, "x", 1)); e == nil {
				t.Fatal("publish corrupt")
			}
		})
	}
	for _, column := range []string{"body", "source_json"} {
		t.Run("mutation-"+column, func(t *testing.T) {
			f := newPanelFixture(t)
			p := f.open(t)
			u := f.update(p, "x", 1)
			if _, e := f.s.PublishTaskPanel(t.Context(), f.src, u); e != nil {
				t.Fatal(e)
			}
			if e := f.s.db.Model(&TaskPanelMutation{}).Where("panel_id = ? AND operation_id = ?", p.ID, "x").UpdateColumn(column, []byte(`{`)).Error; e != nil {
				t.Fatal(e)
			}
			if _, e := f.s.PublishTaskPanel(t.Context(), f.src, u); e == nil {
				t.Fatal("duplicate corrupt receipt")
			}
		})
	}
	t.Run("mutation-schema", func(t *testing.T) {
		f := newPanelFixture(t)
		p := f.open(t)
		u := f.update(p, "x", 1)
		if _, e := f.s.PublishTaskPanel(t.Context(), f.src, u); e != nil {
			t.Fatal(e)
		}
		bad := u
		bad.Operations = []byte(`[{"op":"replace","path":"/count","value":"bad"}]`)
		body, _ := json.Marshal(bad)
		if e := f.s.db.Model(&TaskPanelMutation{}).Where("panel_id = ? AND operation_id = ?", p.ID, "x").UpdateColumn("body", body).Error; e != nil {
			t.Fatal(e)
		}
		if _, e := f.s.PublishTaskPanel(t.Context(), f.src, u); e == nil {
			t.Fatal("invalid durable patch")
		}
	})
}
func TestTaskPanelDeleteStorageFailureRollsBackConversation(t *testing.T) {
	for _, table := range []string{"task_panel_mutations", "task_panel_change_rows", "task_panel_rows"} {
		t.Run(table, func(t *testing.T) {
			f := newPanelFixture(t)
			p := f.open(t)
			injectPanelFault(t, f.s, "delete", table, "")
			if e := f.s.DeleteThread(f.thread.ID); e == nil {
				t.Fatal("delete ignored")
			}
			if _, e := f.s.GetThread(f.thread.ID); e != nil {
				t.Fatal("thread deletion leaked")
			}
			if _, e := f.s.TaskPanel(t.Context(), f.thread.ID, p.ID); e != nil {
				t.Fatal("panel deletion leaked")
			}
		})
	}
}
func TestTaskPanelRewindStorageFailurePreservesTimeline(t *testing.T) {
	for _, fault := range []struct{ stage, table, contains string }{{"query", "task_panel_mutations", ""}, {"delete", "task_panel_mutations", ""}, {"query", "task_panel_rows", ""}, {"update", "task_panel_rows", ""}, {"delete", "task_panel_change_rows", ""}, {"create", "task_panel_change_rows", ""}, {"query", "task_panel_mutations", "count"}} {
		t.Run(fault.stage+fault.table+fault.contains, func(t *testing.T) {
			f := newPanelFixture(t)
			cut := Event{ThreadID: f.thread.ID, TurnID: f.turn.ID, Kind: "user_message", Text: "edit"}
			if e := f.s.AppendEvent(&cut); e != nil {
				t.Fatal(e)
			}
			f.open(t)
			injectPanelFault(t, f.s, fault.stage, fault.table, fault.contains)
			if e := f.s.TruncateFromEventSeq(f.thread.ID, cut.Seq); e == nil {
				t.Fatal("rewind failure ignored")
			}
			if _, e := f.s.GetEvent(f.thread.ID, cut.Seq); e != nil {
				t.Fatal("timeline partial deletion")
			}
		})
	}
}
