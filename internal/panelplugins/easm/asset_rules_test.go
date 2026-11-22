package easm

import (
	"fmt"
	"testing"
)

// Rule names are domain evidence. Only a name that exactly and uniquely
// matches a subject or company on the graph may claim the domain; a product
// or an unknown organization name must not invent a company or ownership.
func TestAssetRulesConfirmOnlyKnownUniqueOwners(t *testing.T) {
	graph := NewGraph("Subject Co", nil)
	graph.Upsert(Node{ID: "company:north", Kind: "company", Label: "North Co"})
	graph.Link(RootID, "company:north", false)
	graph.Upsert(Node{ID: "company:south", Kind: "company", Label: "South Co"})
	graph.Link(RootID, "company:south", false)
	graph.AddDomain("company:north", "north.test", false, Basis{Source: RoleOrganizations})
	graph.AddDomain(RootID, "south.test", false, Basis{Source: RoleOrganizations})
	graph.AddDomain(RootID, "product.test", false, Basis{Source: RoleOrganizations})
	caller := &fakeCaller{replies: map[string]func(map[string]any) (string, error){
		RoleAssetRules: fixed(`{"error":false,"errmsg":"","results":[
			{"domain":"NORTH.test.","names":[{"name":"North Co","time":"2026.10.31"},{"name":" "}]},
			{"domain":"south.test","names":[{"name":"South Co","time":"2026.11.19"}]},
			{"domain":"product.test","names":[{"name":"Some Product","time":"2026.11.04"}]},
			{"domain":"unasked.test","names":[{"name":"North Co"}]}]}`),
	}}
	var out published
	r := stepRun(graph, onlyRoles(RoleAssetRules), caller, &out)
	if err := r.assetRules(t.Context()); err != nil {
		t.Fatal(err)
	}
	state := graphState(r)
	if edge, _ := edgeOf(state, "domain:north.test"); edge.Source != "company:north" || !edge.Confirmed {
		t.Fatalf("matching rule did not confirm its own source edge: %+v", edge)
	}
	if edge, _ := edgeOf(state, "domain:south.test"); edge.Source != RootID || edge.Confirmed {
		t.Fatalf("a rule re-parented a domain with an earlier source: %+v", edge)
	}
	product, _ := nodeOf(state, "domain:product.test")
	if got := product.Detail.Basis[len(product.Detail.Basis)-1]; got != (Basis{Source: RoleAssetRules, Via: "Some Product", Date: "2026.11.04"}) {
		t.Fatalf("rule evidence = %+v", got)
	}
	north, _ := nodeOf(state, "domain:north.test")
	if len(north.Detail.Basis) != 2 {
		t.Fatalf("blank rule names must not add evidence: %+v", north.Detail.Basis)
	}
	if len(state.Nodes) != 6 || len(state.Edges) != 5 {
		t.Fatalf("rules created entities: nodes=%d edges=%d", len(state.Nodes), len(state.Edges))
	}
	if _, ok := nodeOf(state, "domain:unasked.test"); ok {
		t.Fatal("an unrequested row entered the graph")
	}
	if got := caller.calls[0].Arguments["domains"].([]string); len(got) != 3 || got[0] != "north.test" {
		t.Fatalf("rule request = %v", got)
	}
}

// Two companies with the rule's name are different registrations; neither
// may receive the rule-based edge.
func TestAssetRulesIgnoreAmbiguousNames(t *testing.T) {
	graph := NewGraph("Subject Co", nil)
	for _, id := range []string{"company:a", "company:b"} {
		graph.Upsert(Node{ID: id, Kind: "company", Label: "Twin"})
		graph.Link(RootID, id, false)
	}
	graph.AddDomain("company:a", "twin.test", false, Basis{Source: RoleOrganizations})
	caller := &fakeCaller{replies: map[string]func(map[string]any) (string, error){
		RoleAssetRules: fixed(`{"results":[{"domain":"twin.test","names":[{"name":"Twin"}]}]}`),
	}}
	var out published
	r := stepRun(graph, onlyRoles(RoleAssetRules), caller, &out)
	if err := r.assetRules(t.Context()); err != nil {
		t.Fatal(err)
	}
	if edge, _ := edgeOf(graphState(r), "domain:twin.test"); edge.Confirmed {
		t.Fatalf("ambiguous rule confirmed an owner: %+v", edge)
	}
}

// The batch is bounded by MaxDomains, and a graph without domains spends no
// lookup at all.
func TestAssetRuleLookupIsBounded(t *testing.T) {
	graph := NewGraph("Subject Co", nil)
	for i := 0; i < 7; i++ {
		graph.AddDomain(RootID, fmt.Sprintf("d%d.test", i), true, Basis{Source: RoleOrganizations})
	}
	caller := &fakeCaller{replies: map[string]func(map[string]any) (string, error){RoleAssetRules: fixed(`{"results":[]}`)}}
	var out published
	settings := onlyRoles(RoleAssetRules)
	settings.MaxDomains = 3
	if err := stepRun(graph, settings, caller, &out).assetRules(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := caller.calls[0].Arguments["domains"].([]string); len(got) != 3 {
		t.Fatalf("batch = %v", got)
	}
	empty := &fakeCaller{}
	if err := stepRun(NewGraph("S", nil), settings, empty, &out).assetRules(t.Context()); err != nil || len(empty.calls) != 0 {
		t.Fatalf("err=%v calls=%d", err, len(empty.calls))
	}
}

// A malformed reply fails the step without changing the graph.
func TestMalformedAssetRuleReplyFailsTheStep(t *testing.T) {
	graph := NewGraph("Subject Co", nil)
	graph.AddDomain(RootID, "a.test", true, Basis{Source: RoleOrganizations})
	caller := &fakeCaller{replies: map[string]func(map[string]any) (string, error){
		RoleAssetRules: fixed(`{"results":[{"domain":"a.test","names":"Subject Co"}]}`),
	}}
	var out published
	r := stepRun(graph, onlyRoles(RoleAssetRules), caller, &out)
	if err := r.assetRules(t.Context()); err == nil {
		t.Fatal("malformed rule names accepted")
	}
	if nodes, edges := r.graph.Len(); nodes != 2 || edges != 1 {
		t.Fatalf("graph changed: %d %d", nodes, edges)
	}
}
