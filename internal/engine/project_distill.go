package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/LubyRuffy/eino-swarm/internal/knowledge"
	"github.com/LubyRuffy/eino-swarm/internal/memory"
	"github.com/LubyRuffy/eino-swarm/internal/provider"
	"github.com/LubyRuffy/eino-swarm/internal/store"
	"github.com/cloudwego/eino/components/tool"
)

// DistillationRequest selects the saved history for one manual project review.
type DistillationRequest struct {
	Scope           string `json:"scope"`
	ThreadID        string `json:"thread_id"`
	EnableKnowledge bool   `json:"enable_knowledge"`
}

// ErrDistillationScope reports an invalid combination of scope and conversation.
var ErrDistillationScope = errors.New("invalid distillation scope or conversation")

// ErrDistillationTargets reports that none of the requested project's stores is enabled.
var ErrDistillationTargets = errors.New("enable Memory or Knowledge before distilling")

// DistillationResult is the durable status shared by both project panels.
type DistillationResult struct {
	ReviewID         string          `json:"review_id,omitempty"`
	State            string          `json:"state"`
	Scope            string          `json:"scope,omitempty"`
	ThreadID         string          `json:"thread_id,omitempty"`
	TurnID           string          `json:"turn_id,omitempty"`
	HistoryOmitted   bool            `json:"history_omitted,omitempty"`
	MemoryEnabled    bool            `json:"memory_enabled,omitempty"`
	KnowledgeEnabled bool            `json:"knowledge_enabled,omitempty"`
	Changes          []memory.Change `json:"changes,omitempty"`
	Knowledge        []distillChange `json:"knowledge,omitempty"`
	Error            string          `json:"error,omitempty"`
	Uncertain        bool            `json:"uncertain,omitempty"`
}

type distillChange struct {
	Action   string `json:"action"`
	ID       string `json:"id,omitempty"`
	Title    string `json:"title,omitempty"`
	Revision string `json:"revision"`
}

type projectDistillWork struct {
	projectID string
	result    DistillationResult
	anchor    store.Turn
	sources   distillInput
	kb        *knowledge.Store
}

// ProjectDistillationStatus reads the last explicit review without starting work.
func (e *Engine) ProjectDistillationStatus(ctx context.Context, projectID string) (DistillationResult, error) {
	e.distillMu.Lock()
	defer e.distillMu.Unlock()
	if _, err := e.store.GetProject(projectID); err != nil {
		return DistillationResult{}, err
	}
	return e.projectDistillationStatus(ctx, projectID)
}

func (e *Engine) projectDistillationStatus(ctx context.Context, projectID string) (DistillationResult, error) {
	row, err := e.store.ProjectDistillationReceipt(ctx, projectID)
	if err != nil {
		return DistillationResult{}, err
	}
	if row == nil {
		return DistillationResult{State: "idle"}, nil
	}
	var result DistillationResult
	if err := json.Unmarshal(row.Receipt, &result); err != nil {
		return DistillationResult{}, err
	}
	if result.State == "running" && e.distilling[projectID] != row.ID {
		result.State, result.Uncertain = "interrupted", true
	}
	if result.ThreadID != "" {
		thread, err := e.store.GetThreadContext(ctx, result.ThreadID)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return DistillationResult{}, err
		}
		turn, err := e.store.GetTurnContext(ctx, result.TurnID)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return DistillationResult{}, err
		}
		if thread == nil || thread.ProjectID != projectID || turn == nil || turn.ThreadID != thread.ID {
			result.ThreadID, result.TurnID = "", ""
		}
	}
	return result, nil
}

// StartProjectDistillation admits at most one manual review per project.
func (e *Engine) StartProjectDistillation(ctx context.Context, projectID string, req DistillationRequest) (DistillationResult, error) {
	switch req.Scope {
	case "latest_turn", "thread":
		if req.ThreadID == "" {
			return DistillationResult{}, ErrDistillationScope
		}
	case "project":
		if req.ThreadID != "" {
			return DistillationResult{}, ErrDistillationScope
		}
	default:
		return DistillationResult{}, ErrDistillationScope
	}
	done, err := e.beginAdmission()
	if err != nil {
		return DistillationResult{}, err
	}
	defer done()
	e.distillMu.Lock()
	defer e.distillMu.Unlock()
	e.workspaceMu.Lock()
	defer e.workspaceMu.Unlock()

	p, err := e.store.GetProject(projectID)
	if err != nil {
		return DistillationResult{}, err
	}
	if req.ThreadID != "" {
		thread, err := e.store.GetThreadContext(ctx, req.ThreadID)
		if err != nil {
			return DistillationResult{}, err
		}
		if thread.ProjectID != projectID {
			return DistillationResult{}, store.ErrNotFound
		}
	}
	if e.distilling[projectID] != "" {
		return e.projectDistillationStatus(ctx, projectID)
	}
	turns, omitted, err := e.store.ProjectDistillationHistory(ctx, projectID, req.ThreadID, req.Scope == "latest_turn", distillEventKinds)
	if err != nil {
		return DistillationResult{}, err
	}
	input := selectDistillationSources(turns, omitted)
	if len(input.sources) == 0 {
		return DistillationResult{State: "no_work", Scope: req.Scope, HistoryOmitted: input.omitted}, nil
	}
	kb, err := e.ProjectKnowledge(projectID)
	if err != nil {
		return DistillationResult{}, err
	}
	snap, err := kb.Snapshot()
	if err != nil {
		return DistillationResult{}, err
	}
	if req.EnableKnowledge && !snap.Enabled() {
		snap, err = kb.Enable()
		if err != nil {
			return DistillationResult{}, err
		}
	}
	anchor := turns[0].Turn
	result := DistillationResult{
		ReviewID: store.NewID("distill_"), State: "running", Scope: req.Scope,
		ThreadID: anchor.ThreadID, TurnID: anchor.ID, HistoryOmitted: input.omitted,
		MemoryEnabled: e.MemoryEnabled(p), KnowledgeEnabled: snap.Enabled(),
	}
	if !result.MemoryEnabled && !result.KnowledgeEnabled {
		return DistillationResult{}, ErrDistillationTargets
	}
	gate := e.reviews.begin(projectID)
	if gate == nil {
		return DistillationResult{}, ErrRestarting
	}
	row, err := result.row(projectID)
	if err == nil {
		err = e.store.StartProjectDistillation(ctx, row)
	}
	if err != nil {
		e.reviews.done()
		return DistillationResult{}, err
	}
	if e.distilling == nil {
		e.distilling = make(map[string]string)
	}
	e.distilling[projectID] = result.ReviewID
	go e.runProjectDistillation(gate, projectDistillWork{projectID, result, anchor, input, kb})
	return result, nil
}

func (r DistillationResult) row(projectID string) (store.ProjectDistillation, error) {
	body, err := json.Marshal(r)
	return store.ProjectDistillation{ProjectID: projectID, ID: r.ReviewID, State: r.State, Receipt: body}, err
}

func (e *Engine) runProjectDistillation(gate chan struct{}, work projectDistillWork) {
	defer e.reviews.done()
	defer func() {
		if recovered := recover(); recovered != nil {
			e.log.Error("project distillation panicked", "review", work.result.ReviewID, "panic", recovered)
			work.result.State, work.result.Uncertain = "interrupted", true
		}
		e.distillMu.Lock()
		defer e.distillMu.Unlock()
		delete(e.distilling, work.projectID)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		row, err := work.result.row(work.projectID)
		if err == nil {
			err = e.store.FinishProjectDistillation(ctx, row)
		}
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			e.log.Error("could not retain project distillation receipt", "review", work.result.ReviewID, "err", err)
		}
	}()
	ctx := e.reviews.dreamCtx
	select {
	case gate <- struct{}{}:
		defer func() { <-gate }()
	case <-ctx.Done():
		work.result.State = "interrupted"
		return
	}
	e.reviewProjectDistillation(ctx, &work)
}

func (e *Engine) reviewProjectDistillation(ctx context.Context, work *projectDistillWork) {
	if err := e.checkDistillationAnchor(ctx, work); err != nil {
		work.result.State, work.result.Error = "failed", publicTurnError(err)
		return
	}
	var mu sync.Mutex
	var outcome reviewOutcome
	batch := &projectDistillBatch{e: e, work: work, mu: &mu, outcome: &outcome}
	pc := &projectContext{}
	var notes *memory.Snapshot
	var tools []tool.BaseTool
	if work.result.MemoryEnabled {
		pc.memory = e.ProjectMemory(work.projectID)
		snapshot, err := pc.memory.Read()
		if err != nil {
			work.result.State, work.result.Error = "failed", publicTurnError(err)
			return
		}
		notes = &snapshot
		batch.memory = &reviewBatchTool{store: pc.memory, notesRev: notes.Rev, sources: work.sources.memory, mu: &mu, outcome: &outcome}
		tools = memory.ViewTools(pc.memory)
	}
	instruction := projectDistillPrompt
	if work.result.MemoryEnabled {
		instruction += "\nMemory is enabled. Use \"changes\" for both notes and Skills."
		if work.result.KnowledgeEnabled {
			instruction += "\nKeep subject-matter knowledge in the wiki."
		}
	}
	if work.result.KnowledgeEnabled {
		instruction += "\nKnowledge is enabled. Use \"knowledge\" for wiki mutations. Search existing pages and inspect schema first. Read page or schema revisions before editing. Wiki history references come only from proposal evidence."
		tools = append(tools, batch.knowledgeReader())
	}
	settings := e.cfg.Snapshot().Memory
	effort, optional := settings.ReviewEffort(work.anchor.ReasoningEffort)
	e.driveReviewer(reviewRun{
		ctx: ctx, threadID: work.anchor.ThreadID, turnID: work.anchor.ID, recorder: e.distillationRecorder(work),
		providerID: work.anchor.ProviderID, model: work.anchor.Model, effort: effort, optionalEffort: optional,
		description: "distills saved project history", instruction: instruction,
		userMsg: e.reviewUserMessageWithNotes(pc, work.sources.transcript, notes),
		maxIter: settings.ReviewIterations(), tools: append(tools, batch), sequential: true,
		completed: func() (string, bool) {
			mu.Lock()
			defer mu.Unlock()
			return outcome.Note, batch.complete
		},
	}, &mu, &outcome)
	mu.Lock()
	defer mu.Unlock()
	work.result.Changes = outcome.Changes
	if outcome.Err != "" && work.result.Error == "" {
		work.result.Error = outcome.Err
	}
	switch {
	case !batch.complete && ctx.Err() != nil:
		work.result.State = "interrupted"
	case !batch.complete && work.result.Error == "":
		work.result.State, work.result.Error = "failed", "No complete distillation proposal was accepted."
		if batch.lastRefusal != "" {
			work.result.Error = batch.lastRefusal
		}
	case work.result.Error != "" && (len(work.result.Changes) > 0 || len(work.result.Knowledge) > 0):
		work.result.State = "partial"
	case work.result.Error != "":
		work.result.State = "failed"
	default:
		work.result.State = "completed"
	}
}

func (e *Engine) checkDistillationAnchor(ctx context.Context, work *projectDistillWork) error {
	thread, err := e.store.GetThreadContext(ctx, work.anchor.ThreadID)
	if err != nil {
		return err
	}
	turn, err := e.store.GetTurnContext(ctx, work.anchor.ID)
	if err != nil {
		return err
	}
	if thread.ProjectID != work.projectID || turn.ThreadID != thread.ID || turn.Status != work.anchor.Status {
		return fmt.Errorf("%w: the review's history changed", store.ErrHistorySourceUnavailable)
	}
	return nil
}

// A late provider completion cannot attribute a project review to a conversation
// after that conversation has moved. Rewind and deletion also have SQL guards.
func (e *Engine) distillationRecorder(work *projectDistillWork) provider.Recorder {
	record := e.callRecorder(work.anchor.ThreadID, work.anchor.ID)
	return func(call provider.CallRecord) {
		e.workspaceMu.Lock()
		defer e.workspaceMu.Unlock()
		if err := e.checkDistillationAnchor(context.Background(), work); err == nil {
			record(call)
		}
	}
}
