package engine

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/LubyRuffy/eino-swarm/internal/mcpclient"
	"github.com/LubyRuffy/eino-swarm/internal/store"
	"github.com/LubyRuffy/eino-swarm/internal/taskpanel"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type easmCallOrder struct {
	scopeID string
	next    uint64
	latest  map[string]uint64
}

func (e *Engine) beginEASMToolCall(ctx context.Context, call mcpclient.ToolCall) func(*mcp.CallToolResult) {
	if call.Name == "mcp_company_branch" {
		return e.beginEASMBranchCall(ctx, call)
	}
	if call.Name == "mcp_fofa_org_lookup" || call.Name == "mcp_icp_query" || call.Name == "mcp_company_relation" || call.Name == "mcp_company_asset" || call.Name == "mcp_fofa_search" || call.Name == "mcp_fofa_asset_lookup" {
		return func(result *mcp.CallToolResult) {
			e.updateEASMFromMCP(ctx, call, result)
		}
	}
	section, query := easmMetricRequest(call)
	if section == "" {
		return nil
	}
	owner, ok := ctx.Value(historyReadOwnerKey{}).(historyReadOwner)
	if !ok {
		return nil
	}
	source, err := e.panelSource(ctx, &store.Turn{ThreadID: owner.ThreadID, ID: owner.TurnID})
	if err != nil {
		return nil
	}
	return e.bindEASMMetricCall(ctx, source, call, section, query)
}

func easmMetricRequest(call mcpclient.ToolCall) (section, query string) {
	var args struct {
		Query     string `json:"query"`
		Tool      string `json:"tool"`
		Fields    string `json:"fields"`
		UniqCount string `json:"uniq_count"`
	}
	if json.Unmarshal(call.Arguments, &args) != nil {
		return "", ""
	}
	switch call.Name {
	case "mcp_fofa_asset_heat":
		return "heat", args.Query
	case "mcp_fofa_rule_lookup":
		return "labels", ""
	case "mcp_fofa_stats":
		if args.Tool != "fofa" {
			return "", ""
		}
		if easmProductFields(args.Fields) {
			return "components", args.Query
		}
		if args.Fields == "ip" && args.UniqCount == "ip" {
			return "summary", args.Query
		}
	}
	return "", ""
}

// Bind the selected range before remote dispatch, so returning to the same
// query cannot revive a result started during an earlier visit.
func (e *Engine) bindEASMMetricCall(ctx context.Context, source taskpanel.Source, call mcpclient.ToolCall, section, query string) func(*mcp.CallToolResult) {
	if call.ServerID == "" || section != "labels" && strings.TrimSpace(query) == "" {
		return nil
	}
	e.recordMu.Lock()
	defer e.recordMu.Unlock()
	panel, err := e.store.TaskPanelByPlugin(ctx, source.ThreadID, easmPanelID)
	if err != nil || panel.Source.TurnID != source.TurnID {
		return nil
	}
	if section != "labels" {
		panel, err = e.selectEASMQueryLocked(ctx, panel, source, query)
		if err != nil {
			if ctx.Err() == nil {
				e.log.Warn("could not select EASM metric range", "turn_id", source.TurnID, "tool_call_id", source.ToolCallID, "error", err)
			}
			return nil
		}
	}
	scope, ok := easmReadQueryScope(panel.State)
	if !ok {
		return nil
	}
	if section == "labels" && (scope.ComponentsServerID == "" || scope.ComponentsServerID != call.ServerID) {
		return nil
	}
	if e.easmCalls == nil {
		e.easmCalls = make(map[string]easmCallOrder)
	}
	order := e.easmCalls[panel.ID]
	if order.scopeID != scope.ID {
		order = easmCallOrder{scopeID: scope.ID, latest: make(map[string]uint64)}
	}
	order.next++
	sequence := order.next
	// Rule metadata may arrive in disjoint batches. Names attach only to IDs
	// still present in this scope and never replace component counts.
	e.easmCalls[panel.ID] = order
	return func(result *mcp.CallToolResult) {
		e.completeEASMMetricCall(ctx, panel.ID, source, call, scope.ID, section, sequence, result)
	}
}

func (e *Engine) completeEASMMetricCall(ctx context.Context, panelID string, source taskpanel.Source, call mcpclient.ToolCall, scopeID, section string, sequence uint64, result *mcp.CallToolResult) {
	if result == nil || result.IsError || ctx.Err() != nil {
		return
	}
	e.recordMu.Lock()
	defer e.recordMu.Unlock()
	order := e.easmCalls[panelID]
	if order.scopeID != scopeID || section != "labels" && order.latest[section] > sequence {
		return
	}
	panel, err := e.store.TaskPanel(ctx, source.ThreadID, panelID)
	if errors.Is(err, store.ErrNotFound) {
		return
	}
	if err == nil {
		scope, selected := easmReadQueryScope(panel.State)
		if panel.Source.TurnID != source.TurnID || !selected || scope.ID != scopeID {
			return
		}
		if section == "labels" && scope.ComponentsServerID != call.ServerID {
			return
		}
		metricState := panel.State
		differentComponentSource := section == "components" && scope.ComponentsServerID != "" && scope.ComponentsServerID != call.ServerID
		if differentComponentSource {
			// Identical numeric IDs from a different source cannot inherit the
			// earlier source's product names or tags.
			var state map[string]json.RawMessage
			_ = json.Unmarshal(panel.State, &state)
			state["components"] = json.RawMessage(`[]`)
			metricState, _ = json.Marshal(state)
		}
		var operations json.RawMessage
		var valid bool
		if section == "summary" {
			operations, valid = easmSummaryObservation(metricState, call.Arguments, result)
		} else {
			operations, valid = easmMetricsObservation(metricState, call.Name, call.Arguments, result)
		}
		if !valid {
			return
		}
		if differentComponentSource && len(operations) == 0 {
			// A new provider's valid empty result clears the previous provider's
			// buckets even though its fresh comparison state was already empty.
			operations = json.RawMessage(`[{"op":"replace","path":"/components","value":[]}]`)
		}
		if section == "components" && scope.ComponentsServerID != call.ServerID {
			var patches []json.RawMessage
			_ = json.Unmarshal(operations, &patches)
			binding, _ := json.Marshal(map[string]string{"op": "add", "path": "/query_scope/components_server_id", "value": call.ServerID})
			patches = append(patches, binding)
			operations, _ = json.Marshal(patches)
		}
		if len(operations) == 0 {
			if section != "labels" {
				order.latest[section] = sequence
				e.easmCalls[panelID] = order
			}
			return
		}
		_, err = e.publishTaskPanelLocked(ctx, source, taskpanel.Update{
			ProtocolVersion: taskpanel.ProtocolVersion, PanelID: panel.ID, BaseRevision: panel.Revision,
			OperationID: easmOperationID("mcp", source), Kind: "patch", Operations: operations,
		})
		if err == nil && section != "labels" {
			order.latest[section] = sequence
			e.easmCalls[panelID] = order
		}
	}
	if err != nil && ctx.Err() == nil {
		e.log.Warn("could not apply EASM metric update", "turn_id", source.TurnID, "tool_call_id", source.ToolCallID, "error", err)
	}
}
