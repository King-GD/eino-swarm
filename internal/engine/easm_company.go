package engine

import (
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
	"unicode"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type easmCompanyRecord struct {
	ID         json.RawMessage `json:"id"`
	Name       string          `json:"name"`
	Label      string          `json:"label"`
	CreditCode string          `json:"credit_code"`
	RegStatus  string          `json:"reg_status"`
	Percent    string          `json:"percent"`
}

// The relation tool's root owns the direct investments in down. Its up list
// describes investors and does not extend this task's confirmed asset scope.
func easmCompanyRelationPatch(state, arguments json.RawMessage, result *mcp.CallToolResult) json.RawMessage {
	if result == nil || result.IsError {
		return nil
	}
	var request struct{ Keyword string }
	if json.Unmarshal(arguments, &request) != nil || strings.TrimSpace(request.Keyword) == "" {
		return nil
	}
	var reply struct {
		Error   any
		IsError bool
		Errmsg  string
		Root    easmCompanyRecord
		Down    []easmCompanyRecord
	}
	if json.Unmarshal(easmOrganizationBody(result), &reply) != nil || reply.IsError || reply.Errmsg != "" || reply.Error != nil && reply.Error != false || reply.Down == nil || reply.Root.Label != "Company" || strings.TrimSpace(reply.Root.Name) == "" || easmCompanyID(reply.Root.ID) == "" {
		return nil
	}
	records := append([]easmCompanyRecord{reply.Root}, reply.Down...)
	namedIDs := make(map[string]map[string]bool)
	for _, record := range records {
		id := easmCompanyID(record.ID)
		if record.Label != "Company" || strings.TrimSpace(record.Name) == "" || id == "" {
			continue
		}
		if namedIDs[record.Name] == nil {
			namedIDs[record.Name] = make(map[string]bool)
		}
		namedIDs[record.Name][id] = true
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
	var root identity
	for i, raw := range graph.Nodes {
		var node identity
		if json.Unmarshal(raw, &node) != nil || node.ID == "" || identities[node.ID].ID != "" {
			return nil
		}
		identities[node.ID], positions[node.ID] = node, i
		if node.Kind == "root" {
			if root.ID != "" || strings.TrimSpace(node.Label) == "" {
				return nil
			}
			root = node
		}
		if node.Kind == "company" {
			companies[node.Label] = append(companies[node.Label], node.ID)
		}
	}
	if root.ID == "" {
		return nil
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
	changed := false
	resolved := make(map[string]string)
	resolve := func(record easmCompanyRecord, parentName string) (string, bool) {
		sourceID := easmCompanyID(record.ID)
		legalSubject := parentName == "" && (sourceID == root.ID ||
			record.Name == root.Label && len(namedIDs[record.Name]) == 1 && len(companies[record.Name]) <= 1)
		id := resolved[sourceID]
		if id == "" {
			id = sourceID
			if legalSubject {
				id = root.ID
			} else if identities[id].ID == "" && len(namedIDs[record.Name]) == 1 && len(companies[record.Name]) == 1 {
				// FOFA organization IDs and business-registry IDs belong to
				// separate systems. A uniquely named existing company retains its ID.
				id = companies[record.Name][0]
			}
			if existing := identities[id]; existing.ID != "" && existing.Kind != "company" && !(legalSubject && existing.Kind == "root") {
				return "", false
			}
			if identities[id].ID == "" {
				node, _ := json.Marshal(map[string]any{"id": id, "kind": "company", "label": record.Name})
				positions[id] = len(graph.Nodes)
				graph.Nodes = append(graph.Nodes, node)
				identities[id] = identity{id, "company", record.Name}
				companies[record.Name] = append(companies[record.Name], id)
				changed = true
			}
			resolved[sourceID] = id
		}
		fact := "工商关系来源 mcp_company_relation；主体 " + record.Name + "；来源编号 " + strings.TrimPrefix(sourceID, "company:registry:")
		if parentName != "" {
			fact += "；直接投资方 " + parentName
			if record.Percent != "" {
				fact += "；持股比例 " + easmCompanyPercent(record.Percent)
			}
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
			return "", false
		}
		if detail == nil {
			detail = make(map[string]json.RawMessage)
		}
		var reason string
		if raw := detail["reason"]; len(raw) != 0 && json.Unmarshal(raw, &reason) != nil {
			return "", false
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
		return id, true
	}
	addEdge := func(source, target string, confirmed bool) {
		key := [2]string{source, target}
		if positions, exists := linked[key]; exists {
			if confirmed {
				for _, position := range positions {
					var edge map[string]json.RawMessage
					_ = json.Unmarshal(graph.Edges[position], &edge)
					if string(edge["confirmed"]) != "true" {
						edge["confirmed"] = json.RawMessage("true")
						graph.Edges[position], _ = json.Marshal(edge)
						changed = true
					}
				}
			}
			return
		}
		linked[key] = []int{len(graph.Edges)}
		edge, _ := json.Marshal(map[string]any{"source": source, "target": target, "confirmed": confirmed})
		graph.Edges = append(graph.Edges, edge)
		changed = true
	}
	parent, ok := resolve(reply.Root, "")
	if !ok {
		return nil
	}
	if parent != root.ID {
		addEdge(root.ID, parent, false)
	}
	for _, child := range reply.Down {
		if child.Label != "Company" || strings.TrimSpace(child.Name) == "" || easmCompanyID(child.ID) == "" {
			continue
		}
		if easmCompanyID(child.ID) == easmCompanyID(reply.Root.ID) {
			continue
		}
		id, ok := resolve(child, reply.Root.Name)
		if !ok || id == parent {
			return nil
		}
		addEdge(parent, id, true)
	}
	if !changed {
		return nil
	}
	patch, _ := json.Marshal([]map[string]any{
		{"op": "replace", "path": "/nodes", "value": graph.Nodes},
		{"op": "replace", "path": "/edges", "value": graph.Edges},
	})
	return patch
}

func easmCompanyID(raw json.RawMessage) string {
	var id string
	if json.Unmarshal(raw, &id) != nil {
		id = easmProductID(raw)
	}
	if strings.TrimSpace(id) == "" || strings.ContainsFunc(id, unicode.IsControl) {
		return ""
	}
	return "company:registry:" + id
}

func easmCompanyPercent(value string) string {
	if strings.ContainsAny(value, "eE/") {
		return value
	}
	raw, _ := json.Marshal(value)
	if _, ok := easmMetricNumber(raw); !ok {
		return value
	}
	ratio, ok := new(big.Rat).SetString(value)
	if !ok || ratio.Sign() < 0 || ratio.Cmp(big.NewRat(1, 1)) > 0 {
		return value
	}
	scale := 0
	if decimal := strings.IndexByte(value, '.'); decimal >= 0 {
		scale = len(value) - decimal - 1
	}
	percentage := ratio.Mul(ratio, big.NewRat(100, 1)).FloatString(scale)
	if strings.Contains(percentage, ".") {
		percentage = strings.TrimRight(strings.TrimRight(percentage, "0"), ".")
	}
	return fmt.Sprintf("%s%%（接口原值 %s）", percentage, value)
}
