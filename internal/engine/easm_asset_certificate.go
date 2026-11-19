package engine

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	jsonpatch "github.com/evanphx/json-patch"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Asset lookup names its TLS fields directly. Root-domain hints remain pending
// and never turn an IP service into a deployment under that domain.
func easmAssetCertificatePatch(state, arguments json.RawMessage, result *mcp.CallToolResult) json.RawMessage {
	if result == nil || result.IsError {
		return nil
	}
	var request map[string]json.RawMessage
	var reply struct {
		Error   any
		IsError bool
		Errmsg  string
		Results []struct {
			Domain, Host string
			TLS          struct{ Certificate map[string]json.RawMessage }
		}
	}
	if json.Unmarshal(arguments, &request) != nil || request == nil || json.Unmarshal(easmOrganizationBody(result), &reply) != nil || reply.IsError || reply.Errmsg != "" || reply.Error != nil && reply.Error != false {
		return nil
	}
	type observation struct {
		ID, Serial, Issuer, Note string
		Domain                   string
		RootHint                 bool
	}
	var observations []observation
	var rows []map[string]json.RawMessage
	for _, asset := range reply.Results {
		certificate := asset.TLS.Certificate
		if len(certificate) == 0 {
			continue
		}
		var subjects, issuers, roots []string
		if raw := certificate["subject_orgs"]; len(raw) != 0 && json.Unmarshal(raw, &subjects) != nil {
			continue
		}
		if raw := certificate["issuer_orgs"]; len(raw) != 0 && json.Unmarshal(raw, &issuers) != nil {
			continue
		}
		if raw := certificate["root_domains"]; len(raw) != 0 && json.Unmarshal(raw, &roots) != nil {
			continue
		}
		host, hostDomain := easmAssetCertificateHost(asset.Host)
		domains := []string{}
		domain := easmDomain(asset.Domain)
		if domain == "" {
			domain = hostDomain
		}
		rootHint := domain == ""
		if domain != "" {
			domains = append(domains, domain)
		} else {
			seen := make(map[string]bool)
			for _, value := range roots {
				if root := easmDomain(value); root != "" && !seen[root] {
					seen[root] = true
					domains = append(domains, root)
				}
			}
		}
		if len(domains) == 0 {
			domains = append(domains, "")
		}
		if len(subjects) == 0 {
			subjects = append(subjects, "")
		}
		issuer := easmCertificateString(certificate, "issuer_cn")
		if issuer == "" {
			issuer = strings.Join(issuers, " / ")
		}
		note := "证书来源 mcp_fofa_asset_lookup"
		if host != "" {
			note += "；来源服务 " + host
		}
		if rootHint && domains[0] != "" {
			note += "；根域线索来自 tls.certificate.root_domains"
		}
		for _, domain := range domains {
			for _, subject := range subjects {
				row := make(map[string]json.RawMessage)
				for key, value := range map[string]string{"domain": domain, "host": host, "cert.subject.org": subject, "cert.issuer.org": issuer} {
					row[key], _ = json.Marshal(value)
				}
				observedHost := host
				if observedHost == "" && !rootHint {
					observedHost = domain
				}
				row["_zwai_certificate_host"], _ = json.Marshal(observedHost)
				row["_zwai_certificate_source"] = json.RawMessage(`"mcp_fofa_asset_lookup"`)
				if len(subjects) > 1 {
					// One source certificate can name multiple subject organizations.
					allSubjects, _ := json.Marshal(subjects)
					row["_zwai_certificate_subjects"], _ = json.Marshal(string(allSubjects))
				}
				if rootHint && host != "" {
					// The returned root domain is a clue. The observed service owns
					// the local certificate identity, independently of those hints.
					row["_zwai_certificate_asset"], _ = json.Marshal(host)
				}
				for source, field := range map[string]string{
					"serial_number": "cert.serial", "subject_cn": "cert.subject.cn", "not_before": "cert.not_before", "not_after": "cert.not_after",
					"is_valid": "cert.is_valid", "is_match": "cert.is_match", "is_expired": "cert.is_expired", "sha256": "cert.sha256", "fingerprint": "cert.fingerprint", "id": "cert.id",
				} {
					if raw := certificate[source]; len(raw) != 0 {
						row[field] = raw
					}
				}
				rows = append(rows, row)
				if identity, _ := easmCertificateIdentity(row, domain); identity != "" {
					observations = append(observations, observation{
						ID: fmt.Sprintf("feature:cert:%x", sha256.Sum256([]byte(identity))), Serial: easmCertificateString(row, "cert.serial"),
						Issuer: issuer, Note: note, Domain: domain, RootHint: rootHint,
					})
				}
			}
		}
	}
	if len(rows) == 0 {
		return nil
	}
	base := easmCertificateRowsPatch(state, rows)
	updated := state
	if len(base) != 0 {
		patch, err := jsonpatch.DecodePatch(base)
		if err != nil {
			return nil
		}
		updated, err = patch.Apply(state)
		if err != nil {
			return nil
		}
	}
	var graph struct {
		Nodes []map[string]json.RawMessage `json:"nodes"`
		Edges []map[string]json.RawMessage `json:"edges"`
	}
	if json.Unmarshal(updated, &graph) != nil || graph.Nodes == nil || graph.Edges == nil {
		return nil
	}
	positions := make(map[string]int)
	details := make(map[string]map[string]json.RawMessage)
	domainIDs := make(map[string][]string)
	rootCount := 0
	for i, node := range graph.Nodes {
		id := easmCertificateString(node, "id")
		if id == "" {
			return nil
		}
		if _, duplicate := positions[id]; duplicate {
			return nil
		}
		positions[id] = i
		if easmCertificateString(node, "kind") == "root" {
			rootCount++
		}
		var detail map[string]json.RawMessage
		if raw := node["detail"]; len(raw) != 0 && json.Unmarshal(raw, &detail) != nil {
			return nil
		}
		if detail == nil {
			detail = make(map[string]json.RawMessage)
		}
		details[id] = detail
		if easmCertificateString(node, "kind") == "domain" {
			name := easmDomain(easmCertificateString(node, "label"))
			domainIDs[name] = append(domainIDs[name], id)
		}
	}
	if rootCount != 1 {
		return nil
	}
	var previous struct {
		Edges []struct {
			Source, Target string
			Confirmed      bool
		}
	}
	_ = json.Unmarshal(state, &previous)
	confirmed := make(map[[2]string]bool)
	for _, edge := range previous.Edges {
		confirmed[[2]string{edge.Source, edge.Target}] = edge.Confirmed
	}
	changed := len(base) != 0
	for _, observed := range observations {
		ids := []string{}
		if _, exists := positions[observed.ID]; exists {
			ids = append(ids, observed.ID)
		} else if observed.Serial != "" && observed.Issuer != "" {
			for id, detail := range details {
				node := graph.Nodes[positions[id]]
				if easmCertificateString(node, "featureKind") == "cert" && easmCertificateString(detail, "serial") == observed.Serial && easmCertificateString(detail, "issuer") == observed.Issuer {
					ids = append(ids, id)
				}
			}
		}
		for _, id := range ids {
			var reason string
			if raw := details[id]["reason"]; len(raw) != 0 && json.Unmarshal(raw, &reason) != nil {
				return nil
			}
			if !strings.Contains("\n"+reason+"\n", "\n"+observed.Note+"\n") {
				if reason != "" {
					reason += "\n"
				}
				details[id]["reason"], _ = json.Marshal(reason + observed.Note)
				graph.Nodes[positions[id]]["detail"], _ = json.Marshal(details[id])
				changed = true
			}
			if observed.RootHint && observed.Domain != "" {
				for _, target := range domainIDs[observed.Domain] {
					for _, edge := range graph.Edges {
						sourceID, targetID := easmCertificateString(edge, "source"), easmCertificateString(edge, "target")
						if (sourceID == id && targetID == target || sourceID == target && targetID == id) && string(edge["confirmed"]) == "true" && !confirmed[[2]string{sourceID, targetID}] {
							edge["confirmed"] = json.RawMessage(`false`)
							changed = true
						}
					}
				}
			}
		}
	}
	if !changed {
		return nil
	}
	patch, _ := json.Marshal([]map[string]any{{"op": "replace", "path": "/nodes", "value": graph.Nodes}, {"op": "replace", "path": "/edges", "value": graph.Edges}})
	return patch
}

func easmAssetCertificateHost(value string) (string, string) {
	if !strings.Contains(value, "://") {
		value = "//" + value
	}
	host, err := url.Parse(value)
	if err != nil || host.Hostname() == "" {
		return "", ""
	}
	source := host.Host
	if host.Scheme != "" {
		source = host.Scheme + "://" + source
	}
	return source, easmDomain(host.Hostname())
}
