package engine

import (
	"context"
	"encoding/json"

	swarm "github.com/LubyRuffy/eino-swarm"
	"github.com/LubyRuffy/eino-swarm/internal/mcpclient"
	"github.com/LubyRuffy/eino-swarm/internal/store"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/compose"
)

func (rt *runtime) withMCPExecution(ctx context.Context, reg *swarm.Registry) context.Context {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.reg != reg && rt.parked != reg {
		return ctx
	}
	if rt.mcpRegistry != reg {
		rt.mcpRegistry = reg
		rt.mcpExecution = mcpclient.NewExecution(func(ctx context.Context, failure *mcpclient.ExecutionError) {
			rt.recordMCPFailure(ctx, reg, failure)
		})
	}
	return mcpclient.WithExecution(ctx, rt.mcpExecution)
}

// dropParkedMCPExecutionLocked releases a detached Registry's task scope.
func (rt *runtime) dropParkedMCPExecutionLocked(reg *swarm.Registry) {
	if rt.mcpRegistry == reg && rt.reg != reg {
		rt.mcpRegistry, rt.mcpExecution = nil, nil
	}
}

func (rt *runtime) recordMCPFailure(ctx context.Context, reg *swarm.Registry, failure *mcpclient.ExecutionError) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.abandoned || (rt.reg != reg && rt.parked != reg) {
		return
	}
	caller, _ := swarm.ToolCaller(ctx)
	body, _ := json.Marshal(failure)
	rt.engine.record(store.Event{
		ThreadID: rt.threadID, TurnID: rt.turnID, Kind: store.KindMCPExecutionBlocked,
		AgentID: caller.AgentID, ToolCallID: compose.GetToolCallID(ctx), Text: string(body),
	})
}

func (rt *runtime) recoverTurnTools(ctx context.Context, turn *store.Turn, messages []adk.Message, snapshots ...*restoreSnapshot) ([]adk.Message, error) {
	if err := rt.engine.restoredMCPFailure(turn.ID, firstRestore(snapshots)); err != nil {
		return messages, err
	}
	rt.engine.closeOrphanedToolCalls(turn, snapshots...)
	return rt.resolveOrphanedAsk(ctx, turn, messages, snapshots...)
}

func (e *Engine) restoredMCPFailure(turnID string, snapshot *restoreSnapshot) error {
	events, err := e.restoreTurnEvents(turnID, snapshot)
	if err != nil {
		return err
	}
	for i := len(events) - 1; i >= 0; i-- {
		switch events[i].Kind {
		case KindFailureContinued:
			return nil
		case store.KindMCPExecutionBlocked:
			var failure mcpclient.ExecutionError
			if json.Unmarshal([]byte(events[i].Text), &failure) != nil || failure.Code == "" {
				failure = mcpclient.ExecutionError{Code: "recovery_receipt_unavailable"}
			}
			return &failure
		}
	}
	return nil
}
