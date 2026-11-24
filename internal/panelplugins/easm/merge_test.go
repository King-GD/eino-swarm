package easm

import (
	"context"
	"encoding/json"
	"reflect"
	"sync"
	"testing"
	"time"
)

// mergeDetail is what lets a later batch enrich a node the panel already shows
// without renaming it or dropping evidence another tool contributed.
func TestMergeDetailFillsEmptyFieldsAndUnionsEvidence(t *testing.T) {
	current := &Detail{
		Description:   "first",
		Industry:      []string{"a"},
		Organizations: []Organization{{Name: "Org"}},
		Basis:         []Basis{{Source: "tool_a"}},
		Names:         []string{"a.test"},
		Features:      []Feature{{Kind: "cert", Value: "s1", NodeID: "feature:cert:s1"}},
		Observations:  []Observation{{Host: "a.test"}},
	}
	next := &Detail{
		Description:   "second",
		Website:       "https://subject.test",
		Industry:      []string{"a", "b"},
		Organizations: []Organization{{Name: "Org"}, {Name: "Other"}},
		Basis:         []Basis{{Source: "tool_a"}, {Source: "tool_b", Via: "record"}},
		Names:         []string{"b.test"},
		Features:      []Feature{{Kind: "cert", Value: "s1", NodeID: "feature:cert:s1"}, {Kind: "cert", Value: "s2"}},
		Observations:  []Observation{{Host: "b.test"}},
	}
	merged := mergeDetail(current, next)
	if merged.Description != "first" || merged.Website != "https://subject.test" {
		t.Fatalf("merged = %+v", merged)
	}
	if !reflect.DeepEqual(merged.Industry, []string{"a", "b"}) || !reflect.DeepEqual(merged.Names, []string{"a.test", "b.test"}) {
		t.Fatalf("lists = %v %v", merged.Industry, merged.Names)
	}
	if len(merged.Organizations) != 2 || len(merged.Basis) != 2 || len(merged.Features) != 2 || len(merged.Observations) != 2 {
		t.Fatalf("evidence = %+v", merged)
	}
	if mergeDetail(nil, next) == nil || mergeDetail(next, nil).Description != "second" {
		t.Fatal("a missing side was not handled")
	}
	if got := unionStrings([]string{"a"}, []string{"", "a"}); !reflect.DeepEqual(got, []string{"a"}) {
		t.Fatalf("unionStrings = %v", got)
	}
}

// The canvas keeps one node per identity: a second record enriches it and never
// renames it.
func TestUpsertKeepsTheFirstLabelAndMergesDetail(t *testing.T) {
	g := NewGraph("Subject", nil)
	first := g.Upsert(Node{ID: "company:name:A", Kind: "company", Label: "A", Detail: &Detail{Share: "10%"}})
	if first == nil {
		t.Fatal("node was not added")
	}
	second := g.Upsert(Node{ID: "company:name:A", Kind: "company", Label: "A (renamed)", Detail: &Detail{Relation: "branch"}})
	if second.Label != "A" || second.Detail.Share != "10%" || second.Detail.Relation != "branch" {
		t.Fatalf("node = %+v", second)
	}
	if g.Upsert(Node{ID: "", Kind: "company", Label: "A"}) != nil || g.Upsert(Node{ID: "x", Label: ""}) != nil {
		t.Fatal("a node without identity was added")
	}
	nodes, edges := g.Len()
	if nodes != 2 || edges != 0 {
		t.Fatalf("len = %d %d", nodes, edges)
	}
	ipCount := int64(3)
	g.Upsert(Node{ID: "company:name:A", Kind: "company", Label: "A", IPCount: &ipCount})
	if node := g.Node("company:name:A"); node.IPCount == nil || *node.IPCount != 3 {
		t.Fatalf("ip count = %+v", node)
	}
	if g.Root() == nil || g.Node("missing") != nil {
		t.Fatal("lookup changed")
	}
	if _, ok := g.Parent("company:name:A"); ok {
		t.Fatal("a node without an edge reported a parent")
	}
}

// A link only ever adds a first parent, upgrades it, or is refused.
func TestLinkKeepsOneParentAndRefusesCycles(t *testing.T) {
	g := NewGraph("Subject", nil)
	g.Upsert(Node{ID: "company:name:A", Kind: "company", Label: "A"})
	g.Upsert(Node{ID: "company:name:B", Kind: "company", Label: "B"})
	g.Upsert(Node{ID: "domain:a.test", Kind: "domain", Label: "a.test"})
	if !g.Link(RootID, "company:name:A", false) {
		t.Fatal("the first edge was refused")
	}
	if g.Link(RootID, "company:name:A", false) || g.Link("company:name:B", "company:name:A", true) {
		t.Fatal("a second parent was accepted")
	}
	if !g.Link(RootID, "company:name:A", true) {
		t.Fatal("the confirmation was refused")
	}
	if g.Link(RootID, "company:name:A", true) {
		t.Fatal("an already confirmed edge reported a change")
	}
	if !g.Link("company:name:A", "company:name:B", true) || !g.Link("company:name:A", "domain:a.test", true) {
		t.Fatal("the child edge was not added")
	}
	// B → A would close a cycle now that A → B exists.
	if g.Link("company:name:B", "company:name:A", true) {
		t.Fatal("a cycle was accepted")
	}
	if g.Link(RootID, "missing", true) || g.Link(RootID, RootID, true) {
		t.Fatal("an impossible edge was accepted")
	}
	if parent, ok := g.Parent("domain:a.test"); !ok || parent != "company:name:A" {
		t.Fatalf("parent = %q %v", parent, ok)
	}
}

// Restore keeps insertion order, so a continued portrait draws the nodes in the
// order the previous publications established.
func TestRestoreKeepsThePublishedOrder(t *testing.T) {
	state := State{Title: "Subject", Nodes: []Node{
		{ID: RootID, Kind: "root", Label: "Subject"},
		{ID: "domain:a.test", Kind: "domain", Label: "a.test"},
		{ID: "company:name:A", Kind: "company", Label: "A"},
	}, Edges: []Edge{{Source: RootID, Target: "domain:a.test", Confirmed: true}, {Source: RootID, Target: "company:name:A"}}}
	g, err := Restore(state)
	if err != nil {
		t.Fatal(err)
	}
	nodes, edges := g.Snapshot()
	if len(nodes) != 3 || nodes[1].ID != "domain:a.test" || len(edges) != 2 || edges[0].Target != "domain:a.test" {
		t.Fatalf("nodes=%+v edges=%+v", nodes, edges)
	}
	if parent, ok := g.Parent("company:name:A"); !ok || parent != RootID {
		t.Fatalf("parent = %q %v", parent, ok)
	}
	if g.DomainCount() != 1 {
		t.Fatalf("domains = %d", g.DomainCount())
	}
}

// Domain identity is the normalized name; anything that is not a DNS host is
// refused so two reports of one host cannot become two nodes.
func TestNormalizeDomainAcceptsOnlyHostNames(t *testing.T) {
	for raw, want := range map[string]string{
		"Example.TEST.":   "example.test",
		" a.test ":        "a.test",
		"xn--fiqs8s.test": "xn--fiqs8s.test",
		"中文.test":         "xn--fiq228c.test",
		"a-b.test":        "a-b.test",
		"":                "",
		"localhost":       "",
		"1.2.3.4":         "",
		"bad_domain.test": "",
		"-a.test":         "",
		"a-.test":         "",
		"a..test":         "",
		"a.test/":         "",
		"例子.test":         "xn--fsqu00a.test",
	} {
		if got := NormalizeDomain(raw); got != want {
			t.Fatalf("NormalizeDomain(%q) = %q, want %q", raw, got, want)
		}
	}
	long := ""
	for len(long) < 64 {
		long += "a"
	}
	if NormalizeDomain(long+".test") != "" {
		t.Fatal("an over-long label was accepted")
	}
	if DomainID("a.test") != "domain:a.test" {
		t.Fatal("DomainID changed")
	}
}

// settings.CallTimeout bounds one remote call, so a stalled aggregation cannot
// hold the panel open for the server's own timeout.
func TestCallTimeoutBoundsOneRemoteCall(t *testing.T) {
	var mu sync.Mutex
	hasDeadline := true
	caller := CallerFunc(func(ctx context.Context, _ string, _ map[string]any) (json.RawMessage, error) {
		_, observed := ctx.Deadline()
		mu.Lock()
		hasDeadline = hasDeadline && observed
		mu.Unlock()
		return json.RawMessage(`{"size":1}`), nil
	})
	settings := onlyRoles(RoleStats)
	settings.CallTimeout = time.Minute
	var out published
	if _, err := Run(t.Context(), Input{Subject: "Subject", Query: `org="Subject"`}, settings, nil, caller, out.publish); err != nil {
		t.Fatal(err)
	}
	if !hasDeadline {
		t.Fatal("the remote call ran without the configured deadline")
	}
}
