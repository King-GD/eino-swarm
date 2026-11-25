package engine

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	swarm "github.com/LubyRuffy/eino-swarm"
	"github.com/LubyRuffy/eino-swarm/internal/memory"
	"github.com/LubyRuffy/eino-swarm/internal/provider"
	"github.com/LubyRuffy/eino-swarm/internal/store"
)

// A percentage warning must agree with the real write tool at model input,
// while the successful write must not rewrite the turn's cached prompt prefix.
func TestMemoryPressureAllowsFittingToolGrowthWithoutChangingTheTurnPrefix(t *testing.T) {
	initial, added := strings.Repeat("a", 80), strings.Repeat("b", 18)
	const finalAnswer = "The project convention is saved."
	const callID = "memory-pressure-add"
	arguments, err := json.Marshal(map[string]string{"action": "add", "content": added})
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var managerRequests []scheduleCompletionRequest
	var classifications atomic.Int32
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/chat/completions" {
			t.Errorf("unexpected provider route %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		var request scheduleCompletionRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil || len(request.Messages) == 0 {
			t.Errorf("invalid actual provider request: %v", err)
			http.Error(w, "invalid fixture request", http.StatusBadRequest)
			return
		}
		system := request.Messages[0].Content
		if system == scheduleAnswerPrompt {
			// Match the genuine classifier protocol and this exact completed
			// answer. No other auxiliary request gets a fabricated success.
			var candidate struct {
				Messages []string `json:"messages"`
				Now      string   `json:"now"`
			}
			if len(request.Messages) != 2 || request.Messages[0].Role != "system" || request.Messages[1].Role != "user" || json.Unmarshal([]byte(request.Messages[1].Content), &candidate) != nil || len(candidate.Messages) != 1 || candidate.Messages[0] != finalAnswer {
				t.Error("classifier did not receive the exact real terminal answer")
				http.Error(w, "unexpected classifier candidate", http.StatusBadRequest)
				return
			}
			if _, err := time.Parse(time.RFC3339, candidate.Now); err != nil {
				t.Errorf("classifier time is not the normal protocol instant: %v", err)
			}
			classifications.Add(1)
			replyNoScheduleClaimsFixture(t, w, system, request.Stream, len(request.Tools))
			return
		}
		memoryAvailable := false
		for _, tool := range request.Tools {
			memoryAvailable = memoryAvailable || tool.Function.Name == memory.ToolMemory
		}
		if request.Messages[0].Role != "system" || !memoryAvailable {
			t.Error("unexpected non-manager provider request")
			http.Error(w, "unexpected fixture request", http.StatusBadRequest)
			return
		}
		mu.Lock()
		managerRequests = append(managerRequests, request)
		step := len(managerRequests)
		firstSystem := managerRequests[0].Messages[0].Content
		mu.Unlock()
		if !strings.Contains(system, "80/100 characters") || !strings.Contains(system, "Free characters: 20") || !strings.Contains(system, initial) || strings.Contains(system, "A write that increases the character count will be refused") {
			t.Error("actual manager input contradicts fitting growth or lost its initial snapshot")
		}
		switch step {
		case 1:
			writeContextFlowReply(w, request.Stream, map[string]any{"tool_calls": []any{map[string]any{
				"index": 0, "id": callID, "type": "function",
				"function": map[string]any{"name": memory.ToolMemory, "arguments": string(arguments)},
			}}}, "tool_calls")
		case 2:
			if system != firstSystem {
				t.Error("successful memory write changed the cached system prefix mid-turn")
			}
			found := false
			for _, message := range request.Messages {
				if message.Role != "tool" || message.ToolCallID != callID {
					continue
				}
				var receipt struct {
					Success bool     `json:"success"`
					Usage   string   `json:"usage"`
					Entries []string `json:"entries"`
				}
				if err := json.Unmarshal([]byte(message.Content), &receipt); err != nil || !receipt.Success || receipt.Usage != "100/100" || len(receipt.Entries) != 2 || receipt.Entries[0] != initial || receipt.Entries[1] != added {
					t.Errorf("next model call lost the actual successful tool receipt: %v", err)
				}
				found = true
			}
			if !found {
				t.Error("actual ADK memory result did not reach the next model request")
			}
			writeContextFlowReply(w, request.Stream, map[string]any{"content": finalAnswer}, "stop")
		default:
			t.Errorf("unexpected manager generation %d", step)
			http.Error(w, "unexpected fixture generation", http.StatusBadRequest)
		}
	}))
	t.Cleanup(endpoint.Close)
	e := newTestEngine(t)
	e.cfg.Memory.CharLimit, e.cfg.Memory.EntryMax = 100, 90
	e.cfg.Memory.AutoReview = false
	e.cfg.Swarm.AutoTitle, e.cfg.Swarm.ComposerPredictions = false, false
	e.cfg.Models.Providers[0].BaseURL = endpoint.URL
	e.cfg.Models.Providers[0].Model = "offline-memory-pressure"
	e.pool = provider.New(e.cfg)
	project, err := e.CreateProject("Memory pressure fixture", "", "", true)
	if err != nil {
		t.Fatal(err)
	}
	mem := e.ProjectMemory(project.ID)
	if _, err := mem.Overwrite(initial); err != nil {
		t.Fatal(err)
	}
	thread, err := e.CreateThread("Record a project convention", "", project.ID)
	if err != nil {
		t.Fatal(err)
	}
	turn, err := e.StartTurn(thread.ID, "Keep the durable project convention.")
	if err != nil {
		t.Fatal(err)
	}
	finished := waitForTurn(t, e, turn.ID)
	waitSettled(t, e, thread.ID)
	if finished.Status != store.TurnDone || finished.Final != finalAnswer {
		t.Fatalf("the real tool/model turn did not finish normally: %+v", finished)
	}
	mu.Lock()
	managerCalls := len(managerRequests)
	mu.Unlock()
	if managerCalls != 2 || classifications.Load() != 1 {
		t.Fatalf("unexpected actual provider calls: managers=%d classifications=%d", managerCalls, classifications.Load())
	}
	snapshot, err := mem.Read()
	if err != nil || snapshot.Chars != 100 || len(snapshot.Entries) != 2 || snapshot.Entries[0] != initial || snapshot.Entries[1] != added {
		t.Fatalf("real project memory did not retain the fitting write: %+v %v", snapshot, err)
	}
	events, err := e.Store().ListTurnEvents(turn.ID)
	if err != nil {
		t.Fatal(err)
	}
	calls, results := 0, 0
	for _, event := range events {
		if event.ToolCallID != callID {
			continue
		}
		if event.Kind == swarm.NotifyToolCall.String() {
			calls++
		}
		if event.Kind == swarm.NotifyToolResult.String() && event.Err == "" {
			results++
		}
	}
	if calls != 1 || results != 1 {
		t.Fatalf("the durable actual memory tool pair is missing: calls=%d results=%d", calls, results)
	}
}
