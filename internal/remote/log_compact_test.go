package remote

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/LubyRuffy/eino-swarm/internal/config"
	"github.com/LubyRuffy/eino-swarm/internal/store"
)

func TestCompactPhoneHistoryReachesThePreviousAnswerWithoutSendingItsWork(t *testing.T) {
	e := testEngine(t)
	th, err := e.CreateThread("history", "", "")
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.RemoteConfig{WatchEvents: 7}
	var seq int64
	add := func(turn, agent, kind, text, call string) int64 {
		t.Helper()
		row := &store.Event{ThreadID: th.ID, TurnID: turn, AgentID: agent, Kind: kind, Text: text, ToolCallID: call}
		if err := e.Store().AppendEvent(row); err != nil {
			t.Fatal(err)
		}
		seq = row.Seq
		return seq
	}
	old := add("one", "manager", "agent_message", "First response", "")
	prompt := add("two", "manager", "user_message", "Second prompt", "")
	ask := add("two", "manager", "tool_call", " ask_user ({})", "same")
	workStart := add("two", "manager", "reasoning", "First deferred thought", "")
	for i := 0; i < 240; i++ {
		id := fmt.Sprint(i)
		add("two", "manager", "tool_call", "read({})", id)
		add("two", "manager", "tool_result", strings.Repeat("WORK_PAYLOAD ", 500), id)
		add("two", "manager", "usage", "invisible bookkeeping", "")
	}
	workEnd := add("two", "worker-1", "tool_result", "Worker reused the call ID", "same")
	receipt := add("two", "manager", "tool_result", `{"answers":{"x":{"answers":["yes"]}}}`, "same")
	add("two", "manager", "tool_call", `report_schedule({"findings":"Report stays visible"})`, "report")
	focus := add("two", "manager", "agent_message", "Second response", "")
	add("two", "manager", "done", "", "")
	current := add("three", "manager", "user_message", "Third prompt", "")
	add("three", "manager", "agent_message", "Third response", "")

	call := func(req Request) Response {
		req.ThreadID, req.Op = th.ID, OpLog
		got := Handle(e, cfg, req, "relay", "session")
		raw, err := json.Marshal(got)
		if err != nil || len(raw) > plaintextBudget() {
			t.Fatalf("history reply exceeds the frame budget: %d, %v", len(raw), err)
		}
		return got
	}
	page := call(Request{Before: current, CompactHistory: true})
	if !page.OK || !page.More || page.Seq != prompt || page.FocusSeq != focus ||
		len(page.HistoryWork) != 1 || page.HistoryWork[0].From != workStart || page.HistoryWork[0].Through != workEnd ||
		strings.Join(page.HistoryWork[0].AgentIDs, ",") != "worker-1,manager" {
		t.Fatalf("compact history %+v", page)
	}
	got := map[int64]string{}
	for _, event := range page.Events {
		got[event.Seq] = event.Text
		if event.Seq >= workStart && event.Seq <= workEnd || strings.Contains(event.Text, "WORK_PAYLOAD") {
			t.Fatalf("deferred body leaked: %d", event.Seq)
		}
	}
	if got[ask] == "" || got[receipt] == "" || got[focus] != "Second response" {
		t.Fatalf("missing visible events or ask receipt: %v", got)
	}
	if earlier := call(Request{Before: page.Seq, CompactHistory: true}); earlier.FocusSeq != old || earlier.More {
		t.Fatalf("next tap must reach the first response: %+v", earlier)
	}
	legacy := call(Request{Before: current})
	if !legacy.OK || len(legacy.HistoryWork) != 0 || legacy.FocusSeq != 0 {
		t.Fatalf("legacy wire shape changed: %+v", legacy)
	}

	cursor := workStart - 1
	resolved := 0
	for {
		work := call(Request{WorkFrom: workStart, WorkThrough: workEnd, Since: cursor})
		if !work.OK || work.Seq <= cursor || work.Seq > workEnd {
			t.Fatalf("work cursor did not advance within its range: %d -> %d", cursor, work.Seq)
		}
		for _, event := range work.Events {
			if event.Seq <= cursor || event.Seq > workEnd || event.Seq < workStart {
				t.Fatalf("work result outside its page: %d", event.Seq)
			}
			cursor = event.Seq
			resolved++
		}
		cursor = work.Seq
		if !work.More {
			break
		}
	}
	if cursor != workEnd || resolved != 482 {
		t.Fatalf("work replay lost rows: cursor=%d rows=%d", cursor, resolved)
	}
	if invalid := call(Request{WorkFrom: workEnd, WorkThrough: workStart}); invalid.OK || invalid.Code != "bad_request" {
		t.Fatalf("invalid range accepted: %+v", invalid)
	}
}

func TestCompactWatchAndWorkExpansionCrossTheEncryptedLink(t *testing.T) {
	e := testEngine(t)
	th, err := e.CreateThread("encrypted history", "", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range []store.Event{
		{Kind: "user_message", Text: "Read it"},
		{Kind: "reasoning", Text: "Inspect one detail"},
		{Kind: "agent_message", Text: "Here is the answer"},
		{Kind: "done"},
	} {
		event.ThreadID, event.TurnID = th.ID, "turn"
		if err := e.Store().AppendEvent(&event); err != nil {
			t.Fatal(err)
		}
	}
	relay := newFollowupRelay(t, e)
	ready := relay.rpc(t, Request{ID: "compact", ThreadID: th.ID, Op: OpWatch, IncludeDetail: true, CompactHistory: true})
	if !ready.OK || ready.Op != OpReady || ready.Seq != 4 || ready.HistoryBefore != 1 || ready.More ||
		ready.Detail == nil || len(ready.Events) != 3 || len(ready.HistoryWork) != 1 {
		t.Fatalf("encrypted compact ready lost its original live watermark or history: %+v", ready)
	}
	work := ready.HistoryWork[0]
	expanded := relay.rpc(t, Request{ID: "steps", ThreadID: th.ID, Op: OpLog, WorkFrom: work.From, WorkThrough: work.Through})
	if !expanded.OK || expanded.More || expanded.Seq != 2 || len(expanded.Events) != 1 ||
		expanded.Events[0].Text != "Inspect one detail" {
		t.Fatalf("encrypted expansion %+v", expanded)
	}
	legacy := relay.rpc(t, Request{ID: "legacy", ThreadID: th.ID, Op: OpWatch})
	if !legacy.OK || legacy.Seq != ready.Seq || len(legacy.Events) != 4 || legacy.HistoryBefore != 0 {
		t.Fatalf("legacy watch changed: %+v", legacy)
	}
}

func TestHistoryWorkRespectsFrameByteLimitBeforeEventCount(t *testing.T) {
	e := testEngine(t)
	th, err := e.CreateThread("bounded work", "", "")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		row := store.Event{ThreadID: th.ID, Kind: "tool_result", Text: strings.Repeat("\\", 6000)}
		if err := e.Store().AppendEvent(&row); err != nil {
			t.Fatal(err)
		}
	}
	got := Handle(e, config.RemoteConfig{WatchEvents: 80}, Request{Op: OpLog, ThreadID: th.ID, WorkFrom: 1, WorkThrough: 20}, "relay", "session")
	raw, err := json.Marshal(got)
	if !got.OK || !got.More || got.Seq < 1 || got.Seq >= 20 || err != nil || len(raw) > plaintextBudget() {
		t.Fatalf("first work page must advance within the frame budget: ok=%v more=%v seq=%d bytes=%d err=%v",
			got.OK, got.More, got.Seq, len(raw), err)
	}
}

func TestCompactReadyKeepsActiveAndEmptyWatchWindows(t *testing.T) {
	e := testEngine(t)
	pump, _ := testPump(t, e)
	active := Response{OK: true, Seq: 10, Status: &WatchStatus{Running: true, AwaitingAnswer: true},
		Events: []EventView{{Kind: "tool_call", Text: "ask_user({})"}}}
	if got := pump.compactReadyHistory(active); got.HistoryBefore != 0 || len(got.Events) != 1 ||
		got.Events[0].Kind != "tool_call" || !got.Status.AwaitingAnswer {
		t.Fatalf("active ask needs the original live window: %+v", got)
	}
	empty := Response{OK: true, Seq: 1, Status: &WatchStatus{}, ThreadID: "no-history"}
	if got := pump.compactReadyHistory(empty); got.HistoryBefore != 0 || got.Seq != empty.Seq {
		t.Fatalf("an empty compact window changed the ready boundary: %+v", got)
	}
}
