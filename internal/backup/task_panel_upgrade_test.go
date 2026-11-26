package backup

import (
	"bytes"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"github.com/LubyRuffy/eino-swarm/internal/config"
	"github.com/LubyRuffy/eino-swarm/internal/panelplugins/easm"
	"github.com/LubyRuffy/eino-swarm/internal/store"
	"github.com/LubyRuffy/eino-swarm/internal/taskpanel"
)

func TestRestoreUpgradesLegacyEASMPanelAndKeepsItsHistory(t *testing.T) {
	clearEnv(t)
	legacy, current, err := easm.PanelDefinitions()
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []Kind{KindFull, KindProject} {
		t.Run(string(kind), func(t *testing.T) {
			ctx := t.Context()
			dir := t.TempDir()
			cfg, err := config.Load(dir)
			if err != nil {
				t.Fatal(err)
			}
			st, err := store.Open(cfg.DBPath())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = st.Close() })
			db, err := st.DB().DB()
			if err != nil {
				t.Fatal(err)
			}
			project := store.Project{Name: "panel project"}
			if err := st.CreateProject(&project); err != nil {
				t.Fatal(err)
			}
			thread := store.Thread{Title: "panel conversation", ProjectID: project.ID}
			if err := st.CreateThread(&thread); err != nil {
				t.Fatal(err)
			}
			turn := store.Turn{ThreadID: thread.ID, ProjectID: project.ID, Status: store.TurnDone}
			if err := st.CreateTurn(&turn); err != nil {
				t.Fatal(err)
			}
			source := taskpanel.Source{ThreadID: thread.ID, TurnID: turn.ID, AgentID: "manager", ToolCallID: "panel-collection"}
			panel, err := st.OpenTaskPanel(ctx, legacy, source)
			if err != nil {
				t.Fatal(err)
			}
			archivedUpdate := taskpanel.Update{ProtocolVersion: taskpanel.ProtocolVersion, PanelID: panel.ID, OperationID: "archived", BaseRevision: panel.Revision, Kind: "patch", Operations: json.RawMessage(`[{"op":"add","path":"/query","value":"saved"}]`)}
			archived, err := st.PublishTaskPanel(ctx, source, archivedUpdate)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "legacy.zwai-backup")
			if _, err := Create(ctx, Options{DataDir: dir, DB: db, Kind: kind, ProjectID: project.ID, OmitSecrets: true, Output: path}); err != nil {
				t.Fatal(err)
			}
			localUpdate := taskpanel.Update{ProtocolVersion: taskpanel.ProtocolVersion, PanelID: panel.ID, OperationID: "local", BaseRevision: archived.Revision, Kind: "patch", Operations: json.RawMessage(`[{"op":"replace","path":"/query","value":"local edit"}]`)}
			local, err := st.PublishTaskPanel(ctx, source, localUpdate)
			if err != nil {
				t.Fatal(err)
			}

			// A failed import rolls back to the exact local v1 binding, even
			// when its temporarily committed replacement used v2.
			opt := RestoreOptions{DataDir: dir, DB: db, Path: path, Confirm: true, Replace: kind == KindFull, ReplaceProject: kind == KindProject}
			failure := errors.New("cannot swap backup files")
			beforeFileSwap = func() error { return failure }
			_, err = Restore(ctx, opt)
			beforeFileSwap = nil
			if err != failure {
				t.Fatalf("restore did not reach file swap and roll back cleanly: %v", err)
			}
			rolledBack, err := st.VisibleTaskPanel(ctx, thread.ID, panel.ID)
			if err != nil || rolledBack.ID != local.ID || rolledBack.Revision != local.Revision || rolledBack.Source != local.Source || !bytes.Equal(rolledBack.State, local.State) || !taskpanel.DefinitionsMatch(rolledBack.Definition, legacy) {
				t.Fatalf("rollback lost the local v1 panel: id=%s revision=%d version=%s err=%v", rolledBack.ID, rolledBack.Revision, rolledBack.PluginVersion, err)
			}

			if _, err := Restore(ctx, opt); err != nil {
				t.Fatal(err)
			}
			restored, err := st.VisibleTaskPanel(ctx, thread.ID, panel.ID)
			if err != nil || restored.PluginVersion != current.Version || !taskpanel.DefinitionsMatch(restored.Definition, current) {
				t.Fatalf("legacy panel definition after restore: got version=%q want=%q err=%v", restored.PluginVersion, current.Version, err)
			}
			if restored.ID != archived.ID || restored.Revision != archived.Revision+1 || restored.Source != archived.Source || !bytes.Equal(restored.State, archived.State) {
				t.Fatalf("migration changed archived panel identity or payload: id=%s revision=%d source=%+v", restored.ID, restored.Revision, restored.Source)
			}
			receipt, err := st.PublishTaskPanel(ctx, source, archivedUpdate)
			if err != nil || receipt.ID != archived.ID || receipt.PluginVersion != legacy.Version || receipt.Revision != archived.Revision || receipt.Source != archived.Source || !bytes.Equal(receipt.State, archived.State) || !taskpanel.DefinitionsMatch(receipt.Definition, legacy) {
				t.Fatalf("restored historical v1 receipt changed: id=%s revision=%d version=%s err=%v", receipt.ID, receipt.Revision, receipt.PluginVersion, err)
			}
			collect := taskpanel.Update{ProtocolVersion: taskpanel.ProtocolVersion, PanelID: panel.ID, OperationID: "collect-clouds", BaseRevision: restored.Revision, Kind: "patch", Operations: json.RawMessage(`[{"op":"add","path":"/clouds","value":[]}]`)}
			collected, err := st.PublishTaskPanel(ctx, source, collect)
			if err != nil || collected.Revision != restored.Revision+1 || !bytes.Contains(collected.State, []byte(`"clouds":[]`)) {
				t.Fatalf("restored panel could not publish v2 cloud results: revision=%d err=%v", collected.Revision, err)
			}
		})
	}
}
