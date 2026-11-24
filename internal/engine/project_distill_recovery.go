package engine

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/LubyRuffy/eino-swarm/internal/provider"
)

const distillServiceRetryDelay = 500 * time.Millisecond

// A new attempt starts with the same frozen input and fresh destinations.
// A partial publication needs an explicit resume, even if the model failed.
func (e *Engine) reviewProjectDistillation(ctx context.Context, work *projectDistillWork) {
	if !e.reviewDistillationAttempt(ctx, work, false) {
		return
	}
	if work.result.ErrorCode == "temporary_model" {
		timer := time.NewTimer(distillServiceRetryDelay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
		case <-timer.C:
		}
	}
	work.result.State, work.result.Error = "running", ""
	work.result.ErrorCode, work.result.ErrorProviderID = "", ""
	if ctx.Err() != nil {
		work.result.State = "interrupted"
		return
	}
	e.reviewDistillationAttempt(ctx, work, true)
}

func distillationFailure(err error, stopReason string, citationRefused bool) (code string, retry bool) {
	var firstByte provider.FirstByteTimeoutError
	var idle provider.IdleTimeoutError
	switch {
	case err == nil:
		if citationRefused {
			return "evidence", true
		}
		if stopReason == "iteration_limit" {
			return "iteration_limit", false
		}
	case isContextOverflow(err):
		return "context_overflow", false
	case errors.As(err, &firstByte) || isHeaderWaitTimeout(err):
		return "first_byte_timeout", false
	case errors.As(err, &idle):
		return "idle_timeout", false
	case errors.Is(err, context.DeadlineExceeded):
		return "agent_timeout", false
	default:
		status := provider.HTTPStatus(err)
		if status == http.StatusTooManyRequests || modelFailureClass(err) == modelRetryReasonRateLimited {
			return "rate_limited", false
		}
		if status >= http.StatusInternalServerError || isRetryableModelError(err) {
			return "temporary_model", true
		}
		if status >= http.StatusBadRequest {
			return "model_settings", false
		}
	}
	return "", false
}
