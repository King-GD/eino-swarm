package engine

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LubyRuffy/eino-swarm/internal/memory"
	"github.com/LubyRuffy/eino-swarm/internal/store"
)

// An accepted batch and a subsequent fold are separate writes. A fold failure
// must not erase the accepted receipt or claim the whole review completed.
func TestReviewPostCommitFoldFailureKeepsCommittedChangesAndReportsError(t *testing.T) {
	e := newTestEngine(t)
	th, turn, pc := qualityReviewTurn(t, e, store.TurnDone, nil)
	batch := testReviewBatch(t, map[int64]store.Event{1: {Seq: 1, Kind: KindUser, Text: "Use concise reports and keep index recovery steps."}})
	batch.store = pc.memory
	quote := []reviewCitation{{Seq: 1, Quote: "Use concise reports and keep index recovery steps."}}
	invokeTestReviewBatch(t, batch, []reviewOperation{
		{Target: memory.ToolMemory, Action: "add", Content: "Use concise reports.", Evidence: quote},
		{Target: memory.ToolSkillManage, Action: "create", Name: "index-recovery", Description: "Recover an index.", Content: "Inspect, rebuild, and verify.", Evidence: quote},
	}, true)
	for _, name := range []string{"weekly-rollup-notes", "weekly-rollup-send"} {
		plantProjectSkill(t, e, th.ProjectID, name, "Build a weekly rollup.", "Gather notes and send the weekly report.")
	}
	trap := filepath.Join(pc.memory.Dir(), memory.SkillsDir, "weekly-rollup", "SKILL.md.tmp")
	if err := os.MkdirAll(trap, 0700); err != nil {
		t.Fatal(err)
	}
	e.finishReview(th.ID, turn, pc, *batch.outcome)
	event := waitForReview(t, e, turn.ID)
	out := decodeReview(t, event)
	if out.StopReason != "error" || out.Err == "" || !out.Changed || len(out.Changes) != 2 || len(out.Skills) != 1 || out.Notes["add"] != 1 || strings.Contains(event.Text, pc.memory.Dir()) {
		t.Fatalf("fold failure concealed or erased committed changes: %+v", out)
	}
	assertReviewBatchNotes(t, batch, "Use concise reports.")
	if skill, err := pc.memory.ReadSkill("index-recovery"); err != nil || skill.Body != "Inspect, rebuild, and verify." {
		t.Fatalf("fold failure changed the committed skill: %+v err=%v", skill, err)
	}
}

// The commit CAS and the following apply_review share the same fixed revision.
// The first conflict remains repair feedback; another note proposal must then
// stop at the preflight revision check, with no rebase or overwrite.
func TestReviewFinalCommitConflictFeedbackRetainsFixedRevisionAndSafeMetadata(t *testing.T) {
	batch := testReviewBatch(t, map[int64]store.Event{1: {Seq: 1, Kind: KindUser, Text: "Use concise reports."}})
	before, err := batch.store.Overwrite("Use detailed reports.")
	if err != nil {
		t.Fatal(err)
	}
	batch.notesRev = before.Rev
	draft, err := batch.store.BeginNotesDraft()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := draft.Store.Replace("detailed", "Use concise reports."); err != nil {
		t.Fatal(err)
	}
	const edited = "Human correction with private-source-marker."
	if _, err := batch.store.OverwriteIf(before.Rev, edited); err != nil {
		t.Fatal(err)
	}
	_, changed, commitErr := draft.Commit()
	if changed || !errors.Is(commitErr, memory.ErrConflict) {
		t.Fatalf("stale final commit changed notes: changed=%v err=%v", changed, commitErr)
	}
	operations := []reviewOperation{{Target: memory.ToolMemory, Action: "replace", OldText: "detailed", Content: "Use concise reports.", Evidence: []reviewCitation{{Seq: 1, Quote: "concise reports"}}}}
	number := batch.beginAttempt()
	batch.describeAttempt(number, operations)
	response := batch.refuse(number, -1, "storage", commitErr, operations)
	var result struct{ Success bool }
	if err := json.Unmarshal([]byte(response), &result); err != nil || result.Success || batch.complete || batch.outcome.Refused[0].Code != "conflict" || batch.outcome.Attempts[0].Status != "refused" {
		t.Fatalf("final commit conflict lost repair semantics: response=%s outcome=%+v err=%v", response, batch.outcome, err)
	}
	if _, err := batch.InvokableRun(context.Background(), encodeReviewOperations(t, operations)); !errors.Is(err, memory.ErrConflict) || batch.outcome.Attempts[1].Status != "error" {
		t.Fatalf("fixed-revision retry did not terminate: err=%v outcome=%+v", err, batch.outcome)
	}
	assertReviewBatchNotes(t, batch, edited)
	raw, _ := json.Marshal(batch.outcome)
	if strings.Contains(string(raw), "private-source-marker") || batch.outcome.Changed {
		t.Fatalf("rejected conflict retained edited notes: %s", raw)
	}
}

// The actual post-review adapter must publish a keeper that was saved before
// a later source deletion failed, and retain both source content and usage.
func TestReviewFoldPartialDeletionReportsEveryLandedSkillChange(t *testing.T) {
	requireReviewFoldRemovalPermissionFailure(t)
	engine := newTestEngine(t)
	thread, turn, pc := qualityReviewTurn(t, engine, store.TurnDone, nil)
	names := []string{"stable-audit", "stable-audit-first", "stable-audit-second"}
	bodies := []string{
		"Inspect every source record and verify the active generation before marking the recorded batch complete.",
		"Do not inspect every source record and verify the active generation before marking the recorded batch complete.",
		"Inspect every source record and wait exactly 24 seconds before marking the recorded batch complete.",
	}
	for i, name := range names {
		plantProjectSkill(t, engine, thread.ProjectID, name, "When auditing recorded source state.", bodies[i])
		for range i + 1 {
			if _, err := pc.memory.ViewSkill(name); err != nil {
				t.Fatal(err)
			}
		}
	}
	blocked := filepath.Join(pc.memory.Dir(), memory.SkillsDir, names[2])
	if err := os.Chmod(blocked, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(blocked, 0700) })
	engine.finishReview(thread.ID, turn, pc, reviewOutcome{StopReason: "completed"})
	event := waitForReview(t, engine, turn.ID)
	outcome := decodeReview(t, event)
	if !outcome.Changed || outcome.StopReason != "error" || outcome.Err == "" || len(outcome.Changes) != 1 || len(outcome.Skills) != 1 || outcome.Skills[0].Action != "merge" || outcome.Skills[0].Name != names[0] || outcome.Skills[0].Text != names[1] {
		t.Fatalf("actual fold adapter hid partial writes or claimed an undeleted source: %+v", outcome)
	}
	kept, err := pc.memory.ReadSkill(names[0])
	if err != nil || kept.Views != 3 {
		t.Fatalf("saved keeper usage was lost: %+v %v", kept, err)
	}
	remaining, err := pc.memory.ReadSkill(names[2])
	if err != nil || remaining.Views != 3 {
		t.Fatalf("readable failed source lost its own usage: %+v %v", remaining, err)
	}
	if _, err := pc.memory.ReadSkill(names[1]); !errors.Is(err, memory.ErrNoMatch) {
		t.Fatalf("first source was not actually removed: %v", err)
	}
	for _, body := range bodies {
		if !strings.Contains(strings.Join(strings.Fields(kept.Body), " "), strings.Join(strings.Fields(body), " ")) {
			t.Fatalf("partially completed fold lost full source body: %q", body)
		}
	}
	if err := os.Chmod(blocked, 0700); err != nil {
		t.Fatal(err)
	}
	engine.finishReview(thread.ID, turn, pc, reviewOutcome{StopReason: "completed"})
	kept, err = pc.memory.ReadSkill(names[0])
	if err != nil || kept.Views != 6 || !kept.LastViewedAt.Equal(remaining.LastViewedAt) {
		t.Fatalf("retry lost or double-counted source usage: %+v %v", kept, err)
	}
	events, err := engine.store.ListTurnEvents(turn.ID)
	if err != nil {
		t.Fatal(err)
	}
	var reviews []store.Event
	for _, candidate := range events {
		if candidate.Kind == KindMemoryReview {
			reviews = append(reviews, candidate)
		}
	}
	if len(reviews) != 2 {
		t.Fatalf("review receipts=%d, want both partial and retry", len(reviews))
	}
	retry := decodeReview(t, reviews[1])
	if !retry.Changed || retry.Err != "" || retry.StopReason != "completed" || len(retry.Skills) != 1 || retry.Skills[0].Text != names[2] {
		t.Fatalf("retry receipt did not name only the newly removed source: %+v", retry)
	}
}

func requireReviewFoldRemovalPermissionFailure(t *testing.T) {
	t.Helper()
	source := filepath.Join(t.TempDir(), "source")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(source, memory.SkillFile)
	const content = "permission probe"
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, memory.SkillUsageFile), []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(source, 0500); err != nil {
		t.Fatalf("set up review fold permission probe: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(source, 0700); err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Errorf("restore review fold permission probe: %v", err)
		}
	})
	err := os.RemoveAll(source)
	if err == nil {
		t.Skip("filesystem permits removal despite mode 0500; cannot inject required review fold failure")
	}
	if !os.IsPermission(err) {
		t.Fatalf("unexpected review fold removal probe error: %v", err)
	}
	body, readErr := os.ReadFile(path)
	if readErr == nil && string(body) != content {
		t.Fatal("review fold probe content changed unexpectedly")
	}
	if readErr != nil {
		if !errors.Is(readErr, os.ErrNotExist) && !os.IsPermission(readErr) {
			t.Fatalf("inspect review fold probe source: %v", readErr)
		}
		t.Skip("filesystem cannot reproduce a readable retained source after the permission error")
	}
	t.Log("real review fold removal permission probe succeeded: source retained and readable")
}
