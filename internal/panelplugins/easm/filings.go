package easm

import (
	"context"
	"strings"
)

func init() {
	stepFuncs[StepFilings] = stepDef{roles: []string{RoleFilings}, fn: (*run).filings}
}

// filingPageSize is the number of filing records requested per organization.
const filingPageSize = 20

// filingFlushEvery publishes progress after this many organization lookups.
const filingFlushEvery = 3

type filingRecord struct {
	ICP       string `json:"icp"`
	Org       string `json:"org"`
	Domain    string `json:"domain"`
	UpdatedAt string `json:"updated_at"`
}

// filings looks up the ICP filings registered by the subject and by the
// companies already confirmed on the graph. Each filing hangs under its
// registrant and owns the domains it registers: registrant → filing → domain.
func (r *run) filings(ctx context.Context) error {
	subjects := map[string]string{}
	for i, name := range r.filingOrganizations() {
		if i > 0 && i%filingFlushEvery == 0 {
			if err := r.flush(ctx); err != nil {
				return err
			}
		}
		var reply struct {
			Records []filingRecord `json:"records"`
		}
		if err := r.call(ctx, RoleFilings, map[string]any{"query_type": "org", "value": name, "size": filingPageSize}, &reply); err != nil {
			return err
		}
		for _, record := range reply.Records {
			r.addFiling(record, subjects)
		}
	}
	return nil
}

// filingOrganizations lists the subject and the confirmed companies under
// it, bounded by MaxDomains so a large group cannot exhaust the quota.
func (r *run) filingOrganizations() []string {
	names := []string{r.graph.Root().Label}
	seen := map[string]bool{names[0]: true}
	nodes, _ := r.graph.Snapshot()
	for _, node := range nodes {
		if len(names) >= r.settings.MaxDomains {
			break
		}
		if node.Kind != "company" || seen[node.Label] {
			continue
		}
		if edge := r.graph.parent[node.ID]; edge != nil && edge.Confirmed {
			seen[node.Label] = true
			names = append(names, node.Label)
		}
	}
	return names
}

// addFiling applies one record. subjects tracks each filing's registrant over
// the whole step: a filing reported for two different organizations keeps
// an empty subject name so neither claim is shown as the registrant.
func (r *run) addFiling(record filingRecord, subjects map[string]string) {
	org, number := strings.TrimSpace(record.Org), strings.TrimSpace(record.ICP)
	domain := NormalizeDomain(record.Domain)
	if org == "" || number == "" || domain == "" {
		return
	}
	date := strings.TrimSpace(record.UpdatedAt)
	filingID := "feature:icp:" + number
	if previous, ok := subjects[filingID]; !ok {
		subjects[filingID] = org
	} else if previous != org {
		subjects[filingID] = ""
	}
	owner := ""
	switch owners := ownersNamed(r.graph, org); len(owners) {
	case 0:
		owner = "company:icp-org:" + org
		r.graph.Upsert(Node{ID: owner, Kind: "company", Label: org})
		r.graph.AddBasis(owner, Basis{Source: RoleFilings, Via: number, Date: date})
		r.graph.Link(RootID, owner, false)
	case 1:
		owner = owners[0]
	}
	filing := r.graph.Upsert(Node{ID: filingID, Kind: "feature", FeatureKind: "icp", Label: number})
	r.graph.AddBasis(filingID, Basis{Source: RoleFilings, Via: domain, Date: date})
	filing.Detail.SubjectName = subjects[filingID]
	if owner == "" {
		// Several same-name registrants: the filing stays a pending clue of
		// the subject rather than being attributed to one of them.
		r.graph.Link(RootID, filingID, false)
	} else {
		r.graph.Link(owner, filingID, true)
	}
	domainID := DomainID(domain)
	if r.graph.Node(domainID) == nil {
		r.graph.AddDomain(filingID, domain, true, Basis{Source: RoleFilings, Via: number, Date: date})
	} else {
		r.graph.AddBasis(domainID, Basis{Source: RoleFilings, Via: number, Date: date})
		r.graph.Link(filingID, domainID, true)
	}
	r.graph.Upsert(Node{ID: domainID, Label: domain, Detail: &Detail{Features: []Feature{{Kind: "icp", Value: number, NodeID: filingID}}}})
	r.graph.Upsert(Node{ID: filingID, Label: number, Detail: &Detail{Features: []Feature{{Kind: "domain", Value: domain, NodeID: domainID}}}})
}

// ownersNamed returns the root or company nodes labelled exactly name.
func ownersNamed(g *Graph, name string) []string {
	var out []string
	for _, node := range g.nodes {
		if (node.Kind == "root" || node.Kind == "company") && node.Label == name {
			out = append(out, node.ID)
		}
	}
	return out
}
