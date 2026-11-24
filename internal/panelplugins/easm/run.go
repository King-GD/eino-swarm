package easm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// stepFunc performs one aggregation against the run's query.
type stepFunc func(*run, context.Context) error

// stepDef binds a step to the tool roles it needs. A step whose roles are
// not all configured is reported unavailable without being attempted.
type stepDef struct {
	roles []string
	fn    stepFunc
}

// stepFuncs is filled by the per-step files.
var stepFuncs = map[Step]stepDef{}

const interruptRecordTimeout = 2 * time.Second

type run struct {
	input    Input
	settings Settings
	caller   Caller
	publish  Publish

	// mu orders the state writes and publications of the concurrent steps, so
	// stored order and streamed order cannot diverge.
	mu    sync.Mutex
	graph *Graph
	state State
	// previousQuery is the query the metrics in the panel were collected for.
	previousQuery     string
	unresolvedParents int
	refusedDomains    int
}

// Run merges this call's batch into the portrait in current and publishes the
// result. With a query it also collects the asset aggregations, publishing each
// as it finishes; a failed or unavailable aggregation marks only its own step.
// The run fails on cancellation or when publication itself fails.
func Run(ctx context.Context, input Input, settings Settings, current json.RawMessage, caller Caller, publish Publish) (Result, error) {
	input.Subject = strings.TrimSpace(input.Subject)
	input.Query = strings.TrimSpace(input.Query)
	if input.Subject == "" {
		return Result{}, fmt.Errorf("subject is required")
	}
	if caller == nil || publish == nil {
		return Result{}, fmt.Errorf("easm source is not wired")
	}
	if settings.MaxDomains < 1 || settings.MaxComponents < 1 || settings.MaxClouds < 1 || settings.MaxServices < 1 {
		return Result{}, fmt.Errorf("max domains, components, clouds and services must be positive")
	}
	r := &run{input: input, settings: settings, caller: caller, publish: publish}
	if err := r.begin(current); err != nil {
		return r.result(), err
	}
	r.mergeCompanies()
	r.mergeDomains()
	r.mergeCertificates()
	// The merge is published on its own so the graph appears while the slower
	// aggregations still run.
	collect := r.planMetrics()
	if err := r.flush(ctx); err != nil {
		return r.result(), err
	}
	if !collect {
		result := r.result()
		result.Next = r.next()
		return result, nil
	}
	if err := r.collect(ctx); err != nil {
		return r.result(), err
	}
	result := r.result()
	result.Next = r.next()
	return result, nil
}

// finishedNext is the receipt's instruction once the panel holds every
// aggregation for the query. Without it the agent keeps researching after the
// portrait is complete, and the conversation outlives the panel by minutes.
const finishedNext = "The panel now holds the totals, trend, components and hosting providers for this query. Reply with a short summary of the panel and end the turn without calling more tools."

// next words the receipt from what the steps actually produced. A missing
// aggregation is named instead of being implied by a completion claim, so the
// agent cannot report numbers it never got; the turn still ends rather than
// looping on the aggregations that just failed.
func (r *run) next() string {
	if r.input.Query == "" {
		return ""
	}
	var missing []string
	for _, step := range r.state.Collection.Steps {
		if step.Status == statusDone {
			continue
		}
		detail := step.Status
		if step.ID == StepSummary {
			switch step.Code {
			case missingAssetTotal:
				detail = "latest response missing the asset total"
			case missingIPTotal:
				detail = "latest response missing the distinct IP total"
			}
		}
		missing = append(missing, fmt.Sprintf("%s (%s)", step.ID, detail))
	}
	if len(missing) == 0 {
		return finishedNext
	}
	return "The panel could not finish " + strings.Join(missing, ", ") + " for this query. Reply with a short summary of what the panel holds and end the turn without calling more tools."
}

// begin decides whether this call continues the portrait already in the panel.
// A different subject starts a new portrait: the old graph and its metrics
// describe another subject and must not be mixed with the new one.
func (r *run) begin(current json.RawMessage) error {
	registry := selectedRegistry(r.input)
	detail := rootDetail(r.input)
	if registry != "" {
		detail = mergeDetail(detail, &Detail{RegistryID: registry, RegistryName: r.input.Subject})
	}
	r.graph = NewGraph(r.input.Subject, detail)
	r.state = State{SchemaVersion: 1, Title: r.input.Subject, Collection: Collection{Status: statusIdle}, Heat: []HeatPoint{}, Components: []Component{}, Clouds: []Cloud{}}
	for _, step := range Steps {
		r.state.Collection.Steps = append(r.state.Collection.Steps, StepStatus{ID: step, Status: statusPending})
	}
	previous, continuing, err := continuation(current, r.input.Subject, registry)
	if err != nil || !continuing {
		return err
	}
	graph, err := Restore(previous)
	if err != nil {
		return err
	}
	r.graph = graph
	r.graph.Root().Label = r.input.Subject
	r.graph.Upsert(Node{ID: RootID, Label: r.input.Subject, Detail: detail})
	r.previousQuery = strings.TrimSpace(previous.Query)
	r.state.Query = r.previousQuery
	r.state.Summary = previous.Summary
	r.state.Heat = orEmptyHeat(previous.Heat)
	r.state.Components = orEmptyComponents(previous.Components)
	r.state.Clouds = orEmptyClouds(previous.Clouds)
	// Steps are rebuilt from the current pipeline, so a panel stored before a
	// step retired keeps only the statuses this producer still reports.
	for i, step := range Steps {
		for _, stored := range previous.Collection.Steps {
			if stored.ID == step {
				r.state.Collection.Steps[i] = StepStatus{ID: step, Status: stored.Status, Code: stored.Code}
			}
		}
	}
	return nil
}

// planMetrics decides whether this call collects the aggregations and sets the
// collection status its merge publication shows.
func (r *run) planMetrics() bool {
	hasQuery := r.input.Query != ""
	changed := hasQuery && r.input.Query != r.previousQuery
	// Numbers collected for another query describe another asset range, so they
	// leave the panel before the new range is aggregated.
	if changed {
		r.state.Query = r.input.Query
		r.state.Summary = nil
		r.state.Heat = []HeatPoint{}
		r.state.Components = []Component{}
		r.state.Clouds = []Cloud{}
	}
	summary := r.state.Summary
	if summary != nil {
		clues := r.clueCount()
		summary.Clues = &clues
	}
	collect := false
	for i := range r.state.Collection.Steps {
		step := &r.state.Collection.Steps[i]
		// Older panels could mark a summary done with one total missing.
		if !changed && step.ID == StepSummary && step.Status == statusDone {
			if code := missingTotalCode(summary != nil && summary.FOFAAssets != nil, summary != nil && summary.FOFAIPs != nil); code != "" {
				*step = StepStatus{ID: StepSummary, Status: statusFailed, Code: code}
			}
		}
		if hasQuery && (changed || step.Status != statusDone) {
			*step = StepStatus{ID: step.ID, Status: statusRunning}
			collect = true
		}
	}
	switch {
	case collect:
		r.state.Collection.Status = statusRunning
	case r.allStepsDone():
		r.state.Collection.Status = statusDone
	default:
		r.state.Collection.Status = statusIdle
	}
	return collect
}

// collect runs the aggregations concurrently and publishes each completion.
func (r *run) collect(ctx context.Context) error {
	ready := make([]bool, len(Steps))
	r.mu.Lock()
	for i, step := range Steps {
		if r.state.Collection.Steps[i].Status != statusRunning {
			continue
		}
		def, known := stepFuncs[step]
		ready[i] = known && r.settings.configured(def.roles)
		if !ready[i] {
			r.state.Collection.Steps[i] = StepStatus{ID: step, Status: statusUnavailable, Code: "not_configured"}
		}
	}
	r.mu.Unlock()

	flushes := make([]error, len(Steps))
	var wg sync.WaitGroup
	for i, step := range Steps {
		if !ready[i] {
			continue
		}
		def := stepFuncs[step]
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := def.fn(r, ctx)
			r.mu.Lock()
			defer r.mu.Unlock()
			// A cancelled step keeps its running status: interrupt records every
			// unfinished step on a context that outlives this one.
			if ctx.Err() == nil {
				r.state.Collection.Steps[i] = StepStatus{ID: step, Status: outcomeStatus(err), Code: outcomeCode(err)}
				flushes[i] = r.flushLocked(ctx)
			}
		}()
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		r.interrupt(ctx)
		return err
	}
	for _, err := range flushes {
		if err != nil {
			return err
		}
	}
	r.mu.Lock()
	r.state.Collection.Status = statusDone
	r.mu.Unlock()
	return r.flush(ctx)
}

func outcomeStatus(err error) string {
	status, _ := outcome(err)
	return status
}

func outcomeCode(err error) string {
	_, code := outcome(err)
	return code
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

// allStepsDone reports whether every aggregation has a finished result for the
// query in the panel.
func (r *run) allStepsDone() bool {
	for _, status := range r.state.Collection.Steps {
		if status.Status != statusDone {
			return false
		}
	}
	return true
}

// interrupt marks the running and pending steps so a reloaded panel does not
// show a spinner for work that no longer exists. The record is written on a
// short detached context because the run's own context has already ended.
func (r *run) interrupt(ctx context.Context) {
	r.mu.Lock()
	for i := range r.state.Collection.Steps {
		if status := &r.state.Collection.Steps[i]; status.Status == statusPending || status.Status == statusRunning {
			status.Status = statusInterrupted
		}
	}
	r.state.Collection.Status = statusInterrupted
	r.mu.Unlock()
	record, cancel := context.WithTimeout(context.WithoutCancel(ctx), interruptRecordTimeout)
	defer cancel()
	_ = r.flush(record)
}

func (r *run) result() Result {
	result := Result{Components: len(r.state.Components), Steps: append([]StepStatus(nil), r.state.Collection.Steps...), UnresolvedParents: r.unresolvedParents, RefusedDomains: r.refusedDomains}
	if r.graph != nil {
		result.Nodes, result.Edges = r.graph.Len()
	}
	return result
}

// apply writes one step's result under the state lock: the aggregations run
// concurrently and each publishes as it finishes.
func (r *run) apply(write func(*State)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	write(&r.state)
}

// flush publishes the complete state; the host computes the patch.
func (r *run) flush(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.flushLocked(ctx)
}

func (r *run) flushLocked(ctx context.Context) error {
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
	return true
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

// continuation decodes the panel state this call starts from. Only a portrait of
// the same subject is continued; anything else starts a new one.
func continuation(current json.RawMessage, subject, registry string) (State, bool, error) {
	if len(current) == 0 || string(current) == "null" {
		return State{}, false, nil
	}
	var previous State
	if err := json.Unmarshal(current, &previous); err != nil {
		return State{}, false, fmt.Errorf("panel state cannot be read: %w", err)
	}
	for _, root := range previous.Nodes {
		if root.ID == RootID {
			return previous, sameSubject(root, subject, registry), nil
		}
	}
	// Let Restore report a missing root when the old state claims this subject.
	return previous, len(previous.Nodes) > 0 && previous.Title == subject, nil
}

func sameSubject(root Node, subject, registry string) bool {
	if saved := registryID(&root); saved != "" {
		if registry != "" {
			return saved == registry
		}
		if root.Detail.RegistryName == subject {
			return true
		}
	}
	return root.Label == subject
}

func registryID(node *Node) string {
	if node.Detail == nil {
		return ""
	}
	return node.Detail.RegistryID
}

// rootDetail carries the subject's own description. It is shown as reported and
// never used to widen a lookup.
func rootDetail(input Input) *Detail {
	detail := &Detail{Description: strings.TrimSpace(input.Description), Logo: safeLogo(input.Logo), Website: safeWebsite(input.Website)}
	if detail.Description == "" && detail.Logo == "" && detail.Website == "" {
		return nil
	}
	return detail
}

func orEmptyHeat(points []HeatPoint) []HeatPoint {
	if points == nil {
		return []HeatPoint{}
	}
	return points
}

func orEmptyComponents(components []Component) []Component {
	if components == nil {
		return []Component{}
	}
	return components
}

func orEmptyClouds(clouds []Cloud) []Cloud {
	if clouds == nil {
		return []Cloud{}
	}
	return clouds
}

func safeLogo(value string) string {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "https://") || strings.HasPrefix(value, "data:image/") {
		return value
	}
	return ""
}

func safeWebsite(value string) string {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "https://") || strings.HasPrefix(value, "http://") {
		return value
	}
	return ""
}
