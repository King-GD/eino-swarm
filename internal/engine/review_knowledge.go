package engine

import (
	"context"
	"errors"
	"maps"
	"slices"

	swarm "github.com/LubyRuffy/eino-swarm"
	"github.com/LubyRuffy/eino-swarm/internal/knowledge"
	"github.com/LubyRuffy/eino-swarm/internal/store"
)

const automaticKnowledgeReviewPrompt = `This project's Knowledge base is enabled. In the same final apply_memory_review call, use the knowledge array for durable findings about the project's subject. Consider Knowledge before finishing with empty changes. Use read_knowledge to find an existing page or inspect its revision and schema. Preserve known page IDs and prior citations. Cite exact excerpts from the supplied original human messages or successful paired tool results. Assistant statements can support only tentative analysis. Briefings, synthesized summaries, and knowledge or memory tool receipts are not evidence. Use an empty knowledge array for no durable findings, or when deferring a Dream decision. If the audit request restricts tools and you need to inspect related Knowledge, defer with both arrays empty and use the ordinary review tools on the next request.`

func (e *Engine) automaticReviewKnowledge(turn *store.Turn, pc *projectContext, events []store.Event, visible map[int64]store.Event) *projectDistillBatch {
	if turn.Status != store.TurnDone || turn.NuwaReadOnly || !pc.hasKnowledge() || pc.planning || pc.project == nil || turn.ProjectID != pc.project.ID {
		return nil
	}
	original := distillOriginalEvents(events)
	evidence := reviewEvidenceSources(original, visible)
	sources := make(map[int64]distillSource, len(evidence))
	for _, ev := range reviewHumanEvents(original) {
		if _, ok := evidence[ev.Seq]; ok {
			sources[ev.Seq] = distillSource{event: ev}
		}
	}
	for _, ev := range original {
		selected, shown := visible[ev.Seq]
		if !shown {
			continue
		}
		if ev.Kind == swarm.NotifyAgentMessage.String() {
			evidence[ev.Seq] = selected
		}
		if _, ok := evidence[ev.Seq]; ok && ev.Kind != KindSteer {
			sources[ev.Seq] = distillSource{event: ev}
		}
	}
	return &projectDistillBatch{e: e, work: &projectDistillWork{
		projectID: pc.project.ID, anchor: *turn, kb: pc.knowledge,
		sources: distillInput{sources: sources, wiki: evidence},
	}}
}

// Publication shares the move/rewind exclusion used by manual distillation.
// Hold it from source verification through both destinations' writes.
func (b *projectDistillBatch) beginAutomaticKnowledgeWrite(ctx context.Context, ops []distillKnowledgeOperation) (map[int64]knowledgeSourceRef, func(), error) {
	handles := make(map[int64]bool)
	if err := collectDistillKnowledgeHandles(ops, b.work.sources.wiki, handles); err != nil {
		return nil, nil, err
	}
	e := b.e
	done, err := e.beginLifecycleAdmission()
	if err != nil {
		return nil, nil, err
	}
	e.workspaceMu.Lock()
	e.recordMu.Lock()
	unlock := func() {
		e.recordMu.Unlock()
		e.workspaceMu.Unlock()
		done()
	}
	sources, err := b.checkAutomaticKnowledgeWrite(ctx, slices.Sorted(maps.Keys(handles)))
	if err != nil {
		unlock()
		return nil, nil, err
	}
	return sources, unlock, nil
}

func (b *projectDistillBatch) checkAutomaticKnowledgeWrite(ctx context.Context, handles []int64) (map[int64]knowledgeSourceRef, error) {
	e := b.e
	if err := e.checkDistillationAnchor(ctx, b.work); err != nil {
		return nil, err
	}
	thread, err := e.store.GetThreadContext(ctx, b.work.anchor.ThreadID)
	if err != nil {
		return nil, err
	}
	project, err := e.store.GetProject(b.work.projectID)
	if err != nil {
		return nil, err
	}
	if thread.PlanMode || !e.MemoryEnabled(project) || !e.cfg.Snapshot().Memory.AutoReview {
		return nil, errors.New("automatic knowledge review is no longer writable")
	}
	snapshot, err := b.work.kb.Snapshot()
	if err != nil {
		return nil, err
	}
	if !snapshot.Enabled() {
		return nil, knowledge.ErrDisabled
	}
	return e.verifyDistillationSources(ctx, b.work, handles)
}
