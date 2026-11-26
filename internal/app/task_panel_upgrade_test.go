package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"

	"github.com/LubyRuffy/eino-swarm/internal/config"
	"github.com/LubyRuffy/eino-swarm/internal/panelplugins/easm"
	"github.com/LubyRuffy/eino-swarm/internal/store"
	"github.com/LubyRuffy/eino-swarm/internal/taskpanel"
)

func TestStartupUpgradesSavedEASMPanelAndKeepsItsHistory(t *testing.T) {
	ctx := t.Context()
	dataDir := t.TempDir()
	cfg, err := config.Load(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	legacy, current, err := easm.PanelDefinitions()
	if err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(cfg.DBPath())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	thread := store.Thread{Title: "saved asset portrait"}
	if err := db.CreateThread(&thread); err != nil {
		t.Fatal(err)
	}
	turn := store.Turn{ThreadID: thread.ID, Status: store.TurnDone}
	if err := db.CreateTurn(&turn); err != nil {
		t.Fatal(err)
	}
	source := taskpanel.Source{ThreadID: thread.ID, TurnID: turn.ID, AgentID: "manager", ToolCallID: "collection"}
	old, err := db.OpenTaskPanel(ctx, legacy, source)
	if err != nil {
		t.Fatal(err)
	}
	oldUpdate := taskpanel.Update{ProtocolVersion: taskpanel.ProtocolVersion, PanelID: old.ID,
		OperationID: "old-query", BaseRevision: old.Revision, Kind: "patch",
		Operations: json.RawMessage(`[{"op":"add","path":"/query","value":"saved-query"}]`)}
	receipt, err := db.PublishTaskPanel(ctx, source, oldUpdate)
	if err != nil {
		t.Fatal(err)
	}
	newUpdate := taskpanel.Update{ProtocolVersion: taskpanel.ProtocolVersion, PanelID: old.ID,
		OperationID: "cloud-results", BaseRevision: receipt.Revision, Kind: "patch",
		Operations: json.RawMessage(`[{"op":"add","path":"/clouds","value":[]}]`)}
	if _, err := db.PublishTaskPanel(ctx, source, newUpdate); !errors.Is(err, taskpanel.ErrInvalid) {
		t.Fatalf("legacy publication error = %v", err)
	}
	cursor, err := db.TaskPanelCursor(ctx, thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	// The released database predates both definition history columns.
	for _, column := range []string{"previous_definition_json", "definition_revision"} {
		if err := db.DB().Exec("ALTER TABLE task_panel_rows DROP COLUMN " + column).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	a, err := New(Options{DataDir: dataDir, Mock: true, NoAssets: true, DeferRestore: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Shutdown(ctx) })
	upgraded, changes, err := a.Store.TaskPanelReadForTransfer(ctx, thread.ID, old.ID, &receipt.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if upgraded.ID != old.ID || !taskpanel.DefinitionsMatch(upgraded.Definition, current) ||
		upgraded.Revision != receipt.Revision+1 || !bytes.Equal(upgraded.State, receipt.State) ||
		upgraded.Source != receipt.Source || changes != nil {
		t.Fatalf("startup transfer = plugin %q, revision %d, state %s, source %+v, patch %+v",
			upgraded.PluginVersion, upgraded.Revision, upgraded.State, upgraded.Source, changes)
	}
	list, after, err := a.Store.SnapshotTaskPanels(ctx, thread.ID)
	if err != nil || len(list) != 1 || list[0].Revision != upgraded.Revision || after != cursor {
		t.Fatalf("snapshot count = %d, cursor = %d, want %d, error = %v", len(list), after, cursor, err)
	}

	duplicate, err := a.Store.PublishTaskPanel(ctx, source, oldUpdate)
	if err != nil || duplicate.Revision != receipt.Revision || !bytes.Equal(duplicate.State, receipt.State) ||
		!taskpanel.DefinitionsMatch(duplicate.Definition, legacy) {
		t.Fatalf("legacy duplicate = version %q, revision %d, state %s, error %v",
			duplicate.PluginVersion, duplicate.Revision, duplicate.State, err)
	}
	if _, err := a.Store.PublishTaskPanel(ctx, source, newUpdate); !errors.Is(err, taskpanel.ErrConflict) {
		t.Fatalf("old revision error = %v", err)
	}
	newUpdate.BaseRevision = upgraded.Revision
	produced, err := a.Store.PublishTaskPanel(ctx, source, newUpdate)
	if err != nil || produced.PluginVersion != current.Version || !bytes.Contains(produced.State, []byte(`"clouds":[]`)) {
		t.Fatalf("new collection = version %q, state %s, error %v", produced.PluginVersion, produced.State, err)
	}
}
