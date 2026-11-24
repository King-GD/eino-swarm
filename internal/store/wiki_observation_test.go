package store

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"testing"
)

func TestWikiQuoteIdentityUsesExactCurrentEvidenceInTheProject(t *testing.T) {
	st := openTestStore(t)
	ctx := t.Context()
	project, other := Project{Name: "Project"}, Project{Name: "Other project"}
	for _, p := range []*Project{&project, &other} {
		if err := st.CreateProject(p); err != nil {
			t.Fatal(err)
		}
	}
	thread := Thread{ProjectID: project.ID}
	if err := st.CreateThread(&thread); err != nil {
		t.Fatal(err)
	}
	turn := Turn{ThreadID: thread.ID, Status: TurnDone}
	if err := st.CreateTurn(&turn); err != nil {
		t.Fatal(err)
	}
	addEvent := func(kind, body, failure string, private bool) Event {
		t.Helper()
		event := Event{ThreadID: thread.ID, TurnID: turn.ID, Kind: kind, Text: body, Err: failure, WorkRecordPayload: private}
		if err := st.AppendEvent(&event); err != nil {
			t.Fatal(err)
		}
		return event
	}
	read := func(ref HistoryReference, quote string) *HistoryIdentity {
		t.Helper()
		row, err := st.ReadProjectWikiQuoteIdentity(ctx, project.ID, ref, quote)
		if err != nil {
			t.Fatal(err)
		}
		return row
	}
	eventRef := func(event Event) HistoryReference {
		return HistoryReference{Kind: "event", ThreadID: thread.ID, Seq: event.Seq}
	}
	success := addEvent("tool_result", `{"full_command":"private argument","exit_code":0,"stdout":"observed 測定","stderr":"diagnostic"}`, "", false)
	row := read(eventRef(success), "測定\ndiagnostic")
	if row == nil || row.RowID != success.ID || row.TurnID != turn.ID || row.OriginTurnID != turn.ID || row.EventKind != "tool_result" ||
		!row.CreatedAt.Valid || !row.CreatedAt.Time.Equal(success.CreatedAt) {
		t.Fatalf("the projected evidence lost its durable identity: %+v", row)
	}
	for _, quote := range []string{"private argument", `\n`, "Observed", "測定diagnostic"} {
		if row := read(eventRef(success), quote); row != nil {
			t.Fatalf("quote outside the exact projection resolved: %q %+v", quote, row)
		}
	}
	failedEvents := []Event{
		addEvent("tool_result", `{"full_command":"observe","exit_code":1,"stdout":"untrustworthy"}`, "", false),
		addEvent("tool_result", `{"full_command":"observe","exit_code":0,"failed":true,"stdout":"untrustworthy"}`, "", false),
		addEvent("tool_result", `{"ok":false,"detail":"untrustworthy"}`, "", false),
		addEvent("tool_result", `{"success":false,"detail":"untrustworthy"}`, "", false),
		addEvent("tool_result", `untrustworthy`, "tool transport error", false),
		addEvent("tool_result", `untrustworthy`, "", true),
		addEvent("engine_restart_checkpoint", `untrustworthy`, "", false),
	}
	for _, event := range failedEvents {
		if row := read(eventRef(event), "untrustworthy"); row != nil {
			t.Fatalf("failed or private result resolved: seq=%d %+v", event.Seq, row)
		}
	}
	if row, err := st.ReadProjectWikiQuoteIdentity(ctx, other.ID, eventRef(success), "observed"); err != nil || row != nil {
		t.Fatalf("another project could read the original evidence: %+v %v", row, err)
	}
	if err := st.SetThreadProject(thread.ID, other.ID); err != nil {
		t.Fatal(err)
	}
	if row := read(eventRef(success), "observed"); row != nil {
		t.Fatalf("the original project retained a moved source: %+v", row)
	}
	if err := st.SetThreadProject(thread.ID, project.ID); err != nil {
		t.Fatal(err)
	}
	duplicate := success
	duplicate.ID = 0
	duplicate.Text = `{"stdout":"another row"}`
	if err := st.DB().Create(&duplicate).Error; err != nil {
		t.Fatal(err)
	}
	if row := read(eventRef(success), "observed"); row != nil {
		t.Fatalf("one matching quote resolved an ambiguous locator: %+v", row)
	}
	for _, invalid := range []struct {
		projectID string
		ref       HistoryReference
		quote     string
	}{
		{"", eventRef(success), "observed"},
		{project.ID, HistoryReference{Kind: "event", Seq: success.Seq}, "observed"},
		{project.ID, HistoryReference{Kind: "event", ThreadID: thread.ID}, "observed"},
		{project.ID, HistoryReference{Kind: "note", ThreadID: thread.ID, Seq: success.Seq}, "observed"},
		{project.ID, eventRef(success), " \n\t"},
	} {
		if row, err := st.ReadProjectWikiQuoteIdentity(ctx, invalid.projectID, invalid.ref, invalid.quote); row != nil || !errors.Is(err, ErrHistoryReferenceInvalid) {
			t.Fatalf("invalid history locator or quote returned %+v %v", row, err)
		}
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if row, err := st.ReadProjectWikiQuoteIdentity(cancelled, project.ID, eventRef(success), "observed"); row != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation was collapsed into missing evidence: %+v %v", row, err)
	}
}

func TestWikiQuoteIdentityFollowsTheCurrentSteerCaption(t *testing.T) {
	st := openTestStore(t)
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
	steer := Event{ThreadID: thread.ID, TurnID: turn.ID, Kind: "steer", Text: "Old wording; shared observation."}
	if err := st.AppendEvent(&steer); err != nil {
		t.Fatal(err)
	}
	messages := []Message{{Role: "user", Source: MessageSourceHuman, EventSeq: steer.Seq, Content: "[steer] " + steer.Text}}
	if err := st.AppendMessages(thread.ID, turn.ID, messages); err != nil {
		t.Fatal(err)
	}
	message := messages[0]
	ref := HistoryReference{Kind: "message", ThreadID: thread.ID, Seq: message.Seq}
	read := func(quote string) *HistoryIdentity {
		t.Helper()
		row, err := st.ReadProjectWikiQuoteIdentity(t.Context(), project.ID, ref, quote)
		if err != nil {
			t.Fatal(err)
		}
		return row
	}
	old := read("shared observation")
	if old == nil || old.RowID != message.ID || old.Role != "user" || old.Source != MessageSourceHuman ||
		old.SteerDigest != fmt.Sprintf("%x", sha256.Sum256([]byte(message.Content))) {
		t.Fatalf("original caption evidence resolved incorrectly: %+v", old)
	}
	revised := "[steer] New wording; shared observation."
	if err := st.ReviseSteerMessage(thread.ID, steer.Seq, revised); err != nil {
		t.Fatal(err)
	}
	if next := read("shared observation"); next == nil || next.RowID != old.RowID || !next.CreatedAt.Time.Equal(old.CreatedAt.Time) ||
		next.SteerDigest == old.SteerDigest || next.SteerDigest != fmt.Sprintf("%x", sha256.Sum256([]byte(revised))) {
		t.Fatalf("same quote in an edited message retained stale source identity: before=%+v after=%+v", old, next)
	}
	if row := read("Old wording"); row != nil {
		t.Fatalf("a superseded quote survived a steer edit: %+v", row)
	}
	if err := st.DeleteMessageByEventSeq(thread.ID, steer.Seq); err != nil {
		t.Fatal(err)
	}
	if row := read("shared observation"); row != nil {
		t.Fatalf("quote still available after steer retraction: %+v", row)
	}
}
