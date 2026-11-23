package store

import (
	"strings"
	"testing"

	"gorm.io/gorm"
)

func TestOpeningAConversationSkipsStoredBodies(t *testing.T) {
	s := open(t)
	th := &Thread{}
	if err := s.CreateThread(th); err != nil {
		t.Fatal(err)
	}
	prompt := "prompt-" + strings.Repeat("p", 4096)
	result := "result-" + strings.Repeat("r", 4096)
	human := "human-" + strings.Repeat("h", 64)
	protocol := "protocol-" + strings.Repeat("g", 4096)
	answer := "answer-" + strings.Repeat("a", 4096)
	rows := []Event{
		{ThreadID: th.ID, TurnID: "human", Kind: "spawned", AgentID: "worker-1", Role: "worker", Text: prompt},
		{ThreadID: th.ID, TurnID: "human", Kind: "finished", AgentID: "worker-1", Role: "worker", Text: result, Err: "worker failed"},
		{ThreadID: th.ID, TurnID: "human", Kind: "cleanup", AgentID: "manager", Text: "stopped 0"},
		{ThreadID: th.ID, TurnID: "check", Kind: "workers_retained", AgentID: "manager", Text: `{"worker_ids":["worker-1"]}`},
		{ThreadID: th.ID, TurnID: "check", Kind: "error", AgentID: "manager", Text: "visible failure", Err: "stopped"},
	}
	for i := range rows {
		if err := s.AppendEvent(&rows[i]); err != nil {
			t.Fatal(err)
		}
	}
	for _, turn := range []Turn{
		{ID: "human", ThreadID: th.ID, Status: TurnDone, UserText: human, Final: answer, DurationMS: 1200},
		{ID: "goal", ThreadID: th.ID, Status: TurnDone, UserText: protocol, Final: answer, GoalContinue: true, DurationMS: 3400},
		{ID: "schedule", ThreadID: th.ID, Status: TurnError, UserText: protocol, Final: answer, ScheduleContinue: true, Error: "check failed", DurationMS: 900},
	} {
		if err := s.CreateTurn(&turn); err != nil {
			t.Fatal(err)
		}
	}

	var queries []string
	if err := s.db.Callback().Query().After("gorm:query").Register("test:opening_sql", func(tx *gorm.DB) {
		if tx.Statement.Table == "events" || strings.Contains(tx.Statement.Table, "events") {
			queries = append(queries, tx.Statement.SQL.String())
		}
	}); err != nil {
		t.Fatal(err)
	}
	roster, err := s.ListRosterIdentities(th.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(queries) != 1 || !strings.Contains(queries[0], "CASE WHEN kind IN ('spawned', 'finished')") {
		t.Fatalf("roster query=%v", queries)
	}
	if len(roster) != 5 {
		t.Fatalf("roster=%+v", roster)
	}
	if roster[0].Kind != "spawned" || roster[0].AgentID != "worker-1" || roster[0].Role != "worker" || roster[0].Text != "" {
		t.Fatalf("spawn identity=%+v", roster[0])
	}
	if roster[1].Kind != "finished" || roster[1].Text != "" || roster[1].Err != "worker failed" {
		t.Fatalf("finish identity=%+v", roster[1])
	}
	if roster[2].Text != "stopped 0" || roster[3].Text != `{"worker_ids":["worker-1"]}` || roster[4].Text != "visible failure" {
		t.Fatalf("small roster bodies=%+v", roster[2:])
	}
	full, err := s.ListRosterEvents(th.ID)
	if err != nil || len(full) != 5 || full[0].Text != prompt || full[1].Text != result {
		t.Fatalf("resume roster lost the bodies: %+v err=%v", full, err)
	}

	openings, err := s.ListTurnOpenings(th.ID)
	if err != nil || len(openings) != 3 {
		t.Fatalf("openings=%+v err=%v", openings, err)
	}
	if openings[0].UserText != human || openings[0].Final != "" || openings[0].DurationMS != 1200 || openings[0].Status != TurnDone {
		t.Fatalf("human opening=%+v", openings[0])
	}
	if openings[1].UserText != "" || openings[1].Final != "" || !openings[1].GoalContinue || openings[1].DurationMS != 3400 {
		t.Fatalf("goal opening=%+v", openings[1])
	}
	if openings[2].UserText != "" || openings[2].Final != "" || !openings[2].ScheduleContinue || openings[2].Error != "check failed" {
		t.Fatalf("schedule opening=%+v", openings[2])
	}
	stored, err := s.ListTurns(th.ID)
	if err != nil || stored[0].Final != answer || stored[1].UserText != protocol || stored[2].Final != answer {
		t.Fatalf("full turns changed: %+v err=%v", stored, err)
	}
}

func TestOpeningReadsReportAClosedStore(t *testing.T) {
	s := open(t)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ListRosterIdentities("thread"); err == nil || !strings.Contains(err.Error(), "list roster identities") {
		t.Fatalf("roster err=%v", err)
	}
	if _, err := s.ListTurnOpenings("thread"); err == nil || !strings.Contains(err.Error(), "list turn openings") {
		t.Fatalf("turns err=%v", err)
	}
}
