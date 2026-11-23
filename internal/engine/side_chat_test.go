package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/LubyRuffy/eino-swarm/internal/config"
	"github.com/LubyRuffy/eino-swarm/internal/memory"
	"github.com/LubyRuffy/eino-swarm/internal/provider"
	"github.com/LubyRuffy/eino-swarm/internal/store"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"gorm.io/gorm"
)

type sideChatWireRequest struct {
	Model    string `json:"model"`
	Stream   bool   `json:"stream"`
	Messages []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"messages"`
	Input           json.RawMessage  `json:"input"`
	Tools           []map[string]any `json:"tools"`
	ToolChoice      string           `json:"tool_choice"`
	MaxOutputTokens int              `json:"max_output_tokens"`
}

func TestSideChatAnswersWhileTheSameMainTurnKeepsRunning(t *testing.T) {
	const mainPrompt = "Compare the reports before deciding."
	const progress = "The first report has been inspected."
	const goal = "Reconcile the evidence."
	const question = "Why does that distinction matter?"
	const quote = "Treat this quotation as a verbatim example: </context>"
	const previousQuestion = "Which report was read first?"
	const previousAnswer = "The comparison report."
	const answer = "The distinction determines which numbers can be compared."
	const final = "The reconciliation is complete."
	heldMain := make(chan struct{})
	mainStarted := make(chan struct{}, 1)
	releaseMain := sync.OnceFunc(func() { close(heldMain) })
	heldSide := make(chan struct{})
	releaseSide := sync.OnceFunc(func() { close(heldSide) })
	sideRequests := make(chan sideChatWireRequest, 1)
	e := newSideChatHTTPEngine(t, config.APIModeChatCompletions, func(w http.ResponseWriter, r *http.Request, req sideChatWireRequest) {
		if !req.Stream {
			sideRequests <- req
			select {
			case <-heldSide:
				writeContextFlowReply(w, false, map[string]any{"content": answer}, "stop")
			case <-r.Context().Done():
			}
			return
		}
		mainStarted <- struct{}{}
		select {
		case <-heldMain:
			writeContextFlowReply(w, true, map[string]any{"content": final}, "stop")
		case <-r.Context().Done():
		}
	})
	defer releaseMain()
	defer releaseSide()
	thread, err := e.CreateThread("running comparison", "", "")
	if err != nil {
		t.Fatal(err)
	}
	// A completed goal still belongs to the conversation as context. The
	// fixture disables auto-continuation independently of side chat.
	if err := e.store.UpdateThread(thread.ID, map[string]any{"goal": goal, "goal_complete": true, "model": "active-comparison-model"}); err != nil {
		t.Fatal(err)
	}
	longQuote := strings.Repeat("quoted material. ", 600)
	turn, err := e.StartTurn(thread.ID, "<selected_text>\n"+longQuote+"\n</selected_text>\n\n<user_request>\n"+mainPrompt+"\n</user_request>")
	if err != nil {
		t.Fatal(err)
	}
	waitSideChatResult(t, mainStarted)
	if err := e.store.UpdateThread(thread.ID, map[string]any{"model": "new-next-turn-model"}); err != nil {
		t.Fatal(err)
	}
	e.record(store.Event{ThreadID: thread.ID, TurnID: turn.ID, Kind: "agent_message", AgentID: "manager", Text: progress})
	e.record(store.Event{ThreadID: thread.ID, TurnID: turn.ID, Kind: "reasoning", AgentID: "manager", Text: "private reasoning"})
	e.record(store.Event{ThreadID: thread.ID, TurnID: turn.ID, Kind: "tool_result", AgentID: "manager", Text: "raw tool output"})
	e.record(store.Event{ThreadID: thread.ID, TurnID: turn.ID, Kind: "agent_message", AgentID: "worker", Text: "worker scratch text"})
	other, err := e.CreateThread("unrelated", "", "")
	if err != nil {
		t.Fatal(err)
	}
	e.record(store.Event{ThreadID: other.ID, Kind: KindUser, Text: "another conversation"})
	ask := SideChatInput{Question: question, Quote: quote, History: []SideChatExchange{{Question: previousQuestion, Answer: previousAnswer}}}
	type result struct {
		answer string
		err    error
	}
	got := make(chan result, 1)
	go func() {
		out, err := e.AskSideChat(t.Context(), thread.ID, ask)
		got <- result{out, err}
	}()
	var request sideChatWireRequest
	select {
	case request = <-sideRequests:
	case res := <-got:
		t.Fatalf("side chat returned before the provider request: %v", res.err)
	case <-time.After(5 * time.Second):
		t.Fatal("side chat did not reach the provider")
	}
	if request.Model != "active-comparison-model" || request.ToolChoice != "auto" || len(request.Messages) < 2 {
		t.Fatalf("side model identity or tools differ: %+v", request)
	}
	sideLookupToolNames(t, request.Tools)
	var body strings.Builder
	for _, msg := range request.Messages[1:] {
		body.WriteString(msg.Content)
	}

	var reference struct {
		LatestTask string `json:"latest_task"`
		Quote      string `json:"selected_quotation"`
	}
	if err := json.Unmarshal([]byte(request.Messages[1].Content), &reference); err != nil || reference.LatestTask != mainPrompt || reference.Quote != quote {
		t.Fatalf("long quoted main task or side quotation was lost: %+v err=%v", reference, err)
	}
	for _, text := range []string{mainPrompt, progress, goal, question, "</context>", previousQuestion, previousAnswer} {
		// JSON uses unicode escapes for < and > on the model wire.
		escaped, _ := json.Marshal(text)
		if !strings.Contains(body.String(), text) && !strings.Contains(body.String(), strings.Trim(string(escaped), "\"")) {
			t.Errorf("side model lost %q", text)
		}
	}
	for _, text := range []string{"raw tool output", "worker scratch text"} {
		if !strings.Contains(body.String(), text) {
			t.Errorf("side model lost task evidence %q", text)
		}
	}
	for _, text := range []string{"private reasoning", "another conversation"} {
		if strings.Contains(body.String(), text) {
			t.Errorf("side model received %q", text)
		}
	}
	if status := e.Status(thread.ID); !status.Running || status.TurnID != turn.ID {
		t.Fatalf("side request replaced the active turn: %+v", status)
	}

	// A blocked side answer cannot hold main turn finalization or its model.
	releaseMain()
	if ended := waitForTurn(t, e, turn.ID); ended.Status != store.TurnDone || ended.Final != final {
		t.Fatalf("original turn did not finish normally: %+v", ended)
	}
	releaseSide()
	if res := waitSideChatResult(t, got); res.err != nil || res.answer != answer {
		t.Fatalf("side answer=%q err=%v", res.answer, res.err)
	}
	if turns, err := e.store.ListTurns(thread.ID); err != nil || len(turns) != 1 || turns[0].ID != turn.ID {
		t.Fatalf("side chat wrote a turn: %+v err=%v", turns, err)
	}
	if replay := replayDump(t, e, thread.ID); strings.Contains(replay, question) || strings.Contains(replay, answer) {
		t.Fatalf("side chat contaminated main replay: %s", replay)
	}

	calls, err := e.store.ListLLMCalls(turn.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, call := range calls {
		if call.AgentID == sideChatAgentID {
			t.Fatal("side chat wrote main trace telemetry")
		}
	}
}

func TestSideChatFailureAndCancellationNeverCancelOrBackOffMainTask(t *testing.T) {
	const mainPrompt = "Work through the remaining reports."
	const final = "The reports were reviewed."
	const cancelledQuestion = "Please explain the interim result."
	mainStarted := make(chan struct{}, 1)
	sideStarted := make(chan struct{}, 1)
	sideCancelled := make(chan struct{}, 1)
	heldMain := make(chan struct{})
	releaseMain := sync.OnceFunc(func() { close(heldMain) })
	e := newSideChatHTTPEngine(t, config.APIModeChatCompletions, func(w http.ResponseWriter, r *http.Request, req sideChatWireRequest) {
		if req.Stream {
			mainStarted <- struct{}{}
			select {
			case <-heldMain:
				writeContextFlowReply(w, true, map[string]any{"content": final}, "stop")
			case <-r.Context().Done():
			}
			return
		}
		if req.Messages[len(req.Messages)-1].Content == cancelledQuestion {
			sideStarted <- struct{}{}
			<-r.Context().Done()
			sideCancelled <- struct{}{}
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":{"code":"provider_unavailable","message":"fixture route refused","type":"server_error"}}`))
	})
	defer releaseMain()
	thread, err := e.CreateThread("side chat failure fixture", "", "")
	if err != nil {
		t.Fatal(err)
	}
	// A side failure must leave the main model eligible to call the same origin.
	// An incorrectly shared route failure would leave a retry or a cooldown.
	failCtx, cancelFail := context.WithTimeout(t.Context(), time.Second)
	defer cancelFail()
	if _, err := e.AskSideChat(failCtx, thread.ID, SideChatInput{Question: "Explain the expected comparison."}); provider.HTTPStatus(err) != http.StatusServiceUnavailable {
		t.Fatalf("side failure status=%d err=%v", provider.HTTPStatus(err), err)
	}
	turn, err := e.StartTurn(thread.ID, mainPrompt)
	if err != nil {
		t.Fatal(err)
	}
	waitSideChatResult(t, mainStarted)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	side := make(chan error, 1)
	go func() {
		_, err := e.AskSideChat(ctx, thread.ID, SideChatInput{Question: cancelledQuestion})
		side <- err
	}()
	waitSideChatResult(t, sideStarted)
	cancel()
	if err := waitSideChatResult(t, side); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled side request returned %v", err)
	}
	waitSideChatResult(t, sideCancelled)
	if status := e.Status(thread.ID); !status.Running || status.TurnID != turn.ID {
		t.Fatalf("side cancellation affected the main turn: %+v", status)
	}
	releaseMain()
	if ended := waitForTurn(t, e, turn.ID); ended.Status != store.TurnDone || ended.Final != final {
		t.Fatalf("main turn after side cancellation: %+v", ended)
	}
}

func newSideChatHTTPEngine(t *testing.T, mode string, reply func(http.ResponseWriter, *http.Request, sideChatWireRequest)) *Engine {
	t.Helper()
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request sideChatWireRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode model request: %v", err)
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		if len(request.Messages) > 0 && replyNoScheduleClaimsFixture(t, w, request.Messages[0].Content, request.Stream, len(request.Tools)) {
			return
		}
		reply(w, r, request)
	}))
	t.Cleanup(endpoint.Close)
	e := newTestEngine(t)
	e.cfg.Swarm.AutoTitle = false
	p := &e.cfg.Models.Providers[0]
	p.BaseURL, p.Model, p.APIMode = endpoint.URL, "fixture-model", mode
	if mode == config.APIModeResponses {
		p.BuiltinTools = []map[string]any{{"type": "web_search_preview"}}
	}
	e.pool = provider.New(e.cfg)
	return e
}

func waitSideChatResult[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the side chat fixture")
		var zero T
		return zero
	}
}

func TestSideChatOmitsHostedToolsWithoutChangingTheSharedResponsesModel(t *testing.T) {
	requests := make(chan sideChatWireRequest, 4)
	longAnswer := strings.Repeat("界", sideChatMaxAnswer)
	e := newSideChatHTTPEngine(t, config.APIModeResponses, func(w http.ResponseWriter, r *http.Request, req sideChatWireRequest) {
		if r.URL.Path != "/responses" {
			t.Errorf("wrong adapter: %q", r.URL.Path)
		}
		requests <- req
		kind := string(req.Input)
		if req.Stream {
			w.Header().Set("Content-Type", "text/event-stream")
			switch {
			case strings.Contains(kind, "tool-shaped answer"):
				_, _ = fmt.Fprint(w, "data: {\"type\":\"response.output_item.added\",\"item\":{\"type\":\"function_call\",\"call_id\":\"call_1\",\"name\":\"write\",\"arguments\":\"{}\"}}\n\n")
			case strings.Contains(kind, "blank answer"):
				_, _ = fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[]}}\n\n")
			default:
				_, _ = fmt.Fprintf(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":%q}\n\n", longAnswer)
				_, _ = fmt.Fprintf(w, "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":%q}]}]}}\n\n", longAnswer)
			}
			return
		}
		switch {
		case strings.Contains(kind, "tool-shaped answer"):
			_, _ = fmt.Fprint(w, `{"status":"completed","output":[{"type":"function_call","call_id":"call_1","name":"write","arguments":"{}"}]}`)
		case strings.Contains(kind, "blank answer"):
			_, _ = fmt.Fprint(w, `{"status":"completed","output":[]}`)
		default:
			_, _ = fmt.Fprintf(w, `{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":%q}]}]}`, longAnswer)
		}
	})
	e.cfg.Swarm.MaxCompletionTokens = 600
	thread, err := e.CreateThread("Responses fixture", "", "")
	if err != nil {
		t.Fatal(err)
	}
	out, err := e.AskSideChat(t.Context(), thread.ID, SideChatInput{Question: "Summarize the distinction."})
	if err != nil || len(out) > sideChatMaxAnswer || !utf8.ValidString(out) || !strings.HasSuffix(out, "…") {
		t.Fatalf("bounded UTF-8 answer: size=%d err=%v", len(out), err)
	}
	req := waitSideChatResult(t, requests)
	if req.Stream || req.ToolChoice != "auto" || req.MaxOutputTokens != 600 || req.Model != "fixture-model" {
		t.Fatalf("side Responses request = %+v", req)
	}
	sideLookupToolNames(t, req.Tools)
	var preview strings.Builder
	streamed, err := e.StreamSideChat(t.Context(), thread.ID, SideChatInput{Question: "Summarize the distinction."}, func(delta string) {
		preview.WriteString(delta)
	})
	if err != nil || streamed != out || preview.String() != out {
		t.Fatalf("stream preview and final text must share the same UTF-8 bound: preview=%d final=%d err=%v", preview.Len(), len(streamed), err)
	}
	streamReq := waitSideChatResult(t, requests)
	if !streamReq.Stream || streamReq.ToolChoice != "auto" || streamReq.MaxOutputTokens != 600 {
		t.Fatalf("side Responses stream enabled hosted tools or lost its limit: %+v", streamReq)
	}
	sideLookupToolNames(t, streamReq.Tools)

	shared, err := e.pool.Get(t.Context(), thread.ProviderID, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := shared.Generate(t.Context(), []*schema.Message{schema.UserMessage("Ordinary request")}); err != nil {
		t.Fatal(err)
	}
	main := waitSideChatResult(t, requests)
	if len(main.Tools) != 1 || main.Tools[0]["type"] != "web_search_preview" {
		t.Fatalf("side restriction changed the shared model's builtins: %+v", main)
	}
	for _, q := range []string{"tool-shaped answer", "blank answer"} {
		if text, err := e.AskSideChat(t.Context(), thread.ID, SideChatInput{Question: q}); text != "" || !errors.Is(err, ErrSideChatUnavailable) {
			t.Fatalf("model returned %q, err=%v", text, err)
		}
		waitSideChatResult(t, requests)
		if text, err := e.StreamSideChat(t.Context(), thread.ID, SideChatInput{Question: q}, func(delta string) {
			t.Errorf("unexpected text from %q: %s", q, delta)
		}); text != "" || !errors.Is(err, ErrSideChatUnavailable) {
			t.Fatalf("stream returned %q, err=%v", text, err)
		}
		waitSideChatResult(t, requests)
	}
}

func TestSideChatBoundsAndMissingSourcesRefuseWithoutAnswer(t *testing.T) {
	e := newTestEngine(t)
	thread, err := e.CreateThread("side chat", "", "")
	if err != nil {
		t.Fatal(err)
	}
	longQuestion := strings.Repeat("q", sideChatMaxQuestion+1)
	longAnswer := strings.Repeat("a", sideChatMaxAnswer+1)
	for _, input := range []SideChatInput{
		{Question: " \n "},
		{Question: longQuestion},
		{Question: "question", Quote: longQuestion},
		{Question: "question", History: make([]SideChatExchange, sideChatMaxExchanges+1)},
		{Question: "question", History: []SideChatExchange{{Question: " ", Answer: "answer"}}},
		{Question: "question", History: []SideChatExchange{{Question: "prior", Answer: " "}}},
		{Question: "question", History: []SideChatExchange{{Question: longQuestion, Answer: "answer"}}},
		{Question: "question", History: []SideChatExchange{{Question: "prior", Answer: longAnswer}}},
	} {
		if text, err := e.AskSideChat(t.Context(), thread.ID, input); text != "" || !errors.Is(err, ErrInvalidSideChat) {
			t.Fatalf("invalid side request produced %q err=%v", text, err)
		}
	}
	input := SideChatInput{Question: "Why did the result differ?"}
	if text, err := e.AskSideChat(t.Context(), "missing", input); text != "" || !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing thread produced %q err=%v", text, err)
	}
	if text, err := e.AskSideChat(t.Context(), thread.ID, input); text != "Side chat: "+input.Question || err != nil {
		t.Fatalf("offline model produced %q err=%v", text, err)
	}
	if err := e.store.UpdateThread(thread.ID, map[string]any{"provider_id": "missing-provider"}); err != nil {
		t.Fatal(err)
	}
	if text, err := e.AskSideChat(t.Context(), thread.ID, input); text != "" || err == nil || !strings.Contains(err.Error(), "unknown provider") {
		t.Fatalf("missing model produced %q err=%v", text, err)
	}
}

func TestSideChatLooksUpTaskNotesAndSkills(t *testing.T) {
	const buried = "buried-measurement-17"
	const note = "Keep the comparison unit explicit."
	const skillName = "compare-records"
	const skillBody = "Read the recorded measurement before deciding."
	const answer = "The recorded measurement is the one in the task record."
	const privateText = "private-reasoning-token"
	const otherText = "other-thread-token"
	requests := make(chan sideChatWireRequest, 6)
	var calls int
	newEngine := func(streamAnswer bool) *Engine {
		calls = 0
		return newSideChatHTTPEngine(t, config.APIModeChatCompletions, func(w http.ResponseWriter, r *http.Request, req sideChatWireRequest) {
			requests <- req
			calls++
			switch calls {
			case 1:
				writeSideToolCall(w, req.Stream, "call_search", sideChatSearchTool, `{"query":"buried-measurement"}`)
			case 2:
				writeSideToolCall(w, req.Stream, "call_skill", memory.ToolSkillView, `{"name":"compare-records"}`)
			default:
				writeContextFlowReply(w, req.Stream && streamAnswer, map[string]any{"content": answer}, "stop")
			}
		})
	}
	ask := func(t *testing.T, e *Engine, threadID string, stream bool) {
		t.Helper()
		var preview strings.Builder
		var out string
		var err error
		if stream {
			out, err = e.StreamSideChat(t.Context(), threadID, SideChatInput{Question: "Which measurement was recorded?"}, func(delta string) {
				preview.WriteString(delta)
			})
		} else {
			out, err = e.AskSideChat(t.Context(), threadID, SideChatInput{Question: "Which measurement was recorded?"})
		}
		if err != nil || out != answer || (stream && preview.String() != answer) {
			t.Fatalf("answer=%q preview=%q err=%v", out, preview.String(), err)
		}
		first := waitSideChatResult(t, requests)
		second := waitSideChatResult(t, requests)
		third := waitSideChatResult(t, requests)
		names := sideLookupToolNames(t, first.Tools)
		if !names[memory.ToolSkillView] {
			t.Fatal("recorded skill was not offered")
		}
		if body := sideWireText(first); strings.Contains(body, buried) || strings.Contains(body, skillBody) || strings.Contains(body, privateText) || strings.Contains(body, otherText) || !strings.Contains(body, note) || !strings.Contains(body, skillName) {
			t.Fatalf("first reference did not separate the preview from private text: %s", body)
		}
		if body := sideWireText(second); !strings.Contains(body, buried) || strings.Contains(body, privateText) || strings.Contains(body, otherText) || strings.Contains(body, "quiet-only-measurement") {
			t.Fatalf("task search result = %s", body)
		}
		if body := sideWireText(third); !strings.Contains(body, skillBody) {
			t.Fatalf("skill lookup result = %s", body)
		}
	}
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%v", stream), func(t *testing.T) {
			e := newEngine(stream)
			project, err := e.CreateProject("Records", "", "", true)
			if err != nil {
				t.Fatal(err)
			}
			thread, err := e.CreateThread("recorded comparison", "", "")
			if err != nil {
				t.Fatal(err)
			}
			if err := e.store.UpdateThread(thread.ID, map[string]any{"project_id": project.ID}); err != nil {
				t.Fatal(err)
			}
			if _, _, err := e.ProjectMemory(project.ID).Add(note); err != nil {
				t.Fatal(err)
			}
			if _, err := e.ProjectMemory(project.ID).WriteSkill(skillName, "Use when comparing recorded measurements.", skillBody); err != nil {
				t.Fatal(err)
			}
			active := &store.Turn{ThreadID: thread.ID, Status: store.TurnDone, UserText: "Compare the recorded measurements."}
			quiet := &store.Turn{ThreadID: thread.ID, Status: store.TurnDone, Quiet: true}
			if err := e.store.CreateTurn(active); err != nil {
				t.Fatal(err)
			}
			if err := e.store.CreateTurn(quiet); err != nil {
				t.Fatal(err)
			}
			events := []store.Event{
				{ThreadID: thread.ID, TurnID: active.ID, Kind: "tool_result", Text: buried + " stays in the task record"},
				{ThreadID: thread.ID, TurnID: active.ID, Kind: "reasoning", Text: privateText},
				{ThreadID: thread.ID, TurnID: quiet.ID, Kind: "tool_result", Text: "quiet-only-measurement"},
			}
			for i := range 8 {
				events = append(events, store.Event{ThreadID: thread.ID, TurnID: active.ID, Kind: "tool_result", Text: fmt.Sprintf("later-output-%d", i)})
			}
			for i := range events {
				if err := e.store.AppendEvent(&events[i]); err != nil {
					t.Fatal(err)
				}
			}
			other, err := e.CreateThread("elsewhere", "", "")
			if err != nil {
				t.Fatal(err)
			}
			if err := e.store.AppendEvent(&store.Event{ThreadID: other.ID, Kind: "tool_result", Text: otherText}); err != nil {
				t.Fatal(err)
			}
			before, err := e.store.ListEvents(thread.ID, 0, 0)
			if err != nil {
				t.Fatal(err)
			}
			ask(t, e, thread.ID, stream)
			after, err := e.store.ListEvents(thread.ID, 0, 0)
			if err != nil || len(after) != len(before) {
				t.Fatalf("side lookup wrote events: before=%d after=%d err=%v", len(before), len(after), err)
			}
			usage := filepath.Join(e.cfg.ProjectMemoryDir(project.ID), memory.SkillsDir, skillName, memory.SkillUsageFile)
			if _, err := os.Stat(usage); !os.IsNotExist(err) {
				t.Fatalf("opening a skill from side chat recorded usage: %v", err)
			}
		})
	}
}

func TestSideChatLookupRefusesActionToolsAndStopsAfterItsBudget(t *testing.T) {
	const answer = "The preview did not contain another match."
	requests := make(chan sideChatWireRequest, 8)
	e := newSideChatHTTPEngine(t, config.APIModeChatCompletions, func(w http.ResponseWriter, r *http.Request, req sideChatWireRequest) {
		requests <- req
		switch sideQuestion(req) {
		case "Try a short query.":
			if sideHasToolResult(req) {
				writeContextFlowReply(w, false, map[string]any{"content": answer}, "stop")
			} else {
				writeSideToolCall(w, false, "call_short", sideChatSearchTool, `{"query":"x"}`)
			}
		case "Open the missing skill.":
			if sideHasToolResult(req) {
				writeContextFlowReply(w, false, map[string]any{"content": answer}, "stop")
			} else {
				writeSideToolCall(w, false, "call_missing", memory.ToolSkillView, `{"name":"missing-skill"}`)
			}
		case "Use an action tool.":
			writeSideToolCall(w, false, "call_write", "write", `{}`)
		default:
			if req.ToolChoice == "none" {
				writeContextFlowReply(w, false, map[string]any{"content": answer}, "stop")
			} else {
				writeSideToolCall(w, false, "call_again", sideChatSearchTool, `{"query":"later-output"}`)
			}
		}
	})
	project, thread := sideChatProjectThread(t, e)
	if _, _, err := e.ProjectMemory(project.ID).Add("Keep the comparison unit explicit."); err != nil {
		t.Fatal(err)
	}
	if _, err := e.ProjectMemory(project.ID).WriteSkill("compare-records", "Use when comparing recorded measurements.", "Read the recorded measurement."); err != nil {
		t.Fatal(err)
	}
	active := &store.Turn{ThreadID: thread.ID, Status: store.TurnDone}
	if err := e.store.CreateTurn(active); err != nil {
		t.Fatal(err)
	}
	if err := e.store.AppendEvent(&store.Event{ThreadID: thread.ID, TurnID: active.ID, Kind: "tool_result", Text: "later-output"}); err != nil {
		t.Fatal(err)
	}

	out, err := e.AskSideChat(t.Context(), thread.ID, SideChatInput{Question: "Keep searching the task."})
	if err != nil || out != answer {
		t.Fatalf("capped lookup answer=%q err=%v", out, err)
	}
	for range sideChatMaxLookups {
		if req := waitSideChatResult(t, requests); req.ToolChoice == "none" {
			t.Fatal("lookup budget ended before the searches ran")
		}
	}
	if req := waitSideChatResult(t, requests); req.ToolChoice != "none" {
		t.Fatalf("final side answer still offered tools: %+v", req)
	}

	if text, err := e.AskSideChat(t.Context(), thread.ID, SideChatInput{Question: "Use an action tool."}); text != "" || !errors.Is(err, ErrSideChatUnavailable) {
		t.Fatalf("action tool produced %q err=%v", text, err)
	}
	waitSideChatResult(t, requests)

	out, err = e.AskSideChat(t.Context(), thread.ID, SideChatInput{Question: "Try a short query."})
	if err != nil || out != answer {
		t.Fatalf("invalid query answer=%q err=%v", out, err)
	}
	waitSideChatResult(t, requests)
	if body := sideWireText(waitSideChatResult(t, requests)); !strings.Contains(body, "short distinctive") {
		t.Fatalf("short query result = %s", body)
	}

	out, err = e.AskSideChat(t.Context(), thread.ID, SideChatInput{Question: "Open the missing skill."})
	if err != nil || out != answer {
		t.Fatalf("missing skill answer=%q err=%v", out, err)
	}
	waitSideChatResult(t, requests)
	if body := sideWireText(waitSideChatResult(t, requests)); !strings.Contains(body, "no recorded skill") || !strings.Contains(body, "compare-records") {
		t.Fatalf("missing skill result = %s", body)
	}

	e.cfg.Memory.Enabled = false
	out, err = e.AskSideChat(t.Context(), thread.ID, SideChatInput{Question: "Keep searching the task."})
	if err != nil || out != answer {
		t.Fatalf("memory off answer=%q err=%v", out, err)
	}
	for range sideChatMaxLookups {
		req := waitSideChatResult(t, requests)
		if names := sideLookupToolNames(t, req.Tools); names[memory.ToolSkillView] || strings.Contains(sideWireText(req), "Keep the comparison unit explicit.") {
			t.Fatalf("disabled memory stayed visible: %+v", req)
		}
	}
	waitSideChatResult(t, requests)
}

func TestSideChatPromptDoesNotCarryASampleTask(t *testing.T) {
	if strings.Contains(strings.ToLower(sideChatPrompt), "collyer") || strings.Contains(sideChatPrompt, "for example") {
		t.Fatalf("side prompt picked up a sample task: %s", sideChatPrompt)
	}
	notes, clipped := clipSideNotes([]string{strings.Repeat("n", sideChatMaxQuestion+1)})
	if len(notes) != 1 || !clipped || notes[0] == "" || len(notes[0]) > sideChatMaxQuestion {
		t.Fatalf("single oversized note = %+v clipped=%v", notes, clipped)
	}
	notes, clipped = clipSideNotes([]string{"  ", "keep", strings.Repeat("n", sideChatMaxQuestion+1)})
	if len(notes) != 1 || !clipped || notes[0] == "" {
		t.Fatalf("oversized note clip = %+v clipped=%v", notes, clipped)
	}
	many := make([]string, 0, 3)
	many = append(many, strings.Repeat("a", sideChatMaxQuestion-1), "overflow")
	notes, clipped = clipSideNotes(many)
	if len(notes) != 1 || !clipped || notes[0] != many[0] {
		t.Fatalf("note budget = %+v clipped=%v", notes, clipped)
	}
	if sideCallsAllowed([]schema.ToolCall{{Function: schema.FunctionCall{Name: "write"}}}, true) ||
		sideCallsAllowed(make([]schema.ToolCall, sideChatMaxCalls+1), true) ||
		sideCallsAllowed([]schema.ToolCall{{Function: schema.FunctionCall{Name: memory.ToolSkillView}}}, false) {
		t.Fatal("side lookup accepted an action tool")
	}
}

func TestSideChatLookupKeepsBrokenSourcesInsideTheQuestion(t *testing.T) {
	if got := sideLookupJSON(make(chan int)); got != `{"error":"could not encode the lookup"}` {
		t.Fatalf("unencodable lookup = %s", got)
	}
	e := newTestEngine(t)
	project, thread := sideChatProjectThread(t, e)
	mem := e.ProjectMemory(project.ID)
	if _, _, err := mem.Add("Keep the comparison unit explicit."); err != nil {
		t.Fatal(err)
	}
	if _, err := mem.WriteSkill("alpha-skill", "Calibrate the north bearing before departure.", "Record the bearing."); err != nil {
		t.Fatal(err)
	}
	if _, err := mem.WriteSkill("beta-skill", "Archive expired certificates after rotation.", "Move the expired item aside."); err != nil {
		t.Fatal(err)
	}
	e.cfg.Memory.SkillsIndexMax = 1
	active := &store.Turn{ThreadID: thread.ID, Status: store.TurnDone}
	if err := e.store.CreateTurn(active); err != nil {
		t.Fatal(err)
	}
	if err := e.store.AppendEvent(&store.Event{ThreadID: thread.ID, TurnID: active.ID, Kind: "agent_message", AgentID: "worker", Text: "worker-finding-42"}); err != nil {
		t.Fatal(err)
	}
	stored, err := e.store.GetThread(thread.ID)
	if err != nil {
		t.Fatal(err)
	}

	sources, err := e.sideChatSources(t.Context(), *stored)
	if err != nil || len(sources.notes) != 1 || len(sources.skills) != 1 || sources.skills[0].Name != "alpha-skill" || sources.store == nil {
		t.Fatalf("clipped sources = %+v err=%v", sources, err)
	}
	found, err := e.searchSideTask(t.Context(), thread.ID, `{"query":"worker-finding"}`)
	if err != nil || !strings.Contains(found, `"agent":"worker"`) || !strings.Contains(found, "worker-finding-42") {
		t.Fatalf("worker search = %s err=%v", found, err)
	}
	bad, err := e.searchSideTask(t.Context(), thread.ID, "{")
	if err != nil || !strings.Contains(bad, "could not read the arguments") {
		t.Fatalf("malformed search = %s err=%v", bad, err)
	}
	if got := readSideSkill(sideChatSources{}, `{"name":"alpha-skill"}`); !strings.Contains(got, "no recorded skills") {
		t.Fatalf("missing skill store = %s", got)
	}
	if got := readSideSkill(sources, "{"); !strings.Contains(got, "could not read the arguments") {
		t.Fatalf("malformed skill request = %s", got)
	}
	if got := readSideSkill(sources, `{"name":"Not A Name"}`); !strings.Contains(got, "could not read the skill") {
		t.Fatalf("invalid skill name = %s", got)
	}

	lookup := &sideLookupModel{replies: []*schema.Message{
		schema.AssistantMessage("", []schema.ToolCall{{Function: schema.FunctionCall{Name: sideChatSearchTool, Arguments: `{"query":"worker-finding"}`}}}),
		schema.AssistantMessage("The worker record is in the task.", nil),
	}}
	out, err := e.answerSideChat(t.Context(), thread.ID, lookup, []*schema.Message{schema.UserMessage("Which finding?")}, sources, nil, 64)
	if err != nil || out != "The worker record is in the task." || len(lookup.seen) != 2 {
		t.Fatalf("empty call id answer=%q rounds=%d err=%v", out, len(lookup.seen), err)
	}
	var toolID, toolText string
	for _, msg := range lookup.seen[1] {
		if msg != nil && msg.Role == schema.Tool {
			toolID, toolText = msg.ToolCallID, msg.Content
		}
	}
	if toolID != "side-lookup-0-0" || !strings.Contains(toolText, "worker-finding-42") {
		t.Fatalf("synthesized tool result id=%q text=%s", toolID, toolText)
	}
	if _, err := e.answerSideChat(t.Context(), thread.ID, &sideLookupModel{nilOut: true}, nil, sources, nil, 64); !errors.Is(err, ErrSideChatUnavailable) {
		t.Fatalf("nil model message err=%v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	stopping := &sideLookupModel{before: cancel, replies: []*schema.Message{schema.AssistantMessage("too late", nil)}}
	if _, err := e.answerSideChat(ctx, thread.ID, stopping, nil, sources, nil, 64); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled round err=%v", err)
	}
	cancelled, stop := context.WithCancel(t.Context())
	stop()
	if _, err := e.sideLookup(cancelled, thread.ID, sources, schema.ToolCall{Function: schema.FunctionCall{Name: sideChatSearchTool, Arguments: `{"query":"worker-finding"}`}}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled lookup err=%v", err)
	}
	if _, err := e.sideLookup(t.Context(), thread.ID, sources, schema.ToolCall{Function: schema.FunctionCall{Name: "write"}}); !errors.Is(err, ErrSideChatUnavailable) {
		t.Fatalf("disallowed lookup err=%v", err)
	}

	root := e.cfg.ProjectMemoryDir(project.ID)
	notesPath := filepath.Join(root, memory.MemoryFile)
	if err := os.Remove(notesPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(notesPath, 0o700); err != nil {
		t.Fatal(err)
	}
	skillsPath := filepath.Join(root, memory.SkillsDir)
	if err := os.RemoveAll(skillsPath); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(skillsPath, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	broken, err := e.sideChatSources(t.Context(), *stored)
	if err != nil || len(broken.notes) != 0 || len(broken.skills) != 0 || broken.store != nil {
		t.Fatalf("unreadable memory became fatal: %+v err=%v", broken, err)
	}
	off, err := e.CreateProject("Quiet records", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	offThread, err := e.CreateThread("memory off", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.UpdateThread(offThread.ID, map[string]any{"project_id": off.ID}); err != nil {
		t.Fatal(err)
	}
	offStored, err := e.store.GetThread(offThread.ID)
	if err != nil || e.sideMemory(*offStored) != nil {
		t.Fatalf("disabled project memory = %v err=%v", e.sideMemory(*offStored), err)
	}
	missing := *stored
	missing.ProjectID = "missing-project"
	if e.sideMemory(missing) != nil {
		t.Fatal("missing project still exposed memory")
	}
	libraryThread, err := e.CreateThread("library only", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.LibraryMemory().WriteSkill("library-check", "Use when the conversation has no project.", "Read the library procedure."); err != nil {
		t.Fatal(err)
	}
	library, err := e.sideChatSources(t.Context(), *libraryThread)
	if err != nil || len(library.skills) != 1 || library.skills[0].Name != "library-check" {
		t.Fatalf("library skills = %+v err=%v", library.skills, err)
	}

	closed, err := store.Open(filepath.Join(t.TempDir(), "closed.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := closed.Close(); err != nil {
		t.Fatal(err)
	}
	saved := e.store
	e.store = closed
	if _, err := e.searchSideTask(t.Context(), thread.ID, `{"query":"worker-finding"}`); err == nil {
		t.Fatal("closed store returned a task search")
	}
	if _, err := e.sideChatSources(t.Context(), *stored); err == nil {
		t.Fatal("closed store returned task evidence")
	}
	if _, err := e.answerSideChat(t.Context(), thread.ID, &sideLookupModel{replies: []*schema.Message{
		schema.AssistantMessage("", []schema.ToolCall{{Function: schema.FunctionCall{Name: sideChatSearchTool, Arguments: `{"query":"worker-finding"}`}}}),
	}}, nil, sources, nil, 64); err == nil {
		t.Fatal("storage failure became a side answer")
	}
	e.store = saved

	if text, err := e.AskSideChat(t.Context(), thread.ID, SideChatInput{Question: "Which finding was recorded?", ProviderID: e.cfg.Models.Providers[0].ID, Model: "side-model"}); err != nil || text != "Side chat: Which finding was recorded?" {
		t.Fatalf("explicit side model produced %q err=%v", text, err)
	}
	if text, err := e.AskSideChat(t.Context(), thread.ID, SideChatInput{Question: "Which finding was recorded?", ProviderID: "missing-provider", Model: "missing-model"}); text != "" || err == nil {
		t.Fatalf("unknown side model produced %q err=%v", text, err)
	}

	const fault = "test:side_evidence_fault"
	if err := e.store.DB().Callback().Row().After("gorm:row").Register(fault, func(tx *gorm.DB) {
		if strings.Contains(tx.Statement.SQL.String(), "e.kind = 'tool_result'") {
			tx.AddError(errors.New("evidence unavailable"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = e.store.DB().Callback().Row().Remove(fault) }()
	if text, err := e.AskSideChat(t.Context(), thread.ID, SideChatInput{Question: "Which finding was recorded?"}); text != "" || err == nil || !strings.Contains(err.Error(), "evidence unavailable") {
		t.Fatalf("evidence failure produced %q err=%v", text, err)
	}
}

type sideLookupModel struct {
	replies []*schema.Message
	seen    [][]*schema.Message
	before  func()
	nilOut  bool
}

func (m *sideLookupModel) Generate(_ context.Context, in []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	if m.before != nil {
		m.before()
	}
	m.seen = append(m.seen, in)
	if m.nilOut || len(m.replies) == 0 {
		return nil, nil
	}
	next := m.replies[0]
	m.replies = m.replies[1:]
	return next, nil
}

func (m *sideLookupModel) Stream(context.Context, []*schema.Message, ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return nil, errors.New("unused")
}

func sideChatProjectThread(t *testing.T, e *Engine) (*store.Project, *store.Thread) {
	t.Helper()
	project, err := e.CreateProject("Records", "", "", true)
	if err != nil {
		t.Fatal(err)
	}
	thread, err := e.CreateThread("recorded comparison", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.UpdateThread(thread.ID, map[string]any{"project_id": project.ID}); err != nil {
		t.Fatal(err)
	}
	return project, thread
}

func writeSideToolCall(w http.ResponseWriter, stream bool, id, name, arguments string) {
	writeContextFlowReply(w, stream, map[string]any{
		"tool_calls": []any{map[string]any{
			"id": id, "type": "function",
			"function": map[string]any{"name": name, "arguments": arguments},
		}},
	}, "tool_calls")
}

func sideQuestion(req sideChatWireRequest) string {
	question := ""
	for _, msg := range req.Messages {
		if msg.Role == "user" || msg.Role == "" {
			question = msg.Content
		}
	}
	return question
}

func sideHasToolResult(req sideChatWireRequest) bool {
	for _, msg := range req.Messages {
		if msg.Role == "tool" {
			return true
		}
	}
	return false
}

func sideWireText(req sideChatWireRequest) string {
	var b strings.Builder
	for _, msg := range req.Messages {
		b.WriteString(msg.Content)
		b.WriteByte('\n')
	}
	return b.String()
}

func sideLookupToolNames(t *testing.T, tools []map[string]any) map[string]bool {
	t.Helper()
	names := map[string]bool{}
	for _, tool := range tools {
		if typ, _ := tool["type"].(string); typ != "" && typ != "function" {
			t.Fatalf("side chat exposed %q: %+v", typ, tool)
		}
		name, _ := tool["name"].(string)
		if name == "" {
			fn, _ := tool["function"].(map[string]any)
			name, _ = fn["name"].(string)
		}
		if name != sideChatSearchTool && name != memory.ToolSkillView {
			t.Fatalf("side chat exposed %q", name)
		}
		names[name] = true
	}
	if !names[sideChatSearchTool] {
		t.Fatal("side chat did not offer task search")
	}
	return names
}
