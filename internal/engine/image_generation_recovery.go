package engine

import (
	"context"

	swarm "github.com/LubyRuffy/eino-swarm"
	"github.com/LubyRuffy/eino-swarm/internal/store"
)

// An interrupted ADK tool can return to the manager before its HTTP goroutine
// unwinds. Finalization closes its durable card and revokes that goroutine's
// right to publish a result. An abandoned turn is settled by its successor.
func (rt *runtime) settleLiveImage(turn *store.Turn, stopped bool) error {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.imageCallID == "" || rt.turnID != turn.ID || rt.abandoned {
		return nil
	}
	err := rt.engine.recordImageEvent(context.Background(), interruptedImageStage(turn, rt.imageCallID, stopped))
	rt.imageCallID = ""
	return err
}

func interruptedImageStage(turn *store.Turn, callID string, stopped bool) store.Event {
	if stopped {
		return imageStage(turn, callID, "cancelled", "image_generation_cancelled")
	}
	return imageStage(turn, callID, "unknown", errImageUnknown.Error())
}

func latestImageStage(events []store.Event, turnID string) *store.Event {
	for i := len(events) - 1; i >= 0; i-- {
		ev := &events[i]
		if ev.TurnID == turnID && ev.Kind == KindImageGeneration {
			return ev
		}
	}
	return nil
}

// Restore uses the shared checkpoint read. The running receipt was written
// before the image POST, and is authority to recover the card, never to POST.
func (e *Engine) restoreImageGeneration(ctx context.Context, turn *store.Turn, stopped bool, snapshot *restoreSnapshot) (bool, error) {
	var stage *store.Event
	if snapshot != nil {
		stage = latestImageStage(snapshot.events, turn.ID)
	} else {
		var err error
		stage, err = e.store.LatestImageGeneration(ctx, turn.ThreadID, turn.ID)
		if err != nil {
			return turn.ImageCommand, err
		}
	}
	if stage != nil && stage.Text == "running" {
		settled := interruptedImageStage(turn, stage.ToolCallID, stopped)
		if err := e.recordImageEvent(ctx, settled); err != nil {
			return turn.ImageCommand, err
		}
		stage = &settled
	}
	if !turn.ImageCommand {
		return false, nil
	}
	status, code := store.TurnError, "image_generation_failed"
	if stage != nil && stage.Text == "completed" {
		status, code = store.TurnDone, ""
	} else if stopped || stage != nil && stage.Text == "cancelled" {
		status, code = store.TurnCancelled, "interrupted"
	} else if stage != nil && stage.Err != "" {
		code = stage.Err
	}
	return true, e.finishImageCommand(turn, status, code)
}

func orphanedImageToolResult(turnID string, call orphanedToolCall, events []store.Event) (string, string, bool) {
	if toolCallName(call.Text) != generateImageToolName || !isManagerAgent(call.AgentID) {
		return "", "", false
	}
	stage := latestImageStage(events, turnID)
	if stage == nil || stage.ToolCallID != call.ToolCallID || stage.Text == "running" || stage.Text == "unknown" {
		return swarm.HandoffUnknownToolResult, errImageUnknown.Error(), true
	}
	if stage.Text == "completed" && len(stage.Images) == 1 {
		return imageToolResult(nil), "", true
	}
	code := stage.Err
	if code == "" {
		code = errImageUnknown.Error()
	}
	return imageToolFailure(code), code, true
}
