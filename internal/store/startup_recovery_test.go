package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestStartupGenerationIndexTracksBothReceiptsAndTheirAtomicFailure(t *testing.T) {
	st := openTestStore(t)
	thread := &Thread{Title: "Indexed generation"}
	if err := st.CreateThread(thread); err != nil {
		t.Fatal(err)
	}
	turn := seedTurn(t, st, thread.ID, "user_message", "request")
	if err := st.FinishTurn(turn.ID, TurnDone, "closed", ""); err != nil {
		t.Fatal(err)
	}
	appendReceipt := func(kind, messageID, text string) Event {
		t.Helper()
		ev := Event{ThreadID: thread.ID, TurnID: turn.ID, Kind: kind, MessageID: messageID, Text: text}
		if err := st.AppendEvent(&ev); err != nil {
			t.Fatal(err)
		}
		return ev
	}
	check := func(want ...Event) {
		t.Helper()
		got, err := st.StartupGenerationReceipts(t.Context(), thread.ID)
		if err != nil || len(got) != len(want) {
			t.Fatalf("generation pointers=%+v want=%+v err=%v", got, want, err)
		}
		for i := range want {
			if got[i].ID != want[i].ID || got[i].Seq != want[i].Seq {
				t.Fatalf("generation pointers=%+v want=%+v", got, want)
			}
		}
	}
	appendReceipt("generation_started", "same-output", "")
	interrupted := appendReceipt("generation_interrupted", "same-output", `{"recovery":"pending"}`)
	restarted := appendReceipt("generation_started", "same-output", "")
	check(interrupted, restarted)
	appendReceipt("generation_completed", "same-output", "")
	check(interrupted)
	appendReceipt("generation_recovered", "same-output", "")
	check()
	appendReceipt("generation_interrupted", "malformed-output", `{"recovery":"pending","reason":42}`)
	appendReceipt("generation_interrupted", "duplicate-field", `{"recovery":"pending","Recovery":"none"}`)
	check()

	// An index save failure rolls back the trigger's source INSERT as well.
	if err := st.db.Exec(`CREATE TRIGGER refuse_startup_generation BEFORE INSERT ON startup_recovery_generations
		WHEN NEW.message_id='must-rollback' BEGIN SELECT RAISE(ABORT, 'index unavailable'); END`).Error; err != nil {
		t.Fatal(err)
	}
	if err := st.AppendEvent(&Event{ThreadID: thread.ID, TurnID: turn.ID, Kind: "generation_started", MessageID: "must-rollback"}); err == nil {
		t.Fatal("source receipt succeeded without an index write")
	}
	var count int64
	if err := st.db.Model(&Event{}).Where("message_id=?", "must-rollback").Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("receipt committed without index: count=%d err=%v", count, err)
	}
}

func TestStartupIndexRebuildsOnlyInvalidatedConversationsAfterRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	a := &Thread{Title: "Rewound conversation"}
	if err := st.CreateThread(a); err != nil {
		t.Fatal(err)
	}
	b := &Thread{Title: "Unchanged conversation"}
	if err := st.CreateThread(b); err != nil {
		t.Fatal(err)
	}
	turnA := seedTurn(t, st, a.ID, "user_message", "request")
	turnB := seedTurn(t, st, b.ID, "user_message", "request")
	for _, turn := range []*Turn{turnA, turnB} {
		for _, kind := range []string{"generation_started", "generation_completed"} {
			if err := st.AppendEvent(&Event{ThreadID: turn.ThreadID, TurnID: turn.ID, Kind: kind, MessageID: "output"}); err != nil {
				t.Fatal(err)
			}
		}
	}
	// An older writer changing a receipt still goes through the database
	// triggers. The resulting open generation must be found once on restart.
	if err := st.db.Delete(&Event{}, "thread_id=? AND kind='generation_completed'", a.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	st = reopened
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := st.BackfillStartupRecovery(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel=%v", err)
	}
	if count, err := st.BackfillStartupRecovery(t.Context()); err != nil || count != 1 {
		t.Fatalf("backfilled=%d err=%v", count, err)
	}
	got, err := st.StartupGenerationReceipts(t.Context(), a.ID)
	if err != nil || len(got) != 1 || got[0].Kind != "generation_started" {
		t.Fatalf("rewind lost original output: %+v %v", got, err)
	}
	for _, thread := range []string{a.ID, b.ID} {
		if err := st.DeleteThread(thread); err != nil {
			t.Fatal(err)
		}
		rows, err := st.StartupGenerationReceipts(t.Context(), thread)
		if err != nil || len(rows) != 0 {
			t.Fatalf("deleted conversation keeps pointers: %+v %v", rows, err)
		}
	}
	if count, err := st.BackfillStartupRecovery(t.Context()); err != nil || count != 0 {
		t.Fatalf("repeated backfill=%d err=%v", count, err)
	}
}

func TestStartupGenerationBackfillUsesSequenceAfterAnOlderReceiptArrivesLate(t *testing.T) {
	st := openTestStore(t)
	th := &Thread{Title: "Imported receipts"}
	if err := st.CreateThread(th); err != nil {
		t.Fatal(err)
	}
	turn := seedTurn(t, st, th.ID, "user_message", "request")
	for _, ev := range []Event{
		{ThreadID: th.ID, TurnID: turn.ID, Seq: 4, Kind: "generation_completed", MessageID: "finished"},
		{ThreadID: th.ID, TurnID: turn.ID, Seq: 3, Kind: "generation_started", MessageID: "finished"},
	} {
		if err := st.db.Create(&ev).Error; err != nil {
			t.Fatal(err)
		}
	}
	if n, err := st.BackfillStartupRecovery(t.Context()); err != nil || n != 1 {
		t.Fatalf("late import did not invalidate its projection: %d %v", n, err)
	}
	events, err := st.StartupGenerationReceipts(t.Context(), th.ID)
	if err != nil || len(events) != 0 {
		t.Fatalf("late receipt revived a completed output: %+v %v", events, err)
	}
}

func TestStartupCandidateIndexKeepsEveryOriginalRunningTurn(t *testing.T) {
	st := openTestStore(t)
	for i := range 14 {
		th := &Thread{Title: "Startup fixture"}
		if err := st.CreateThread(th); err != nil {
			t.Fatal(err)
		}
		earlier := seedTurn(t, st, th.ID, "user_message", "started")
		later := seedTurn(t, st, th.ID, "user_message", "finished")
		if err := st.FinishTurn(later.ID, TurnDone, "done", ""); err != nil {
			t.Fatal(err)
		}
		if i >= 12 {
			if err := st.FinishTurn(earlier.ID, TurnCancelled, "", "finished"); err != nil {
				t.Fatal(err)
			}
		}
	}
	candidates, err := st.StartupRecoveryCandidates(t.Context(), time.Now(), 10)
	if err != nil || len(candidates) != 12 {
		t.Fatalf("candidates=%+v err=%v", candidates, err)
	}
	for _, candidate := range candidates {
		turns, err := st.StartupTurns(t.Context(), candidate.ThreadID)
		if err != nil || !candidate.Running || len(turns) != 2 || turns[0].Status != TurnRunning || turns[1].Status != TurnDone {
			t.Fatalf("running turn hidden by latest status: %+v %+v %v", candidate, turns, err)
		}
	}
}

func TestStartupBackfillFindsAnInterruptedWorkerForAnArmedLegacyWake(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	th := &Thread{Title: "Future wake"}
	if err := st.CreateThread(th); err != nil {
		t.Fatal(err)
	}
	turn := seedTurn(t, st, th.ID, "user_message", "request")
	if err := st.FinishTurn(turn.ID, TurnDone, "waiting", ""); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := st.CreateSchedule(&Schedule{Kind: ScheduleThread, ThreadID: th.ID, Status: ScheduleActive, NextRunAt: now.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if err := st.db.Exec("DROP TRIGGER startup_recovery_event_added").Error; err != nil {
		t.Fatal(err)
	}
	if err := st.AppendEvent(&Event{ThreadID: th.ID, TurnID: turn.ID, Kind: "finished", AgentID: "legacy-worker", Err: "context canceled"}); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	st = reopened
	if n, err := st.BackfillStartupRecovery(t.Context()); err != nil || n != 1 {
		t.Fatalf("backfill=%d err=%v", n, err)
	}
	candidates, err := st.StartupRecoveryCandidates(t.Context(), now, 10)
	if err != nil || len(candidates) != 1 || candidates[0].ThreadID != th.ID || !candidates[0].Wake || candidates[0].Running {
		t.Fatalf("legacy interrupted wake was not selected: %+v %v", candidates, err)
	}
}

// The index can be rebuilt in more than one attempt without publishing a
// partial projection as clean or retrying the previously committed threads.
func TestStartupGenerationMigrationCommitsPerConversation(t *testing.T) {
	st := openTestStore(t)
	for _, id := range []string{"a", "b"} {
		if err := st.db.Create(&Thread{ID: id, Title: id}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := st.db.Exec("UPDATE startup_recovery_threads SET generation_dirty=1").Error; err != nil {
		t.Fatal(err)
	}
	if err := st.db.Exec(`CREATE TRIGGER refuse_second_backfill BEFORE UPDATE ON startup_recovery_threads
		WHEN NEW.thread_id='b' AND NEW.generation_dirty=0 BEGIN SELECT RAISE(ABORT, 'temporary error'); END`).Error; err != nil {
		t.Fatal(err)
	}
	if n, err := st.BackfillStartupRecovery(t.Context()); err == nil || n != 1 {
		t.Fatalf("partial migration=%d err=%v", n, err)
	}
	if err := st.db.Exec("DROP TRIGGER refuse_second_backfill").Error; err != nil {
		t.Fatal(err)
	}
	var pending []string
	if err := st.db.Table("startup_recovery_threads").Where("generation_dirty=1").Pluck("thread_id", &pending).Error; err != nil || len(pending) != 1 || pending[0] != "b" {
		t.Fatalf("pending=%v err=%v", pending, err)
	}
	if n, err := st.BackfillStartupRecovery(t.Context()); err != nil || n != 1 {
		t.Fatalf("retry migration=%d err=%v", n, err)
	}
}
