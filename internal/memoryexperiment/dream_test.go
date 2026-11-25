package memoryexperiment

import (
	"testing"
	"time"

	"github.com/LubyRuffy/eino-swarm/internal/memory"
)

func dreamFixture(t *testing.T) (*memory.Store, memory.Snapshot, []DreamSource, DreamOptions, DreamProposal) {
	t.Helper()
	store := memory.NewLimited(t.TempDir(), 600, 360)
	notes, _, err := store.Add("The selected parser rejects empty input.")
	if err != nil {
		t.Fatal(err)
	}
	when := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	sources := []DreamSource{{ProjectID: "project", ThreadID: "earlier", Seq: 1, Kind: "human", Text: "The selected parser accepts empty input when compatibility mode is enabled.", ObservedAt: when}}
	options := DreamOptions{ProjectID: "project", Cutoff: when.Add(time.Hour), MaxSourceChars: 2000, MaxChanges: 4}
	proposal := DreamProposal{Revision: notes.Rev, Changes: []DreamChange{{Action: "replace", OldText: notes.Entries[0], NewText: sources[0].Text, Evidence: []DreamCitation{{ThreadID: "earlier", Seq: 1, Quote: sources[0].Text}}}}}
	return store, notes, sources, options, proposal
}

// A correction must replace its old rule, retaining the condition that makes
// the apparent contradiction true, without any write during proposal checking.
func TestDreamCorrectionIsEvidenceBoundAndNotWrittenUntilApply(t *testing.T) {
	store, notes, sources, options, proposal := dreamFixture(t)
	if err := ValidateDreamProposal(proposal, notes, sources, options); err != nil {
		t.Fatal(err)
	}
	before, err := store.Read()
	if err != nil || before.Rev != notes.Rev {
		t.Fatalf("validation wrote notes: %+v %v", before, err)
	}
	got, changed, err := ApplyDream(store, proposal, sources, options)
	if err != nil || !changed || len(got.Entries) != 1 || got.Entries[0] != sources[0].Text {
		t.Fatalf("apply: %+v %v %v", got, changed, err)
	}
}

func TestDreamFutureEvidenceNeverReachesTheModelPrompt(t *testing.T) {
	_, notes, sources, options, _ := dreamFixture(t)
	sources[0].ObservedAt = options.Cutoff.Add(time.Second)
	if messages, err := BuildDreamMessages(notes, sources, options); err == nil || len(messages) != 0 {
		t.Fatalf("future source admitted: %+v %v", messages, err)
	}
}
