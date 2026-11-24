package easm

import (
	"encoding/json"
	"fmt"
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
	nodes    []*Node
	byID     map[string]*Node
	edges    []*Edge
	parent   map[string]*Edge
	detached map[string]bool
}

// NewGraph starts a portrait at its subject.
func NewGraph(subject string, detail *Detail) *Graph {
	g := &Graph{byID: map[string]*Node{}, parent: map[string]*Edge{}}
	g.Upsert(Node{ID: RootID, Kind: "root", Label: subject, Detail: detail})
	return g
}

// Restore keeps saved observations and their first sources. Only nodes
// reachable from the confirmed subject can be sources for later findings.
func Restore(state State) (*Graph, error) {
	g, err := loadGraph(state)
	if err != nil {
		return nil, err
	}
	g.keepRootedSources(len(g.edges))
	return g, nil
}

func loadGraph(state State) (*Graph, error) {
	g := &Graph{byID: map[string]*Node{}, parent: map[string]*Edge{}}
	roots := 0
	for _, node := range state.Nodes {
		if node.ID == "" || node.Label == "" {
			return nil, fmt.Errorf("panel state has a node without identity")
		}
		if _, duplicate := g.byID[node.ID]; duplicate {
			return nil, fmt.Errorf("panel state repeats node %q", node.ID)
		}
		added := node
		g.nodes = append(g.nodes, &added)
		g.byID[node.ID] = &added
		if node.Kind == "root" {
			roots++
		}
	}
	if roots != 1 || g.byID[RootID] == nil || g.byID[RootID].Kind != "root" {
		return nil, fmt.Errorf("panel state has no single subject root")
	}
	for _, edge := range state.Edges {
		// A saved clue can outlive its source. Keep its raw edge and let
		// root reachability exclude it until that source exists again.
		if edge.Source == "" || g.byID[edge.Target] == nil {
			return nil, fmt.Errorf("panel state has a dangling edge")
		}
		if edge.Source == edge.Target || edge.Target == RootID {
			return nil, fmt.Errorf("panel state has an edge into the subject root")
		}
		if _, second := g.parent[edge.Target]; second {
			return nil, fmt.Errorf("panel state gives %q a second parent", edge.Target)
		}
		added := edge
		g.edges = append(g.edges, &added)
		g.parent[edge.Target] = &added
	}
	return g, nil
}

func (g *Graph) reachable() map[string]bool {
	children := make(map[string][]string)
	for _, edge := range g.edges {
		children[edge.Source] = append(children[edge.Source], edge.Target)
	}
	queue := []string{RootID}
	reached := map[string]bool{RootID: true}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		for _, child := range children[id] {
			if !reached[child] {
				reached[child] = true
				queue = append(queue, child)
			}
		}
	}
	return reached
}

// Keep the raw historic relationships, but do not accept new relationships
// from a batch that couldn't establish a path to the subject.
func (g *Graph) keepRootedSources(savedEdges int) {
	reached := g.reachable()
	g.detached = make(map[string]bool)
	for id := range g.byID {
		if !reached[id] {
			g.detached[id] = true
		}
	}
	edges := g.edges[:savedEdges]
	for _, edge := range g.edges[savedEdges:] {
		if reached[edge.Target] {
			edges = append(edges, edge)
		} else {
			delete(g.parent, edge.Target)
		}
	}
	g.edges = edges
}

// ValidateGraphUpdate checks new relations and preserves the first sources of
// the same subject and their confirmation, including historical detached links.
func ValidateGraphUpdate(previous, state json.RawMessage) error {
	var before, after State
	if err := json.Unmarshal(previous, &before); err != nil {
		return err
	}
	if err := json.Unmarshal(state, &after); err != nil {
		return err
	}
	if len(after.Nodes) == 0 && len(after.Edges) == 0 {
		if len(before.Edges) != 0 {
			return fmt.Errorf("panel state with saved sources requires a subject root")
		}
		return nil
	}
	graph, err := loadGraph(after)
	if err != nil {
		return err
	}
	oldSources := make(map[string]Edge, len(before.Edges))
	for _, edge := range before.Edges {
		oldSources[edge.Target] = edge
	}
	root := graph.Root()
	previousNodes := make(map[string]bool, len(before.Nodes))
	var samePortrait bool
	for _, node := range before.Nodes {
		previousNodes[node.ID] = true
		if node.ID == RootID {
			samePortrait = sameSubject(node, root.Label, registryID(root))
		}
	}
	reached := graph.reachable()
	for _, edge := range after.Edges {
		prior, existed := oldSources[edge.Target]
		lostSource := graph.byID[edge.Source] == nil && previousNodes[edge.Source]
		if !reached[edge.Target] && (!existed || prior != edge || lostSource) {
			return fmt.Errorf("panel state gives %q a source outside the subject's graph", edge.Target)
		}
	}
	if samePortrait {
		for _, prior := range before.Edges {
			current := graph.parent[prior.Target]
			if current == nil || current.Source != prior.Source {
				return fmt.Errorf("panel state must retain the first source of %q", prior.Target)
			}
			if prior.Confirmed && !current.Confirmed {
				return fmt.Errorf("panel state must retain confirmation of %q", prior.Target)
			}
		}
	} else {
		before.Nodes = nil
	}
	return validateCertificateSources(before.Nodes, after.Nodes)
}

func validateCertificateSources(before, after []Node) error {
	firstSaved, saved := make(map[string]string), make(map[string]string)
	for i := range before {
		if serial := certificateSerial(&before[i]); serial != "" {
			id := before[i].ID
			saved[id] = serial
			if firstSaved[serial] == "" {
				firstSaved[serial] = id
			}
		}
	}
	first := make(map[string]string)
	for i := range after {
		node := &after[i]
		serial := certificateSerial(node)
		if serial == "" {
			continue
		}
		// Old duplicates may stay in the raw state. They cannot become
		// the first observation through reordering or a new duplicate.
		if first[serial] == "" {
			if id := firstSaved[serial]; id != "" && id != node.ID {
				return fmt.Errorf("panel state changes the first observation of a certificate")
			}
			first[serial] = node.ID
		} else if saved[node.ID] != serial {
			return fmt.Errorf("panel state repeats a certificate serial")
		}
	}
	return nil
}

// Root returns the subject node.
func (g *Graph) Root() *Node { return g.byID[RootID] }

// Node returns an existing node or nil.
func (g *Graph) Node(id string) *Node { return g.byID[id] }

// Parent returns the source of a node's incoming edge, if any.
func (g *Graph) Parent(id string) (string, bool) {
	edge, ok := g.parent[id]
	if !ok || g.detached[id] {
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
	if source == target || target == RootID || g.byID[source] == nil || g.byID[target] == nil || g.detached[source] {
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
	delete(g.detached, target)
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

// DomainCount reports the domain nodes of the whole portrait, which is what the
// configured domain budget bounds.
func (g *Graph) DomainCount() int {
	count := 0
	for _, node := range g.nodes {
		if node.Kind == "domain" {
			count++
		}
	}
	return count
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
	fill(&current.Relation, next.Relation)
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
