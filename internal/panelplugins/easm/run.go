package easm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// stepFunc performs one lookup against the run's graph and metrics. It returns
// ErrUnavailable, a *ToolError or a cancellation; any other error is reported
// as a malformed reply for that step only.
type stepFunc func(*run, context.Context) error

// stepDef binds a step to the tool roles it needs. A step whose roles are
// not all configured is reported unavailable without being attempted.
type stepDef struct {
	roles []string
	fn    stepFunc
}

// stepFuncs is filled by the per-source files.
var stepFuncs = map[Step]stepDef{}

const interruptRecordTimeout = 2 * time.Second

// metricSteps need an asset query; they are skipped without one.
var metricSteps = map[Step]bool{StepSummary: true, StepHeat: true, StepComponents: true}

type run struct {
	input    Input
	settings Settings
	caller   Caller
	graph    *Graph
	state    State
	publish  Publish
}

// Run collects the portrait step by step and publishes the complete state
// after every step. A failed or unavailable lookup marks only its own step;
// the run fails only on cancellation or when publication itself fails.
func Run(ctx context.Context, input Input, settings Settings, caller Caller, publish Publish) (Result, error) {
	input.Subject = strings.TrimSpace(input.Subject)
	input.Query = strings.TrimSpace(input.Query)
	if input.Subject == "" {
		return Result{}, fmt.Errorf("subject is required")
	}
	if caller == nil || publish == nil {
		return Result{}, fmt.Errorf("easm source is not wired")
	}
	if settings.MaxDomains < 1 || settings.MaxComponents < 1 {
		return Result{}, fmt.Errorf("max domains and components must be positive")
	}
	detail := &Detail{Description: input.Description, Logo: safeLogo(input.Logo), Website: safeWebsite(input.Website)}
	if detail.Description == "" && detail.Logo == "" && detail.Website == "" {
		detail = nil
	}
	r := &run{input: input, settings: settings, caller: caller, publish: publish, graph: NewGraph(input.Subject, detail)}
	r.state = State{SchemaVersion: 1, Title: input.Subject, Query: input.Query, Collection: Collection{Status: statusRunning}, Heat: []HeatPoint{}, Components: []Component{}}
	for _, step := range Steps {
		r.state.Collection.Steps = append(r.state.Collection.Steps, StepStatus{ID: step, Status: statusPending})
	}
	if err := r.flush(ctx); err != nil {
		return r.result(), err
	}
	for i, step := range Steps {
		if err := ctx.Err(); err != nil {
			r.interrupt(ctx, i)
			return r.result(), err
		}
		status := &r.state.Collection.Steps[i]
		def, ok := stepFuncs[step]
		switch {
		case metricSteps[step] && input.Query == "":
			status.Status, status.Code = statusSkipped, "no_query"
		case !ok || !settings.configured(def.roles):
			status.Status, status.Code = statusUnavailable, "not_configured"
		default:
			status.Status = statusRunning
			if err := r.flush(ctx); err != nil {
				return r.result(), err
			}
			err := def.fn(r, ctx)
			if ctx.Err() != nil {
				r.interrupt(ctx, i)
				return r.result(), ctx.Err()
			}
			status.Status, status.Code = outcome(err)
		}
		if err := r.flush(ctx); err != nil {
			return r.result(), err
		}
	}
	r.state.Collection.Status = statusDone
	return r.result(), r.flush(ctx)
}

func outcome(err error) (string, string) {
	var toolErr *ToolError
	switch {
	case err == nil:
		return statusDone, ""
	case errors.Is(err, ErrUnavailable):
		return statusUnavailable, "unavailable"
	case errors.As(err, &toolErr):
		return statusFailed, toolErr.Code
	default:
		return statusFailed, "malformed_reply"
	}
}

// interrupt marks the running and pending steps so a reloaded panel does not
// show a spinner for work that no longer exists. The record is written on a
// short detached context because the run's own context has already ended.
func (r *run) interrupt(ctx context.Context, from int) {
	defer func() {
		record, cancel := context.WithTimeout(context.WithoutCancel(ctx), interruptRecordTimeout)
		defer cancel()
		_ = r.flush(record)
	}()
	for i := from; i < len(r.state.Collection.Steps); i++ {
		if status := &r.state.Collection.Steps[i]; status.Status == statusPending || status.Status == statusRunning {
			status.Status = statusInterrupted
		}
	}
	r.state.Collection.Status = statusInterrupted
}

func (r *run) result() Result {
	nodes, edges := r.graph.Len()
	return Result{Nodes: nodes, Edges: edges, Components: len(r.state.Components), Steps: append([]StepStatus(nil), r.state.Collection.Steps...)}
}

// flush publishes the complete state; the host computes the patch.
func (r *run) flush(ctx context.Context) error {
	r.state.Nodes, r.state.Edges = r.graph.Snapshot()
	body, err := json.Marshal(r.state)
	if err != nil {
		return err
	}
	return r.publish(ctx, body)
}

func (s Settings) configured(roles []string) bool {
	for _, role := range roles {
		if s.Tools[role] == "" {
			return false
		}
	}
	return len(roles) > 0
}

// call runs the tool configured for role and decodes its reply into out.
func (r *run) call(ctx context.Context, role string, arguments map[string]any, out any) error {
	tool := r.settings.Tools[role]
	if tool == "" {
		return ErrUnavailable
	}
	if r.settings.CallTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, r.settings.CallTimeout)
		defer cancel()
	}
	body, err := r.caller.Call(ctx, tool, arguments)
	if err != nil {
		return err
	}
	return decodeReply(body, out)
}

// decodeReply rejects the error envelopes FOFA-style servers return with a
// successful MCP status, then decodes the body with exact numbers.
func decodeReply(body json.RawMessage, out any) error {
	var envelope struct {
		Error   any    `json:"error"`
		IsError bool   `json:"isError"`
		Errmsg  string `json:"errmsg"`
	}
	if len(body) == 0 || json.Unmarshal(body, &envelope) != nil {
		return fmt.Errorf("reply is not a JSON object")
	}
	if envelope.IsError || envelope.Errmsg != "" || envelope.Error != nil && envelope.Error != false {
		return &ToolError{Code: "tool_error"}
	}
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.UseNumber()
	return decoder.Decode(out)
}

func safeLogo(value string) string {
	if strings.HasPrefix(value, "https://") || strings.HasPrefix(value, "data:image/") {
		return value
	}
	return ""
}

func safeWebsite(value string) string {
	if strings.HasPrefix(value, "https://") || strings.HasPrefix(value, "http://") {
		return value
	}
	return ""
}
