package store

import (
	"errors"
	"testing"
)

func TestSaveDistilledWorkRollsBackAnInvalidBatchAndRejectsChangedHistory(t *testing.T) {
	s := newNuwaFixture(t).s
	ctx := t.Context()
	thread, turn := addWorkThread(t, s, "")
	source := Event{ThreadID: thread.ID, TurnID: turn.ID, Kind: "user", Text: "Original evidence"}
	if err := s.AppendEvent(&source); err != nil {
		t.Fatal(err)
	}
	state, err := s.WorkDistillationState(ctx, thread.ID)
	if err != nil || state.HasRecords || state.LastEventID != source.ID {
		t.Fatalf("unexpected extraction state: %+v, err=%v", state, err)
	}
	good := newWorkNode(turn, "fact", WorkScopeThread)
	good.Evidence = []NuwaEvidence{{ThreadID: thread.ID, TurnID: turn.ID, Seq: source.Seq}}
	invalid := good
	invalid.Evidence = []NuwaEvidence{{ThreadID: thread.ID, TurnID: turn.ID, Seq: source.Seq + 100}}
	if err := s.SaveDistilledWork(ctx, state, []DistilledWorkRecord{{Key: "good", Node: good}, {Key: "invalid", Node: invalid}}); !errors.Is(err, ErrInvalidNuwaNode) {
		t.Fatalf("missing evidence did not reject the entire batch: %v", err)
	}
	orphan := good
	orphan.Kind, orphan.ArtifactPath = "artifact", "results/report.md"
	for name, record := range map[string]DistilledWorkRecord{
		"duplicate key":         {Key: "good", Node: good},
		"unresolved key":        {Key: "second", Node: good, DependsOn: []string{"unknown"}},
		"artifact with no task": {Key: "result", Node: orphan, DependsOn: []string{"good"}},
	} {
		if err := s.SaveDistilledWork(ctx, state, []DistilledWorkRecord{{Key: "good", Node: good}, record}); !errors.Is(err, ErrInvalidNuwaNode) {
			t.Fatalf("%s did not reject the entire batch: %v", name, err)
		}
	}
	if view := mustWorkSnapshot(t, s, thread.ID); len(view.Nodes) != 0 || view.ThreadRevision != state.ThreadRevision {
		t.Fatalf("invalid batch left records or advanced the revision: %+v", view)
	}
	later := Event{ThreadID: thread.ID, TurnID: turn.ID, Kind: "user", Text: "The observation was corrected"}
	if err := s.AppendEvent(&later); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveDistilledWork(ctx, state, []DistilledWorkRecord{{Key: "good", Node: good}}); !errors.Is(err, ErrNuwaConflict) {
		t.Fatalf("changed history accepted an old extraction: %v", err)
	}
}
