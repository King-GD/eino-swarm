package engine

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/LubyRuffy/eino-swarm/internal/config"
	"github.com/LubyRuffy/eino-swarm/internal/provider"
	"github.com/LubyRuffy/eino-swarm/internal/store"
	"github.com/cloudwego/eino/components/tool"
)

// The reviewer builds its own tool node. An invented name must stay a tool
// result there too, or a memory review fails after the turn already finished.
func TestReviewerUnknownToolDoesNotFailTheReview(t *testing.T) {
	var step atomic.Int32
	var requests atomic.Int32
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		var request scheduleCompletionRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode review request: %v", err)
			http.Error(w, "invalid fixture request", http.StatusBadRequest)
			return
		}
		if step.Add(1) == 1 {
			writeScheduleCompletionTool(w, request, "missing_tool", `{"token":"secret"}`)
			return
		}
		var result string
		for _, msg := range request.Messages {
			if msg != nil && msg.Role == "tool" && strings.Contains(msg.Content, "tool_not_registered") {
				result = msg.Content
			}
		}
		if !strings.Contains(result, `"code":"tool_not_registered"`) || strings.Contains(result, "secret") {
			t.Errorf("reviewer did not receive the unknown-tool result: %s", result)
		}
		writeContextFlowReply(w, request.Stream, map[string]any{"content": "Review finished."}, "stop")
	}))
	t.Cleanup(endpoint.Close)
	t.Setenv("OPENAI_BASE_URL", "")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("OPENAI_MODEL", "")
	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cfg.Models.Default = config.DefaultProviderID
	cfg.Models.Providers = []config.Provider{{
		ID: config.DefaultProviderID, BaseURL: endpoint.URL, Model: "offline-review", ContextWindow: 128000,
	}}
	cfg.Memory.AutoReview = false
	st, err := store.Open(cfg.DBPath())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	e := New(cfg, st, provider.New(cfg), slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(e.Shutdown)
	th, turn, _ := qualityReviewTurn(t, e, store.TurnDone, nil)
	var mu sync.Mutex
	out := reviewOutcome{}
	// An empty tool list builds the no-tools runner, so the tools node — and
	// its unknown-name handler — never runs. The reviewer always has tools.
	e.driveReviewer(reviewRun{
		threadID: th.ID, turnID: turn.ID, maxIter: 3, userMsg: "Review the source.",
		tools: []tool.BaseTool{newScheduleTool("read_note", "Read one stored note.", nil, func(string) (string, error) {
			return "note-body", nil
		})},
	}, &mu, &out)
	if requests.Load() == 0 || strings.Contains(out.Err, "toolsNode") || strings.Contains(out.Err, "not found") || out.StopReason != "completed" || out.Note != "Review finished." {
		t.Fatalf("requests=%d review outcome %+v", requests.Load(), out)
	}
}
