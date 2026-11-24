package engine

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/LubyRuffy/eino-swarm/internal/knowledge"
)

const wikiExtractParallelism = 6
const wikiDocumentBatch = 64

type wikiDraftResult struct {
	proposal *wikiProposal
	code     string
	err      error
}

func (e *Engine) runWikiBuild(ctx context.Context, gate chan struct{}, job *projectDistillWork, cursor uint) {
	for job.result.Progress.Remaining > 0 {
		select {
		case gate <- struct{}{}:
		case <-ctx.Done():
			job.result.State = "interrupted"
			return
		}
		next, err := func() (uint, error) {
			defer func() { <-gate }()
			return e.runWikiWindow(ctx, job, cursor)
		}()
		cursor = next
		if err != nil {
			stopDistillation(ctx, &job.result, err)
			return
		}
	}
	e.renderWikiBuild(ctx, gate, job, cursor)
}

// Plan independent model inputs with a temporary cursor. Publication and
// durable progress still follow source order. No window can commit past an
// unaccepted batch, and cancellation joins its in-flight reviewers.
func (e *Engine) planWikiWindow(ctx context.Context, job *projectDistillWork, cursor uint) ([]*projectDistillWork, error) {
	plan := *job
	progress := *plan.result.Progress
	plan.result.Progress = &progress
	var batches []*projectDistillWork
	for len(batches) < wikiExtractParallelism && progress.Remaining > 0 {
		batch, err := e.loadDistillationSlice(ctx, &plan, cursor)
		if err != nil {
			return nil, err
		}
		batches = append(batches, batch)
		next := batch.checkpoint
		cursor = next.to
		progress.Remaining -= next.count
		progress.SegmentBytes, progress.SegmentTotalBytes = next.segmentBytes, next.segmentTotalBytes
	}
	return batches, nil
}

func (e *Engine) runWikiWindow(ctx context.Context, job *projectDistillWork, cursor uint) (uint, error) {
	if err := e.validateDistillationDestinations(job.projectID, job.kb, job.result); err != nil {
		return cursor, err
	}
	batches, err := e.planWikiWindow(ctx, job, cursor)
	if err != nil {
		return cursor, err
	}
	windowCtx, cancel := context.WithCancel(ctx)
	var workers sync.WaitGroup
	defer func() {
		cancel()
		workers.Wait()
	}()
	results := make([]chan wikiDraftResult, len(batches))
	for i, batch := range batches {
		results[i] = make(chan wikiDraftResult, 1)
		if len(batch.sources.sources) == 0 {
			results[i] <- wikiDraftResult{}
			continue
		}
		result := results[i]
		workers.Go(func() {
			proposal, code, err := e.extractWikiDraft(windowCtx, batch)
			if err != nil && windowCtx.Err() == nil && (code == "evidence" || code == "temporary_model") {
				proposal, code, err = e.extractWikiDraft(windowCtx, batch)
			}
			result <- wikiDraftResult{proposal, code, err}
		})
	}
	for i, batch := range batches {
		select {
		case <-windowCtx.Done():
			return cursor, context.Cause(ctx)
		case draft := <-results[i]:
			if draft.err != nil {
				job.result.ErrorCode, job.result.ErrorProviderID = draft.code, job.route.provider
				return cursor, draft.err
			}
			batch.checkpoint.current = &job.result
			if err := e.publishWikiDraft(ctx, batch, draft.proposal); err != nil {
				return cursor, err
			}
			cursor = batch.checkpoint.to
		}
	}
	return cursor, nil
}

func (e *Engine) publishWikiDraft(ctx context.Context, batch *projectDistillWork, proposal *wikiProposal) error {
	if err := context.Cause(ctx); err != nil {
		return err
	}
	done, err := e.beginLifecycleAdmission()
	if err != nil {
		return err
	}
	defer done()
	e.workspaceMu.Lock()
	defer e.workspaceMu.Unlock()
	e.recordMu.Lock()
	defer e.recordMu.Unlock()
	if !e.cfg.SwarmSnapshot().Knowledge {
		return knowledge.ErrDisabled
	}
	if proposal == nil || len(*proposal.Nodes) == 0 {
		return batch.checkpoint.accept(batch.result, nil, proposal != nil)
	}
	if err := e.checkDistillationAnchor(ctx, batch); err != nil {
		return err
	}
	handles := wikiSourceHandles(*proposal)
	refs, err := e.verifyDistillationSources(ctx, batch, handles)
	if err != nil {
		return err
	}
	ordered := make([]knowledgeSourceRef, 0, len(handles))
	for _, handle := range handles {
		ordered = append(ordered, refs[handle])
	}
	resolved, err := e.resolveKnowledgeSources(ctx, batch.projectID, ordered)
	if err != nil {
		return err
	}
	sources := make(map[int64]knowledge.Source, len(handles))
	for i, handle := range handles {
		sources[handle] = resolved[i]
	}
	pages := wikiPages(*proposal, sources)
	if err := batch.kb.EnsureWikiSchema(); err != nil {
		return err
	}
	if err := batch.checkpoint.beforeWrite(); err != nil {
		return err
	}
	stamp := knowledge.Stamp{UpdatedAt: time.Now(), UpdatedTurn: batch.anchor.ID, UpdatedThread: batch.anchor.ThreadID}
	if err := batch.kb.StageWikiNodes(ctx, batch.result.ReviewID, pages, stamp); err != nil {
		return err
	}
	return batch.checkpoint.accept(batch.result, nil, true)
}

func (e *Engine) renderWikiBuild(ctx context.Context, gate chan struct{}, job *projectDistillWork, cursor uint) {
	checkpoint := &distillCheckpoint{e: e, current: &job.result, from: cursor, projectID: job.projectID}
	for {
		select {
		case gate <- struct{}{}:
		case <-ctx.Done():
			job.result.State = "interrupted"
			return
		}
		err := func() error {
			defer func() { <-gate }()
			return e.renderWikiDocumentBatch(ctx, job, checkpoint, cursor)
		}()
		if err != nil {
			stopDistillation(ctx, &job.result, err)
			return
		}
		if job.result.State == "completed" {
			return
		}
	}
}

func (e *Engine) renderWikiDocumentBatch(ctx context.Context, job *projectDistillWork, checkpoint *distillCheckpoint, cursor uint) error {
	done, err := e.beginLifecycleAdmission()
	if err != nil {
		return err
	}
	defer done()
	e.workspaceMu.Lock()
	defer e.workspaceMu.Unlock()
	if !e.cfg.SwarmSnapshot().Knowledge {
		return knowledge.ErrDisabled
	}
	if _, err := e.store.GetProject(job.projectID); err != nil {
		return err
	}
	snap, err := job.kb.Snapshot()
	if err != nil {
		return err
	}
	if !snap.Enabled() {
		return knowledge.ErrDisabled
	}
	result := job.result
	total, written := snap.WikiDocumentProgress(result.ReviewID)
	if result.WikiPhase == "graph" {
		// Make the visible boundary durable before any document is enriched.
		result.WikiPhase, result.WikiDocuments, result.WikiDocumented = "documents", total, written
	} else if written < total {
		if err := job.kb.WriteWikiDocuments(ctx, result.ReviewID, wikiDocumentBatch); err != nil {
			return err
		}
		snap, err = job.kb.Snapshot()
		if err != nil {
			return err
		}
		total, written = snap.WikiDocumentProgress(result.ReviewID)
	}
	if total != result.WikiDocuments {
		return fmt.Errorf("wiki graph changed during document publication: %d of %d pages remain associated with this run", total, result.WikiDocuments)
	}
	result.WikiDocumented = written
	if written == total {
		result.State = "completed"
	}
	if err := checkpoint.save(result, cursor); err != nil {
		return err
	}
	job.result = result
	return nil
}
