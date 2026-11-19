package mcpclient

import (
	"context"
	"fmt"
	"sync/atomic"
)

// Execution tracks a task's first verified MCP failure. A tool error stays a
// structured result for ADK history. Admission checks stop further work.
type Execution struct {
	failed    atomic.Pointer[ExecutionError]
	onFailure func(context.Context, *ExecutionError)
	blocked   context.Context
	cancel    context.CancelCauseFunc
}

type executionKey struct{}

// ExecutionError retains the adapter's safe failure class and server identity.
type ExecutionError struct {
	Code     string `json:"code"`
	ServerID string `json:"server_id"`
}

func (e *ExecutionError) Error() string {
	message := fmt.Sprintf("MCP tool failed (%s, %s). Check the MCP connection in Settings, then retry the task.", e.ServerID, e.Code)
	switch e.Code {
	case "timeout", "call_failed", "closed":
		message += " Verify the remote operation's outcome before repeating it."
	}
	return message
}

// NewExecution invokes onFailure once, at the first admission-blocking result.
func NewExecution(onFailure func(context.Context, *ExecutionError)) *Execution {
	blocked, cancel := context.WithCancelCause(context.Background())
	return &Execution{onFailure: onFailure, blocked: blocked, cancel: cancel}
}

// WithExecution binds a manager and its workers to the same admission state.
func WithExecution(ctx context.Context, state *Execution) context.Context {
	return context.WithValue(ctx, executionKey{}, state)
}

// InheritExecution retains the execution identity without inheriting cancellation.
func InheritExecution(parent, detached context.Context) context.Context {
	if state := execution(parent); state != nil {
		return WithExecution(detached, state)
	}
	return detached
}

func execution(ctx context.Context) *Execution {
	state, _ := ctx.Value(executionKey{}).(*Execution)
	return state
}

// ExecutionFailure is checked on the actual model and tool admission paths.
func ExecutionFailure(ctx context.Context) *ExecutionError {
	if state := execution(ctx); state != nil {
		return state.failed.Load()
	}
	return nil
}

// WatchExecution ends work already admitted by the manager when any member
// of the task reports an MCP failure. Its caller retains the original parent
// for normal Stop/restart and final status classification.
func WatchExecution(ctx context.Context) (context.Context, func()) {
	state := execution(ctx)
	if state == nil {
		return ctx, func() {}
	}
	watched, cancel := context.WithCancelCause(ctx)
	stop := context.AfterFunc(state.blocked, func() {
		cancel(context.Cause(state.blocked))
	})
	return watched, func() {
		stop()
		cancel(nil)
	}
}

// AwaitExecutionFailure lets the first failure's receipt attempt finish before
// the manager publishes its terminal error. Human Stop can end that wait.
func AwaitExecutionFailure(ctx context.Context) *ExecutionError {
	state := execution(ctx)
	if state == nil {
		return nil
	}
	if failure := state.failed.Load(); failure != nil {
		select {
		case <-state.blocked.Done():
			return failure
		case <-ctx.Done():
		}
	}
	return nil
}

func markExecutionFailed(ctx context.Context, code, serverID string) {
	if state := execution(ctx); state != nil && ctx.Err() == nil && code != "cancelled" {
		failure := &ExecutionError{Code: code, ServerID: serverID}
		if state.failed.CompareAndSwap(nil, failure) {
			defer state.cancel(failure)
			if state.onFailure != nil {
				state.onFailure(ctx, failure)
			}
		}
	}
}
