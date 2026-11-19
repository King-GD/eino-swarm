package engine

import (
	"encoding/json"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Filing records preserve the source path, including registrations shared by
// several domains and domains recorded under more than one registration.
func easmICPPatch(state json.RawMessage, result *mcp.CallToolResult) json.RawMessage {
	if result == nil || result.IsError {
		return nil
	}
	var reply struct {
		Error   any                                 `json:"error"`
		IsError bool                                `json:"isError"`
		Errmsg  string                              `json:"errmsg"`
		Records []struct{ ICP, Org, Domain string } `json:"records"`
	}
	if json.Unmarshal(easmOrganizationBody(result), &reply) != nil || reply.IsError || reply.Errmsg != "" || reply.Error != nil && reply.Error != false {
		return nil
	}
	subjects := make(map[string]string)
	addSubject := func(filing, subject string) {
		if previous, exists := subjects[filing]; exists && previous != subject {
			subjects[filing] = ""
		} else if !exists {
			subjects[filing] = subject
		}
	}
	for _, record := range reply.Records {
		if strings.TrimSpace(record.Org) != "" && strings.TrimSpace(record.ICP) != "" && easmDomain(record.Domain) != "" {
			addSubject(record.ICP, record.Org)
		}
	}
	var graph struct {
		Nodes []json.RawMessage `json:"nodes"`
		Edges []json.RawMessage `json:"edges"`
	}
	if json.Unmarshal(state, &graph) != nil || graph.Nodes == nil || graph.Edges == nil {
		return nil
	}
	type nodeIdentity struct{ ID, Kind, Label, FeatureKind string }
	identities := make(map[string]nodeIdentity)
	positions := make(map[string]int)
	companies, registrations, domains := make(map[string][]string), make(map[string][]string), make(map[string][]string)
	var root nodeIdentity
	for i, raw := range graph.Nodes {
		var node nodeIdentity
		if json.Unmarshal(raw, &node) != nil || node.ID == "" || identities[node.ID].ID != "" {
			return nil
		}
		identities[node.ID], positions[node.ID] = node, i
		switch node.Kind {
		case "root":
			if root.ID != "" || node.Label == "" {
				return nil
			}
			root = node
		case "company":
			companies[node.Label] = append(companies[node.Label], node.ID)
		case "feature":
			if node.FeatureKind == "icp" {
				registrations[node.Label] = append(registrations[node.Label], node.ID)
			}
		case "domain":
			if domain := easmDomain(node.Label); domain != "" {
				domains[domain] = append(domains[domain], node.ID)
			}
		}
	}
	if root.ID == "" {
		return nil
	}
	linked := make(map[[2]string]bool)
	for _, raw := range graph.Edges {
		var edge struct {
			Source, Target string
			Confirmed      *bool
		}
		if json.Unmarshal(raw, &edge) != nil || identities[edge.Source].ID == "" || identities[edge.Target].ID == "" {
			return nil
		}
		linked[[2]string{edge.Source, edge.Target}] = true
		if edge.Confirmed != nil && !*edge.Confirmed {
			continue
		}
		for _, pair := range [][2]string{{edge.Source, edge.Target}, {edge.Target, edge.Source}} {
			owner, feature := identities[pair[0]], identities[pair[1]]
			if (owner.Kind == "company" || owner.Kind == "root") && feature.Kind == "feature" && feature.FeatureKind == "icp" {
				addSubject(feature.Label, owner.Label)
			}
		}
	}
	changed := false
	addNode := func(id, kind, label, featureKind string) bool {
		if _, exists := identities[id]; exists {
			return false
		}
		node := map[string]any{"id": id, "kind": kind, "label": label}
		if featureKind != "" {
			node["featureKind"] = featureKind
		}
		if kind == "company" {
			node["detail"] = map[string]string{"reason": "备案主体来自 ICP 查询记录。"}
		}
		raw, _ := json.Marshal(node)
		positions[id] = len(graph.Nodes)
		identities[id] = nodeIdentity{id, kind, label, featureKind}
		graph.Nodes = append(graph.Nodes, raw)
		changed = true
		return true
	}
	addEdge := func(source, target string, confirmed bool) {
		if linked[[2]string{source, target}] || !confirmed && linked[[2]string{target, source}] {
			return
		}
		raw, _ := json.Marshal(map[string]any{"source": source, "target": target, "confirmed": confirmed})
		graph.Edges = append(graph.Edges, raw)
		linked[[2]string{source, target}] = true
		changed = true
	}
	for _, record := range reply.Records {
		domain := easmDomain(record.Domain)
		if strings.TrimSpace(record.Org) == "" || strings.TrimSpace(record.ICP) == "" || domain == "" {
			continue
		}
		owners, filings, matches := companies[record.Org], registrations[record.ICP], domains[domain]
		owner := ""
		if len(owners) == 1 {
			owner = owners[0]
		} else if len(owners) == 0 {
			owner = root.ID
			if record.Org != root.Label {
				owner = "company:icp-org:" + record.Org
				if !addNode(owner, "company", record.Org, "") {
					return nil
				}
				companies[record.Org] = []string{owner}
			}
		}
		if len(filings) == 0 {
			filing := "feature:icp:" + record.ICP
			if !addNode(filing, "feature", record.ICP, "icp") {
				return nil
			}
			filings = []string{filing}
			registrations[record.ICP] = filings
		}
		if len(matches) == 0 {
			domainID := "domain:" + domain
			if !addNode(domainID, "domain", domain, "") {
				return nil
			}
			matches = []string{domainID}
			domains[domain] = matches
		}
		if owner != "" && owner != root.ID {
			addEdge(root.ID, owner, false)
		}
		for _, filing := range filings {
			filingIndex := positions[filing]
			var filingNode map[string]json.RawMessage
			_ = json.Unmarshal(graph.Nodes[filingIndex], &filingNode)
			var filingDetail map[string]json.RawMessage
			if raw := filingNode["detail"]; len(raw) != 0 && json.Unmarshal(raw, &filingDetail) != nil {
				return nil
			}
			if filingDetail == nil {
				filingDetail = make(map[string]json.RawMessage)
			}
			var previousSubject string
			if raw := filingDetail["subject_name"]; len(raw) != 0 && json.Unmarshal(raw, &previousSubject) != nil {
				return nil
			}
			var reason string
			if raw := filingDetail["reason"]; len(raw) != 0 && json.Unmarshal(raw, &reason) != nil {
				return nil
			}
			subject := subjects[record.ICP]
			if previousSubject != subject || strings.TrimSpace(reason) == "" {
				if subject == "" {
					delete(filingDetail, "subject_name")
				} else {
					filingDetail["subject_name"], _ = json.Marshal(subject)
				}
				if strings.TrimSpace(reason) == "" {
					filingDetail["reason"], _ = json.Marshal("备案号和关联域名来自 ICP 查询记录。")
				}
				filingNode["detail"], _ = json.Marshal(filingDetail)
				graph.Nodes[filingIndex], _ = json.Marshal(filingNode)
				changed = true
			}
			if owner == "" {
				addEdge(root.ID, filing, false)
			} else {
				addEdge(owner, filing, true)
			}
			for _, domainID := range matches {
				addEdge(filing, domainID, true)
			}
		}
		for _, domainID := range matches {
			filing := filings[0]
			nodeIndex := positions[domainID]
			var node map[string]json.RawMessage
			_ = json.Unmarshal(graph.Nodes[nodeIndex], &node)
			var detail map[string]json.RawMessage
			if raw := node["detail"]; len(raw) != 0 && json.Unmarshal(raw, &detail) != nil {
				return nil
			}
			if detail == nil {
				detail = make(map[string]json.RawMessage)
			}
			var previous []json.RawMessage
			if raw := detail["features"]; len(raw) != 0 && json.Unmarshal(raw, &previous) != nil {
				return nil
			}
			features := make([]json.RawMessage, 0, len(previous)+1)
			found, featureChanged := false, false
			for _, raw := range previous {
				var feature struct {
					Kind, Value string
					NodeID      string `json:"node_id"`
				}
				if json.Unmarshal(raw, &feature) != nil {
					return nil
				}
				if feature.Kind == "domain" && (feature.NodeID == domainID || easmDomain(feature.Value) == domain) {
					featureChanged = true
					continue
				}
				if feature.Kind == "icp" && feature.Value == record.ICP {
					found = true
					linkedFeature := identities[feature.NodeID]
					if linkedFeature.Kind != "feature" || linkedFeature.FeatureKind != "icp" || linkedFeature.Label != record.ICP {
						var updated map[string]json.RawMessage
						_ = json.Unmarshal(raw, &updated)
						updated["node_id"], _ = json.Marshal(filing)
						raw, _ = json.Marshal(updated)
						featureChanged = true
					}
				}
				features = append(features, raw)
			}
			if !found {
				raw, _ := json.Marshal(map[string]string{"kind": "icp", "value": record.ICP, "node_id": filing})
				features = append(features, raw)
				featureChanged = true
			}
			if featureChanged {
				detail["features"], _ = json.Marshal(features)
				node["detail"], _ = json.Marshal(detail)
				graph.Nodes[nodeIndex], _ = json.Marshal(node)
				changed = true
			}
		}
	}
	if !changed {
		return nil
	}
	// Collection replacement stays below the patch-operation limit for any batch.
	patch, _ := json.Marshal([]map[string]any{
		{"op": "replace", "path": "/nodes", "value": graph.Nodes},
		{"op": "replace", "path": "/edges", "value": graph.Edges},
	})
	return patch
}
