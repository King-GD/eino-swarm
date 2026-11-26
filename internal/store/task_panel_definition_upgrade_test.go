package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/LubyRuffy/eino-swarm/internal/taskpanel"
	"gorm.io/gorm"
)

func updatedPanelDefinition(legacy taskpanel.Definition) taskpanel.Definition {
	current := legacy
	current.Version = "2"
	current.Schema = json.RawMessage(`{"type":"object","properties":{"count":{"type":"integer"},"extra":{"type":"boolean"}}}`)
	return current
}

func TestTaskPanelDefinitionUpgradePreservesProjectionVisibility(t *testing.T) {
	s := open(t)
	ctx := t.Context()
	for _, test := range []struct {
		name     string
		change   func(row *TaskPanelRow)
		migrates bool
	}{
		{name: "matching", migrates: true},
		{name: "closed", change: func(row *TaskPanelRow) { row.Closed = true }, migrates: true},
		{name: "hidden", change: func(row *TaskPanelRow) { row.Hidden = true }, migrates: true},
		{name: "unknown contract", change: func(row *TaskPanelRow) {
			var def taskpanel.Definition
			_ = json.Unmarshal(row.DefinitionJSON, &def)
			def.Description = "a different panel contract"
			row.DefinitionJSON, _ = json.Marshal(def)
		}},
		{name: "unknown field", change: func(row *TaskPanelRow) {
			row.DefinitionJSON = append(row.DefinitionJSON[:len(row.DefinitionJSON)-1], []byte(`,"display_mode":"other"}`)...)
		}},
		{name: "malformed definition", change: func(row *TaskPanelRow) { row.DefinitionJSON = []byte(`}`) }},
		{name: "invalid state", change: func(row *TaskPanelRow) { row.State = []byte(`{"count":false}`) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := panelFixtureForStore(t, s)
			p := f.open(t)
			var before TaskPanelRow
			if err := s.db.Take(&before, "id = ?", p.ID).Error; err != nil {
				t.Fatal(err)
			}
			if test.change != nil {
				test.change(&before)
				if err := s.db.Save(&before).Error; err != nil {
					t.Fatal(err)
				}
			}
			current := updatedPanelDefinition(f.def)
			for range 2 {
				if err := s.UpgradeTaskPanelDefinition(ctx, f.def, current); err != nil {
					t.Fatal(err)
				}
			}
			var after TaskPanelRow
			if err := s.db.Take(&after, "id = ?", p.ID).Error; err != nil {
				t.Fatal(err)
			}
			if after.Hidden != before.Hidden || after.Closed != before.Closed || !bytes.Equal(after.State, before.State) ||
				!bytes.Equal(after.SourceJSON, before.SourceJSON) || !after.UpdatedAt.Equal(before.UpdatedAt) {
				t.Fatalf("upgrade changed stored visibility, content, source or date: %+v", after)
			}
			if test.migrates {
				var upgraded taskpanel.Definition
				if err := json.Unmarshal(after.DefinitionJSON, &upgraded); err != nil {
					t.Fatal(err)
				}
				if !taskpanel.DefinitionsMatch(upgraded, current) || after.Revision != before.Revision+1 ||
					after.DefinitionRevision != after.Revision || !bytes.Equal(after.PreviousDefinitionJSON, before.DefinitionJSON) {
					t.Fatalf("unexpected upgrade version %q / revision %d / boundary %d", upgraded.Version, after.Revision, after.DefinitionRevision)
				}
			} else if !bytes.Equal(after.DefinitionJSON, before.DefinitionJSON) || after.Revision != before.Revision ||
				after.DefinitionRevision != 0 || len(after.PreviousDefinitionJSON) != 0 {
				t.Fatalf("unsupported projection changed: %+v", after)
			}
		})
	}
}

func TestTaskPanelDefinitionUpgradeRollsBackPartialWrites(t *testing.T) {
	s := open(t)
	ctx := t.Context()
	f := panelFixtureForStore(t, s)
	first := f.open(t)
	secondFixture := panelFixtureForStore(t, s)
	second := secondFixture.open(t)
	current := updatedPanelDefinition(f.def)
	if err := s.UpgradeTaskPanelDefinition(ctx, f.def, f.def); !errors.Is(err, taskpanel.ErrInvalid) {
		t.Fatalf("same-version upgrade error = %v", err)
	}
	invalid := current
	invalid.Schema = json.RawMessage(`{}`)
	invalid.InitialState = json.RawMessage(`null`)
	if err := s.UpgradeTaskPanelDefinition(ctx, f.def, invalid); !errors.Is(err, taskpanel.ErrInvalid) {
		t.Fatalf("invalid definition upgrade error = %v", err)
	}
	storageFailure := errors.New("cannot update definition")
	writes := 0
	if err := s.db.Callback().Update().Before("gorm:update").Register("fail_second_definition_upgrade", func(tx *gorm.DB) {
		writes++
		if writes == 2 {
			tx.AddError(storageFailure)
		}
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpgradeTaskPanelDefinition(ctx, f.def, current); !errors.Is(err, storageFailure) || writes != 2 {
		t.Fatalf("update failure = %v, writes = %d", err, writes)
	}
	for _, panel := range []taskpanel.Panel{first, second} {
		got, err := s.TaskPanel(ctx, panel.ThreadID, panel.ID)
		if err != nil || got.Revision != panel.Revision || !taskpanel.DefinitionsMatch(got.Definition, f.def) {
			t.Fatalf("rollback version = %q, revision = %d, error = %v", got.PluginVersion, got.Revision, err)
		}
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := s.UpgradeTaskPanelDefinition(cancelled, f.def, current); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled upgrade error = %v", err)
	}
	readFailure := errors.New("cannot read panel state")
	if err := s.db.Callback().Query().Before("gorm:query").Register("fail_definition_upgrade_state_read", func(tx *gorm.DB) {
		if len(tx.Statement.Selects) == 1 && tx.Statement.Selects[0] == "state" {
			tx.AddError(readFailure)
		}
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpgradeTaskPanelDefinition(ctx, f.def, current); !errors.Is(err, readFailure) {
		t.Fatalf("state read error = %v", err)
	}
}

func TestTaskPanelRewindRetainsUpgradedDefinitionAndOriginalReceipt(t *testing.T) {
	f := newPanelFixture(t)
	ctx := t.Context()
	p := f.open(t)
	oldUpdate := f.update(p, "old", 1)
	first, err := f.s.PublishTaskPanel(ctx, f.src, oldUpdate)
	if err != nil {
		t.Fatal(err)
	}
	current := updatedPanelDefinition(f.def)
	if err := f.s.UpgradeTaskPanelDefinition(ctx, f.def, current); err != nil {
		t.Fatal(err)
	}
	upgraded, err := f.s.TaskPanel(ctx, f.thread.ID, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	next := Turn{ThreadID: f.thread.ID}
	if err := f.s.CreateTurn(&next); err != nil {
		t.Fatal(err)
	}
	cut := Event{ThreadID: f.thread.ID, TurnID: next.ID, Kind: "user_message", Text: "follow-up"}
	if err := f.s.AppendEvent(&cut); err != nil {
		t.Fatal(err)
	}
	source := f.src
	source.TurnID = next.ID
	source.ToolCallID = "new-source"
	postUpgrade, err := f.s.PublishTaskPanel(ctx, source, f.update(upgraded, "new", 2))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.s.TruncateFromEventSeq(f.thread.ID, cut.Seq); err != nil {
		t.Fatal(err)
	}
	rewound, err := f.s.TaskPanel(ctx, f.thread.ID, p.ID)
	if err != nil || rewound.Revision <= postUpgrade.Revision || !bytes.Equal(rewound.State, first.State) ||
		!taskpanel.DefinitionsMatch(rewound.Definition, current) || rewound.Source != first.Source {
		t.Fatalf("rewound version %q, revision = %d, state = %s, error = %v", rewound.PluginVersion, rewound.Revision, rewound.State, err)
	}
	duplicate, err := f.s.PublishTaskPanel(ctx, f.src, oldUpdate)
	if err != nil || duplicate.Revision != first.Revision || !bytes.Equal(duplicate.State, first.State) ||
		!taskpanel.DefinitionsMatch(duplicate.Definition, f.def) {
		t.Fatalf("historical version = %q, revision = %d, error = %v", duplicate.PluginVersion, duplicate.Revision, err)
	}
}
