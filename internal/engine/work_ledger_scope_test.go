package engine

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/LubyRuffy/eino-swarm/internal/store"
	"github.com/cloudwego/eino/schema"
)

func TestRequestedWorkScopeDistinguishesOmittedAndExplicitValues(t *testing.T) {
	for _, test := range []struct {
		args, message string
		supplied      bool
	}{
		{`{"title":"Observed result"}`, "", false},
		{`{"scope":"thread"}`, "", true},
		{`{"SCOPE":"project"}`, "", true},
		{`{"scope":""}`, "scope must be thread or project", false},
		{`{"scope":null}`, "scope must be thread or project", false},
		{`{"scope":"workspace"}`, "scope must be thread or project", false},
		{`{"scope":"thread","SCOPE":"project"}`, "multiple spellings", false},
	} {
		t.Run(test.args, func(t *testing.T) {
			var input recordWorkArgs
			if err := decodeNuwaArgs(test.args, &input); err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal([]byte(test.args), &fields); err != nil {
				t.Fatal(err)
			}
			supplied, err := requestedWorkScope(input, fields)
			if (test.message == "" && err != nil) || (test.message != "" && (err == nil || !strings.Contains(err.Error(), test.message))) ||
				supplied != test.supplied {
				t.Fatalf("supplied=%v err=%v, want supplied=%v error containing %q", supplied, err, test.supplied, test.message)
			}
		})
	}
}

func TestWorkToolPayloadDoesNotEnterBriefingOrReview(t *testing.T) {
	events := []store.Event{
		{Seq: 1, Kind: KindUser, Text: "Retain the actual user request."},
		{Seq: 2, Kind: "tool_call", AgentID: "manager", ToolCallID: "work", WorkRecordPayload: true, Text: "private work arguments"},
		{Seq: 3, Kind: "tool_result", AgentID: "manager", ToolCallID: "work", WorkRecordPayload: true, Text: "private work receipt"},
		{Seq: 4, Kind: "agent_message", AgentID: "manager", Text: "Retain the ordinary assistant answer."},
		{Seq: 5, Kind: "tool_call", AgentID: "manager", ToolCallID: "ordinary", Text: "read({})"},
		{Seq: 6, Kind: "tool_result", AgentID: "manager", ToolCallID: "ordinary", Text: "Ordinary evidence remains available."},
	}
	for _, projected := range []string{sessionMemoryInput("", events), renderReviewFromEvents(events, "", "")} {
		if strings.Contains(projected, "private work") || !strings.Contains(projected, events[0].Text) ||
			!strings.Contains(projected, events[3].Text) || !strings.Contains(projected, events[5].Text) {
			t.Fatalf("derived preview retained the wrong sources: %s", projected)
		}
	}
	if got := distillOriginalEvents(events); len(got) != 4 {
		t.Fatalf("distillation retained work receipts or removed ordinary evidence: %+v", got)
	}
}

func TestCompactionDropsActualAndRecoveredWorkTools(t *testing.T) {
	e := &Engine{}
	work := "private evidence from the work ledger"
	ordinary := "ordinary evidence for the conversation"
	messages := []*schema.Message{
		schema.ToolMessage(work, "fresh-work", schema.WithToolName("read_work")),
		e.storedToSchema(store.Message{Role: "tool", Content: work, ToolCallID: "saved-work", WorkRecordPayload: true}),
		schema.ToolMessage(ordinary, "ordinary-tool", schema.WithToolName("read")),
		schema.AssistantMessage("Ordinary assistant text.", nil),
	}
	briefing := compactInputFromADK("", messages)
	if strings.Contains(briefing, work) || !strings.Contains(briefing, ordinary) || !strings.Contains(briefing, "Ordinary assistant text.") {
		t.Fatalf("compaction retained the wrong sources: %s", briefing)
	}
}
