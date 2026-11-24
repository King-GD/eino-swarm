package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	swarm "github.com/LubyRuffy/eino-swarm"
	"github.com/LubyRuffy/eino-swarm/internal/store"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"gorm.io/gorm"
)

// A tool that ignores cancellation must leave time for the final durable
// checkpoint, including the results of workers that already finished.
func TestRestartBlockedWorkerLeavesTimeForDurableCheckpoints(t *testing.T) {
	e := newTestEngineAt(t, t.TempDir())
	var checkpoints int
	if err := e.store.DB().Callback().Create().Before("gorm:create").Register("delay_final_restart_checkpoint", func(tx *gorm.DB) {
		if ev, ok := tx.Statement.Dest.(*store.Event); ok && ev.Kind == KindRestartCheckpoint {
			checkpoints++
			if checkpoints == 2 {
				// Make the final write take longer than the old one-second reserve.
				time.Sleep(1200 * time.Millisecond)
			}
		}
	}); err != nil {
		t.Fatal(err)
	}
	th, err := e.CreateThread("handoff fixture", "", "")
	if err != nil {
		t.Fatal(err)
	}
	turn := plantUnfinishedTurn(t, e, th.ID, "collect both worker results")
	reg := swarm.NewRegistry()
	t.Cleanup(reg.Close)
	entered, release := make(chan struct{}), make(chan struct{})
	const toolName, callID, args = "submit_record", "pending-submission", `{"record":"fixture"}`
	blocked, err := reg.Spawn(t.Context(), "writer", "submit the record", func(_, _ string) model.BaseChatModel {
		return restartWorkerReply{schema.AssistantMessage("", []schema.ToolCall{{
			ID: callID, Type: "function", Function: schema.FunctionCall{Name: toolName, Arguments: args},
		}})}
	}, &jsonArgsTool{name: toolName, run: func(string) (string, error) {
		close(entered)
		<-release
		return `{"confirmed":true}`, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		close(release)
		select {
		case <-blocked.Done():
		case <-time.After(5 * time.Second):
			t.Error("blocked worker did not exit after release")
		}
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not enter the actual tool executor")
	}
	const completedResult = "independently verified"
	finished, err := reg.Spawn(t.Context(), "reader", "verify the record", func(_, _ string) model.BaseChatModel {
		return restartWorkerReply{schema.AssistantMessage(completedResult, nil)}
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-finished.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("second worker did not complete before handoff")
	}
	rt := e.runtimeFor(th.ID)
	rt.mu.Lock()
	rt.turnID, rt.parked = turn.ID, reg
	rt.mu.Unlock()
	ctx, cancel := context.WithTimeout(t.Context(), 7*time.Second)
	defer cancel()
	status, err := e.PrepareRestart(ctx)
	deadline, _ := ctx.Deadline()
	if err != nil || !status.Ready || status.Stage != "ready" || status.Checkpointed != 2 || !time.Now().Before(deadline) {
		t.Fatalf("handoff did not confirm both checkpoints before the parent deadline: %+v, err=%v, time remaining=%s", status, err, time.Until(deadline))
	}
	if checkpoints != 2 {
		t.Fatalf("expected baseline and final checkpoints, got %d", checkpoints)
	}
	cp := e.checkpointForTurn(turn.ID)
	if cp == nil || len(cp.Workers) != 2 {
		t.Fatalf("expected both workers in the durable checkpoint: %+v", cp)
	}
	workers := map[string]swarm.WorkerCheckpoint{cp.Workers[0].ID: cp.Workers[0], cp.Workers[1].ID: cp.Workers[1]}
	unknown := workers[blocked.ID]
	if unknown.Finished || unknown.Result != "" || len(unknown.InFlight) != 1 ||
		unknown.InFlight[0] != (swarm.WorkerToolCheckpoint{Name: toolName, CallID: callID, Arguments: args}) {
		t.Fatalf("in-flight tool lost its unknown outcome: %+v", unknown)
	}
	for _, msg := range unknown.History {
		if msg.Role == schema.Tool && msg.ToolCallID == callID && msg.Content != swarm.HandoffUnknownToolResult {
			t.Fatalf("unreturned tool was recorded as a confirmed result: %+v", msg)
		}
	}
	if worker := workers[finished.ID]; !worker.Finished || worker.Result != completedResult {
		t.Fatalf("finished worker lost its result: %+v", worker)
	}
}

func TestRestartSummaryDeadlineCapsWaitAndReservesTimeForWrites(t *testing.T) {
	start := time.Unix(0, 0)
	for _, tc := range []struct {
		remaining time.Duration
		want      time.Duration
	}{
		{19 * time.Second, 3 * time.Second},
		{4 * time.Second, 3 * time.Second},
		{2 * time.Second, 1600 * time.Millisecond},
		{0, 0},
		{-time.Second, -time.Second},
	} {
		if got := restartSummaryDeadline(start, start.Add(tc.remaining)).Sub(start); got != tc.want {
			t.Errorf("summary deadline with %s left = %s, want %s", tc.remaining, got, tc.want)
		}
	}
}

type restartWorkerReply struct{ reply *schema.Message }

func (m restartWorkerReply) Generate(context.Context, []*schema.Message, ...model.Option) (*schema.Message, error) {
	return m.reply, nil
}

func (m restartWorkerReply) Stream(context.Context, []*schema.Message, ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return schema.StreamReaderFromArray([]*schema.Message{m.reply}), nil
}

func TestRestartExpiredFinalWriteDoesNotAuthorizeReplacement(t *testing.T) {
	e := newTestEngineAt(t, t.TempDir())
	var checkpoints int
	err := e.store.DB().Callback().Create().Before("gorm:create").Register("wait_for_restart_write_deadline", func(tx *gorm.DB) {
		if ev, ok := tx.Statement.Dest.(*store.Event); ok && ev.Kind == KindRestartCheckpoint {
			checkpoints++
			if checkpoints == 2 {
				// Let the real final write cross its deadline inside persistence.
				select {
				case <-tx.Statement.Context.Done():
				case <-time.After(2 * time.Second):
				}
			}
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	th, err := e.CreateThread("handoff deadline fixture", "", "")
	if err != nil {
		t.Fatal(err)
	}
	turn := plantUnfinishedTurn(t, e, th.ID, "continue")
	rt := e.runtimeFor(th.ID)
	rt.mu.Lock()
	rt.turnID, rt.parked = turn.ID, swarm.NewRegistry()
	rt.mu.Unlock()
	ctx, cancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
	defer cancel()
	if got, err := e.PrepareRestart(ctx); !errors.Is(err, context.DeadlineExceeded) || got.Ready || got.Stage != "failed" {
		t.Fatalf("expired final write authorized replacement: %+v, err=%v", got, err)
	}
	if got, err := e.PrepareRestart(t.Context()); err != nil || !got.Ready || got.ThreadsCheckpointed != 1 {
		t.Fatalf("retry did not save the retained checkpoint: %+v, err=%v", got, err)
	}
}

// Checkpoints have not been rewritten while workers summarize. That is a
// separate count from workers whose summary has already settled.
func TestRestartSummaryReportsSettledWorkersAndPendingIdentities(t *testing.T) {
	e := newTestEngine(t)
	var output bytes.Buffer
	e.log = slog.New(slog.NewTextHandler(&output, nil))
	th, err := e.CreateThread("", "", "")
	if err != nil {
		t.Fatal(err)
	}
	turn := plantUnfinishedTurn(t, e, th.ID, "continue")
	reg := swarm.NewRegistry()
	t.Cleanup(reg.Close)
	entered := make(chan struct{})
	reg.ModelBuilder = func(_, _ string) model.BaseChatModel { return &holdModel{entered: entered} }
	h, err := reg.Spawn(t.Context(), "observer", "inspect current state", reg.ModelBuilder)
	if err != nil {
		t.Fatal(err)
	}
	<-entered
	if err := reg.PlantFinished(swarm.FinishedWorker{ID: "archived-2", Role: "reader", Result: "confirmed"}); err != nil {
		t.Fatal(err)
	}
	rt := e.runtimeFor(th.ID)
	rt.mu.Lock()
	rt.turnID, rt.parked = turn.ID, reg
	rt.mu.Unlock()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	done := make(chan RestartStatus, 1)
	var prepareErr error
	go func() {
		status, err := e.PrepareRestart(ctx)
		prepareErr = err
		done <- status
	}()
	waitRestartState(t, func() bool { return e.GetRestartStatus().Stage == "summarizing" })
	raw, err := json.Marshal(e.GetRestartStatus())
	if err != nil {
		t.Fatal(err)
	}
	var status struct {
		Workers      int `json:"workers"`
		Checkpointed int `json:"checkpointed"`
		Settled      int `json:"settled"`
	}
	if err := json.Unmarshal(raw, &status); err != nil {
		t.Fatal(err)
	}
	if status.Workers != 2 || status.Settled != 1 || status.Checkpointed != 0 {
		t.Errorf("summary progress hid an already-settled worker: %s", raw)
	}
	final := <-done
	if prepareErr != nil || !final.Ready {
		t.Fatalf("handoff did not finish: %+v, err=%v", final, prepareErr)
	}
	if logs := output.String(); !strings.Contains(logs, "level=WARN") || !strings.Contains(logs, h.ID) || !strings.Contains(logs, "observer") || strings.Contains(logs, "archived-2") {
		t.Fatalf("half-budget warning did not identify only pending workers: %s", logs)
	}
}
