package store

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestDistillationQueueFreezesIdentityAndKeepsAResultWithItsOriginalCall(t *testing.T) {
	st := openTestStore(t)
	ctx := t.Context()
	project := Project{Name: "Project"}
	if err := st.CreateProject(&project); err != nil {
		t.Fatal(err)
	}
	thread := Thread{ProjectID: project.ID}
	peer := Thread{ProjectID: project.ID}
	foreign := Thread{}
	for _, th := range []*Thread{&thread, &peer, &foreign} {
		if err := st.CreateThread(th); err != nil {
			t.Fatal(err)
		}
	}
	ok := Turn{ThreadID: thread.ID, Status: TurnDone}
	failed := Turn{ThreadID: thread.ID, Status: TurnError}
	running := Turn{ThreadID: thread.ID, Status: TurnRunning}
	other := Turn{ThreadID: peer.ID, Status: TurnDone}
	outside := Turn{ThreadID: foreign.ID, Status: TurnDone}
	for _, turn := range []*Turn{&ok, &failed, &running, &other, &outside} {
		if err := st.CreateTurn(turn); err != nil {
			t.Fatal(err)
		}
	}
	appendEvent := func(turn Turn, kind, text, actor string) Event {
		t.Helper()
		ev := Event{TurnID: turn.ID, ThreadID: turn.ThreadID, Kind: kind, Text: text, AgentID: actor, ToolCallID: "shared"}
		if err := st.AppendEvent(&ev); err != nil {
			t.Fatal(err)
		}
		return ev
	}
	human := appendEvent(ok, "user_message", "Retained request.", "")
	call := appendEvent(ok, "tool_call", "inspect()", "author")
	appendEvent(ok, "tool_call", "unrelated()", "other-worker")
	result := appendEvent(ok, "tool_result", "Retained observation.", "author")
	large := appendEvent(ok, "agent_message", strings.Repeat("z", 65537), "author")
	failedHuman := appendEvent(failed, "user_message", "Direct requirement.", "")
	appendEvent(failed, "agent_message", "Do not admit failed analysis.", "author")
	appendEvent(running, "user_message", "Unfinished work.", "")
	peerEvidence := appendEvent(other, "user_message", "Other conversation.", "")
	appendEvent(outside, "user_message", "Outside the project.", "")

	for scope, want := range map[string]string{"": other.ID, thread.ID: failed.ID, foreign.ID: ""} {
		anchor, err := st.ProjectDistillationAnchor(ctx, project.ID, scope)
		if err != nil || anchor.ID != want {
			t.Fatalf("Trace anchor used an ineligible turn: scope=%s anchor=%s want=%s %v", scope, anchor.ID, want, err)
		}
	}
	if calls, err := st.DistillationToolCalls(ctx, nil); err != nil || len(calls) != 0 {
		t.Fatalf("empty batch unexpectedly loaded calls: %v %v", calls, err)
	}

	makeReceipt := func(n int) ([]byte, error) { return []byte(`{"state":"running"}`), nil }
	record := ProjectDistillation{ProjectID: project.ID, ID: "thread-job", State: "running"}
	total, err := st.StartDistillationQueue(ctx, record, thread.ID, makeReceipt)
	if err != nil || total != 4 {
		t.Fatalf("task count %d, %v", total, err)
	}
	empty := ProjectDistillation{ProjectID: project.ID, ID: "empty", State: "running"}
	if _, err := st.StartDistillationQueue(ctx, empty, foreign.ID, makeReceipt); !errors.Is(err, ErrNotFound) {
		t.Fatalf("empty source range replaced a saved queue: %v", err)
	}
	refusal := errors.New("receipt failed")
	replacement := ProjectDistillation{ProjectID: project.ID, ID: "failed-replacement", State: "running"}
	if _, err := st.StartDistillationQueue(ctx, replacement, "", func(int) ([]byte, error) { return nil, refusal }); !errors.Is(err, refusal) {
		t.Fatalf("failed receipt creation returned %v", err)
	}
	appendEvent(ok, "user_message", "Arrived after starting.", "")
	window, err := st.DistillationWindow(ctx, record.ID, 0, 2)
	if err != nil || len(window) != 2 || window[0].Position != human.ID ||
		window[1].Position != result.ID || window[1].CallID != call.ID {
		t.Fatalf("wrong source order or tool pairing: %+v, %v", window, err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := st.DistillationToolCalls(cancelled, []uint{call.ID}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation was masked as an absent tool call: %v", err)
	}
	calls, err := st.DistillationToolCalls(ctx, []uint{call.ID, result.ID})
	if err != nil || len(calls) != 1 || calls[call.ID].Text != "inspect()" {
		t.Fatalf("wrong evidence call: %+v, %v", calls, err)
	}
	if err := st.DB().Delete(&Event{}, failedHuman.ID).Error; err != nil {
		t.Fatal(err)
	}
	window, err = st.DistillationWindow(ctx, record.ID, result.ID, 64)
	if err != nil || len(window) != 2 || window[0].Position != large.ID || !window[0].Oversize ||
		window[0].Text != "" || window[1].Position != failedHuman.ID || window[1].ID != 0 {
		t.Fatalf("snapshot hid missing evidence or read oversized payload: %+v, %v", window, err)
	}
	meta, err := st.DistillationMetadataWindow(ctx, record.ID, 0, 4)
	if err != nil || len(meta) != 4 || meta[0].Text != "" || meta[1].CallID != call.ID || meta[2].Position != large.ID || meta[3].ID != 0 {
		t.Fatalf("metadata window lost identities or returned source text: %+v %v", meta, err)
	}
	part, err := st.ReadDistillationText(ctx, record.ID, large.ID, 65530, 4)
	if err != nil || part.Text != "zzzz" || part.TotalBytes != 65537 {
		t.Fatalf("bounded read lost a long source's tail: %+v %v", part, err)
	}
	if _, err := st.ReadDistillationText(ctx, record.ID, failedHuman.ID, 0, 16); !errors.Is(err, ErrHistorySourceUnavailable) {
		t.Fatalf("removed source became readable: %v", err)
	}
	if _, err := st.ReadDistillationText(ctx, record.ID, peerEvidence.ID, 0, 16); !errors.Is(err, ErrHistorySourceUnavailable) {
		t.Fatalf("task segment read crossed its frozen scope: %v", err)
	}
	if _, err := st.ReadDistillationText(cancelled, record.ID, large.ID, 0, 16); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation was masked as an absent segment: %v", err)
	}
	record.CursorEventID, record.State = result.ID, "interrupted"
	if err := st.DB().Exec(`CREATE TRIGGER reject_review_update BEFORE UPDATE ON project_distillations
		BEGIN SELECT RAISE(ABORT,'fixture rejected transaction'); END`).Error; err != nil {
		t.Fatal(err)
	}
	if err := st.CheckpointDistillation(ctx, record, 0); err == nil {
		t.Fatal("failed checkpoint reported success")
	}
	if err := st.ResumeDistillation(ctx, ProjectDistillation{ProjectID: project.ID, ID: record.ID}, "running"); err == nil {
		t.Fatal("failed resume claim reported success")
	}
	saved, err := st.ProjectDistillationReceipt(ctx, project.ID)
	if err != nil || saved.CursorEventID != 0 || saved.State != "running" {
		t.Fatalf("failed update mutated progress: %+v, %v", saved, err)
	}
	if err := st.DB().Exec("DROP TRIGGER reject_review_update").Error; err != nil {
		t.Fatal(err)
	}
	if err := st.CheckpointDistillation(ctx, record, 0); err != nil {
		t.Fatal(err)
	}
	if err := st.CheckpointDistillation(ctx, record, 0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a stale state/cursor replaced the checkpoint: %v", err)
	}
	if err := st.ResumeDistillation(ctx, record, "interrupted"); err != nil {
		t.Fatal(err)
	}
	if err := st.ResumeDistillation(ctx, record, "interrupted"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("resume claimed a running review twice: %v", err)
	}
	record.State, record.CursorEventID = "completed", failedHuman.ID
	if err := st.CheckpointDistillation(ctx, record, result.ID); err != nil {
		t.Fatal(err)
	}
	window, err = st.DistillationWindow(ctx, record.ID, 0, 64)
	if err != nil || len(window) != 0 {
		t.Fatalf("completed queue retained source rows: %+v %v", window, err)
	}
	record.ID, record.State, record.CursorEventID = "project-job", "running", 0
	total, err = st.StartDistillationQueue(ctx, record, "", makeReceipt)
	if err != nil || total != 5 {
		t.Fatalf("project did not add its peer and later evidence: %d, %v", total, err)
	}
	window, err = st.DistillationWindow(ctx, record.ID, large.ID, 64)
	if err != nil || len(window) != 2 || window[0].Position != peerEvidence.ID {
		t.Fatalf("project did not keep the peer in order: %+v %v", window, err)
	}
	if err := st.DeleteProject(project.ID); err != nil {
		t.Fatal(err)
	}
	window, err = st.DistillationWindow(ctx, record.ID, 0, 64)
	if err != nil || len(window) != 0 {
		t.Fatalf("deleted project left a review queue: %+v %v", window, err)
	}
}

func TestDistillationQueueExcludesWorkRecordPayloadsFromNewAndResumedReviews(t *testing.T) {
	st := openTestStore(t)
	ctx := t.Context()
	project := Project{Name: "Project"}
	if err := st.CreateProject(&project); err != nil {
		t.Fatal(err)
	}
	thread := Thread{ProjectID: project.ID}
	if err := st.CreateThread(&thread); err != nil {
		t.Fatal(err)
	}
	turn := Turn{ThreadID: thread.ID, Status: TurnDone}
	if err := st.CreateTurn(&turn); err != nil {
		t.Fatal(err)
	}
	add := func(kind, call string, flagged bool) uint {
		t.Helper()
		event := Event{ThreadID: thread.ID, TurnID: turn.ID, Kind: kind, Text: "source text", ToolCallID: call, WorkRecordPayload: flagged}
		if err := st.AppendEvent(&event); err != nil {
			t.Fatal(err)
		}
		return event.ID
	}
	privateCall := add("tool_call", "private", true)
	add("tool_result", "private", true)
	if anchor, err := st.ProjectDistillationAnchor(ctx, project.ID, thread.ID); err != nil || anchor.ID != "" {
		t.Fatalf("private work alone produced an eligible review anchor: %s, %v", anchor.ID, err)
	}
	call := add("tool_call", "ordinary", false)
	result := add("tool_result", "ordinary", false)
	human := add("user_message", "", false)
	job := ProjectDistillation{ProjectID: project.ID, ID: "review", State: "running"}
	makeReceipt := func(int) ([]byte, error) { return []byte(`{"state":"running"}`), nil }
	if count, err := st.StartDistillationQueue(ctx, job, thread.ID, makeReceipt); err != nil || count != 2 {
		t.Fatalf("work payload entered the admission denominator: %d, %v", count, err)
	}
	// A resumed snapshot can predate the work-record tag. Keep its saved
	// positions while refusing to load the now-private text and call.
	if err := st.DB().Model(&Event{}).Where("id IN ?", []uint{human, call}).Update("work_record_payload", true).Error; err != nil {
		t.Fatal(err)
	}
	rows, err := st.DistillationWindow(ctx, job.ID, 0, 10)
	if err != nil || len(rows) != 2 || rows[0].Position != result || rows[0].CallID != call ||
		rows[1].Position != human || rows[1].ID != 0 || rows[1].Text != "" {
		t.Fatalf("resumed snapshot loaded a private payload or moved its position: %+v, %v", rows, err)
	}
	if calls, err := st.DistillationToolCalls(ctx, []uint{privateCall, call}); err != nil || len(calls) != 0 {
		t.Fatalf("work payload was read as supporting evidence: %+v, %v", calls, err)
	}
	if calls, err := st.DistillationCallHeads(ctx, []uint{privateCall, call}); err != nil || len(calls) != 0 {
		t.Fatalf("full-text review loaded a private call head: %+v %v", calls, err)
	}
	if _, err := st.ReadDistillationText(ctx, job.ID, human, 0, 16); !errors.Is(err, ErrHistorySourceUnavailable) {
		t.Fatalf("full-text review loaded a private segment: %v", err)
	}
}
