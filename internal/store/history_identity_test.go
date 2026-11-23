package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestKnowledgeSourceIdentityScopesLocatorsAndResolvesLegacyHumanMessages(t *testing.T) {
	s := openTestStore(t)
	project := &Project{Name: "Source fixture"}
	if err := s.CreateProject(project); err != nil {
		t.Fatal(err)
	}
	thread, outside := &Thread{ProjectID: project.ID}, &Thread{}
	for _, th := range []*Thread{thread, outside} {
		if err := s.CreateThread(th); err != nil {
			t.Fatal(err)
		}
	}
	turn := &Turn{ThreadID: thread.ID, ProjectID: project.ID, UserText: "Original request"}
	if err := s.CreateTurn(turn); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendMessages(thread.ID, turn.ID, []Message{
		{Role: "user", Content: "Original request"},
		{Role: "user", Content: "Runtime correction"},
		{Role: "assistant", Content: "No independent observation"},
		{Role: "user", Source: MessageSourceHuman, Content: "[steer] Original caption", EventSeq: 12},
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendMessages(outside.ID, "", []Message{{Role: "user", Source: MessageSourceHuman, Content: "Other scope"}}); err != nil {
		t.Fatal(err)
	}
	private := &Event{ThreadID: thread.ID, TurnID: turn.ID, Kind: "engine_restart_checkpoint"}
	observed := &Event{ThreadID: thread.ID, TurnID: turn.ID, Kind: "tool_result"}
	for _, event := range []*Event{private, observed} {
		if err := s.AppendEvent(event); err != nil {
			t.Fatal(err)
		}
	}
	refs := []HistoryReference{
		{Kind: "message", ThreadID: thread.ID, Seq: 1},
		{Kind: "message", ThreadID: thread.ID, Seq: 2},
		{Kind: "event", ThreadID: thread.ID, Seq: observed.Seq},
		{Kind: "event", ThreadID: thread.ID, Seq: private.Seq},
		{Kind: "message", ThreadID: outside.ID, Seq: 1},
		{Kind: "message", ThreadID: thread.ID, Seq: 99},
	}
	identities, err := s.ReadProjectHistoryIdentities(t.Context(), project.ID, refs)
	if err != nil || len(identities) != 6 {
		t.Fatalf("source metadata: %+v err=%v", identities, err)
	}
	if identities[0] == nil || identities[0].Source != MessageSourceHuman || identities[0].TurnID != turn.ID ||
		identities[1] == nil || identities[1].Source == MessageSourceHuman ||
		identities[2] == nil || identities[2].EventKind != "tool_result" ||
		!identities[2].CreatedAt.Valid || !identities[2].CreatedAt.Time.Equal(observed.CreatedAt) ||
		identities[3] != nil || identities[4] != nil || identities[5] != nil {
		t.Fatalf("wrong owner, human attribution, original time or private row: %+v", identities)
	}
	steerRef := []HistoryReference{{Kind: "message", ThreadID: thread.ID, Seq: 4}}
	oldSteer, err := s.ReadProjectHistoryIdentities(t.Context(), project.ID, steerRef)
	if err != nil || oldSteer[0] == nil || oldSteer[0].SteerDigest == "" || identities[0].SteerDigest != "" {
		t.Fatalf("digest of mutable caption: %+v, %v", oldSteer, err)
	}
	if err := s.ReviseSteerMessage(thread.ID, 12, "[steer] Revised caption"); err != nil {
		t.Fatal(err)
	}
	newSteer, err := s.ReadProjectHistoryIdentities(t.Context(), project.ID, steerRef)
	if err != nil || newSteer[0] == nil || newSteer[0].SteerDigest == oldSteer[0].SteerDigest ||
		newSteer[0].RowID != oldSteer[0].RowID || newSteer[0].CreatedAt != oldSteer[0].CreatedAt {
		t.Fatalf("revised caption did not change only source content identity: %+v, %v", newSteer, err)
	}
	dup := &Message{ThreadID: thread.ID, TurnID: turn.ID, Seq: 1, Role: "user", CreatedAt: time.Now()}
	if err := s.DB().Create(dup).Error; err != nil {
		t.Fatal(err)
	}
	ambiguous, err := s.ReadProjectHistoryIdentities(t.Context(), project.ID, refs[:1])
	if err != nil || len(ambiguous) != 1 || ambiguous[0] != nil {
		t.Fatalf("ambiguous original source: %+v err=%v", ambiguous, err)
	}
	if _, err := s.ReadProjectHistoryIdentities(t.Context(), "", refs); !errors.Is(err, ErrHistoryReferenceInvalid) {
		t.Fatalf("globally scoped identity lookup: %v", err)
	}
	if _, err := s.ReadProjectHistoryIdentities(t.Context(), project.ID, []HistoryReference{{Kind: "turn", ThreadID: thread.ID, Seq: 1}}); !errors.Is(err, ErrHistoryReferenceInvalid) {
		t.Fatalf("invalid locator kind: %v", err)
	}
	if none, err := s.ReadProjectHistoryIdentities(t.Context(), project.ID, nil); err != nil || len(none) != 0 {
		t.Fatalf("empty source set: %+v err=%v", none, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := s.ReadProjectHistoryIdentities(ctx, project.ID, refs); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled metadata lookup: %v", err)
	}
}
