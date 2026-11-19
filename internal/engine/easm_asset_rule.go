package engine

import (
	"encoding/json"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Asset rules supply reported names and creation dates, without a company type
// or identity. Only an existing unique subject supplies a company relationship.
func easmAssetRulePatch(state, arguments json.RawMessage, result *mcp.CallToolResult) json.RawMessage {
	if result == nil || result.IsError {
		return nil
	}
	var request struct{ Domains []string }
	if json.Unmarshal(arguments, &request) != nil || len(request.Domains) == 0 {
		return nil
	}
	requested := make(map[string]bool, len(request.Domains))
	for _, value := range request.Domains {
		if domain := easmDomain(value); domain != "" {
			requested[domain] = true
		}
	}
	var reply struct {
		Error   any
		IsError bool
		Errmsg  string
		Results []struct {
			Domain string
			Names  []struct{ Name, Time string }
		}
	}
	if json.Unmarshal(easmOrganizationBody(result), &reply) != nil || reply.IsError || reply.Errmsg != "" || reply.Error != nil && reply.Error != false {
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
	subjects, domains := make(map[string][]string), make(map[string][]string)
	root := ""
	for i, raw := range graph.Nodes {
		var node identity
		if json.Unmarshal(raw, &node) != nil || node.ID == "" || identities[node.ID].ID != "" {
			return nil
		}
		identities[node.ID], positions[node.ID] = node, i
		if node.Kind == "root" {
			if root != "" || strings.TrimSpace(node.Label) == "" {
				return nil
			}
			root = node.ID
		}
		if node.Kind == "root" || node.Kind == "company" {
			subjects[node.Label] = append(subjects[node.Label], node.ID)
		}
		if node.Kind == "domain" {
			if domain := easmDomain(node.Label); domain != "" {
				domains[domain] = append(domains[domain], node.ID)
			}
		}
	}
	if root == "" {
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
	for _, record := range reply.Results {
		domain := easmDomain(record.Domain)
		if !requested[domain] || len(record.Names) == 0 {
			continue
		}
		var facts []string
		var owners []string
		for _, rule := range record.Names {
			if strings.TrimSpace(rule.Name) == "" {
				continue
			}
			fact := "企业归属规则 mcp_company_asset；名称 " + rule.Name
			if rule.Time != "" {
				fact += "；创建时间 " + rule.Time
			}
			facts = append(facts, fact)
			if matches := subjects[rule.Name]; len(matches) == 1 {
				owners = append(owners, matches[0])
			}
		}
		if len(facts) == 0 {
			continue
		}
		domainIDs := domains[domain]
		if len(domainIDs) == 0 {
			domainID := "domain:" + domain
			if identities[domainID].ID != "" {
				return nil
			}
			raw, _ := json.Marshal(map[string]string{"id": domainID, "kind": "domain", "label": domain})
			positions[domainID] = len(graph.Nodes)
			graph.Nodes = append(graph.Nodes, raw)
			identities[domainID] = identity{domainID, "domain", domain}
			domainIDs = []string{domainID}
			domains[domain] = domainIDs
			changed = true
		}
		for _, domainID := range domainIDs {
			position := positions[domainID]
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
			for _, fact := range facts {
				if !strings.Contains("\n"+reason+"\n", "\n"+fact+"\n") {
					if reason != "" {
						reason += "\n"
					}
					reason += fact
					changed = true
				}
			}
			detail["reason"], _ = json.Marshal(reason)
			node["detail"], _ = json.Marshal(detail)
			graph.Nodes[position], _ = json.Marshal(node)
			for _, owner := range owners {
				key := [2]string{owner, domainID}
				if indexes, exists := linked[key]; exists {
					for _, index := range indexes {
						var edge map[string]json.RawMessage
						_ = json.Unmarshal(graph.Edges[index], &edge)
						if string(edge["confirmed"]) != "true" {
							edge["confirmed"] = json.RawMessage(`true`)
							graph.Edges[index], _ = json.Marshal(edge)
							changed = true
						}
					}
					continue
				}
				raw, _ := json.Marshal(map[string]any{"source": owner, "target": domainID, "confirmed": true})
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
