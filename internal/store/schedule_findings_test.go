package store

import (
	"errors"
	"testing"
)

func TestStandaloneFindingsDestinationAndProvenanceFromHistoricalRuns(t *testing.T) {
	s := openTestStore(t)
	makeThread := func(title, project string) *Thread {
		t.Helper()
		th := &Thread{Title: title, ProjectID: project}
		if err := s.CreateThread(th); err != nil {
			t.Fatal(err)
		}
		return th
	}
	origin := makeThread("Creation conversation", "")
	quiet := makeThread("Archived quiet check", "")
	legacy := makeThread("Original result", "")
	duplicate := makeThread("Historical duplicate", "")
	other := makeThread("Other project", "pj_other")
	wakeTarget := makeThread("Wake target", "")
	row := &Schedule{Kind: ScheduleStandalone, OriginThreadID: origin.ID, Title: "Monitor"}
	if err := s.CreateSchedule(row); err != nil {
		t.Fatal(err)
	}
	for _, run := range []ScheduleRun{
		{ThreadID: quiet.ID, Kind: ScheduleStandalone, Status: ScheduleRunQuiet},
		{ThreadID: wakeTarget.ID, Kind: ScheduleThread, Status: ScheduleRunFindings},
		{ThreadID: legacy.ID, Status: ScheduleRunFindings, Unread: true},
		{ThreadID: duplicate.ID, Kind: ScheduleStandalone, Status: ScheduleRunFindings, Unread: true},
		{ThreadID: other.ID, Kind: ScheduleStandalone, Status: ScheduleRunFindings},
	} {
		run.ScheduleID = row.ID
		if err := s.CreateRun(&run); err != nil {
			t.Fatal(err)
		}
	}
	assertDestination := func(project, want string) {
		t.Helper()
		got, err := s.StandaloneFindingsThread(row.ID, project)
		if err != nil || (want == "" && got != nil) || (want != "" && (got == nil || got.ID != want)) {
			t.Fatalf("destination for %q: %+v, %v, want %q", project, got, err, want)
		}
	}
	assertDestination("", legacy.ID)
	assertDestination(other.ProjectID, other.ID)
	assertDestination("pj_missing", "")
	for _, fixture := range []struct {
		threadID string
		want     bool
	}{{legacy.ID, true}, {quiet.ID, false}, {wakeTarget.ID, false}} {
		got, err := s.HasStandaloneFindings(fixture.threadID, row.ID)
		if err != nil || got != fixture.want {
			t.Fatalf("findings for %s: %v, %v, want %v", fixture.threadID, got, err, fixture.want)
		}
	}

	rows, err := s.ListThreadsWithScheduleSources(false, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, thread := range rows {
		if thread.ID == origin.ID || thread.ID == wakeTarget.ID {
			if thread.ScheduleSource != nil {
				t.Fatalf("ordinary conversation has a standalone source: %+v", thread.ScheduleSource)
			}
			continue
		}
		want := ScheduleSource{ScheduleID: row.ID, OriginThreadID: origin.ID, OriginTitle: origin.Title, OriginAvailable: true}
		if thread.ScheduleSource == nil || *thread.ScheduleSource != want {
			t.Fatalf("thread %s source=%+v want %+v", thread.ID, thread.ScheduleSource, want)
		}
	}
	if err := s.DeleteThread(origin.ID); err != nil {
		t.Fatal(err)
	}
	th, err := s.GetThreadWithScheduleSource(legacy.ID)
	if err != nil || th.ScheduleSource == nil || th.ScheduleSource.OriginAvailable ||
		th.ScheduleSource.OriginThreadID != origin.ID || th.ScheduleSource.OriginTitle != "" {
		t.Fatalf("deleted origin must have durable identity without a link: %+v, %v", th, err)
	}
	if _, err := s.GetThreadWithScheduleSource(origin.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing origin: %v", err)
	}

	// Retargeting freezes legacy kind before changing the row. The old
	// results must still identify the schedule after it becomes a wake.
	if _, err := s.PatchScheduleFields(row.ID, map[string]any{
		"kind": ScheduleThread, "thread_id": wakeTarget.ID,
	}, 0); err != nil {
		t.Fatal(err)
	}
	th, err = s.GetThreadWithScheduleSource(legacy.ID)
	if err != nil || th.ScheduleSource == nil || th.ScheduleSource.ScheduleID != row.ID {
		t.Fatalf("retargeting removed the legacy result source: %+v, %v", th, err)
	}

	// A user deleting the result leaves its old run readable, while future
	// runs can use the next surviving findings conversation.
	if err := s.DeleteThread(legacy.ID); err != nil {
		t.Fatal(err)
	}
	assertDestination("", duplicate.ID)
	runs, err := s.ListRuns(row.ID)
	if err != nil || len(runs) != 5 || !runs[2].Unread || runs[2].ThreadID != legacy.ID {
		t.Fatalf("legacy runs changed: %+v, %v", runs, err)
	}
}

func TestStandaloneFindingsLookupSurfacesUnavailableStorage(t *testing.T) {
	s := openTestStore(t)
	th := &Thread{Title: "Results"}
	if err := s.CreateThread(th); err != nil {
		t.Fatal(err)
	}
	if err := s.DB().Migrator().DropTable(&ScheduleRun{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.StandaloneFindingsThread("schedule", ""); err == nil {
		t.Fatal("a failed lookup cannot mint a duplicate conversation")
	}
	if _, err := s.HasStandaloneFindings(th.ID, "schedule"); err == nil {
		t.Fatal("a failed lookup cannot archive existing findings")
	}
	if _, err := s.GetThreadWithScheduleSource(th.ID); err == nil {
		t.Fatal("a failed source read cannot look like an ordinary conversation")
	}
	if _, err := s.ListThreadsWithScheduleSources(false, ""); err == nil {
		t.Fatal("a failed source read cannot be silently dropped from the list")
	}
}
