package store

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/LubyRuffy/eino-swarm/internal/taskpanel"
)

func TestTaskPanelTransferSelectsOnlyExactCurrentPatch(t *testing.T) {
	f := newPanelFixture(t)
	opened := f.open(t)
	snapshot, change, err := f.s.TaskPanelReadForTransfer(t.Context(), f.thread.ID, opened.ID, nil)
	if err != nil || change != nil || snapshot.ID != opened.ID || string(snapshot.State) != string(opened.State) {
		t.Fatal(snapshot, change, err)
	}
	updated, err := f.s.PublishTaskPanel(t.Context(), f.src, f.update(opened, "new", 1))
	if err != nil {
		t.Fatal(err)
	}
	_, change, err = f.s.TaskPanelReadForTransfer(t.Context(), f.thread.ID, opened.ID, &opened.Revision)
	if err != nil || change == nil || change.Kind != "patch" || change.BaseRevision != opened.Revision || change.Revision != updated.Revision {
		t.Fatal(change, err)
	}
	saved := append([]byte(nil), change.Operations...)
	change.Operations[0] = '!'
	_, again, err := f.s.TaskPanelReadForTransfer(t.Context(), f.thread.ID, opened.ID, &opened.Revision)
	if err != nil || again == nil || string(again.Operations) != string(saved) {
		t.Fatal(again, err)
	}
	for _, base := range []int64{-1, 0, updated.Revision} {
		if _, change, err = f.s.TaskPanelReadForTransfer(t.Context(), f.thread.ID, opened.ID, &base); err != nil || change != nil {
			t.Fatal(base, change, err)
		}
	}
	if _, _, err = f.s.TaskPanelReadForTransfer(t.Context(), "other-thread", opened.ID, nil); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	// Metadata transitions are full snapshots, so an exact data base cannot
	// omit a terminal source status by reusing its older patch.
	started, err := f.s.StartTaskPanelSource(t.Context(), f.src, opened.ID)
	if err != nil {
		t.Fatal(err)
	}
	got, change, err := f.s.TaskPanelReadForTransfer(t.Context(), f.thread.ID, opened.ID, &updated.Revision)
	if err != nil || change != nil || got.SourceStatus != "running" || got.Revision != started.Revision {
		t.Fatal(got, change, err)
	}
}
func TestTaskPanelTransferWithoutCurrentJournalOrDefinitionOpeningFallsBack(t *testing.T) {
	for _, missing := range []string{"journal", "opening", "new-incarnation"} {
		t.Run(missing, func(t *testing.T) {
			f := newPanelFixture(t)
			opened := f.open(t)
			if _, e := f.s.PublishTaskPanel(t.Context(), f.src, f.update(opened, "data", 1)); e != nil {
				t.Fatal(e)
			}
			var err error
			switch missing {
			case "journal":
				err = f.s.db.Where("panel_id = ?", opened.ID).Delete(&TaskPanelChangeRow{}).Error
			case "opening":
				err = f.s.db.Where("panel_id = ? AND operation_id = ?", opened.ID, "").Delete(&TaskPanelMutation{}).Error
			case "new-incarnation":
				err = f.s.db.Model(&TaskPanelMutation{}).Where("panel_id = ? AND operation_id = ?", opened.ID, "").UpdateColumn("revision", 100).Error
			}
			if err != nil {
				t.Fatal(err)
			}
			snapshot, change, err := f.s.TaskPanelReadForTransfer(t.Context(), f.thread.ID, opened.ID, &opened.Revision)
			if err != nil || change != nil || snapshot.Revision != 2 {
				t.Fatal(snapshot, change, err)
			}
		})
	}
}
func TestTaskPanelManifestOmitsBulkAndVisibleCheckTracksLifetime(t *testing.T) {
	f := newPanelFixture(t)
	opened := f.open(t)
	rows, more, err := f.s.TaskPanelManifests(t.Context(), f.thread.ID, 1)
	if err != nil || more || len(rows) != 1 || rows[0].PluginVersion != f.def.Version {
		t.Fatal(rows, more, err)
	}
	raw, _ := json.Marshal(rows)
	var maps []map[string]any
	if err = json.Unmarshal(raw, &maps); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"state", "definition", "source", "schema"} {
		if _, ok := maps[0][field]; ok {
			t.Fatal("manifest contains bulk/private metadata", field)
		}
	}
	second := f.def
	second.ID = "other"
	if _, err = f.s.OpenTaskPanel(t.Context(), second, f.src); err != nil {
		t.Fatal(err)
	}
	if rows, more, err = f.s.TaskPanelManifests(t.Context(), f.thread.ID, 1); err != nil || !more || len(rows) != 2 {
		t.Fatal(rows, more, err)
	}
	if _, _, err = f.s.TaskPanelManifests(t.Context(), f.thread.ID, 0); !errors.Is(err, taskpanel.ErrInvalid) {
		t.Fatal(err)
	}
	if _, _, err = f.s.TaskPanelManifests(t.Context(), "missing", 1); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if visible, e := f.s.TaskPanelVisible(t.Context(), f.thread.ID, opened.ID); e != nil || !visible {
		t.Fatal(visible, e)
	}
	if visible, e := f.s.TaskPanelVisible(t.Context(), "other", opened.ID); e != nil || visible {
		t.Fatal(visible, e)
	}
	if err = f.s.DeleteThread(f.thread.ID); err != nil {
		t.Fatal(err)
	}
	if visible, e := f.s.TaskPanelVisible(t.Context(), f.thread.ID, opened.ID); e != nil || visible {
		t.Fatal(visible, e)
	}
}
func TestTaskPanelTransferReadFailuresRemainErrors(t *testing.T) {
	for _, table := range []string{"task_panel_rows", "task_panel_change_rows", "task_panel_mutations"} {
		t.Run(table, func(t *testing.T) {
			f := newPanelFixture(t)
			opened := f.open(t)
			if _, e := f.s.PublishTaskPanel(t.Context(), f.src, f.update(opened, "new", 1)); e != nil {
				t.Fatal(e)
			}
			injectPanelFault(t, f.s, "query", table, "")
			if _, _, e := f.s.TaskPanelReadForTransfer(t.Context(), f.thread.ID, opened.ID, &opened.Revision); e == nil {
				t.Fatal("query fault ignored")
			}
		})
	}
	t.Run("corrupt-source", func(t *testing.T) {
		f := newPanelFixture(t)
		opened := f.open(t)
		if e := f.s.db.Model(&TaskPanelRow{}).Where("id = ?", opened.ID).UpdateColumn("source_json", []byte(`{`)).Error; e != nil {
			t.Fatal(e)
		}
		if _, _, e := f.s.TaskPanelReadForTransfer(t.Context(), f.thread.ID, opened.ID, nil); e == nil {
			t.Fatal("corrupt source accepted")
		}
	})
	t.Run("manifest-query", func(t *testing.T) {
		f := newPanelFixture(t)
		f.open(t)
		injectPanelFault(t, f.s, "row", "task_panel_rows", "")
		if _, _, e := f.s.TaskPanelManifests(t.Context(), f.thread.ID, 10); e == nil {
			t.Fatal("manifest query fault ignored")
		}
	})
	t.Run("manifest-thread-query", func(t *testing.T) {
		f := newPanelFixture(t)
		injectPanelFault(t, f.s, "query", "threads", "")
		if _, _, e := f.s.TaskPanelManifests(t.Context(), f.thread.ID, 10); e == nil {
			t.Fatal("thread query fault ignored")
		}
	})
	t.Run("cancelled", func(t *testing.T) {
		f := newPanelFixture(t)
		opened := f.open(t)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if _, _, e := f.s.TaskPanelReadForTransfer(ctx, f.thread.ID, opened.ID, nil); !errors.Is(e, context.Canceled) {
			t.Fatal(e)
		}
	})
}
