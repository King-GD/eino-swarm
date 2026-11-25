package knowledge

import "slices"

// Graph is the read-only projection of one project wiki.
type Graph struct {
	Nodes []GraphNode `json:"nodes"`
	Edges []GraphEdge `json:"edges"`
}

type GraphNode struct {
	PageRef
	Aliases []string `json:"aliases,omitempty"`
}

type GraphEdge struct {
	Source    string   `json:"source"`
	Target    string   `json:"target"`
	Relations []string `json:"relations,omitempty"`
	Wikilink  bool     `json:"wikilink,omitempty"`
}

// Graph keeps one node per browsable page and one undirected edge per pair.
// The reverse indexes already exclude missing targets and invalid relations.
func (s *Snapshot) Graph() (Graph, error) {
	if !s.Enabled() {
		return Graph{}, ErrDisabled
	}
	graph := Graph{Nodes: make([]GraphNode, 0, len(s.ordered)), Edges: []GraphEdge{}}
	byPair := make(map[[2]string]int)
	edgeFor := func(a, b string) *GraphEdge {
		pair := [2]string{min(a, b), max(a, b)}
		index, ok := byPair[pair]
		if !ok {
			index = len(graph.Edges)
			byPair[pair] = index
			graph.Edges = append(graph.Edges, GraphEdge{Source: pair[0], Target: pair[1]})
		}
		return &graph.Edges[index]
	}
	for _, target := range s.ordered {
		if s.reviewOnly[target] {
			continue
		}
		page := s.pages[target].page
		graph.Nodes = append(graph.Nodes, GraphNode{PageRef: page.ref(), Aliases: slices.Clone(page.Aliases)})
		for _, link := range s.incoming[target] {
			if s.reviewOnly[link.Target] {
				continue
			}
			edge := edgeFor(link.Target, target)
			if !slices.Contains(edge.Relations, link.Relation) {
				edge.Relations = append(edge.Relations, link.Relation)
			}
		}
		for _, source := range s.mentions[target] {
			if !s.reviewOnly[source.ID] {
				edgeFor(source.ID, target).Wikilink = true
			}
		}
	}
	return graph, nil
}
