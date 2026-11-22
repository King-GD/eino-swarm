package easm

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
	"unicode"
)

func init() {
	stepFuncs[StepRelations] = stepDef{roles: []string{RoleRelations}, fn: (*run).relations}
}

// registryRecord is one company row of the corporate registry tools. Up-tree
// investors and nested children are not decoded: they describe ownership
// above or beyond the direct relationship and never widen the asset scope.
type registryRecord struct {
	ID        json.RawMessage `json:"id"`
	Name      string          `json:"name"`
	Label     string          `json:"label"`
	RegStatus string          `json:"reg_status"`
	Percent   string          `json:"percent"`
}

// company reports whether the row is a legal company with an exact identity.
func (record registryRecord) company() (string, string, bool) {
	name := strings.TrimSpace(record.Name)
	id := registryID(record.ID)
	return id, name, record.Label == "Company" && name != "" && id != ""
}

// relations records the subject's legal parent and its direct investments.
// The reply's root is the legal entity the keyword resolved to; it is the
// subject itself only when its name is the subject's and nothing on the
// graph already claims a different registration for the root.
func (r *run) relations(ctx context.Context) error {
	subject := r.graph.Root().Label
	var reply struct {
		Root *registryRecord  `json:"root"`
		Down []registryRecord `json:"down"`
	}
	if err := r.call(ctx, RoleRelations, map[string]any{"keyword": subject}, &reply); err != nil {
		return err
	}
	if reply.Root == nil || reply.Down == nil {
		return fmt.Errorf("relations reply has no root or down list")
	}
	parentID, parentName, ok := reply.Root.company()
	if !ok {
		return fmt.Errorf("relations reply root is not an identified company")
	}
	names := map[string]map[string]bool{}
	for _, record := range append([]registryRecord{*reply.Root}, reply.Down...) {
		if id, name, ok := record.company(); ok {
			if names[name] == nil {
				names[name] = map[string]bool{}
			}
			names[name][id] = true
		}
	}
	unique := func(name string) bool { return len(names[name]) == 1 }

	parent := r.graph.registryNode(parentID)
	if parent == "" {
		root := r.graph.Root()
		if parentName == subject && unique(parentName) && (root.Detail == nil || root.Detail.RegistryID == "") && r.graph.namedCompanies(parentName) <= 1 {
			parent = RootID
		} else if parent = r.graph.unboundCompany(parentName); parent == "" || !unique(parentName) {
			parent = registryNodeID(parentID)
		}
	}
	r.graph.Upsert(Node{ID: parent, Kind: "company", Label: parentName, Detail: &Detail{RegistryID: parentID, RegistryName: parentName, Status: strings.TrimSpace(reply.Root.RegStatus)}})
	r.graph.AddBasis(parent, Basis{Source: "relations"})
	if parent != RootID {
		r.graph.Link(RootID, parent, false)
	}

	for _, record := range reply.Down {
		id, name, ok := record.company()
		if !ok || id == parentID {
			continue
		}
		child := r.graph.registryNode(id)
		if child == "" {
			if child = r.graph.unboundCompany(name); child == "" || !unique(name) {
				child = registryNodeID(id)
			}
		}
		if child == RootID || child == parent {
			continue
		}
		r.graph.Upsert(Node{ID: child, Kind: "company", Label: name, Detail: &Detail{RegistryID: id, RegistryName: name, Status: strings.TrimSpace(record.RegStatus), Share: sharePercent(record.Percent)}})
		r.graph.AddBasis(child, Basis{Source: "relations", Via: parentName})
		// The registry states this direct investment itself.
		r.graph.Link(parent, child, true)
	}
	return nil
}

func registryNodeID(id string) string { return "company:reg:" + id }

func nameNodeID(name string) string { return "company:name:" + name }

// registryNode returns the company or root already holding a registry ID.
func (g *Graph) registryNode(id string) string {
	for _, node := range g.nodes {
		if (node.Kind == "company" || node.Kind == "root") && node.Detail != nil && node.Detail.RegistryID == id {
			return node.ID
		}
	}
	return ""
}

// namedCompanies counts the company nodes carrying exactly this name.
func (g *Graph) namedCompanies(name string) int {
	count := 0
	for _, node := range g.nodes {
		if node.Kind == "company" && node.Label == name {
			count++
		}
	}
	return count
}

// unboundCompany returns the only company with this exact name when it has
// no registry identity yet, e.g. a clue from the organization lookup, so one
// legal company does not appear twice. Several candidates bind none.
func (g *Graph) unboundCompany(name string) string {
	found := ""
	for _, node := range g.nodes {
		if node.Kind != "company" || node.Label != name {
			continue
		}
		if found != "" || node.Detail != nil && node.Detail.RegistryID != "" {
			return ""
		}
		found = node.ID
	}
	return found
}

// registryID accepts a registry identity as an opaque string or an exact
// non-negative integer. Fractional, exponent or rounded numbers are rejected
// because two registrations must never collapse into one node.
func registryID(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var value string
	if json.Unmarshal(raw, &value) == nil {
		value = strings.TrimSpace(value)
		if strings.ContainsFunc(value, unicode.IsControl) {
			return ""
		}
		return value
	}
	var number big.Int
	if _, ok := number.SetString(string(raw), 10); !ok || number.Sign() < 0 {
		return ""
	}
	return number.String()
}

// sharePercent converts the registry's 0-1 ratio into an exact percentage.
// Anything that is not such a ratio is dropped rather than shown as-is.
func sharePercent(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || strings.ContainsAny(value, "eE/+-") {
		return ""
	}
	ratio, ok := new(big.Rat).SetString(value)
	if !ok || ratio.Cmp(big.NewRat(1, 1)) > 0 {
		return ""
	}
	scale := 0
	if decimal := strings.IndexByte(value, '.'); decimal >= 0 {
		scale = len(value) - decimal - 1
	}
	percentage := ratio.Mul(ratio, big.NewRat(100, 1)).FloatString(scale)
	if strings.Contains(percentage, ".") {
		percentage = strings.TrimRight(strings.TrimRight(percentage, "0"), ".")
	}
	return percentage + "%"
}
