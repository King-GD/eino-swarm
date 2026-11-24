package engine

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	swarm "github.com/LubyRuffy/eino-swarm"
	"github.com/LubyRuffy/eino-swarm/internal/store"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

type recordWorkArgs struct {
	Scope          string               `json:"scope"`
	ID             string               `json:"id"`
	Version        int64                `json:"version"`
	Kind           string               `json:"kind"`
	Title          string               `json:"title"`
	Summary        string               `json:"summary"`
	Detail         string               `json:"detail"`
	Status         string               `json:"status"`
	DependsOn      []string             `json:"depends_on"`
	Evidence       []store.NuwaEvidence `json:"evidence"`
	OwnToolCallIDs json.RawMessage      `json:"own_tool_call_ids"`
	ArtifactPath   string               `json:"artifact_path"`
}

func decodeNuwaArgs(args string, value any) error {
	if strings.TrimSpace(args) == "" {
		args = "{}"
	}
	decoder := json.NewDecoder(strings.NewReader(args))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return fmt.Errorf("invalid tool arguments: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("tool arguments must contain one JSON object")
	}
	return nil
}

func decodeNuwaEvidenceFields(fields map[string]json.RawMessage) (bool, []string, error) {
	var hasEvidence bool
	var ownIDsJSON json.RawMessage
	for key, raw := range fields {
		switch {
		case strings.EqualFold(key, "evidence"):
			hasEvidence = true
		case strings.EqualFold(key, "own_tool_call_ids"):
			if ownIDsJSON != nil {
				return false, nil, fmt.Errorf("own_tool_call_ids was supplied with multiple spellings")
			}
			ownIDsJSON = raw
		}
	}
	if ownIDsJSON == nil {
		return hasEvidence, nil, nil
	}
	if hasEvidence {
		return false, nil, fmt.Errorf("evidence and own_tool_call_ids are mutually exclusive")
	}
	// Decode the same final JSON value the receipt hash sees, into a fresh slice.
	var ownIDs []string
	if err := json.Unmarshal(ownIDsJSON, &ownIDs); err != nil || ownIDs == nil {
		return false, nil, fmt.Errorf("own_tool_call_ids must be an array of strings")
	}
	return true, ownIDs, nil
}

func nuwaArtifactPath(workspace, selected string) (string, error) {
	if strings.TrimSpace(selected) == "" {
		return "", nil
	}
	root, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		return "", fmt.Errorf("resolve artifact workspace: %w", err)
	}
	path := selected
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("artifact does not exist: %w", err)
	}
	rel, err := filepath.Rel(root, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("published artifacts must be within this conversation's workspace")
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("read artifact identity: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("artifact must name a regular file")
	}
	return filepath.ToSlash(rel), nil
}

func (e *Engine) nuwaWorkTools(workspace string, turn *store.Turn, readOnly bool) []tool.BaseTool {
	read := &nuwaTool{name: "read_work", description: "Read work records visible to this conversation, including work explicitly shared with its project. With id, read one complete node and its provenance. Otherwise receive bounded summaries; use offset to page. Reports remain claims; inspect original evidence before relying on them.",
		params: map[string]*schema.ParameterInfo{
			"id": {Type: schema.String}, "kind": {Type: schema.String}, "offset": {Type: schema.Integer}, "limit": {Type: schema.Integer},
		}, run: func(ctx context.Context, args string) (string, error) {
			owner, ok := ctx.Value(historyReadOwnerKey{}).(historyReadOwner)
			if !ok || turn == nil || owner.ThreadID != turn.ThreadID || owner.TurnID != turn.ID {
				return "", fmt.Errorf("work record read has no matching trusted turn owner")
			}
			return e.readNuwaWork(ctx, turn.ProjectID, args)
		}}
	if readOnly {
		return []tool.BaseTool{read}
	}
	record := &nuwaTool{name: "record_work", description: "Record a task, reported fact, or existing workspace artifact. Creates require kind/title and default to this conversation. Updates require id/current version and patch only supplied fields. When evidence disproves a record, update its status and evidence on the same ID. Restore a supported fact or artifact to reported with its current version; review downstream records and confirm or correct their dependencies. Origin and correction identity come from the runtime.",
		params: map[string]*schema.ParameterInfo{
			"scope": {Type: schema.String, Desc: "On creation, thread (default) or project. Project scope also shares the private prerequisites. To share an existing record, send only id, current version, and scope project. Sharing is one-way and requires membership in the project."},
			"id":    {Type: schema.String}, "version": {Type: schema.Integer}, "title": {Type: schema.String},
			"summary": {Type: schema.String}, "detail": {Type: schema.String},
			"kind": {Type: schema.String, Desc: "A task, fact, or artifact. Required on creation."},
			"status": {Type: schema.String,
				Desc: "For a task use pending, running, done, blocked, or cancelled. For a fact or artifact use reported, superseded, or retracted. On creation a task defaults to pending and a fact or artifact defaults to reported."},
			"artifact_path": {Type: schema.String,
				Desc: "Required for an artifact. Give a workspace-relative or absolute path that resolves to an existing regular file within this conversation's workspace."},
			"depends_on": {Type: schema.Array, ElemInfo: &schema.ParameterInfo{Type: schema.String},
				Desc: "Exact node IDs returned by record_work or read_work for material prerequisites actually relied on. On update, omit this field to preserve existing dependencies or supply [] to clear them."},
			"own_tool_call_ids": {Type: schema.Array, ElemInfo: &schema.ParameterInfo{Type: schema.String},
				Desc: "Exact IDs of your own persisted tool results in this manager turn or worker execution. Mutually exclusive with evidence, even when empty. An empty array clears evidence."},
			"evidence": {Type: schema.Array, Desc: "References to existing events within the record's conversation or project scope. Mutually exclusive with own_tool_call_ids, even when empty. An empty array clears evidence.",
				ElemInfo: &schema.ParameterInfo{Type: schema.Object, SubParams: map[string]*schema.ParameterInfo{
					"thread_id": {Type: schema.String, Required: true}, "turn_id": {Type: schema.String}, "seq": {Type: schema.Integer, Required: true}, "label": {Type: schema.String},
				}}},
		}, run: func(ctx context.Context, args string) (string, error) {
			return e.recordNuwaWork(ctx, workspace, turn, args)
		}}
	return []tool.BaseTool{read, record}
}

func requestedWorkScope(input recordWorkArgs, fields map[string]json.RawMessage) (bool, error) {
	found := false
	for field := range fields {
		if !strings.EqualFold(field, "scope") {
			continue
		}
		if found {
			return false, fmt.Errorf("scope was supplied with multiple spellings")
		}
		found = true
	}
	if found && input.Scope != store.WorkScopeProject && input.Scope != store.WorkScopeThread {
		return false, fmt.Errorf("scope must be thread or project")
	}
	return found, nil
}

func (e *Engine) recordNuwaWork(ctx context.Context, workspace string, turn *store.Turn, args string) (string, error) {
	if err := context.Cause(ctx); err != nil {
		return "", err
	}
	caller, ok := swarm.ToolCaller(ctx)
	if !ok || caller.AgentID == "" || !workLedgerEnabled(turn) || workLedgerReadOnly(turn) {
		return "", fmt.Errorf("work record writes require an actual agent invocation authorized for this turn")
	}
	owner, ok := ctx.Value(historyReadOwnerKey{}).(historyReadOwner)
	if !ok || owner.ThreadID != turn.ThreadID || owner.TurnID != turn.ID {
		return "", fmt.Errorf("work record write has no matching trusted turn owner")
	}
	callID := compose.GetToolCallID(ctx)
	if callID == "" {
		return "", fmt.Errorf("work record write requires a real tool call identity")
	}
	var input recordWorkArgs
	if err := decodeNuwaArgs(args, &input); err != nil {
		return "", err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(args), &fields); err != nil {
		return "", err
	}
	scopeSupplied, err := requestedWorkScope(input, fields)
	if err != nil {
		return "", err
	}
	evidenceSupplied, ownToolCallIDs, err := decodeNuwaEvidenceFields(fields)
	if err != nil {
		return "", err
	}
	canonical, err := json.Marshal(fields)
	if err != nil {
		return "", err
	}
	identity, err := json.Marshal([]any{turn.ThreadID, turn.ID, caller.AgentID, caller.Generation, callID})
	if err != nil {
		return "", err
	}
	receipt := store.NuwaWriteReceipt{
		Key: fmt.Sprintf("%x", sha256.Sum256(identity)), Hash: fmt.Sprintf("%x", sha256.Sum256(canonical)),
		CallerProjectID: &turn.ProjectID, OwnToolCallIDs: ownToolCallIDs,
		CurrentToolCallID: callID, ToolReceiptScope: swarm.ToolReceiptScope(ctx),
	}
	if input.ID != "" && input.Scope == store.WorkScopeProject {
		if len(fields) != 3 || input.Version <= 0 {
			return "", fmt.Errorf("sharing an existing record requires only id, current version and scope project")
		}
		unlock, err := e.lockWorkRecordWrite(ctx, turn.ID)
		if err != nil {
			return "", err
		}
		defer unlock()
		promotion, err := e.store.PromoteNuwaNodes(ctx, store.NuwaPromoteInput{
			ThreadID: turn.ThreadID, IDs: []string{input.ID}, ProjectID: turn.ProjectID, TurnID: turn.ID,
			AgentID: caller.AgentID, Generation: caller.Generation, Version: input.Version, Receipt: receipt,
		})
		if err != nil {
			return "", err
		}
		for _, node := range promotion.Nodes {
			if node.ID == input.ID {
				body, err := json.Marshal(map[string]any{"ok": true, "node": node})
				return string(body), err
			}
		}
		return "", fmt.Errorf("sharing did not return its requested record")
	}
	if input.Scope == "" {
		input.Scope = store.WorkScopeThread
	}
	node := store.NuwaNode{
		ID: input.ID, Scope: input.Scope, Kind: input.Kind, Title: input.Title, Summary: input.Summary,
		Detail: input.Detail, Status: input.Status, DependsOn: input.DependsOn, Evidence: input.Evidence,
		ArtifactPath: input.ArtifactPath, ThreadID: turn.ThreadID, TurnID: turn.ID, AgentID: caller.AgentID,
		Generation: caller.Generation,
	}
	artifactThreadID := turn.ThreadID
	if input.ID != "" {
		view, err := e.store.WorkSnapshotForTurn(ctx, turn.ProjectID, owner.ThreadID, owner.TurnID)
		if err != nil {
			return "", err
		}
		found := false
		for _, previous := range view.Nodes {
			if previous.ID != input.ID {
				continue
			}
			found = true
			// An explicit scope belongs to the original request. The store
			// replays its committed receipt before checking the current scope.
			if !scopeSupplied {
				node.Scope = previous.Scope
			}
			applyWorkPatch(&node, previous, fields, evidenceSupplied)
			artifactThreadID = previous.ThreadID
			break
		}
		if !found {
			return "", fmt.Errorf("unknown work record id: omit id and version to create, or use an existing id and version from read_work to update: %w", store.ErrNotFound)
		}
	}
	if node.Scope == store.WorkScopeProject {
		node.ProjectID = turn.ProjectID
	}
	if artifactThreadID != turn.ThreadID {
		workspace = e.WorkspaceDir(artifactThreadID)
	}
	fileRoot := e.TaskFileDir(artifactThreadID)
	if workspace != fileRoot && workspace == e.WorkspaceDir(artifactThreadID) {
		_, supplied := fields["artifact_path"]
		if (supplied || input.ID == "") && strings.TrimSpace(node.ArtifactPath) != "" && !filepath.IsAbs(node.ArtifactPath) {
			node.ArtifactPath = filepath.Join(workspace, node.ArtifactPath)
		}
		workspace = fileRoot
	}
	node.ArtifactPath, err = nuwaArtifactPath(workspace, node.ArtifactPath)
	if err != nil {
		return "", err
	}
	unlock, err := e.lockWorkRecordWrite(ctx, turn.ID)
	if err != nil {
		return "", err
	}
	defer unlock()
	if err := e.store.UpsertNuwaNode(ctx, &node, input.Version, receipt); err != nil {
		return "", err
	}
	body, err := json.Marshal(map[string]any{"ok": true, "node": node})
	return string(body), err
}

// The mutation and turn removal share ownership. Artifact resolution and the
// snapshot used to prepare a patch do not hold the event writer lock.
func (e *Engine) lockWorkRecordWrite(ctx context.Context, turnID string) (func(), error) {
	e.recordMu.Lock()
	if _, dropped := e.droppedTurns[turnID]; dropped {
		e.recordMu.Unlock()
		return nil, store.ErrNotFound
	}
	if err := context.Cause(ctx); err != nil {
		e.recordMu.Unlock()
		return nil, err
	}
	return e.recordMu.Unlock, nil
}

func applyWorkPatch(node *store.NuwaNode, previous store.NuwaNode, fields map[string]json.RawMessage, evidenceSupplied bool) {
	if _, set := fields["kind"]; !set {
		node.Kind = previous.Kind
	}
	if _, set := fields["title"]; !set {
		node.Title = previous.Title
	}
	if _, set := fields["summary"]; !set {
		node.Summary = previous.Summary
	}
	if _, set := fields["detail"]; !set {
		node.Detail = previous.Detail
	}
	if _, set := fields["status"]; !set {
		node.Status = previous.Status
	}
	if _, set := fields["depends_on"]; !set {
		node.DependsOn = previous.DependsOn
		node.DependencyVersions = previous.DependencyVersions
	}
	if !evidenceSupplied {
		node.Evidence = previous.Evidence
	}
	if _, set := fields["artifact_path"]; !set {
		node.ArtifactPath = previous.ArtifactPath
	}
}

func (e *Engine) readNuwaWork(ctx context.Context, projectID, args string) (string, error) {
	var input struct {
		ID     string `json:"id"`
		Kind   string `json:"kind"`
		Offset int    `json:"offset"`
		Limit  int    `json:"limit"`
	}
	if err := decodeNuwaArgs(args, &input); err != nil {
		return "", err
	}
	if input.Offset < 0 || input.Limit < 0 {
		return "", fmt.Errorf("offset and limit must be nonnegative")
	}
	owner, ok := ctx.Value(historyReadOwnerKey{}).(historyReadOwner)
	if !ok {
		return "", fmt.Errorf("work record read has no matching trusted turn owner")
	}
	view, err := e.store.WorkSnapshotForTurn(ctx, projectID, owner.ThreadID, owner.TurnID)
	if err != nil {
		return "", err
	}
	nodes := view.Nodes
	revision := view.WorkRevision
	// Keep one committed budget for the whole response during live settings updates.
	budget := e.cfg.Snapshot().Tools.ToolResultLimit()
	if input.ID != "" {
		for _, node := range nodes {
			if node.ID == input.ID {
				return boundedNuwaNode(node, revision, input.Offset, input.Limit, budget)
			}
		}
		return "", store.ErrNotFound
	}
	filtered := make([]store.NuwaNode, 0, len(nodes))
	for _, node := range nodes {
		if input.Kind == "" || node.Kind == input.Kind {
			node.Detail = ""
			filtered = append(filtered, node)
		}
	}
	if input.Offset > len(filtered) {
		input.Offset = len(filtered)
	}
	end := len(filtered)
	if input.Limit > 0 && input.Limit < end-input.Offset {
		end = input.Offset + input.Limit
	}
	page := filtered[input.Offset:end]
	// Omit whole nodes under the existing tool-result budget; never truncate JSON.
	for {
		body, err := json.Marshal(struct {
			workReadRevision
			Nodes      []store.NuwaNode `json:"nodes"`
			Total      int              `json:"total"`
			NextOffset int              `json:"next_offset"`
			More       bool             `json:"more"`
		}{readRevision(revision), page, len(filtered), input.Offset + len(page), input.Offset+len(page) < len(filtered)})
		if err != nil {
			return "", err
		}
		if len([]rune(string(body))) <= budget {
			return string(body), nil
		}
		if len(page) <= 1 {
			return "", fmt.Errorf("shared node metadata exceeds the tool result budget; use a node id or a later offset instead of assuming this page is complete")
		}
		page = page[:len(page)-1]
	}
}

type workReadRevision struct {
	store.WorkRevision
	Revision int64 `json:"revision"`
}

func readRevision(revision store.WorkRevision) workReadRevision {
	return workReadRevision{WorkRevision: revision, Revision: revision.ProjectRevision}
}

func boundedNuwaNode(node store.NuwaNode, revision store.WorkRevision, offset, limit, budget int) (string, error) {
	runes := []rune(node.Detail)
	if offset > len(runes) {
		offset = len(runes)
	}
	end := len(runes)
	if limit > 0 && limit < end-offset {
		end = offset + limit
	}
	lo, hi := 0, end-offset
	var accepted []byte
	for lo <= hi {
		count := (lo + hi) / 2
		node.Detail = string(runes[offset : offset+count])
		body, err := json.Marshal(struct {
			workReadRevision
			Node         store.NuwaNode `json:"node"`
			DetailOffset int            `json:"detail_offset"`
			NextOffset   int            `json:"next_offset"`
			DetailTotal  int            `json:"detail_total"`
			More         bool           `json:"more"`
		}{readRevision(revision), node, offset, offset + count, len(runes), offset+count < len(runes)})
		if err != nil {
			return "", err
		}
		if len([]rune(string(body))) <= budget {
			accepted = body
			lo = count + 1
		} else {
			hi = count - 1
		}
	}
	if accepted == nil || (hi == 0 && offset < len(runes)) {
		return "", fmt.Errorf("shared node metadata exceeds the configured tool result budget")
	}
	return string(accepted), nil
}
