package engine

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/LubyRuffy/eino-swarm/internal/mcpclient"
	"github.com/LubyRuffy/eino-swarm/internal/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type easmBranchBindingKey struct{}

type easmBranchBinding struct {
	PanelID, ParentID, Keyword string
}

// The branch result does not return its parent. Bind the actual request to an
// already identified legal company before display labels can change.
func (e *Engine) beginEASMBranchCall(ctx context.Context, call mcpclient.ToolCall) func(*mcp.CallToolResult) {
	var request struct{ Keyword string }
	if json.Unmarshal(call.Arguments, &request) != nil || strings.TrimSpace(request.Keyword) == "" {
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
	e.recordMu.Lock()
	defer e.recordMu.Unlock()
	panel, err := e.store.TaskPanelByPlugin(ctx, source.ThreadID, easmPanelID)
	if err != nil || panel.Source.TurnID != source.TurnID {
		return nil
	}
	var graph struct {
		Nodes []struct{ ID, Kind, Label string }
	}
	if json.Unmarshal(panel.State, &graph) != nil {
		return nil
	}
	var matches []string
	for _, node := range graph.Nodes {
		knownRoot := node.Kind == "root" && (strings.HasPrefix(node.ID, "company:registry:") || strings.HasPrefix(node.ID, "company:fofa-org:"))
		if node.Label == request.Keyword && (node.Kind == "company" || knownRoot) {
			matches = append(matches, node.ID)
		}
	}
	binding := easmBranchBinding{PanelID: panel.ID, Keyword: request.Keyword}
	if len(matches) == 1 {
		binding.ParentID = matches[0]
	}
	bound := context.WithValue(ctx, easmBranchBindingKey{}, binding)
	return func(result *mcp.CallToolResult) { e.updateEASMFromMCP(bound, call, result) }
}

func easmCompanyBranchPatch(state json.RawMessage, binding easmBranchBinding, result *mcp.CallToolResult) json.RawMessage {
	if result == nil || result.IsError || strings.TrimSpace(binding.Keyword) == "" {
		return nil
	}
	var reply struct {
		Error   any
		IsError bool
		Errmsg  string
		Items   []easmCompanyRecord
	}
	body := easmOrganizationBody(result)
	// The SDK decodes structured numbers into float64. The original JSON text
	// retains large registry IDs exactly when both representations are supplied.
	if len(result.Content) == 1 {
		if text, ok := result.Content[0].(*mcp.TextContent); ok && text != nil && json.Valid([]byte(text.Text)) {
			body = json.RawMessage(text.Text)
		}
	}
	if json.Unmarshal(body, &reply) != nil || reply.IsError || reply.Errmsg != "" || reply.Error != nil && reply.Error != false || reply.Items == nil {
		return nil
	}
	var graph struct {
		Nodes []json.RawMessage `json:"nodes"`
		Edges []json.RawMessage `json:"edges"`
	}
	if json.Unmarshal(state, &graph) != nil || graph.Nodes == nil || graph.Edges == nil {
		return nil
	}
	type identity struct{ ID, Kind, Label string }
	identities := make(map[string]identity)
	positions := make(map[string]int)
	companies := make(map[string][]string)
	rootID := ""
	for i, raw := range graph.Nodes {
		var node identity
		if json.Unmarshal(raw, &node) != nil || node.ID == "" || identities[node.ID].ID != "" {
			return nil
		}
		identities[node.ID], positions[node.ID] = node, i
		if node.Kind == "root" {
			if rootID != "" || strings.TrimSpace(node.Label) == "" {
				return nil
			}
			rootID = node.ID
		}
		if node.Kind == "company" {
			companies[node.Label] = append(companies[node.Label], node.ID)
		}
	}
	if rootID == "" {
		return nil
	}
	parent := binding.ParentID
	if node := identities[parent]; node.Kind != "company" && node.Kind != "root" {
		parent = ""
	}
	// Another in-flight lookup can identify the parent before this result.
	// A previously resolved binding keeps its original identity.
	if binding.ParentID == "" {
		matches := append([]string(nil), companies[binding.Keyword]...)
		root := identities[rootID]
		knownRoot := strings.HasPrefix(root.ID, "company:registry:") || strings.HasPrefix(root.ID, "company:fofa-org:")
		if knownRoot && root.Label == binding.Keyword {
			matches = append(matches, root.ID)
		}
		if len(matches) == 1 {
			parent = matches[0]
		}
	}
	linked := make(map[[2]string][]int)
	for i, raw := range graph.Edges {
		var edge struct{ Source, Target string }
		if json.Unmarshal(raw, &edge) != nil || identities[edge.Source].ID == "" || identities[edge.Target].ID == "" {
			return nil
		}
		key := [2]string{edge.Source, edge.Target}
		linked[key] = append(linked[key], i)
	}
	namedIDs := make(map[string]map[string]bool)
	for _, record := range reply.Items {
		if namedIDs[record.Name] == nil {
			namedIDs[record.Name] = make(map[string]bool)
		}
		id := easmCompanyID(record.ID)
		if id == "" {
			id = "company:branch-name:" + record.Name
		}
		namedIDs[record.Name][id] = true
	}
	changed := false
	resolved := make(map[string]string)
	for _, record := range reply.Items {
		if strings.TrimSpace(record.Name) == "" {
			continue
		}
		sourceID := easmCompanyID(record.ID)
		id := sourceID
		if id == "" {
			id = "company:branch-name:" + record.Name
		}
		identityKey := id
		matches := companies[record.Name]
		if retained := resolved[identityKey]; retained != "" {
			id = retained
		} else if identities[id].ID == "" && len(namedIDs[record.Name]) == 1 && len(matches) == 1 && (sourceID == "" || !strings.HasPrefix(matches[0], "company:registry:")) {
			id = matches[0]
		}
		if id == parent {
			continue
		}
		if node := identities[id]; node.ID != "" && node.Kind != "company" && !(node.Kind == "root" && id == sourceID) {
			continue
		}
		resolved[identityKey] = id
		if identities[id].ID == "" {
			raw, _ := json.Marshal(map[string]string{"id": id, "kind": "company", "label": record.Name})
			positions[id] = len(graph.Nodes)
			identities[id] = identity{id, "company", record.Name}
			companies[record.Name] = append(companies[record.Name], id)
			graph.Nodes = append(graph.Nodes, raw)
			changed = true
		}
		fact := "分公司来源 mcp_company_branch；查询对象 " + binding.Keyword + "；记录名称 " + record.Name
		if sourceID != "" {
			fact += "；来源编号 " + strings.TrimPrefix(sourceID, "company:registry:")
		}
		if record.RegStatus != "" {
			fact += "；登记状态 " + record.RegStatus
		}
		if record.CreditCode != "" {
			fact += "；统一社会信用代码 " + record.CreditCode
		}
		position := positions[id]
		var node map[string]json.RawMessage
		_ = json.Unmarshal(graph.Nodes[position], &node)
		var detail map[string]json.RawMessage
		if raw := node["detail"]; len(raw) != 0 && json.Unmarshal(raw, &detail) != nil {
			return nil
		}
		if detail == nil {
			detail = make(map[string]json.RawMessage)
		}
		var reason string
		if raw := detail["reason"]; len(raw) != 0 && json.Unmarshal(raw, &reason) != nil {
			return nil
		}
		if !strings.Contains(reason, fact) {
			if reason != "" {
				reason += "\n"
			}
			detail["reason"], _ = json.Marshal(reason + fact)
			node["detail"], _ = json.Marshal(detail)
			graph.Nodes[position], _ = json.Marshal(node)
			changed = true
		}
		if parent != "" {
			key := [2]string{parent, id}
			if positions, exists := linked[key]; exists {
				for _, position := range positions {
					var edge map[string]json.RawMessage
					_ = json.Unmarshal(graph.Edges[position], &edge)
					if string(edge["confirmed"]) != "true" {
						edge["confirmed"] = json.RawMessage("true")
						graph.Edges[position], _ = json.Marshal(edge)
						changed = true
					}
				}
			} else {
				raw, _ := json.Marshal(map[string]any{"source": parent, "target": id, "confirmed": true})
				linked[key] = []int{len(graph.Edges)}
				graph.Edges = append(graph.Edges, raw)
				changed = true
			}
		}
	}
	if !changed {
		return nil
	}
	patch, _ := json.Marshal([]map[string]any{{"op": "replace", "path": "/nodes", "value": graph.Nodes}, {"op": "replace", "path": "/edges", "value": graph.Edges}})
	return patch
}
