package easm

import (
	"context"
	"strings"
)

func init() {
	stepFuncs[StepAssetRules] = stepDef{roles: []string{RoleAssetRules}, fn: (*run).assetRules}
}

// assetRules asks which ownership rules match the graph's domains. A rule
// carries only a reported name and creation time, never a company type or
// identity, so it is domain evidence. Only an unambiguous root or company
// with exactly that name receives a confirmed rule-based edge; an unknown
// name creates nothing and implies no relationship to the target.
func (r *run) assetRules(ctx context.Context) error {
	domains := r.graph.Domains()
	if len(domains) > r.settings.MaxDomains {
		domains = domains[:r.settings.MaxDomains]
	}
	if len(domains) == 0 {
		return nil
	}
	requested := make(map[string]bool, len(domains))
	for _, domain := range domains {
		requested[domain] = true
	}
	var reply struct {
		Results []struct {
			Domain string `json:"domain"`
			Names  []struct {
				Name string `json:"name"`
				Time string `json:"time"`
			} `json:"names"`
		} `json:"results"`
	}
	if err := r.call(ctx, RoleAssetRules, map[string]any{"domains": domains}, &reply); err != nil {
		return err
	}
	for _, result := range reply.Results {
		domain := NormalizeDomain(result.Domain)
		// A row for a domain that was not asked about is not evidence for
		// anything on the graph.
		if !requested[domain] {
			continue
		}
		id := DomainID(domain)
		for _, rule := range result.Names {
			name := strings.TrimSpace(rule.Name)
			if name == "" {
				continue
			}
			r.graph.AddBasis(id, Basis{Source: RoleAssetRules, Via: name, Date: strings.TrimSpace(rule.Time)})
			if owners := ownersNamed(r.graph, name); len(owners) == 1 {
				r.graph.Link(owners[0], id, true)
			}
		}
	}
	return nil
}
