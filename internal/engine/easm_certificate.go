package engine

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"

	jsonpatch "github.com/evanphx/json-patch"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Search fields identify certificate observations. Without a global certificate
// identity, the observation belongs to one domain and is never merged across it.
func easmCertificatePatch(state, arguments json.RawMessage, result *mcp.CallToolResult) json.RawMessage {
	rows := easmCertificateRows(arguments, result)
	for _, row := range rows {
		row["_zwai_certificate_source"] = json.RawMessage(`"mcp_fofa_search"`)
		delete(row, "_zwai_certificate_host")
	}
	return easmCertificateRowsPatch(state, rows)
}

func easmCertificateRowsPatch(state json.RawMessage, rows []map[string]json.RawMessage) json.RawMessage {
	if len(rows) == 0 {
		return nil
	}
	var graph struct {
		Nodes []map[string]json.RawMessage `json:"nodes"`
		Edges []map[string]json.RawMessage `json:"edges"`
	}
	if json.Unmarshal(state, &graph) != nil || graph.Nodes == nil || graph.Edges == nil {
		return nil
	}
	positions := make(map[string]int)
	details := make(map[string]map[string]json.RawMessage)
	domains, organizations := make(map[string][]string), make(map[string][]string)
	certificates := make(map[string][]string)
	root := ""
	for i, node := range graph.Nodes {
		id, kind := easmCertificateString(node, "id"), easmCertificateString(node, "kind")
		if id == "" {
			return nil
		}
		if _, duplicate := positions[id]; duplicate {
			return nil
		}
		positions[id] = i
		var detail map[string]json.RawMessage
		if raw := node["detail"]; len(raw) != 0 && json.Unmarshal(raw, &detail) != nil {
			return nil
		}
		if detail == nil {
			detail = make(map[string]json.RawMessage)
		}
		details[id] = detail
		switch kind {
		case "root":
			if root != "" || easmCertificateString(node, "label") == "" {
				return nil
			}
			root = id
		case "domain":
			if name := easmDomain(easmCertificateString(node, "label")); name != "" {
				domains[name] = append(domains[name], id)
			}
		case "feature":
			if easmCertificateString(node, "featureKind") == "org" {
				label := easmCertificateString(node, "label")
				organizations[label] = append(organizations[label], id)
			} else if easmCertificateString(node, "featureKind") == "cert" {
				serial, issuer := easmCertificateString(detail, "serial"), easmCertificateString(detail, "issuer")
				if serial != "" && issuer != "" {
					key, _ := json.Marshal([]string{issuer, serial})
					certificates[string(key)] = append(certificates[string(key)], id)
				}
			}
		}
	}
	if root == "" {
		return nil
	}
	linked := make(map[[2]string]int)
	for i, edge := range graph.Edges {
		source, target := easmCertificateString(edge, "source"), easmCertificateString(edge, "target")
		if _, exists := positions[source]; !exists {
			return nil
		}
		if _, exists := positions[target]; !exists {
			return nil
		}
		linked[[2]string{source, target}] = i
	}
	changed := false
	addNode := func(id, kind, label, featureKind string) bool {
		if position, exists := positions[id]; exists {
			node := graph.Nodes[position]
			return easmCertificateString(node, "kind") == kind && easmCertificateString(node, "featureKind") == featureKind
		}
		node := make(map[string]json.RawMessage)
		for key, value := range map[string]string{"id": id, "kind": kind, "label": label} {
			node[key], _ = json.Marshal(value)
		}
		if featureKind != "" {
			node["featureKind"], _ = json.Marshal(featureKind)
		}
		positions[id] = len(graph.Nodes)
		details[id] = make(map[string]json.RawMessage)
		graph.Nodes = append(graph.Nodes, node)
		changed = true
		return true
	}
	addEdge := func(source, target string, confirmed, downgrade bool) {
		position, exists := linked[[2]string{source, target}]
		if !exists {
			position, exists = linked[[2]string{target, source}]
		}
		if exists {
			if downgrade && !confirmed && string(graph.Edges[position]["confirmed"]) == "true" {
				graph.Edges[position]["confirmed"] = json.RawMessage(`false`)
				changed = true
			}
			return
		}
		edge := make(map[string]json.RawMessage)
		edge["source"], _ = json.Marshal(source)
		edge["target"], _ = json.Marshal(target)
		edge["confirmed"], _ = json.Marshal(confirmed)
		linked[[2]string{source, target}] = len(graph.Edges)
		graph.Edges = append(graph.Edges, edge)
		changed = true
	}
	setDetail := func(id string, values map[string]json.RawMessage) {
		for key, value := range values {
			if !jsonpatch.Equal(details[id][key], value) {
				details[id][key] = value
				changed = true
			}
		}
		graph.Nodes[positions[id]]["detail"], _ = json.Marshal(details[id])
	}
	for _, row := range rows {
		domainValue := easmCertificateString(row, "domain")
		domain := easmDomain(domainValue)
		cn, organization := easmCertificateString(row, "cert.subject.cn"), easmCertificateString(row, "cert.subject.org")
		issuer := easmCertificateString(row, "cert.issuer.org")
		serial := easmCertificateString(row, "cert.serial")
		if serial == "" {
			serial = easmCertificateString(row, "cert.serial_number")
		}
		before, after := easmCertificateString(row, "cert.not_before"), easmCertificateString(row, "cert.not_after")
		identity, local := easmCertificateIdentity(row, domain)
		if domainValue != "" && domain == "" || len(domains[domain]) > 1 || identity == "" && organization == "" {
			continue
		}
		domainID := ""
		if domain != "" {
			domainID = "domain:" + domain
			if len(domains[domain]) == 1 {
				domainID = domains[domain][0]
			}
			if !addNode(domainID, "domain", domain, "") {
				return nil
			}
			domains[domain] = []string{domainID}
		}
		isValid, isMatch := easmCertificateFlag(easmCertificateValue(row, "cert.is_valid")), easmCertificateFlag(easmCertificateValue(row, "cert.is_match"))
		confirmed := (isValid == nil || *isValid) && (isMatch == nil || *isMatch)
		orgID := ""
		if organization != "" && len(organizations[organization]) <= 1 {
			orgID = "feature:cert-org:" + organization
			if len(organizations[organization]) == 1 {
				orgID = organizations[organization][0]
			}
			if !addNode(orgID, "feature", organization, "org") {
				return nil
			}
			organizations[organization] = []string{orgID}
			addEdge(root, orgID, false, false)
		}
		if identity == "" {
			if orgID != "" && domainID != "" {
				addEdge(orgID, domainID, confirmed, true)
			}
			continue
		}
		certificateID := fmt.Sprintf("feature:cert:%x", sha256.Sum256([]byte(identity)))
		if _, exists := positions[certificateID]; !exists && serial != "" && issuer != "" {
			key, _ := json.Marshal([]string{issuer, serial})
			if matches := certificates[string(key)]; len(matches) == 1 {
				certificateID = matches[0]
			}
		}
		label := cn
		if label == "" {
			label = serial
			if label == "" {
				label = identity
			}
		}
		if !addNode(certificateID, "feature", label, "cert") {
			return nil
		}
		metadata := make(map[string]json.RawMessage)
		for key, value := range map[string]string{"serial": serial, "issuer": issuer, "not_before": before, "not_after": after, "subject_name": organization} {
			if value != "" {
				metadata[key], _ = json.Marshal(value)
			}
		}
		if local && len(details[certificateID]["reason"]) == 0 {
			metadata["reason"], _ = json.Marshal("来源返回的资产内证书观察，未提供可跨资产合并的证书标识。")
		}
		observationChanged, valid := easmCertificateObserve(details[certificateID], row, domain)
		if !valid {
			return nil
		}
		changed = changed || observationChanged
		setDetail(certificateID, metadata)
		confirmed = confirmed && string(details[certificateID]["is_valid"]) == "true" && string(details[certificateID]["is_match"]) == "true"
		if orgID != "" {
			addEdge(orgID, certificateID, confirmed, true)
		} else {
			addEdge(root, certificateID, false, false)
		}
		if domainID == "" {
			continue
		}
		addEdge(certificateID, domainID, confirmed, true)
		var features []map[string]json.RawMessage
		if raw := details[domainID]["features"]; len(raw) != 0 && json.Unmarshal(raw, &features) != nil {
			return nil
		}
		found := false
		for _, feature := range features {
			found = found || easmCertificateString(feature, "kind") == "cert" && easmCertificateString(feature, "node_id") == certificateID
		}
		if !found {
			feature := make(map[string]json.RawMessage)
			for key, value := range map[string]string{"kind": "cert", "value": label, "node_id": certificateID} {
				feature[key], _ = json.Marshal(value)
			}
			features = append(features, feature)
			raw, _ := json.Marshal(features)
			setDetail(domainID, map[string]json.RawMessage{"features": raw})
		}
	}
	if !changed {
		return nil
	}
	var original map[string]json.RawMessage
	_ = json.Unmarshal(state, &original)
	nodes, _ := json.Marshal(graph.Nodes)
	edges, _ := json.Marshal(graph.Edges)
	if jsonpatch.Equal(original["nodes"], nodes) && jsonpatch.Equal(original["edges"], edges) {
		return nil
	}
	patch, _ := json.Marshal([]map[string]any{{"op": "replace", "path": "/nodes", "value": graph.Nodes}, {"op": "replace", "path": "/edges", "value": graph.Edges}})
	return patch
}

func easmCertificateIdentity(row map[string]json.RawMessage, domain string) (string, bool) {
	for _, field := range []string{"cert.sha256", "cert.fingerprint", "cert.id"} {
		value := easmCertificateString(row, field)
		if value == "" && field == "cert.id" {
			value = easmProductID(easmCertificateValue(row, field))
		}
		if value != "" {
			return field + ":" + value, false
		}
	}
	serial := easmCertificateString(row, "cert.serial")
	if serial == "" {
		serial = easmCertificateString(row, "cert.serial_number")
	}
	issuer := easmCertificateString(row, "cert.issuer.org")
	if serial != "" && issuer != "" {
		raw, _ := json.Marshal([]string{issuer, serial})
		return "issuer-serial:" + string(raw), false
	}
	cn := easmCertificateString(row, "cert.subject.cn")
	asset, prefix := domain, "domain-observation:"
	if observed := easmCertificateString(row, "_zwai_certificate_asset"); observed != "" {
		asset, prefix = observed, "asset-observation:"
	} else if asset == "" {
		asset, prefix = easmCertificateString(row, "host"), "asset-observation:"
	}
	if asset == "" || cn == "" && serial == "" {
		return "", false
	}
	subject := easmCertificateString(row, "_zwai_certificate_subjects")
	if subject == "" {
		subject = easmCertificateString(row, "cert.subject.org")
	}
	raw, _ := json.Marshal([]string{asset, cn, subject, issuer, serial, easmCertificateString(row, "cert.not_before"), easmCertificateString(row, "cert.not_after")})
	return prefix + string(raw), true
}

func easmCertificateRows(arguments json.RawMessage, result *mcp.CallToolResult) []map[string]json.RawMessage {
	if result == nil || result.IsError {
		return nil
	}
	var request struct{ Fields json.RawMessage }
	var reply struct {
		Error   any
		IsError bool
		Errmsg  string
		Fields  json.RawMessage
		Results []json.RawMessage
	}
	if json.Unmarshal(arguments, &request) != nil || json.Unmarshal(easmOrganizationBody(result), &reply) != nil || reply.IsError || reply.Errmsg != "" || reply.Error != nil && reply.Error != false {
		return nil
	}
	fields := easmCertificateFields(request.Fields)
	if len(reply.Fields) != 0 {
		fields = easmCertificateFields(reply.Fields)
	}
	var rows []map[string]json.RawMessage
	for _, raw := range reply.Results {
		var row map[string]json.RawMessage
		if json.Unmarshal(raw, &row) == nil && row != nil {
			rows = append(rows, row)
			continue
		}
		var values []json.RawMessage
		if len(fields) == 0 || json.Unmarshal(raw, &values) != nil || len(values) != len(fields) {
			continue
		}
		row = make(map[string]json.RawMessage, len(fields))
		for i, field := range fields {
			row[field] = values[i]
		}
		rows = append(rows, row)
	}
	return rows
}

func easmCertificateFields(raw json.RawMessage) []string {
	var fields []string
	if json.Unmarshal(raw, &fields) != nil {
		var text string
		if json.Unmarshal(raw, &text) != nil {
			return nil
		}
		fields = strings.Split(text, ",")
	}
	seen := make(map[string]bool)
	for i, field := range fields {
		field = strings.TrimSpace(field)
		if field == "" || seen[field] {
			return nil
		}
		fields[i], seen[field] = field, true
	}
	return fields
}

func easmCertificateValue(row map[string]json.RawMessage, field string) json.RawMessage {
	if value, exists := row[field]; exists {
		return value
	}
	parts := strings.SplitN(field, ".", 2)
	if len(parts) != 2 {
		return nil
	}
	var nested map[string]json.RawMessage
	if json.Unmarshal(row[parts[0]], &nested) != nil {
		return nil
	}
	return easmCertificateValue(nested, parts[1])
}

func easmCertificateString(row map[string]json.RawMessage, field string) string {
	var value string
	_ = json.Unmarshal(easmCertificateValue(row, field), &value)
	return value
}

func easmCertificateFlag(raw json.RawMessage) *bool {
	var flag bool
	if json.Unmarshal(raw, &flag) == nil && string(raw) != "null" {
		return &flag
	}
	var text string
	if json.Unmarshal(raw, &text) == nil && (text == "true" || text == "false") {
		flag = text == "true"
		return &flag
	}
	return nil
}
