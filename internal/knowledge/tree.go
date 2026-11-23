package knowledge

import (
	"fmt"
	"slices"
	"time"
)

const maxTreeNodes = 10000

type Tree struct {
	View      string     `json:"view"`
	Nodes     []TreeNode `json:"nodes"`
	Issues    []Issue    `json:"issues,omitempty"`
	Truncated bool       `json:"truncated,omitempty"`
}

type TreeNode struct {
	ID       string     `json:"id"`
	Label    string     `json:"label"`
	Kind     string     `json:"kind"`
	Type     string     `json:"type,omitempty"`
	Aliases  []string   `json:"aliases,omitempty"`
	Cycle    bool       `json:"cycle,omitempty"`
	Children []TreeNode `json:"children,omitempty"`
}

func (s *Snapshot) Tree(id string, now time.Time) (Tree, error) {
	if !s.Enabled() {
		return Tree{}, ErrDisabled
	}
	if id == "" {
		id = typeView
	}
	tree := Tree{View: id, Nodes: []TreeNode{}}
	switch id {
	case typeView:
		grouped := make(map[string][]TreeNode)
		for _, id := range s.ordered {
			page := s.pages[id].page
			grouped[page.Type] = append(grouped[page.Type], pageNode(page))
		}
		for _, kind := range s.schema.Types {
			tree.Nodes = append(tree.Nodes, TreeNode{ID: kind.ID, Label: kind.ID, Kind: "type", Children: grouped[kind.ID]})
		}
	case pendingView:
		tree.Issues = s.Lint(now)
	default:
		view, ok := s.index.views[id]
		if !ok {
			return Tree{}, fmt.Errorf("%w: view %q", ErrNotFound, id)
		}
		counter := treeCounter{remaining: maxTreeNodes}
		path := map[string]bool{}
		for _, id := range s.ordered {
			if s.pages[id].page.Type == view.RootType && counter.take() {
				tree.Nodes = append(tree.Nodes, s.viewNode(id, view, view.Depth, path, &counter))
			}
		}
		tree.Truncated = counter.truncated
	}
	return tree, nil
}

type treeCounter struct {
	remaining int
	truncated bool
}

func (c *treeCounter) take() bool {
	if c.remaining == 0 {
		c.truncated = true
		return false
	}
	c.remaining--
	return true
}

func pageNode(page Page) TreeNode {
	return TreeNode{ID: page.ID, Label: page.Title, Kind: "page", Type: page.Type, Aliases: slices.Clone(page.Aliases)}
}

func (s *Snapshot) viewNode(id string, view View, depth int, path map[string]bool, counter *treeCounter) TreeNode {
	node := pageNode(s.pages[id].page)
	if path[id] {
		node.Cycle = true
		return node
	}
	if depth == 0 {
		return node
	}
	path[id] = true
	defer delete(path, id)
	for _, rel := range view.Follow {
		group := TreeNode{ID: rel, Label: rel, Kind: "relation"}
		relation := s.index.relations[rel]
		for _, out := range s.outgoing[id] {
			if out.Relation != rel || !resolvedEndpoint(out, relation) {
				continue
			}
			if (len(group.Children) == 0 && !counter.take()) || !counter.take() {
				break
			}
			group.Children = append(group.Children, s.viewNode(out.Target, view, depth-1, path, counter))
		}
		if len(group.Children) > 0 {
			node.Children = append(node.Children, group)
		}
	}
	return node
}
