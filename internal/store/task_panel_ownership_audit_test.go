package store

import (
	"errors"
	"testing"

	"github.com/LubyRuffy/eino-swarm/internal/taskpanel"
)

// A data author is not necessarily the owner of a still-running source. Until
// source leases are represented independently, an unrelated update must either
// refuse ownership takeover or retain the durable running-source observation.
func TestTaskPanelAuditRunningSourceCannotDisappearOnOtherPublisher(t *testing.T) {
	f := newPanelFixture(t)
	opened := f.open(t)
	running, err := f.s.StartTaskPanelSource(t.Context(), f.src, opened.ID)
	if err != nil {
		t.Fatal(err)
	}
	other := f.src
	other.AgentID = "other-worker"
	other.Generation = 1
	other.ToolCallID = "other-observation"
	updated, err := f.s.PublishTaskPanel(t.Context(), other, f.update(running, "other-data-author", 3))
	if errors.Is(err, taskpanel.ErrConflict) {
		return
	} // An exclusive-source contract is safe too.
	if err != nil {
		t.Fatal(err)
	}
	recovered, err := f.s.RecoverTaskPanelSources(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if updated.SourceStatus != "running" || recovered != 1 {
		t.Fatalf("running source disappeared without finishing: data author=%+v source_status=%q startup recovered=%d", updated.Source, updated.SourceStatus, recovered)
	}
}

// An accepted duplicate is a read of its immutable receipt, not a new write.
// Exclusive ownership must not make that receipt disappear or transfer a lease.
func TestTaskPanelAuditRunningSourceAllowsPastReceiptButRefusesNewWriter(t *testing.T) {
	f := newPanelFixture(t)
	opened := f.open(t)
	other := f.src
	other.AgentID = "observer"
	other.Generation = 1
	other.ToolCallID = "old-data"
	original := f.update(opened, "old-observation", 7)
	accepted, err := f.s.PublishTaskPanel(t.Context(), other, original)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := f.s.PublishTaskPanel(t.Context(), f.src, f.update(accepted, "source-claim", 7))
	if err != nil {
		t.Fatal(err)
	}
	running, err := f.s.StartTaskPanelSource(t.Context(), f.src, claimed.ID)
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := f.s.PublishTaskPanel(t.Context(), other, original)
	if err != nil || duplicate.Revision != accepted.Revision {
		t.Fatal(duplicate, err)
	}
	if _, err = f.s.PublishTaskPanel(t.Context(), other, f.update(running, "new-observation", 9)); !errors.Is(err, taskpanel.ErrConflict) {
		t.Fatal(err)
	}
	unchanged, err := f.s.TaskPanel(t.Context(), f.thread.ID, running.ID)
	if err != nil || unchanged.Source != f.src || unchanged.SourceStatus != "running" || unchanged.Revision != running.Revision {
		t.Fatal(unchanged, err)
	}
	finished, err := f.s.FinishTaskPanelSource(t.Context(), f.src, running.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.PublishTaskPanel(t.Context(), other, f.update(finished, "new-observation", 9)); err != nil {
		t.Fatal("finished source retained write lock", err)
	}
}

func TestTaskPanelAuditThreadRecoveryLeavesOtherSourcesUntouched(t *testing.T) {
	f := newPanelFixture(t)
	opened := f.open(t)
	if _, err := f.s.StartTaskPanelSource(t.Context(), f.src, opened.ID); err != nil {
		t.Fatal(err)
	}
	if n, err := f.s.RecoverTaskPanelSourcesForThread(t.Context(), "absent-thread"); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	before, err := f.s.TaskPanel(t.Context(), f.thread.ID, opened.ID)
	if err != nil || before.SourceStatus != "running" {
		t.Fatal(before, err)
	}
	if n, err := f.s.RecoverTaskPanelSourcesForThread(t.Context(), f.thread.ID); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	if _, err := f.s.RecoverTaskPanelSourcesForThread(t.Context(), ""); !errors.Is(err, taskpanel.ErrInvalid) {
		t.Fatal(err)
	}
}

// Claim publication and starting a source are separate transactions. A writer
// between them may win admission, but the displaced source must refuse before
// its external runner starts; its deferred cleanup cannot mark that writer.
func TestTaskPanelAuditInterveningWriterRefusesUnstartedSourceAndCleanup(t *testing.T) {
	f := newPanelFixture(t)
	opened := f.open(t)
	source := f.src
	source.ToolCallID = "source-attempt"
	claim := taskpanel.Update{ProtocolVersion: 1, PanelID: opened.ID, OperationID: "source-attempt:start", BaseRevision: opened.Revision, Kind: "patch", Operations: []byte(`[]`)}
	claimed, err := f.s.PublishTaskPanel(t.Context(), source, claim)
	if err != nil {
		t.Fatal(err)
	}
	other := f.src
	other.ToolCallID = "intervening-observation"
	observed, err := f.s.PublishTaskPanel(t.Context(), other, f.update(claimed, "intervening-write", 9))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.StartTaskPanelSource(t.Context(), source, opened.ID); !errors.Is(err, taskpanel.ErrConflict) {
		t.Fatal("displaced source was started", err)
	}
	if _, err = f.s.InterruptTaskPanelSource(t.Context(), source, opened.ID); !errors.Is(err, taskpanel.ErrConflict) {
		t.Fatal("unstarted source cleanup changed successor", err)
	}
	current, err := f.s.TaskPanel(t.Context(), f.thread.ID, opened.ID)
	if err != nil || current.Source != other || current.Revision != observed.Revision || current.SourceStatus != "" {
		t.Fatal(current, err)
	}
}
