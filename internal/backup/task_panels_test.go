package backup

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/LubyRuffy/eino-swarm/internal/taskpanel"
)

// The separate cursor journal must survive project restore with its numeric
// offset, and an intentionally omitted conversation must not leave panel data.
func TestTaskPanelProjectBackupRestoresStateAndOmitsRunningConversation(t *testing.T) {
	for _, omit := range []bool{false, true} {
		t.Run(map[bool]string{false: "retain", true: "omit"}[omit], func(t *testing.T) {
			src := seed(t)
			turns, err := src.st.ListTurns(src.thread.ID)
			if err != nil || len(turns) == 0 {
				t.Fatal(err)
			}
			owner := taskpanel.Source{ThreadID: src.thread.ID, TurnID: turns[0].ID, AgentID: "manager", ToolCallID: "fixture-open"}
			definition := taskpanel.Definition{ID: "fixture-panel", Version: "1", Name: "Fixture", Entry: "task-panels/fixture-panel.html", Schema: json.RawMessage(`{"type":"object"}`), InitialState: json.RawMessage(`{"count":0}`)}
			panel, err := src.st.OpenTaskPanel(context.Background(), definition, owner)
			if err != nil {
				t.Fatal(err)
			}
			panel, err = src.st.PublishTaskPanel(t.Context(), owner, taskpanel.Update{ProtocolVersion: 1, PanelID: panel.ID, OperationID: "one", BaseRevision: panel.Revision, Kind: "patch", Operations: json.RawMessage(`[{"op":"replace","path":"/count","value":1}]`)})
			if err != nil {
				t.Fatal(err)
			}
			out := filepath.Join(t.TempDir(), "panel.zwai-backup")
			db, err := src.st.DB().DB()
			if err != nil {
				t.Fatal(err)
			}
			options := Options{DataDir: src.dir, DB: db, Kind: KindProject, ProjectID: src.project.ID, Output: out}
			if omit {
				options.OmitThreads = []OmittedThread{{ID: src.thread.ID, Title: "Fixture", Reason: ReasonRunning}}
			}
			if _, err := Create(t.Context(), options); err != nil {
				t.Fatal(err)
			}
			dest := seed(t)
			destDB, err := dest.st.DB().DB()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := Restore(t.Context(), RestoreOptions{DataDir: dest.dir, DB: destDB, Path: out, Confirm: true}); err != nil {
				t.Fatal(err)
			}
			got, err := dest.st.TaskPanel(t.Context(), src.thread.ID, panel.ID)
			if omit {
				if err == nil {
					t.Fatal("omitted conversation left a panel")
				}
				return
			}
			if err != nil || got.Revision != panel.Revision || string(got.State) != string(panel.State) {
				t.Fatalf("restored=%+v err=%v", got, err)
			}
			cursor, err := dest.st.TaskPanelCursor(t.Context(), src.thread.ID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := dest.st.PublishTaskPanel(t.Context(), owner, taskpanel.Update{ProtocolVersion: 1, PanelID: panel.ID, OperationID: "two", BaseRevision: got.Revision, Kind: "patch", Operations: json.RawMessage(`[{"op":"replace","path":"/count","value":2}]`)}); err != nil {
				t.Fatal(err)
			}
			changes, err := dest.st.TaskPanelChanges(t.Context(), src.thread.ID, cursor)
			if err != nil || len(changes) != 1 || changes[0].Cursor <= cursor {
				t.Fatalf("new cursor changes=%v err=%v", changes, err)
			}
		})
	}
}
