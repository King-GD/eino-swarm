package engine

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strings"

	"github.com/LubyRuffy/eino-swarm/internal/taskpanel"
)

// Agent additions reuse reported entities. Typed MCP projections can preserve
// ambiguous source identities and deliberately bypass this publication guard.
func easmValidateAgentGraph(before, after json.RawMessage) error {
	type node struct {
		ID, Kind, Label, FeatureKind string
		Detail                       struct {
			Serial, Issuer string
			Observations   json.RawMessage
			Features       []struct {
				NodeID string `json:"node_id"`
			}
		}
	}
	var current, proposed struct {
		Nodes []node
		Edges []struct{ Source, Target string }
	}
	if json.Unmarshal(before, &current) != nil || json.Unmarshal(after, &proposed) != nil {
		return fmt.Errorf("%w: malformed EASM graph", taskpanel.ErrInvalid)
	}
	ids := make(map[string]bool)
	companies, domains := make(map[string][]string), make(map[string][]string)
	certificates, services := make(map[[2]string][]string), make(map[[2]string][]string)
	index := func(n node) {
		switch n.Kind {
		case "root", "company":
			name := strings.TrimSpace(n.Label)
			companies[name] = append(companies[name], n.ID)
		case "domain":
			if domain := easmDomain(n.Label); domain != "" {
				domains[domain] = append(domains[domain], n.ID)
			}
		case "feature":
			if n.FeatureKind != "cert" {
				return
			}
			if n.Detail.Serial != "" && n.Detail.Issuer != "" {
				key := [2]string{n.Detail.Issuer, n.Detail.Serial}
				certificates[key] = append(certificates[key], n.ID)
			}
			hosts, _ := easmPublicationCertificateHosts(n.Detail.Observations)
			for _, host := range hosts {
				key := [2]string{strings.ToLower(strings.TrimSuffix(strings.TrimSpace(n.Label), ".")), host}
				services[key] = append(services[key], n.ID)
			}
		}
	}
	for _, n := range current.Nodes {
		ids[n.ID] = true
		index(n)
	}
	proposedIDs := make(map[string]bool)
	for _, n := range proposed.Nodes {
		if proposedIDs[n.ID] {
			return fmt.Errorf("%w: duplicate node ID %q in proposed EASM graph", taskpanel.ErrInvalid, n.ID)
		}
		proposedIDs[n.ID] = true
		if ids[n.ID] {
			continue
		}
		var reuse []string
		switch n.Kind {
		case "company":
			reuse = companies[strings.TrimSpace(n.Label)]
		case "domain":
			if domain := easmDomain(n.Label); domain != "" {
				reuse = domains[domain]
			}
		case "feature":
			if n.FeatureKind != "cert" {
				break
			}
			if strings.TrimSpace(n.Detail.Serial) != "" && strings.TrimSpace(n.Detail.Issuer) != "" {
				reuse = certificates[[2]string{n.Detail.Issuer, n.Detail.Serial}]
			} else {
				hosts, valid := easmPublicationCertificateHosts(n.Detail.Observations)
				seen := make(map[string]bool)
				for _, host := range hosts {
					key := [2]string{strings.ToLower(strings.TrimSuffix(strings.TrimSpace(n.Label), ".")), host}
					for _, id := range services[key] {
						if !seen[id] {
							reuse = append(reuse, id)
							seen[id] = true
						}
					}
				}
				if len(reuse) != 0 {
					break
				}
				if !valid || len(hosts) != 1 {
					return fmt.Errorf("%w: new certificate %q requires reported serial and issuer, or observations for exactly one explicit service host", taskpanel.ErrInvalid, n.ID)
				}
			}
		}
		if len(reuse) != 0 {
			return fmt.Errorf("%w: new node %q duplicates a reported entity; reuse existing node ID(s): %s", taskpanel.ErrInvalid, n.ID, strings.Join(reuse, ", "))
		}
		// Also prevent duplicate identities within one Agent publication.
		index(n)
	}
	for _, edge := range proposed.Edges {
		if !proposedIDs[edge.Source] || !proposedIDs[edge.Target] {
			return fmt.Errorf("%w: edge %q -> %q references a missing node", taskpanel.ErrInvalid, edge.Source, edge.Target)
		}
	}
	for _, n := range proposed.Nodes {
		for _, feature := range n.Detail.Features {
			if feature.NodeID != "" && !proposedIDs[feature.NodeID] {
				return fmt.Errorf("%w: node %q feature references missing node %q", taskpanel.ErrInvalid, n.ID, feature.NodeID)
			}
		}
	}
	return nil
}

func easmPublicationCertificateHosts(raw json.RawMessage) ([]string, bool) {
	var observations []easmCertificateServiceObservation
	if len(raw) != 0 && json.Unmarshal(raw, &observations) != nil {
		return nil, false
	}
	seen := make(map[string]bool)
	var hosts []string
	for _, observation := range observations {
		value := strings.TrimSpace(observation.Host)
		if !strings.Contains(value, "://") {
			value = "//" + value
		}
		service, err := url.Parse(value)
		if err != nil || service.Hostname() == "" || service.User != nil {
			return nil, false
		}
		host := easmDomain(service.Hostname())
		if ip := net.ParseIP(service.Hostname()); ip != nil {
			host = ip.String()
		}
		if host == "" {
			return nil, false
		}
		if port := service.Port(); port != "" {
			host = net.JoinHostPort(host, port)
		}
		if service.Scheme != "" {
			host = strings.ToLower(service.Scheme) + "://" + host
		}
		if !seen[host] {
			hosts = append(hosts, host)
			seen[host] = true
		}
	}
	return hosts, true
}
