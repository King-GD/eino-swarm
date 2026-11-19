package engine

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/LubyRuffy/eino-swarm/internal/mcpclient"
	"github.com/LubyRuffy/eino-swarm/internal/store"
	"github.com/LubyRuffy/eino-swarm/internal/taskpanel"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const easmPanelID = "easm-asset-portrait"

func (e *Engine) openEASMWithSubject(ctx context.Context, definition taskpanel.Definition, source taskpanel.Source, subject string) (taskpanel.Panel, error) {
	subject = strings.TrimSpace(subject)
	if utf8.RuneCountInString(subject) > 256 || strings.ContainsFunc(subject, unicode.IsControl) {
		return taskpanel.Panel{}, fmt.Errorf("EASM subject must be a target name of at most 256 characters")
	}
	nodes := []map[string]string{{"id": "root", "kind": "root", "label": subject}}
	state, _ := json.Marshal(map[string]any{
		"schema_version": 1, "title": subject, "nodes": nodes,
		"edges": []any{}, "heat": []any{}, "components": []any{},
	})
	panel, err := e.store.OpenTaskPanel(ctx, definition, source, state)
	if err != nil {
		return panel, err
	}
	var graph struct {
		Nodes []struct{ Kind, Label string }
	}
	if err = json.Unmarshal(panel.State, &graph); err != nil {
		return panel, err
	}
	ops := json.RawMessage(`[]`)
	if len(graph.Nodes) == 0 {
		// A panel opened before the target was known can already have other results.
		ops, _ = json.Marshal([]map[string]any{
			{"op": "replace", "path": "/title", "value": subject},
			{"op": "replace", "path": "/nodes", "value": nodes},
		})
	} else {
		sameSubject := false
		for _, node := range graph.Nodes {
			sameSubject = sameSubject || node.Kind == "root" && node.Label == subject
		}
		if !sameSubject || panel.Source.TurnID == source.TurnID {
			return panel, nil
		}
		previous, err := e.store.GetTurnContext(ctx, panel.Source.TurnID)
		if err != nil {
			return panel, err
		}
		current, err := e.store.GetTurnContext(ctx, source.TurnID)
		if err != nil || current.Seq <= previous.Seq {
			return panel, err
		}
	}
	return e.publishTaskPanelLocked(ctx, source, taskpanel.Update{
		ProtocolVersion: taskpanel.ProtocolVersion, PanelID: panel.ID, BaseRevision: panel.Revision,
		OperationID: easmOperationID("root", source), Kind: "patch", Operations: ops,
	})
}

func easmOperationID(kind string, source taskpanel.Source) string {
	return fmt.Sprintf("easm-%s:%x", kind, sha256.Sum256(fmt.Appendf(nil, "%q:%q:%d:%q",
		source.TurnID, source.AgentID, source.Generation, source.ToolCallID)))
}

// The MCP adapter calls this before the result goes back to the model. Other
// results remain available to the agent for evidence-based publication.
func (e *Engine) updateEASMFromMCP(ctx context.Context, call mcpclient.ToolCall, result *mcp.CallToolResult) {
	if call.Name != "mcp_fofa_org_lookup" && call.Name != "mcp_icp_query" && call.Name != "mcp_company_relation" && call.Name != "mcp_company_branch" && call.Name != "mcp_company_asset" && call.Name != "mcp_fofa_search" && call.Name != "mcp_fofa_asset_lookup" {
		return
	}
	owner, ok := ctx.Value(historyReadOwnerKey{}).(historyReadOwner)
	if !ok {
		return
	}
	source, err := e.panelSource(ctx, &store.Turn{ThreadID: owner.ThreadID, ID: owner.TurnID})
	if err != nil {
		return
	}
	e.recordMu.Lock()
	defer e.recordMu.Unlock()
	panel, err := e.store.TaskPanelByPlugin(ctx, source.ThreadID, easmPanelID)
	if errors.Is(err, store.ErrNotFound) {
		return
	}
	if err == nil && panel.Source.TurnID == source.TurnID {
		var ops json.RawMessage
		switch call.Name {
		case "mcp_icp_query":
			ops = easmICPPatch(panel.State, result)
		case "mcp_company_relation":
			ops = easmCompanyRelationPatch(panel.State, call.Arguments, result)
		case "mcp_company_asset":
			ops = easmAssetRulePatch(panel.State, call.Arguments, result)
		case "mcp_company_branch":
			if binding, ok := ctx.Value(easmBranchBindingKey{}).(easmBranchBinding); ok && binding.PanelID == panel.ID {
				ops = easmCompanyBranchPatch(panel.State, binding, result)
			}
		case "mcp_fofa_search":
			ops = easmCertificatePatch(panel.State, call.Arguments, result)
		case "mcp_fofa_asset_lookup":
			ops = easmAssetCertificatePatch(panel.State, call.Arguments, result)
		default:
			ops = easmOrganizationPatch(panel.State, result)
		}
		if len(ops) != 0 {
			_, err = e.publishTaskPanelLocked(ctx, source, taskpanel.Update{
				ProtocolVersion: taskpanel.ProtocolVersion, PanelID: panel.ID, BaseRevision: panel.Revision,
				OperationID: easmOperationID("mcp", source), Kind: "patch", Operations: ops,
			})
		}
	}
	if err != nil && ctx.Err() == nil {
		e.log.Warn("could not apply EASM graph update", "turn_id", source.TurnID, "tool_call_id", source.ToolCallID, "error", err)
	}
}
