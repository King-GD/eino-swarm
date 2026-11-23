package store

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestDistillationReceiptDoesNotOverwriteAnotherRun(t *testing.T) {
	db := openTestStore(t)
	ctx := t.Context()
	project := Project{Name: "Scoped receipt"}
	if err := db.CreateProject(&project); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"old", "new"} {
		if err := db.StartProjectDistillation(ctx, ProjectDistillation{ProjectID: project.ID, ID: id, State: "running"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.FinishProjectDistillation(ctx, ProjectDistillation{ProjectID: project.ID, ID: "old", State: "completed"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("older process replaced the receipt: %v", err)
	}
	out, err := db.ProjectDistillationReceipt(ctx, project.ID)
	if err != nil || out.ID != "new" || out.State != "running" {
		t.Fatalf("new run lost its slot: %+v, %v", out, err)
	}
}

func TestDistillationHistoryBoundsRowsButDoesNotReviveAnOversizedCorrection(t *testing.T) {
	db := openTestStore(t)
	project := Project{Name: "Bounded data"}
	if err := db.CreateProject(&project); err != nil {
		t.Fatal(err)
	}
	th := Thread{ProjectID: project.ID, Archived: true}
	if err := db.CreateThread(&th); err != nil {
		t.Fatal(err)
	}
	turn := Turn{ThreadID: th.ID, Status: TurnDone}
	if err := db.CreateTurn(&turn); err != nil {
		t.Fatal(err)
	}
	events := []*Event{
		{ThreadID: th.ID, TurnID: turn.ID, Kind: "user_message", Text: "Keep this requirement."},
		{ThreadID: th.ID, TurnID: turn.ID, Kind: "steer", Text: "Earlier caption"},
		{ThreadID: th.ID, TurnID: turn.ID, Kind: "steer_revised", Text: strings.Repeat("界", 30_000)},
	}
	for range 768 {
		events = append(events, &Event{ThreadID: th.ID, TurnID: turn.ID, Kind: "user_message", Text: "Retained input."})
	}
	if err := db.AppendEventsAtomic(events); err != nil {
		t.Fatal(err)
	}
	kinds := []string{"user_message", "steer", "steer_revised"}
	got, omitted, err := db.ProjectDistillationHistory(t.Context(), project.ID, "", false, kinds)
	if err != nil || !omitted || len(got) != 1 || len(got[0].Events) != 768 || got[0].Events[0].Text != "Retained input." {
		t.Fatalf("history window is not bounded: turns=%d omitted=%v err=%v", len(got), omitted, err)
	}
	second := Turn{ThreadID: th.ID, Status: TurnDone}
	if err := db.CreateTurn(&second); err != nil {
		t.Fatal(err)
	}
	for _, event := range events[:3] {
		ev := Event{ThreadID: th.ID, TurnID: second.ID, Kind: event.Kind, Text: event.Text}
		if err := db.AppendEvent(&ev); err != nil {
			t.Fatal(err)
		}
	}
	got, omitted, err = db.ProjectDistillationHistory(t.Context(), project.ID, th.ID, true, kinds)
	if err != nil || !omitted || len(got) != 1 || len(got[0].Events) != 1 || got[0].Events[0].Text != "Keep this requirement." {
		t.Fatalf("oversize correction made a stale steer admissible: %+v, omitted=%v, %v", got, omitted, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := db.ProjectDistillationHistory(ctx, project.ID, "", false, kinds); !errors.Is(err, context.Canceled) {
		t.Fatalf("history scan ignored cancellation: %v", err)
	}
}
