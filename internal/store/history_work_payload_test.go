package store

import (
	"errors"
	"testing"
)

func TestHistoryReportCannotUsePrivateWorkToolPayloads(t *testing.T) {
	f := newHistoryEvidenceFixture(t)
	ctx := t.Context()
	for _, tc := range []struct {
		kind string
		seq  int64
	}{
		{kind: "event", seq: f.event.Seq},
		{kind: "message", seq: f.message.Seq},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			switch tc.kind {
			case "event":
				if err := f.s.DB().Model(&Event{}).Where("thread_id = ? AND seq = ?", f.source.ID, tc.seq).
					Update("work_record_payload", true).Error; err != nil {
					t.Fatal(err)
				}
			case "message":
				if err := f.s.DB().Model(&Message{}).Where("thread_id = ? AND seq = ?", f.source.ID, tc.seq).
					Updates(map[string]any{"role": "tool", "work_record_payload": true}).Error; err != nil {
					t.Fatal(err)
				}
			}
			refs := []HistoryReference{{Kind: tc.kind, ThreadID: f.source.ID, Seq: tc.seq}}
			if rows, err := f.s.ReadHistorySources(ctx, f.project.ID, refs, 10000); !errors.Is(err, ErrHistorySourceUnavailable) || len(rows) != 0 {
				t.Fatalf("private payload resolved by direct reference: rows=%v err=%v", rows, err)
			}
			if rows, err := f.s.ReadProjectHistoryIdentities(ctx, f.project.ID, refs); err != nil || len(rows) != 1 || rows[0] != nil {
				t.Fatalf("private identity exposed: rows=%v err=%v", rows, err)
			}
		})
	}
}

func TestProjectDistillationReadsOriginalEventsWithoutWorkToolPayloads(t *testing.T) {
	f := newHistoryEvidenceFixture(t)
	ctx := t.Context()
	turn := Turn{ThreadID: f.source.ID, ProjectID: f.project.ID, Status: TurnDone}
	if err := f.s.CreateTurn(&turn); err != nil {
		t.Fatal(err)
	}
	events := []*Event{
		{ThreadID: f.source.ID, TurnID: turn.ID, Kind: "tool_call", Text: "original call"},
		{ThreadID: f.source.ID, TurnID: turn.ID, Kind: "tool_result", Text: "private record contents", WorkRecordPayload: true},
	}
	if err := f.s.AppendEventsAtomic(events); err != nil {
		t.Fatal(err)
	}
	turns, _, err := f.s.ProjectDistillationHistory(ctx, f.project.ID, f.source.ID, false, []string{"tool_call", "tool_result"})
	if err != nil || len(turns) != 1 || len(turns[0].Events) != 1 || turns[0].Events[0].Text != "original call" {
		t.Fatalf("distillation exposed private work payload or lost original evidence: %+v, %v", turns, err)
	}
}
