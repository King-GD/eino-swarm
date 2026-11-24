package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/LubyRuffy/eino-swarm/internal/knowledge"
	"github.com/LubyRuffy/eino-swarm/internal/memory"
	"github.com/LubyRuffy/eino-swarm/internal/store"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

const distillBatchName = "apply_project_distillation"

const projectDistillPrompt = `Review the supplied Project history once. Keep only useful durable changes.
Notes hold explicit project working preferences and constraints. Skills hold reusable, observed procedures, including verified recovery conditions. Wiki pages hold knowledge about the project's subject.
The input labels each original source with a unique review seq and its real conversation and event_seq. In evidence use the review seq and an exact quote from visible source bytes. Cite only original human input or successful paired tools for notes and Skills. From a failed turn, use only explicit human requirements for notes. Wiki pages may cite original assistant observations, retaining uncertainty; the host attributes them as unverified analysis.
Keep each note short and preserve scope, conditions, negation and numeric spelling. For Skills, check an existing matching skill before changing it. Retain reusable, observed steps and separate normal and recovery conditions. Prior attempts, derived summaries and existing memory do not by themselves support new assertions.
Search existing wiki pages before creating one. Keep original citations and mark uncertain findings in the prose. Adopt existing types and stable page IDs where possible.
Prefer updates to existing related content. Supply both changes and knowledge arrays to apply_project_distillation. Each can be empty; submit both empty when nothing warrants an update. After a refusal without writes, repair within the available rounds. One accepted proposal ends the review.`

type distillKnowledgeOperation struct {
	knowledgeWriteInput
	Evidence []reviewCitation `json:"evidence"`
}

type distillProposal struct {
	Changes   *[]reviewOperation           `json:"changes"`
	Knowledge *[]distillKnowledgeOperation `json:"knowledge"`
}

type projectDistillBatch struct {
	e           *Engine
	work        *projectDistillWork
	mu          *sync.Mutex
	outcome     *reviewOutcome
	publishMu   sync.Mutex
	closed      bool
	memory      *reviewBatchTool
	complete    bool
	lastRefusal string
}

func (b *projectDistillBatch) Info(context.Context) (*schema.ToolInfo, error) {
	changes := reviewChangesParameter()
	if b.memory == nil {
		changes.Desc = "Memory is disabled. Supply an empty array."
	}
	knowledgeList := distillKnowledgeParameter(true)
	if !b.work.result.KnowledgeEnabled {
		knowledgeList.Desc = "Knowledge is disabled. Supply an empty array."
	}
	return &schema.ToolInfo{
		Name: distillBatchName, Desc: "Submit a complete, source-bound project distillation proposal for the enabled stores.",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{"changes": changes, "knowledge": knowledgeList}),
	}, nil
}

func distillKnowledgeParameter(required bool) *schema.ParameterInfo {
	proposal := knowledgeWriteParams()
	delete(proposal, "sources")
	proposal["action"].Enum = []string{"write_page", "append_schema"}
	proposal["evidence"] = reviewChangesParameter().ElemInfo.SubParams["evidence"]
	return &schema.ParameterInfo{
		Type: schema.Array, Required: required, Desc: "Source-bound wiki updates and schema additions.",
		ElemInfo: &schema.ParameterInfo{Type: schema.Object, SubParams: proposal},
	}
}

func (b *projectDistillBatch) InvokableRun(ctx context.Context, raw string, _ ...tool.Option) (string, error) {
	b.publishMu.Lock()
	defer b.publishMu.Unlock()
	if b.closed {
		return "", context.Canceled
	}
	b.mu.Lock()
	complete := b.complete
	b.mu.Unlock()
	if complete {
		return reviewBatchResult(true, "Project distillation already finished."), nil
	}
	if compose.GetToolCallID(ctx) == "" {
		return "", errors.New("project distillation requires a real tool call identity")
	}
	var proposal distillProposal
	if err := decodeNuwaArgs(raw, &proposal); err != nil {
		return b.refuse(err), nil
	}
	if proposal.Changes == nil || proposal.Knowledge == nil {
		return b.refuse(errors.New("provide both changes and knowledge arrays")), nil
	}
	handles, err := b.validateSources(proposal)
	if err != nil {
		return b.refuse(err), nil
	}
	response, committed, err := b.publish(ctx, proposal, handles)
	if err != nil && !committed {
		var terminal *reviewTerminalError
		if errors.As(err, &terminal) {
			return "", err
		}
		return b.refuse(err), nil
	}
	if !committed {
		return response, nil
	}
	b.mu.Lock()
	if err == nil && b.work.checkpoint != nil {
		err = b.work.checkpoint.accept(b.work.result, b.outcome.Changes, true)
	}
	b.complete = true
	b.outcome.StopReason = "completed"
	b.outcome.Note = "Project distillation finished."
	if err != nil {
		b.work.result.Error = publicTurnError(err)
	}
	b.mu.Unlock()
	if err := adk.SendToolGenAction(ctx, distillBatchName, adk.NewExitAction()); err != nil {
		return "", err
	}
	if err != nil {
		return reviewBatchResult(false, err.Error()), nil
	}
	return reviewBatchResult(true, "Project distillation finished."), nil
}

func (b *projectDistillBatch) refuse(err error) string {
	b.lastRefusal = err.Error()
	return reviewBatchResult(false, err.Error())
}

func (b *projectDistillBatch) validateSources(p distillProposal) ([]int64, error) {
	if b.memory == nil && len(*p.Changes) > 0 {
		return nil, errors.New("Memory is disabled")
	}
	if !b.work.result.KnowledgeEnabled && len(*p.Knowledge) > 0 {
		return nil, errors.New("Knowledge is disabled")
	}
	handles := make(map[int64]bool)
	for i, op := range *p.Changes {
		for _, cite := range op.Evidence {
			source, ok := b.work.sources.sources[cite.Seq]
			if !ok || (source.failed && op.Target != memory.ToolMemory) {
				return nil, fmt.Errorf("changes[%d]: source %d is outside this target's evidence", i, cite.Seq)
			}
			handles[cite.Seq] = true
		}
	}
	if err := collectDistillKnowledgeHandles(*p.Knowledge, b.work.sources.wiki, handles); err != nil {
		return nil, err
	}
	return slices.Sorted(maps.Keys(handles)), nil
}

func collectDistillKnowledgeHandles(ops []distillKnowledgeOperation, visible map[int64]store.Event, handles map[int64]bool) error {
	for i, op := range ops {
		if op.Sources != nil || (op.Action != "write_page" && op.Action != "append_schema") ||
			(op.Action == "append_schema" && (op.Add == nil || op.Revision == "")) {
			return fmt.Errorf("knowledge[%d]: use write_page or revision-checked append_schema and supply only evidence", i)
		}
		seqs, _, err := validateReviewEvidence(reviewOperation{Evidence: op.Evidence}, visible, false)
		if err != nil {
			return fmt.Errorf("knowledge[%d]: %w", i, err)
		}
		for _, seq := range seqs {
			handles[seq] = true
		}
	}
	return nil
}

func (b *projectDistillBatch) publish(ctx context.Context, proposal distillProposal, handles []int64) (response string, committed bool, err error) {
	done, err := b.e.beginLifecycleAdmission()
	if err != nil {
		return "", false, err
	}
	defer done()
	b.e.workspaceMu.Lock()
	defer b.e.workspaceMu.Unlock()
	b.e.recordMu.Lock()
	defer b.e.recordMu.Unlock()

	if err := b.e.checkDistillationAnchor(ctx, b.work); err != nil {
		return "", false, err
	}
	p, err := b.e.store.GetProject(b.work.projectID)
	if err != nil {
		return "", false, err
	}
	if len(*proposal.Changes) > 0 && !b.e.MemoryEnabled(p) {
		return "", false, errors.New("Memory was disabled during distillation")
	}
	if len(*proposal.Knowledge) > 0 {
		snap, err := b.work.kb.Snapshot()
		if err != nil {
			return "", false, err
		}
		if !snap.Enabled() {
			return "", false, knowledge.ErrDisabled
		}
	}
	sources, err := b.e.verifyDistillationSources(ctx, b.work, handles)
	if err != nil {
		return "", false, err
	}
	if err := context.Cause(ctx); err != nil {
		return "", false, err
	}
	if b.work.checkpoint != nil && len(*proposal.Changes)+len(*proposal.Knowledge) > 0 {
		if err := b.work.checkpoint.beforeWrite(); err != nil {
			return "", false, &reviewTerminalError{cause: err}
		}
	}
	if len(*proposal.Changes) > 0 {
		body, err := json.Marshal(map[string]any{"changes": proposal.Changes})
		if err != nil {
			return "", false, err
		}
		result, err := b.memory.InvokableRun(ctx, string(body))
		var reply struct {
			Success bool   `json:"success"`
			Error   string `json:"error"`
		}
		if err == nil {
			err = json.Unmarshal([]byte(result), &reply)
		}
		committed = b.outcome.Changed
		if err != nil {
			return "", committed, err
		}
		if !reply.Success {
			if !committed {
				b.lastRefusal = reply.Error
				return result, false, nil
			}
			return "", true, fmt.Errorf("Memory partially saved: %s; Knowledge operations skipped: %d", reply.Error, len(*proposal.Knowledge))
		}
	}
	changes, err := b.saveKnowledgeOperations(ctx, *proposal.Knowledge, sources)
	b.work.result.Knowledge = append(b.work.result.Knowledge, changes...)
	committed = committed || len(changes) > 0
	if err != nil && !committed && b.memory != nil {
		b.memory.complete = false
		b.outcome.StopReason = ""
	}
	return "", committed || err == nil, err
}

func (b *projectDistillBatch) saveKnowledgeOperations(ctx context.Context, ops []distillKnowledgeOperation, sources map[int64]knowledgeSourceRef) ([]distillChange, error) {
	stamp := knowledge.Stamp{UpdatedAt: time.Now().UTC(), UpdatedTurn: b.work.anchor.ID, UpdatedThread: b.work.anchor.ThreadID}
	var changes []distillChange
	for i, op := range ops {
		change, err := b.saveKnowledge(ctx, op, sources, stamp)
		if err != nil {
			return changes, fmt.Errorf("knowledge[%d] %s %s: %w; subsequent operations skipped: %d", i, op.Action, op.ID, err, len(ops)-i-1)
		}
		changes = append(changes, change)
	}
	return changes, nil
}

func (b *projectDistillBatch) saveKnowledge(ctx context.Context, op distillKnowledgeOperation, verified map[int64]knowledgeSourceRef, stamp knowledge.Stamp) (distillChange, error) {
	if err := context.Cause(ctx); err != nil {
		return distillChange{}, err
	}
	if op.Action == "append_schema" {
		snap, err := b.work.kb.AppendSchema(*op.Add, op.Revision)
		if err != nil {
			return distillChange{}, err
		}
		return distillChange{Action: op.Action, Revision: snap.Status(stamp.UpdatedAt).Revision}, nil
	}
	refs := make([]knowledgeSourceRef, 0, len(op.Evidence))
	seen := make(map[knowledgeSourceRef]bool)
	for _, citation := range op.Evidence {
		ref := verified[citation.Seq]
		if !seen[ref] {
			refs = append(refs, ref)
			seen[ref] = true
		}
	}
	page := pageFromKnowledgeWrite(op.knowledgeWriteInput)
	sources, err := b.e.resolveKnowledgeSources(ctx, b.work.projectID, refs)
	if err != nil {
		return distillChange{}, err
	}
	if op.Revision != "" {
		snap, err := b.work.kb.Snapshot()
		if err != nil {
			return distillChange{}, err
		}
		old, err := snap.Page(op.ID)
		if err != nil {
			return distillChange{}, err
		}
		page.Sources = old.Sources
	}
	for _, source := range sources {
		if !slices.ContainsFunc(page.Sources, func(existing knowledge.Source) bool {
			return historyReference(existing) == historyReference(source) && existing.RecordID == source.RecordID
		}) {
			page.Sources = append(page.Sources, source)
		}
	}
	written, err := b.work.kb.WritePage(knowledge.PageWrite{Page: page}, stamp)
	if err != nil {
		return distillChange{}, err
	}
	return distillChange{Action: op.Action, ID: written.ID, Title: written.Title, Revision: written.Revision}, nil
}

func (b *projectDistillBatch) knowledgeReader() tool.BaseTool {
	return newKnowledgeReader("", func(ctx context.Context, raw string) (string, error) {
		if err := b.e.checkDistillationAnchor(ctx, b.work); err != nil {
			return "", err
		}
		return b.e.readKnowledge(ctx, b.work.projectID, b.work.kb, raw, b.e.cfg.Snapshot().Tools.ToolResultLimit())
	})
}
