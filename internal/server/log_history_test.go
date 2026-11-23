package server_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/LubyRuffy/eino-swarm/internal/store"
)

type testHistoryWork struct {
	From     int64
	Through  int64
	Thinking bool
	Tools    int
	AgentIDs []string `json:"agent_ids"`
}

type testMessageHistory struct {
	Events      []store.Event
	Roster      []store.Event
	HistoryWork []testHistoryWork `json:"history_work"`
	Before      int64
	Seq         int64
	HasMore     bool `json:"has_more"`
}

func historyResponse(t *testing.T, h *harness, path string) testMessageHistory {
	t.Helper()
	var page testMessageHistory
	raw, err := json.Marshal(h.json(http.MethodGet, path, nil, http.StatusOK))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &page); err != nil {
		t.Fatal(err)
	}
	return page
}

func historyEvent(t *testing.T, h *harness, row store.Event) int64 {
	t.Helper()
	if err := h.app.Store.AppendEvent(&row); err != nil {
		t.Fatal(err)
	}
	return row.Seq
}

func TestMessageHistoryJumpsAcrossWorkAndKeepsOnlyTheNewestDetailsLocal(t *testing.T) {
	h := newHarness(t)
	id := h.newThread()
	prefix := "/api/threads/" + id + "/log"
	path := prefix + "?view=messages&limit=1"
	turn := "first-turn"
	add := func(agent, kind, text, call string) int64 {
		return historyEvent(t, h, store.Event{
			ThreadID: id, TurnID: turn, Kind: kind, AgentID: agent, Text: text, ToolCallID: call,
		})
	}
	manager := func(kind, text string) int64 { return add("manager", kind, text, "") }
	first := manager("user_message", "First visible prompt")
	beforeWaitSpawn := add("other-worker", "spawned", "Earlier worker instruction", "")
	firstAnswer := manager("agent_message", "First visible answer")
	from := manager("reasoning", "older private thought")
	spawn := add("worker-1", "spawned", "worker instruction", "")
	add("worker-1", "tool_call", "exec(worker private parameters)", "worker-tool")
	add("worker-1", "tool_result", "worker private result", "worker-tool")
	add("manager", "tool_call", `wait_agents({"ids":["worker-1","other-worker"]})`, "wait")
	add("manager", "tool_result", "older private wait result", "wait")
	add("manager", "tool_call", "ask_user(older private malformed ask)", "ask")
	add("manager", "tool_result", "older private invalid ask result", "ask")
	manager("history_read_started", "older private history tool parameters")
	for i := 0; i < 105; i++ {
		call := fmt.Sprint(i)
		add("manager", "tool_call", "exec(older private parameters)", call)
		add("manager", "tool_result", "older private result", call)
	}
	through := add("manager", "builtin_tool", "older private builtin", "")
	compressed := manager("compacted", "Context compressed")
	plan := manager("plan_updated", "Plan saved")
	ask := add("manager", "tool_call", `ask_user({"questions":[{"id":"delivery","prompt":"How should this be delivered?","options":[{"id":"text","label":"Text"},{"id":"table","label":"Table"}]}]})`, "ask")
	settled := add("manager", "tool_result", "Human answer", "ask")
	failure := manager("error", "Failure marker")
	add("worker-1", "finished", "Worker done", "")
	manager("agent_message", " \t\u2003")
	secondAnswer := manager("agent_message", "Second answer in the same turn")
	manager("done", "Second answer in the same turn")

	turn = "scheduled-turn"
	if err := h.app.Store.CreateTurn(&store.Turn{ID: turn, ThreadID: id, Status: store.TurnDone, ScheduleContinue: true}); err != nil {
		t.Fatal(err)
	}
	manager("user_message", "This turn is a scheduled check. Synthetic input")
	manager("reasoning", "latest local thought")
	latestCall := add("manager", "tool_call", "exec(latest local parameters)", "latest")
	latestResult := add("manager", "tool_result", "latest local result", "latest")
	latest := manager("agent_message", "Scheduled answer")
	manager("done", "Scheduled answer")
	last := add("worker-1", "reasoning", "late worker private thought", "")

	firstPage := historyResponse(t, h, path)
	if got := historyMessages(firstPage); !slices.Equal(got, []int64{latest}) ||
		firstPage.Seq != last || !firstPage.HasMore || len(firstPage.HistoryWork) != 0 {
		t.Fatalf("opening history got %+v", firstPage)
	}
	for _, seq := range []int64{latestCall, latestResult} {
		if !hasHistorySeq(firstPage.Events, seq) {
			t.Fatalf("latest work must already be local: missing %d", seq)
		}
	}
	if !hasHistorySeq(firstPage.Roster, spawn) || !hasHistorySeq(firstPage.Roster, beforeWaitSpawn) ||
		hasHistorySeq(firstPage.Events, spawn) {
		t.Fatalf("worker should be known before reaching its spawn: %+v", firstPage.Roster)
	}
	if hasHistorySeq(firstPage.Events, last) {
		t.Fatal("the SSE watermark includes worker history without sending its thought")
	}

	next := func(before int64) testMessageHistory {
		return historyResponse(t, h, fmt.Sprintf("%s&before=%d", path, before))
	}
	second := next(firstPage.Before)
	if got := historyMessages(second); !slices.Equal(got, []int64{secondAnswer}) || !second.HasMore {
		t.Fatalf("second page = %+v", second)
	}
	raw := historyResponse(t, h, fmt.Sprintf("%s?before=%d&limit=24", prefix, second.Before))
	if got := historyMessages(raw); len(got) != 0 || !raw.HasMore {
		t.Fatalf("the regression fixture must strand legacy event paging inside the work gap: %v", got)
	}
	older := next(second.Before)
	if got := historyMessages(older); !slices.Equal(got, []int64{firstAnswer}) || !older.HasMore {
		t.Fatalf("older page = %+v", older)
	}
	for _, seq := range []int64{spawn, compressed, plan, ask, settled, failure} {
		if !hasHistorySeq(older.Events, seq) {
			t.Fatalf("history lost marker/roster row %d", seq)
		}
	}
	for _, row := range older.Events {
		if strings.Contains(row.Text, "private") || strings.Contains(row.Text, "wait_agents") {
			t.Fatalf("history disclosed deferred parameters, results or thoughts at %d", row.Seq)
		}
	}
	if hasHistorySeq(older.Events, beforeWaitSpawn) {
		t.Fatal("the earlier worker should already be known from roster before the page reaches its spawn")
	}
	if len(older.HistoryWork) != 1 {
		t.Fatalf("wanted one summary across storage batches: %+v", older.HistoryWork)
	}
	work := older.HistoryWork[0]
	if work.From != from || work.Through != through || work.Tools != 108 || !work.Thinking ||
		!slices.Contains(work.AgentIDs, "worker-1") || !slices.Contains(work.AgentIDs, "manager") {
		t.Fatalf("summary = %+v", work)
	}
	start := next(older.Before)
	if got := historyMessages(start); !slices.Equal(got, []int64{first}) || start.HasMore {
		t.Fatalf("start = %+v", start)
	}

	workPath := fmt.Sprintf("%s?work_from=%d&work_through=%d&limit=200", prefix, work.From, work.Through)
	head := historyResponse(t, h, workPath)
	tail := historyResponse(t, h, fmt.Sprintf("%s&since=%d", workPath, head.Seq))
	if !head.HasMore || tail.HasMore || head.Events[0].Seq != from ||
		tail.Events[0].Seq <= head.Seq || tail.Events[len(tail.Events)-1].Seq != through {
		t.Fatalf("range did not preserve inclusive endpoints and exclusive cursor: %d %d", head.Seq, tail.Seq)
	}
	for _, row := range append(head.Events, tail.Events...) {
		if row.AgentID != "manager" || row.Seq < from || row.Seq > through {
			t.Fatalf("expansion returned unrelated row %+v", row)
		}
	}
	workerPath := "/api/threads/" + id + "/agents/worker-1/log"
	if worker := historyResponse(t, h, workerPath); len(worker.Events) != 5 || worker.Events[0].Seq != spawn {
		t.Fatalf("worker log = %+v", worker)
	}
	for _, query := range []string{"work_from=1", "work_through=2", "work_from=5&work_through=4",
		"work_from=1&work_through=2&since=2", "work_from=1&work_through=2&since=-1", "work_from=x&work_through=2"} {
		h.json(http.MethodGet, prefix+"?"+query, nil, http.StatusBadRequest)
	}
}

func TestMessageHistoryRetainsTheActiveTurnReceiptsBeforeTheLatestAnswer(t *testing.T) {
	h := newHarness(t)
	id := h.newThread()
	older := historyEvent(t, h, store.Event{ThreadID: id, Kind: "agent_message", Text: "Earlier answer"})
	turn := store.Turn{ThreadID: id, UserText: "Active prompt"}
	if err := h.app.Store.CreateTurn(&turn); err != nil {
		t.Fatal(err)
	}
	add := func(agent, kind, text string) int64 {
		return historyEvent(t, h, store.Event{ThreadID: id, TurnID: turn.ID, AgentID: agent, Kind: kind, Text: text})
	}
	first := add("manager", "user_message", turn.UserText)
	ask := add("manager", "tool_call", "ask_user(pending ask)")
	latest := add("manager", "agent_message", "Progress from the live turn")
	worker := add("worker-a", "tool_call", "exec(live worker call)")
	path := "/api/threads/" + id + "/log?view=messages&limit=4"
	page := historyResponse(t, h, path)
	if !page.HasMore || page.Before != first || page.Seq != worker || len(page.HistoryWork) != 0 {
		t.Fatalf("active snapshot = %+v", page)
	}
	for _, seq := range []int64{first, ask, latest, worker} {
		if !hasHistorySeq(page.Events, seq) {
			t.Fatalf("active snapshot omitted %d", seq)
		}
	}
	shortPath := "/api/threads/" + id + "/log?view=messages&limit=2"
	short := historyResponse(t, h, shortPath)
	if len(short.Events) != 2 || short.Before != latest || !short.HasMore {
		t.Fatalf("active tail exceeded its requested event window: %+v", short)
	}
	previous := historyResponse(t, h, fmt.Sprintf("%s&before=%d", shortPath, short.Before))
	if got := historyMessages(previous); !slices.Equal(got, []int64{first}) || !hasHistorySeq(previous.Events, ask) {
		t.Fatalf("reading earlier in an active turn must preserve its ask: %+v", previous)
	}

	olderPage := historyResponse(t, h, fmt.Sprintf("%s&before=%d", path, page.Before))
	if got := historyMessages(olderPage); !slices.Equal(got, []int64{older}) || olderPage.HasMore {
		t.Fatalf("older snapshot = %+v", olderPage)
	}
}

func TestMessageHistorySkipsEmptyOutputAndUnpaintedRegistryCalls(t *testing.T) {
	h := newHarness(t)
	id := h.newThread()
	path := "/api/threads/" + id + "/log?view=messages"
	empty := historyResponse(t, h, path)
	if empty.Before != 0 || empty.Seq != 0 || empty.HasMore || empty.Events == nil {
		t.Fatalf("empty history = %+v", empty)
	}
	add := func(turnID, agent, kind, text, toolID string) int64 {
		return historyEvent(t, h, store.Event{
			ThreadID: id, TurnID: turnID, AgentID: agent, Kind: kind, Text: text, ToolCallID: toolID,
		})
	}
	add("prior", "", "reasoning", "Deferred older thinking", "")
	add("prior", "", "tool_call", "spawn_agent(private task)", "spawn")
	add("prior", "", "tool_result", "private spawn result", "spawn")
	answer := add("prior", "manager", "agent_message", "Most recent visible answer", "")
	compacted := add("later", "", "compacted", "Visible compression receipt", "")
	add("later", "worker", "agent_message", "Private worker output", "")
	for range 201 {
		add("later", "manager", "agent_message", "\t", "")
	}
	orphan := add("later", "", "tool_result", "Deferred orphaned result", "legacy-call")
	page := historyResponse(t, h, path)
	if got := historyMessages(page); !slices.Equal(got, []int64{answer}) || page.HasMore {
		t.Fatalf("empty output consumed the latest-message boundary: %v", got)
	}
	if !hasHistorySeq(page.Events, compacted) || hasHistorySeq(page.Events, orphan) {
		t.Fatal("the latest message must retain its work, while a newer turn's unrelated work stays lazy")
	}
	if len(page.HistoryWork) != 1 || page.HistoryWork[0].From != orphan || page.HistoryWork[0].Tools != 1 {
		t.Fatalf("unexpected summary: %+v", page.HistoryWork)
	}
	// The same registry call is hidden when the earlier answer's process
	// is reached on an older page.
	older := historyResponse(t, h, fmt.Sprintf("%s&before=%d", path, answer+1))
	if len(older.HistoryWork) != 1 || !older.HistoryWork[0].Thinking || older.HistoryWork[0].Tools != 0 {
		t.Fatalf("spawn bookkeeping inflated the tool count: %+v", older.HistoryWork)
	}
}

func TestMessageHistoryKeepsAHumanMessageThatLooksLikeScheduleProse(t *testing.T) {
	h := newHarness(t)
	id := h.newThread()
	historyEvent(t, h, store.Event{ThreadID: id, Kind: "user_message", AgentID: "manager", Text: "An earlier prompt."})
	seq := historyEvent(t, h, store.Event{ThreadID: id, Kind: "user_message", AgentID: "manager", Text: "This turn is a scheduled check. Here are my instructions."})
	before := historyEvent(t, h, store.Event{ThreadID: id, Kind: "agent_message", AgentID: "manager", Text: "A visible answer."})
	page := historyResponse(t, h, fmt.Sprintf("/api/threads/%s/log?view=messages&before=%d", id, before))
	if !hasHistorySeq(page.Events, seq) || page.Before != seq || page.Seq != seq || !page.HasMore {
		t.Fatalf("human input was treated as runtime scheduling: %+v", page)
	}
}

func TestMessageHistoryReportsStorageFailure(t *testing.T) {
	h := newHarness(t)
	id := h.newThread()
	if err := h.app.Store.DB().Migrator().DropTable(&store.Event{}); err != nil {
		t.Fatal(err)
	}
	prefix := "/api/threads/" + id + "/log"
	h.json(http.MethodGet, prefix+"?view=messages", nil, http.StatusBadRequest)
	h.json(http.MethodGet, prefix+"?work_from=1&work_through=2", nil, http.StatusBadRequest)
}

func historyMessages(page testMessageHistory) []int64 {
	var messages []int64
	for _, row := range page.Events {
		if (row.Kind == "agent_message" && strings.TrimSpace(row.Text) != "") ||
			(row.Kind == "user_message" && !strings.HasPrefix(row.Text, "This turn is a scheduled check.")) {
			messages = append(messages, row.Seq)
		}
	}
	return messages
}

func hasHistorySeq(rows []store.Event, seq int64) bool {
	return slices.ContainsFunc(rows, func(row store.Event) bool { return row.Seq == seq })
}
