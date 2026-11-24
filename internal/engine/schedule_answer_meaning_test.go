package engine

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/LubyRuffy/eino-swarm/internal/provider"
	"github.com/LubyRuffy/eino-swarm/internal/store"
	"github.com/cloudwego/eino/schema"
)

// The classifier must preserve the complete assertion's meaning. Merely
// finding an affirmative fragment inside a denial or quotation is insufficient.
func TestScheduleAnswerMeaningExcludesNonCurrentOrReportedAssertions(t *testing.T) {
	for _, tc := range []struct{ name, stance, temporal, attribution string }{
		{"current", "affirmed", "current", "assistant"},
		{"denial", "negated", "current", "assistant"},
		{"hypothetical", "hypothetical", "current", "assistant"},
		{"ended", "affirmed", "historical", "assistant"},
		{"proposal", "affirmed", "intended", "assistant"},
		{"human-policy", "affirmed", "current", "reported"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Keep a concrete cadence so this tests meaning independently of
			// the rule that ignores assertions with no verifiable fields.
			const text = "complete assertion every 90 seconds"
			claim := map[string]any{"quote": text, "scope": "unspecified", "new": true, "every_s": 90,
				"stance": tc.stance, "temporal": tc.temporal, "attribution": tc.attribution}
			body, _ := json.Marshal(map[string]any{"claims": []any{claim}})
			claims, err := decodeScheduleAnswer(schema.AssistantMessage(string(body), nil), []string{text})
			want := 0
			if tc.name == "current" {
				want = 1
			}
			if err != nil || len(claims) != want {
				t.Fatalf("meaning became an armed claim: claims=%+v err=%v want=%d", claims, err, want)
			}
		})
	}
	for _, field := range []string{"stance", "temporal", "attribution"} {
		for _, invalid := range []any{nil, "unknown", true} {
			claim := map[string]any{"quote": "complete assertion", "scope": "unspecified", "new": true,
				"stance": "affirmed", "temporal": "current", "attribution": "assistant"}
			claim[field] = invalid
			body, _ := json.Marshal(map[string]any{"claims": []any{claim}})
			if _, err := decodeScheduleAnswer(schema.AssistantMessage(string(body), nil), []string{"complete assertion"}); err == nil {
				t.Fatalf("unknown %s=%v silently granted admission", field, invalid)
			}
		}
	}
}

func TestNonClaimMeaningPublishesThroughRegistryAndHTTPWithoutGoalBlock(t *testing.T) {
	for _, tc := range []struct {
		name, text, stance, temporal, attribution string
		externalActor                             string
		mixed                                     bool
	}{
		{name: "denial", text: "No automatic check was arranged.", stance: "negated", temporal: "current", attribution: "assistant"},
		{name: "status", text: "The operation remains in progress."},
		{name: "human-policy", text: "The human forbids parking the conversation for an automatic check.", stance: "affirmed", temporal: "current", attribution: "reported"},
		{name: "ended", text: "The earlier automatic check finished.", stance: "affirmed", temporal: "historical", attribution: "assistant"},
		{name: "cancelled", text: "The earlier automatic check was cancelled.", stance: "affirmed", temporal: "historical", attribution: "assistant"},
		{name: "external-process", text: "观察到备份服务每 45 秒检查一次队列。", stance: "affirmed", temporal: "current", attribution: "assistant", externalActor: "备份服务"},
		{name: "external-and-local", text: "The backup service checks its queue every 45 seconds.", stance: "affirmed", temporal: "current", attribution: "assistant", externalActor: "The backup service", mixed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var managers, policies atomic.Int32
			endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req struct {
					Messages []*schema.Message `json:"messages"`
					Stream   bool              `json:"stream"`
					Tools    []any             `json:"tools"`
				}
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Error(err)
					return
				}
				if req.Messages[0].Content == scheduleAnswerPrompt {
					policies.Add(1)
					if req.Stream || len(req.Tools) != 0 || len(req.Messages) != 2 {
						t.Errorf("policy escaped read-only boundary: %+v", req)
					}
					expected := tc.text
					if tc.mixed && managers.Load() == 1 {
						expected += " " + fixtureScheduleClaim
					}
					var input struct {
						Messages []string `json:"messages"`
					}
					if json.Unmarshal([]byte(req.Messages[1].Content), &input) != nil || len(input.Messages) != 1 || input.Messages[0] != expected {
						t.Error("classifier lost full candidate context")
					}
					claims := []any{}
					if tc.stance != "" {
						claim := map[string]any{"quote": tc.text, "scope": "unspecified", "new": true,
							"stance": tc.stance, "temporal": tc.temporal, "attribution": tc.attribution}
						if tc.externalActor != "" {
							claim["scope"], claim["scope_quote"], claim["every_s"] = "external", tc.externalActor, 45
						}
						claims = append(claims, claim)
					}
					if tc.mixed && managers.Load() == 1 {
						claims = append(claims, map[string]any{"quote": fixtureScheduleClaim, "scope": "unspecified", "new": true, "every_s": 90,
							"stance": "affirmed", "temporal": "current", "attribution": "assistant"})
					}
					body, _ := json.Marshal(map[string]any{"claims": claims})
					writeContextFlowReply(w, false, map[string]any{"content": string(body)}, "stop")
					return
				}
				answer := tc.text
				if strings.Contains(req.Messages[0].Content, "## Waiting") {
					step := managers.Add(1)
					if tc.mixed && step == 1 {
						answer += " " + fixtureScheduleClaim
					}
					if tc.mixed && step == 2 && !strings.Contains(req.Messages[len(req.Messages)-1].Content, "identify the external owner") {
						t.Error("correction did not tell the manager how to preserve the external finding")
					}
				}
				writeContextFlowReply(w, req.Stream, map[string]any{"content": answer, "reasoning_content": "Inspect current state before reporting."}, "stop")
			}))
			t.Cleanup(endpoint.Close)
			e := newTestEngine(t)
			e.cfg.Swarm.AutoTitle = false
			e.cfg.Models.Providers[0].BaseURL = endpoint.URL
			e.cfg.Models.Providers[0].Model = "offline-claim-meaning"
			e.pool = provider.New(e.cfg)
			th, err := e.CreateThread("nonclaim admission", "", "")
			if err != nil {
				t.Fatal(err)
			}
			if err := e.SetThreadGoal(th.ID, "finish the assigned work"); err != nil {
				t.Fatal(err)
			}
			// A no-tool continuation uses the normal idle hold after publication;
			// that unrelated policy must neither clear its budget nor block it.
			if err := e.store.UpdateThread(th.ID, map[string]any{"goal_auto_turns": 2}); err != nil {
				t.Fatal(err)
			}
			turn, err := e.StartTurnInput(th.ID, UserInput{Text: "Inspect current state.", ContinueGoal: true})
			if err != nil {
				t.Fatal(err)
			}
			got := waitForTurn(t, e, turn.ID)
			waitSettled(t, e, th.ID)
			want := int32(1)
			if tc.mixed {
				want++
			}
			if got.Status != store.TurnDone || got.Final != tc.text || managers.Load() != want || policies.Load() != want {
				t.Fatalf("nonclaim withheld/replayed: turn=%+v managers=%d policies=%d", got, managers.Load(), policies.Load())
			}
			updated, err := e.store.GetThread(th.ID)
			if err != nil || updated.GoalBlocked || updated.GoalBlockReason != "" || updated.GoalAutoTurns != 2 {
				t.Fatalf("admission changed goal ownership/budget: thread=%+v err=%v", updated, err)
			}
			if scheduleAnswerRetryCount(t, e, turn.ID) != int(want-1) {
				t.Fatal("correction was not limited to the unsupported local claim")
			}
			if tc.externalActor != "" {
				if replay := replayDump(t, e, th.ID); !strings.Contains(replay, tc.text) || strings.Contains(replay, fixtureScheduleClaim) {
					t.Fatal("replay lost the external finding or admitted an unverified local claim")
				}
				if schedules, err := e.store.ListSchedules(); err != nil || len(schedules) != 0 {
					t.Fatalf("reporting an external cadence created a local wait: %+v, %v", schedules, err)
				}
			}
			rows, err := e.store.ListPendingScheduleAnswers(turn.ID)
			if err != nil || len(rows) != int(want) || rows[len(rows)-1].State != "accepted" || rows[len(rows)-1].Payload != "" {
				t.Fatalf("terminal candidate payload/decision mismatch: rows=%+v err=%v", rows, err)
			}
			if tc.mixed && (rows[0].State != "rejected" || rows[0].Payload != "") {
				t.Fatalf("unsupported local candidate was retained: %+v", rows[0])
			}
		})
	}
}

func TestExternalScheduleClaimNeedsExactOwnerAndCannotCarryAppScheduleID(t *testing.T) {
	const text = "The backup service checks its queue every 45 seconds. An app check is armed as sch_local."
	for _, tc := range []struct{ name, quote, witness, scheduleID, wantDetail string }{
		{"missing", text, "", "", "scope_quote"},
		{"out-of-quote", "An app check is armed as sch_local.", "backup service", "", "scope_quote"},
		{"app-id", text, "backup service", "sch_local", "schedule_id"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			claim := map[string]any{"quote": tc.quote, "scope": "external", "scope_quote": tc.witness, "schedule_id": tc.scheduleID, "new": false,
				"stance": "affirmed", "temporal": "current", "attribution": "assistant"}
			body, _ := json.Marshal(map[string]any{"claims": []any{claim}})
			claims, err := decodeScheduleAnswer(schema.AssistantMessage(string(body), nil), []string{text})
			failure, ok := err.(*scheduleAnswerError)
			if !ok || failure.Detail != tc.wantDetail || len(claims) != 0 {
				t.Fatalf("invalid external evidence reached admission: claims=%+v diagnostic=%#v", claims, failure)
			}
		})
	}
}
