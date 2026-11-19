package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/LubyRuffy/eino-swarm/internal/store"
	"github.com/LubyRuffy/eino-swarm/internal/taskpanel"
)

func newEASMOpenFixture(t *testing.T) (*Engine, taskpanel.Definition, taskpanel.Source) {
	t.Helper()
	e := newTestEngine(t)
	thread, err := e.CreateThread("Asset panel open refusals", "", "")
	if err != nil {
		t.Fatal(err)
	}
	turn := store.Turn{ThreadID: thread.ID, Status: store.TurnDone}
	if err := e.store.CreateTurn(&turn); err != nil {
		t.Fatal(err)
	}
	source := taskpanel.Source{ThreadID: thread.ID, TurnID: turn.ID, AgentID: "manager", ToolCallID: "open-fixture"}
	definitions, err := e.TaskPanelCatalog()
	if err != nil {
		t.Fatal(err)
	}
	for _, definition := range definitions {
		if definition.ID == easmPanelID {
			return e, definition, source
		}
	}
	t.Fatal("EASM definition missing from the installed catalog")
	return nil, taskpanel.Definition{}, taskpanel.Source{}
}

// A cancelled collection cannot create a visible panel after its owner stops.
func TestEASMCancelledOpenDoesNotCreateAPanel(t *testing.T) {
	e, definition, source := newEASMOpenFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := e.openEASMWithSubject(ctx, definition, source, "Fixture organization"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled open returned %v", err)
	}
	panels, err := e.store.TaskPanels(t.Context(), source.ThreadID)
	if err != nil || len(panels) != 0 {
		t.Fatalf("cancelled open created a panel: panels=%v error=%v", panels, err)
	}
}

// Damaged saved evidence must stay available for recovery rather than being
// overwritten by another open that happens to carry the same subject.
func TestEASMOpenRefusesDamagedSavedState(t *testing.T) {
	e, definition, source := newEASMOpenFixture(t)
	opened, err := e.openEASMWithSubject(t.Context(), definition, source, "Fixture organization")
	if err != nil {
		t.Fatal(err)
	}
	damaged := []byte(`{"nodes":`)
	if err := e.store.DB().Model(&store.TaskPanelRow{}).Where("id = ?", opened.ID).Update("state", damaged).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := e.openEASMWithSubject(t.Context(), definition, source, "Fixture organization"); err == nil {
		t.Fatal("damaged saved graph was accepted")
	}
	saved, err := e.store.TaskPanel(t.Context(), source.ThreadID, opened.ID)
	if err != nil || saved.Revision != opened.Revision || !bytes.Equal(saved.State, damaged) || saved.Source != opened.Source {
		t.Fatalf("failed open replaced saved recovery evidence: panel=%+v error=%v", saved, err)
	}
}

func TestEASMOpenRefusesMissingPreviousOwnerMetadata(t *testing.T) {
	e, definition, source := newEASMOpenFixture(t)
	opened, err := e.openEASMWithSubject(t.Context(), definition, source, "Fixture organization")
	if err != nil {
		t.Fatal(err)
	}
	previous := source
	previous.TurnID = "missing-previous-owner"
	raw, _ := json.Marshal(previous)
	if err := e.store.DB().Model(&store.TaskPanelRow{}).Where("id = ?", opened.ID).Update("source_json", raw).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := e.openEASMWithSubject(t.Context(), definition, source, "Fixture organization"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing previous owner returned %v", err)
	}
	saved, err := e.store.TaskPanel(t.Context(), source.ThreadID, opened.ID)
	if err != nil || saved.Revision != opened.Revision || saved.Source != previous || !bytes.Equal(saved.State, opened.State) {
		t.Fatalf("missing-owner refusal changed the saved panel: panel=%+v error=%v", saved, err)
	}
}

func TestEASMAnOlderTurnCannotClaimTheLaterTurnPanel(t *testing.T) {
	e, definition, source := newEASMOpenFixture(t)
	later := store.Turn{ThreadID: source.ThreadID, Status: store.TurnDone}
	if err := e.store.CreateTurn(&later); err != nil {
		t.Fatal(err)
	}
	laterSource := source
	laterSource.TurnID, laterSource.ToolCallID = later.ID, "later-open"
	opened, err := e.openEASMWithSubject(t.Context(), definition, laterSource, "Fixture organization")
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := e.openEASMWithSubject(t.Context(), definition, source, "Fixture organization")
	if err != nil || reopened.ID != opened.ID || reopened.Revision != opened.Revision || reopened.Source != laterSource || !bytes.Equal(reopened.State, opened.State) {
		t.Fatalf("older turn replaced later ownership: panel=%+v error=%v", reopened, err)
	}
	saved, err := e.store.TaskPanel(t.Context(), source.ThreadID, opened.ID)
	if err != nil || saved.Source != laterSource || saved.Revision != opened.Revision {
		t.Fatalf("older open changed durable ownership: panel=%+v error=%v", saved, err)
	}
}
