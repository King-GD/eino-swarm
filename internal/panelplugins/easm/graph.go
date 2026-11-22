package easm

import (
	"net"
	"strings"

	"golang.org/x/net/idna"
)

// RootID is the fixed identity of the portrait subject.
const RootID = "root"

// Graph is the producer's in-memory portrait. It keeps the published tree
// invariants in one place: one root without incoming edges, and every other
// node keeps the first source that reached it. A later observation may
// confirm that same edge or enrich node details, never re-parent a node.
type Graph struct {
	nodes  []*Node
	byID   map[string]*Node
	edges  []*Edge
	parent map[string]*Edge
}

// NewGraph starts a portrait at its subject.
func NewGraph(subject string, detail *Detail) *Graph {
	g := &Graph{byID: map[string]*Node{}, parent: map[string]*Edge{}}
	g.Upsert(Node{ID: RootID, Kind: "root", Label: subject, Detail: detail})
	return g
}

// Root returns the subject node.
func (g *Graph) Root() *Node { return g.byID[RootID] }

// Node returns an existing node or nil.
func (g *Graph) Node(id string) *Node { return g.byID[id] }

// Parent returns the source of a node's incoming edge, if any.
func (g *Graph) Parent(id string) (string, bool) {
	edge, ok := g.parent[id]
	if !ok {
		return "", false
	}
	return edge.Source, true
}

// Upsert adds a node or merges its detail into the existing one. The first
// label wins so a node's identity on the canvas stays stable.
func (g *Graph) Upsert(node Node) *Node {
	if node.ID == "" || node.Label == "" {
		return nil
	}
	if existing, ok := g.byID[node.ID]; ok {
		if node.IPCount != nil {
			existing.IPCount = node.IPCount
		}
		existing.Detail = mergeDetail(existing.Detail, node.Detail)
		return existing
	}
	added := node
	g.nodes = append(g.nodes, &added)
	g.byID[node.ID] = &added
	return &added
}

// Link adds source → target. It reports whether the graph changed. An
// existing edge from the same source can only be upgraded to confirmed.
func (g *Graph) Link(source, target string, confirmed bool) bool {
	if source == target || target == RootID || g.byID[source] == nil || g.byID[target] == nil {
		return false
	}
	if edge, ok := g.parent[target]; ok {
		if edge.Source == source && confirmed && !edge.Confirmed {
			edge.Confirmed = true
			return true
		}
		return false
	}
	if g.reaches(target, source) {
		return false
	}
	edge := &Edge{Source: source, Target: target, Confirmed: confirmed}
	g.edges = append(g.edges, edge)
	g.parent[target] = edge
	return true
}

// reaches reports whether from is an ancestor of to, which would close a cycle.
func (g *Graph) reaches(from, to string) bool {
	for current, seen := to, 0; seen <= len(g.nodes); seen++ {
		if current == from {
			return true
		}
		edge, ok := g.parent[current]
		if !ok {
			return false
		}
		current = edge.Source
	}
	return true
}

// AddBasis appends a distinct piece of evidence to a node.
func (g *Graph) AddBasis(id string, basis Basis) {
	node := g.byID[id]
	if node == nil || basis.Source == "" {
		return
	}
	if node.Detail == nil {
		node.Detail = &Detail{}
	}
	for _, existing := range node.Detail.Basis {
		if existing == basis {
			return
		}
	}
	node.Detail.Basis = append(node.Detail.Basis, basis)
}

// Domains returns the normalized domains on the graph in insertion order.
func (g *Graph) Domains() []string {
	var out []string
	for _, node := range g.nodes {
		if node.Kind == "domain" {
			out = append(out, node.Label)
		}
	}
	return out
}

// Len returns the node and edge counts.
func (g *Graph) Len() (int, int) { return len(g.nodes), len(g.edges) }

// Snapshot copies nodes and edges into published form.
func (g *Graph) Snapshot() ([]Node, []Edge) {
	nodes := make([]Node, len(g.nodes))
	for i, node := range g.nodes {
		nodes[i] = *node
	}
	edges := make([]Edge, len(g.edges))
	for i, edge := range g.edges {
		edges[i] = *edge
	}
	return nodes, edges
}

// DomainID is the node identity of a normalized domain.
func DomainID(domain string) string { return "domain:" + domain }

// AddDomain places a domain under source. A malformed name is ignored.
func (g *Graph) AddDomain(source, raw string, confirmed bool, basis Basis) (string, bool) {
	domain := NormalizeDomain(raw)
	if domain == "" {
		return "", false
	}
	id := DomainID(domain)
	g.Upsert(Node{ID: id, Kind: "domain", Label: domain})
	g.AddBasis(id, basis)
	g.Link(source, id, confirmed)
	return id, true
}

// NormalizeDomain returns the lowercase ASCII form of a host name, or "" for
// IPs and names that are not valid DNS labels. Every source uses this one
// normalization so an IDN reaches the same node from any lookup.
func NormalizeDomain(value string) string {
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

// mergeDetail fills empty fields of current from next and unions lists.
func mergeDetail(current, next *Detail) *Detail {
	if next == nil {
		return current
	}
	if current == nil {
		merged := *next
		return &merged
	}
	fill := func(dst *string, src string) {
		if *dst == "" {
			*dst = src
		}
	}
	fill(&current.Description, next.Description)
	fill(&current.Logo, next.Logo)
	fill(&current.Website, next.Website)
	fill(&current.Region, next.Region)
	fill(&current.RegistryID, next.RegistryID)
	fill(&current.RegistryName, next.RegistryName)
	fill(&current.OrganizationID, next.OrganizationID)
	fill(&current.Status, next.Status)
	fill(&current.Share, next.Share)
	fill(&current.Serial, next.Serial)
	fill(&current.Issuer, next.Issuer)
	fill(&current.NotBefore, next.NotBefore)
	fill(&current.NotAfter, next.NotAfter)
	fill(&current.SubjectName, next.SubjectName)
	if next.IsValid != nil {
		current.IsValid = next.IsValid
	}
	if next.IsMatch != nil {
		current.IsMatch = next.IsMatch
	}
	current.Industry = unionStrings(current.Industry, next.Industry)
	current.Names = unionStrings(current.Names, next.Names)
	for _, basis := range next.Basis {
		if !containsBasis(current.Basis, basis) {
			current.Basis = append(current.Basis, basis)
		}
	}
	for _, organization := range next.Organizations {
		if !containsOrganization(current.Organizations, organization.Name) {
			current.Organizations = append(current.Organizations, organization)
		}
	}
	for _, feature := range next.Features {
		if !containsFeature(current.Features, feature) {
			current.Features = append(current.Features, feature)
		}
	}
	current.Observations = append(current.Observations, next.Observations...)
	return current
}

func unionStrings(current, next []string) []string {
	for _, value := range next {
		found := false
		for _, existing := range current {
			if existing == value {
				found = true
				break
			}
		}
		if !found && value != "" {
			current = append(current, value)
		}
	}
	return current
}

func containsBasis(list []Basis, basis Basis) bool {
	for _, existing := range list {
		if existing == basis {
			return true
		}
	}
	return false
}

func containsOrganization(list []Organization, name string) bool {
	for _, existing := range list {
		if existing.Name == name {
			return true
		}
	}
	return false
}

func containsFeature(list []Feature, feature Feature) bool {
	for _, existing := range list {
		if existing.Kind == feature.Kind && existing.Value == feature.Value {
			return true
		}
	}
	return false
}
