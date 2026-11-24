package engine

import (
	"testing"

	"github.com/LubyRuffy/eino-swarm/internal/provider"
	"github.com/LubyRuffy/eino-swarm/internal/store"
)

func TestStandaloneFindingsReuseOneConversationWithoutSurfacingQuietChecks(t *testing.T) {
	e := newTestEngine(t)
	clock := newScheduleClock()
	e.now = clock.Now
	origin, err := e.CreateThread("Source", "", "")
	if err != nil {
		t.Fatal(err)
	}
	row, err := e.CreateSchedule(ScheduleInput{
		Kind: store.ScheduleStandalone, OriginThreadID: origin.ID,
		Title: "Monitor", Prompt: scheduleWaitPrompt, EveryS: 60,
		ProviderID: origin.ProviderID, CreatedBy: store.ScheduleCreatedHuman,
	})
	if err != nil {
		t.Fatal(err)
	}
	fire := func(quiet bool) (*store.Turn, *store.ScheduleRun) {
		t.Helper()
		provider.SetMockScheduleQuiet(quiet)
		provider.SetMockScheduleFindings(!quiet)
		turn, err := e.RunScheduleNow(row.ID)
		if err != nil {
			t.Fatal(err)
		}
		run := waitForScheduleRun(t, e, turn.ScheduleRunID)
		return waitForTurn(t, e, turn.ID), run
	}
	t.Cleanup(func() {
		provider.SetMockScheduleQuiet(false)
		provider.SetMockScheduleFindings(false)
	})

	firstQuiet, firstQuietRun := fire(true)
	waitStandaloneArchived(t, e, firstQuiet.ThreadID)
	first, firstRun := fire(false)
	// The real ticker must join the same result as a manual Run now.
	if err := e.Store().UpdateSchedule(row.ID, map[string]any{"next_run_at": clock.Now()}); err != nil {
		t.Fatal(err)
	}
	e.fireDueSchedules()
	runs := scheduleRuns(t, e, row.ID)
	if len(runs) != 3 || runs[2].TurnID == "" {
		t.Fatalf("ticker did not bind the next automatic run: %+v", runs)
	}
	secondRun := waitForScheduleRun(t, e, runs[2].ID)
	second := waitForTurn(t, e, secondRun.TurnID)
	if first.ThreadID == origin.ID || first.ThreadID == firstQuiet.ThreadID ||
		second.ThreadID != first.ThreadID || second.ID == first.ID || second.Seq != first.Seq+1 {
		t.Fatalf("findings did not share the result conversation: first=%s/%s second=%s/%s quiet=%s",
			first.ThreadID, first.ID, second.ThreadID, second.ID, firstQuiet.ThreadID)
	}
	for _, run := range []*store.ScheduleRun{firstRun, secondRun} {
		if run.ThreadID != first.ThreadID || run.Status != store.ScheduleRunFindings || !run.Unread {
			t.Fatalf("findings lost their run/attention identity: %+v", run)
		}
	}
	before, err := e.Store().GetThread(first.ThreadID)
	if err != nil {
		t.Fatal(err)
	}

	lastQuiet, lastQuietRun := fire(true)
	after, err := e.Store().GetThread(first.ThreadID)
	if err != nil {
		t.Fatal(err)
	}
	if lastQuiet.ThreadID != first.ThreadID || !lastQuiet.Quiet || lastQuietRun.Status != store.ScheduleRunQuiet ||
		firstQuietRun.Status != store.ScheduleRunQuiet || firstQuietRun.Unread || lastQuietRun.Unread ||
		after.Archived || after.CompletedSeq != before.CompletedSeq || !after.LastActiveAt.Equal(before.LastActiveAt) {
		t.Fatalf("quiet check affected existing findings: run=%+v activity=%s->%s archived=%v completion=%d->%d",
			lastQuietRun, before.LastActiveAt, after.LastActiveAt, after.Archived, before.CompletedSeq, after.CompletedSeq)
	}
	for _, turn := range []*store.Turn{first, second} {
		events, err := e.Store().ListTurnEvents(turn.ID)
		if err != nil || len(events) == 0 || events[0].Kind != KindScheduleFired || events[0].Text != scheduleFiredNotice {
			t.Fatalf("findings no longer start with the scheduled notice: events=%+v err=%v", events, err)
		}
	}
}

func TestStandaloneFindingsKeepRecencyWhenAQuietCheckResumes(t *testing.T) {
	e := newTestEngine(t)
	result, err := e.CreateThread("Earlier result", "", "")
	if err != nil {
		t.Fatal(err)
	}
	row, err := e.CreateSchedule(ScheduleInput{
		Kind: store.ScheduleStandalone, Title: "Monitor", Prompt: scheduleWaitPrompt,
		EveryS: 3600, ProviderID: result.ProviderID, CreatedBy: store.ScheduleCreatedHuman,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Store().CreateRun(&store.ScheduleRun{
		ScheduleID: row.ID, ThreadID: result.ID, Kind: store.ScheduleStandalone,
		Status: store.ScheduleRunFindings, Unread: true,
	}); err != nil {
		t.Fatal(err)
	}

	// An unfinished legacy standalone turn starts from its persisted cue
	// and resumes on its original run and conversation.
	run := &store.ScheduleRun{ScheduleID: row.ID, ThreadID: result.ID}
	if err := e.Store().CreateRun(run); err != nil {
		t.Fatal(err)
	}
	turn := &store.Turn{
		ThreadID: result.ID, ProviderID: result.ProviderID, ScheduleContinue: true,
		ScheduleRunID: run.ID, UserText: ScheduleContinueText(row.Prompt),
	}
	if err := e.Store().CreateTurn(turn); err != nil {
		t.Fatal(err)
	}
	if err := e.Store().BindRunIfUnbound(result.ID, turn.ID, run.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.Store().AppendMessages(result.ID, turn.ID, []store.Message{
		{Role: "user", Source: "schedule", Content: turn.UserText},
	}); err != nil {
		t.Fatal(err)
	}

	provider.SetMockScheduleQuiet(true)
	t.Cleanup(func() { provider.SetMockScheduleQuiet(false) })
	n, err := e.ResumeOrphanedTurns()
	if err != nil || n != 1 {
		t.Fatalf("resumed %d turns, err=%v", n, err)
	}
	finished := waitForScheduleRun(t, e, run.ID)
	th, err := e.Store().GetThread(result.ID)
	if err != nil || th.Archived || !th.LastActiveAt.Equal(result.LastActiveAt) ||
		finished.Status != store.ScheduleRunQuiet || finished.ThreadID != result.ID || finished.TurnID != turn.ID {
		t.Fatalf("resumed quiet check affected the result conversation: thread=%+v run=%+v err=%v", th, finished, err)
	}
}
