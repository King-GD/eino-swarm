package store

import (
	"errors"
	"testing"

	"gorm.io/gorm"
)

func TestWorkLedgerRejectsArtifactsWithoutLinkedTaskAndFact(t *testing.T) {
	f := newNuwaFixture(t)
	thread, turn := addWorkThread(t, f.s, f.project.ID)
	task := newWorkNode(turn, "task", WorkScopeProject)
	saveTestWork(t, f.s, turn, &task, 0)
	fact := newWorkNode(turn, "fact", WorkScopeProject)
	fact.DependsOn = []string{task.ID}
	saveTestWork(t, f.s, turn, &fact, 0)
	before := mustWorkSnapshot(t, f.s, thread.ID)
	artifact := newWorkNode(turn, "artifact", WorkScopeThread)
	artifact.ArtifactPath = "results/report.md"
	for _, dependencies := range [][]string{nil, {task.ID}} {
		artifact.DependsOn = dependencies
		if err := f.s.UpsertNuwaNode(t.Context(), &artifact, 0); !errors.Is(err, ErrInvalidNuwaNode) {
			t.Fatalf("artifact without both prerequisites was accepted: depends_on=%v, err=%v", dependencies, err)
		}
	}
	if after := mustWorkSnapshot(t, f.s, thread.ID); after.ThreadRevision != before.ThreadRevision || len(after.Nodes) != len(before.Nodes) {
		t.Fatalf("a rejected artifact changed the work ledger: %+v", after)
	}
	artifact.DependsOn = []string{fact.ID}
	saveTestWork(t, f.s, turn, &artifact, 0)
}

func TestWorkLedgerLegacyArtifactCanRecoverOnTheSameRecord(t *testing.T) {
	f := newNuwaFixture(t)
	thread, turn := addWorkThread(t, f.s, f.project.ID)
	artifact := newWorkNode(turn, "artifact", WorkScopeThread)
	artifact.ID, artifact.Status, artifact.ArtifactPath = NewID("nw_"), "reported", "results/report.md"
	// Seed the durable state a previous version could write without a basis.
	err := f.s.db.Transaction(func(tx *gorm.DB) error {
		return saveWorkVersion(tx, &artifact, nil, workUpdater{threadID: thread.ID, turnID: turn.ID, by: artifact.AgentID}, NuwaWriteReceipt{})
	})
	if err != nil {
		t.Fatal(err)
	}
	legacy := findTestWork(t, mustWorkSnapshot(t, f.s, thread.ID).Nodes, artifact.ID)
	if !legacy.IncompleteBasis || legacy.Status != "reported" || legacy.Version != 1 {
		t.Fatalf("legacy record lost its original report or looked complete: %+v", legacy)
	}
	task := newWorkNode(turn, "task", WorkScopeThread)
	saved := Event{ThreadID: thread.ID, TurnID: turn.ID, Kind: "tool_result", Text: "Observed the output"}
	if err := f.s.AppendEvent(&saved); err != nil {
		t.Fatal(err)
	}
	saveTestWork(t, f.s, turn, &task, 0)
	fact := newWorkNode(turn, "fact", WorkScopeThread)
	fact.Evidence = []NuwaEvidence{{ThreadID: thread.ID, TurnID: turn.ID, Seq: saved.Seq}}
	saveTestWork(t, f.s, turn, &fact, 0)

	artifact.DependsOn = []string{task.ID, fact.ID}
	saveTestWork(t, f.s, turn, &artifact, 1)
	current := findTestWork(t, mustWorkSnapshot(t, f.s, thread.ID).Nodes, legacy.ID)
	if current.IncompleteBasis || current.Version != 2 || current.CreatedAt != legacy.CreatedAt {
		t.Fatalf("repair did not retain the original artifact: %+v", current)
	}
}
