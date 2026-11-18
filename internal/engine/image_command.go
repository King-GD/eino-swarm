package engine

import (
	"context"
	"errors"
	"strings"

	swarm "github.com/LubyRuffy/eino-swarm"
	"github.com/LubyRuffy/eino-swarm/internal/provider"
	"github.com/LubyRuffy/eino-swarm/internal/store"
	"github.com/cloudwego/eino/schema"
)

func (e *Engine) startImageCommand(threadID string, in UserInput, prompt string) (*store.Turn, error) {
	if len(in.Images) != 0 || len(in.Files) != 0 {
		return nil, ErrImageTextOnly
	}
	if in.HistoryReport || in.FromEventSeq > 0 {
		return nil, ErrImageReadOnly
	}
	prompt, err := provider.ValidateImagePrompt(prompt)
	if err != nil {
		return nil, ErrImagePrompt
	}
	rt := e.runtimeFor(threadID)
	rt.projectMoveMu.Lock()
	defer rt.projectMoveMu.Unlock()
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.running || rt.parked != nil {
		return nil, ErrBusy
	}
	thread, err := e.store.GetThread(threadID)
	if err != nil {
		return nil, err
	}
	if thread.PlanMode {
		return nil, ErrImageReadOnly
	}
	if !e.pool.ImageAvailable(thread.ProviderID) {
		return nil, ErrImageUnavailable
	}
	turn := &store.Turn{ThreadID: threadID, ProjectID: thread.ProjectID, ProviderID: thread.ProviderID,
		UserText: strings.TrimSpace(in.Text), ImageCommand: true}
	if err := e.store.CreateTurn(turn); err != nil {
		return nil, err
	}
	if err := e.store.AppendMessages(threadID, turn.ID, []store.Message{
		{Role: string(schema.User), Content: turn.UserText, Source: store.MessageSourceHuman},
	}); err != nil {
		return nil, errors.Join(err, e.store.FinishTurn(turn.ID, store.TurnError, "", errImageSave.Error()))
	}
	if err := e.recordImageEvent(context.Background(), store.Event{ThreadID: threadID, TurnID: turn.ID,
		Kind: KindUser, AgentID: swarm.DefaultManagerID, Text: turn.UserText}); err != nil {
		return nil, errors.Join(err, e.store.FinishTurn(turn.ID, store.TurnError, "", errImageSave.Error()))
	}
	// Use the same runtime owner, stop and restart checkpoint as ordinary
	// turns. A registry with no model supplies the lifecycle only.
	ctx, cancel := context.WithCancel(context.Background())
	idle := make(chan struct{})
	reg := swarm.NewRegistry()
	rt.occupyLocked(reg, cancel, turn.ID, idle)
	rt.imageOnly = true
	go rt.runImageCommand(ctx, cancel, idle, reg, turn, prompt)
	return turn, nil
}

func (rt *runtime) runImageCommand(ctx context.Context, cancel context.CancelFunc, idle chan struct{}, reg *swarm.Registry, turn *store.Turn, prompt string) {
	defer rt.release(cancel, idle, turn.ID)
	e := rt.engine
	_, runErr := rt.generateImage(ctx, turn, turn.ProviderID, turn.ID, prompt)
	unlock, _ := rt.lockFinish(context.Background())
	status, _, abandoned := rt.turnOutcome(ctx, runErr)
	if abandoned {
		if !e.restarting() {
			reg.Close()
		}
		unlock()
		return
	}
	reg.Close()
	if err := rt.settleLiveImage(turn, ctx.Err() != nil); err != nil {
		e.log.Warn("could not settle image generation", "turn", turn.ID, "err", err)
	}
	rt.release(cancel, idle, turn.ID)
	code := ""
	if runErr != nil {
		code = imageErrorCode(runErr)
	}
	if status == store.TurnCancelled {
		code = "interrupted"
	}
	if err := e.finishImageCommand(turn, status, code); err != nil {
		e.log.Warn("could not finish image generation turn", "turn", turn.ID, "err", err)
	}
	unlock()
	if err := e.store.TouchThread(rt.threadID); err != nil && !errors.Is(err, store.ErrNotFound) {
		e.log.Warn("could not update image conversation activity", "thread", rt.threadID, "err", err)
	}
	rt.flushFollowup(status)
}

func (e *Engine) finishImageCommand(turn *store.Turn, status, code string) error {
	event := store.Event{ThreadID: turn.ThreadID, TurnID: turn.ID, AgentID: swarm.DefaultManagerID}
	if status == store.TurnDone {
		event.Kind = swarm.NotifyDone.String()
	} else {
		event.Kind = swarm.NotifyError.String()
		event.Err = code
		event.SuppressCompletion = status == store.TurnCancelled
	}
	if err := e.recordImageEvent(context.Background(), event); err != nil {
		return err
	}
	return e.store.FinishTurn(turn.ID, status, "", code)
}
