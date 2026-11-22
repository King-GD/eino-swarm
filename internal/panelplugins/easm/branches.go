package easm

import (
	"context"
	"fmt"
	"strings"
)

func init() {
	stepFuncs[StepBranches] = stepDef{roles: []string{RoleBranches}, fn: (*run).branches}
}

const (
	branchPageSize = 20
	// branchMaxPages bounds one run's lookups for a company with many
	// branches; the panel shows a portrait, not a complete registry export.
	branchMaxPages = 5
)

// branches lists the registered branches of the root's legal entity. The
// lookup uses the verified registry ID, then the legal name, so a changed
// display name still reaches the same registration; those branches are
// confirmed. A lookup by the display name alone may resolve to another
// registration, so its branches stay pending clues.
func (r *run) branches(ctx context.Context) error {
	root := r.graph.Root()
	keyword, via, confirmed := root.Label, root.Label, false
	if detail := root.Detail; detail != nil && detail.RegistryID != "" {
		keyword, confirmed = detail.RegistryID, true
		if detail.RegistryName != "" {
			via = detail.RegistryName
		}
	} else if detail != nil && detail.RegistryName != "" {
		keyword, via, confirmed = detail.RegistryName, detail.RegistryName, true
	}
	var parentRegistry string
	if root.Detail != nil {
		parentRegistry = root.Detail.RegistryID
	}

	// A later page's failure keeps the branches already returned on the
	// graph but still fails the step, so an incomplete list is visible.
	var items []registryRecord
	var pageErr error
	for page := 1; page <= branchMaxPages; page++ {
		var reply struct {
			Total *int64           `json:"total"`
			Items []registryRecord `json:"items"`
		}
		if err := r.call(ctx, RoleBranches, map[string]any{"keyword": keyword, "page_num": page, "page_size": branchPageSize}, &reply); err != nil {
			pageErr = err
			break
		}
		if reply.Items == nil {
			pageErr = fmt.Errorf("branches reply has no items list")
			break
		}
		items = append(items, reply.Items...)
		if len(reply.Items) < branchPageSize || reply.Total != nil && int64(len(items)) >= *reply.Total {
			break
		}
	}

	names := map[string]map[string]bool{}
	for _, item := range items {
		name := strings.TrimSpace(item.Name)
		key := registryID(item.ID)
		if key == "" {
			key = nameNodeID(name)
		}
		if names[name] == nil {
			names[name] = map[string]bool{}
		}
		names[name][key] = true
	}
	for _, item := range items {
		name := strings.TrimSpace(item.Name)
		id := registryID(item.ID)
		if name == "" || id != "" && id == parentRegistry {
			continue
		}
		node := ""
		if id != "" {
			node = r.graph.registryNode(id)
		}
		if node == "" && len(names[name]) == 1 {
			node = r.graph.unboundCompany(name)
		}
		if node == "" && id != "" {
			node = registryNodeID(id)
		}
		if node == "" {
			node = nameNodeID(name)
		}
		if node == RootID {
			continue
		}
		detail := &Detail{RegistryName: name, Status: strings.TrimSpace(item.RegStatus)}
		if id != "" {
			detail.RegistryID = id
		}
		r.graph.Upsert(Node{ID: node, Kind: "company", Label: name, Detail: detail})
		r.graph.AddBasis(node, Basis{Source: "branches", Via: via})
		r.graph.Link(RootID, node, confirmed)
	}
	return pageErr
}
