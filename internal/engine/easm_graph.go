package engine

import (
	"encoding/json"
	"net"
	"strings"

	"github.com/LubyRuffy/eino-swarm/internal/taskpanel"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/net/idna"
)

// Organization lookup records provide their own domain associations. Candidate
// entities remain distinct until their relationship to the target is confirmed.
func easmOrganizationPatch(state json.RawMessage, result *mcp.CallToolResult) json.RawMessage {
	if result == nil || result.IsError {
		return nil
	}
	var graph struct {
		Nodes []struct{ ID, Kind, Label string }
		Edges []struct{ Source, Target string }
	}
	if json.Unmarshal(state, &graph) != nil || graph.Nodes == nil || graph.Edges == nil {
		return nil
	}
	rootID, rootName := "", ""
	ids := make(map[string]string, len(graph.Nodes))
	domains := make(map[string][]string)
	companies := make(map[string][]string)
	for _, node := range graph.Nodes {
		if node.ID == "" || ids[node.ID] != "" {
			return nil
		}
		ids[node.ID] = node.Kind
		if node.Kind == "company" {
			companies[node.Label] = append(companies[node.Label], node.ID)
		}
		if node.Kind == "root" {
			if rootID != "" || node.Label == "" {
				return nil
			}
			rootID, rootName = node.ID, node.Label
		}
		if node.Kind == "domain" {
			if domain := easmDomain(node.Label); domain != "" {
				domains[domain] = append(domains[domain], node.ID)
			}
		}
	}
	if rootID == "" {
		return nil
	}
	body := easmOrganizationBody(result)
	var reply struct {
		Error   any    `json:"error"`
		IsError bool   `json:"isError"`
		Errmsg  string `json:"errmsg"`
		Results []struct {
			ID      json.RawMessage           `json:"id"`
			Name    string                    `json:"name"`
			Domains []struct{ Domain string } `json:"domains"`
		} `json:"results"`
	}
	if json.Unmarshal(body, &reply) != nil || reply.IsError || reply.Errmsg != "" || (reply.Error != nil && reply.Error != false) {
		return nil
	}
	names := make(map[string]int)
	namedIdentities := make(map[[2]string]bool)
	identities := make([]string, len(reply.Results))
	identityNames := make(map[string]string)
	conflicted := make(map[string]bool)
	rootMatches := make(map[string]bool)
	for i, organization := range reply.Results {
		if strings.TrimSpace(organization.Name) == "" {
			continue
		}
		identities[i] = easmOrganizationIdentity(organization.ID, organization.Name)
		key := [2]string{organization.Name, identities[i]}
		if identities[i] != "" && !namedIdentities[key] {
			names[organization.Name]++
			namedIdentities[key] = true
		}
		if previous, exists := identityNames[identities[i]]; exists && previous != organization.Name {
			conflicted[identities[i]] = true
		}
		identityNames[identities[i]] = organization.Name
	}
	for i, organization := range reply.Results {
		if identities[i] != "" && !conflicted[identities[i]] && organization.Name == rootName {
			rootMatches[identities[i]] = true
		}
	}
	linked := make(map[[2]string]bool)
	for _, edge := range graph.Edges {
		linked[[2]string{edge.Source, edge.Target}] = true
	}
	type operation struct {
		Op    string `json:"op"`
		Path  string `json:"path"`
		Value any    `json:"value"`
	}
	var operations []operation
	addEdge := func(source, target string, confirmed bool) {
		key, reverse := [2]string{source, target}, [2]string{target, source}
		if linked[key] || !confirmed && linked[reverse] {
			return
		}
		linked[key] = true
		operations = append(operations, operation{"add", "/edges/-", map[string]any{"source": source, "target": target, "confirmed": confirmed}})
	}
	for i, organization := range reply.Results {
		owner := identities[i]
		if owner == "" {
			continue
		}
		// Older task snapshots may already identify the company with a local ID.
		matching := companies[organization.Name]
		if ids[owner] == "" && !conflicted[owner] && names[organization.Name] == 1 && len(matching) == 1 && !strings.HasPrefix(matching[0], "company:fofa-org:") {
			owner = matching[0]
		}
		if organization.Name == rootName && len(rootMatches) == 1 && len(matching) == 0 && ids[owner] == "" {
			owner = rootID
		}
		if owner != rootID {
			if kind := ids[owner]; kind != "" && kind != "company" {
				return nil
			}
			if ids[owner] == "" {
				ids[owner] = "company"
				reason := "主体及域名关联来自该组织的检索记录。"
				if conflicted[owner] {
					reason += "同一来源主体编号返回多个名称，名称待核验。"
				}
				operations = append(operations, operation{"add", "/nodes/-", map[string]any{
					"id": owner, "kind": "company", "label": organization.Name,
					"detail": map[string]string{"reason": reason},
				}})
			}
			addEdge(rootID, owner, false)
		}
		for _, entry := range organization.Domains {
			domain := easmDomain(entry.Domain)
			if domain == "" {
				continue
			}
			matches := domains[domain]
			if len(matches) == 0 {
				id := "domain:" + domain
				if ids[id] != "" {
					return nil
				}
				matches = []string{id}
				domains[domain], ids[id] = matches, "domain"
				operations = append(operations, operation{"add", "/nodes/-", map[string]string{"id": id, "kind": "domain", "label": domain}})
			}
			for _, id := range matches {
				addEdge(owner, id, true)
			}
		}
	}
	if len(operations) == 0 {
		return nil
	}
	if len(operations) > taskpanel.MaxPatchOperations {
		var accumulated struct {
			Nodes []json.RawMessage `json:"nodes"`
			Edges []json.RawMessage `json:"edges"`
		}
		// The graph was validated above. Raw entries retain unknown fields and
		// numeric precision while replacing collections within the operation limit.
		_ = json.Unmarshal(state, &accumulated)
		for _, appendOperation := range operations {
			value, _ := json.Marshal(appendOperation.Value)
			if appendOperation.Path == "/nodes/-" {
				accumulated.Nodes = append(accumulated.Nodes, value)
			} else {
				accumulated.Edges = append(accumulated.Edges, value)
			}
		}
		operations = nil
		if len(accumulated.Nodes) != len(graph.Nodes) {
			operations = append(operations, operation{"replace", "/nodes", accumulated.Nodes})
		}
		if len(accumulated.Edges) != len(graph.Edges) {
			operations = append(operations, operation{"replace", "/edges", accumulated.Edges})
		}
	}
	patch, _ := json.Marshal(operations)
	return patch
}

func easmOrganizationIdentity(raw json.RawMessage, name string) string {
	if len(raw) == 0 || string(raw) == "null" {
		return "company:fofa-org-name:" + name
	}
	var id string
	if json.Unmarshal(raw, &id) == nil {
		if strings.TrimSpace(id) == "" {
			return ""
		}
		return "company:fofa-org:" + id
	}
	var number json.Number
	if json.Unmarshal(raw, &number) != nil {
		return ""
	}
	for _, char := range number.String() {
		if char < '0' || char > '9' {
			return ""
		}
	}
	return "company:fofa-org:" + number.String()
}

func easmOrganizationBody(result *mcp.CallToolResult) json.RawMessage {
	if result.StructuredContent != nil {
		body, _ := json.Marshal(result.StructuredContent)
		return body
	}
	if len(result.Content) != 1 {
		return nil
	}
	text, ok := result.Content[0].(*mcp.TextContent)
	if !ok || text == nil {
		return nil
	}
	return json.RawMessage(text.Text)
}

func easmDomain(value string) string {
	domain, err := idna.Lookup.ToASCII(strings.TrimSuffix(strings.TrimSpace(value), "."))
	domain = strings.ToLower(domain)
	if err != nil || len(domain) > 253 || !strings.Contains(domain, ".") || net.ParseIP(domain) != nil {
		return ""
	}
	for _, label := range strings.Split(domain, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return ""
		}
		for _, char := range label {
			if !(char >= 'a' && char <= 'z' || char >= '0' && char <= '9' || char == '-') {
				return ""
			}
		}
	}
	return domain
}
