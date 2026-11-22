package easm

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func branchRun(rootDetail *Detail, reply func(map[string]any) (string, error)) (*run, *fakeCaller) {
	caller := &fakeCaller{replies: map[string]func(map[string]any) (string, error){RoleBranches: reply}}
	return &run{graph: NewGraph("Brand", rootDetail), caller: caller, settings: onlyRoles(RoleBranches)}, caller
}

// A root verified by relations keeps its registry identity as the lookup key,
// so a renamed display label still reaches the same legal entity, and the
// returned branches are confirmed with their registration status.
func TestBranchesOfAVerifiedRootAreConfirmed(t *testing.T) {
	r, caller := branchRun(&Detail{RegistryID: "9007199254740993", RegistryName: "Legal Name"}, fixed(`{"total":4,"items":[
		{"id":9007199254740995,"name":"North Branch","reg_status":"Active"},
		{"id":"9007199254740993","name":"Legal Name","reg_status":"Active"},
		{"name":"West Branch","reg_status":"Closed"},
		{"id":"b2","name":" "}]}`))
	if err := r.branches(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := caller.calls[0].Arguments; got["keyword"] != "9007199254740993" || got["page_num"] != 1 || got["page_size"] != branchPageSize {
		t.Fatalf("branch request = %v", got)
	}
	north := r.graph.Node("company:reg:9007199254740995")
	if north == nil || north.Detail.Status != "Active" || north.Detail.RegistryID != "9007199254740995" {
		t.Fatalf("north = %+v", north)
	}
	if north.Detail.Basis[0] != (Basis{Source: "branches", Via: "Legal Name"}) {
		t.Fatalf("basis = %+v", north.Detail.Basis)
	}
	west := r.graph.Node("company:name:West Branch")
	if west == nil || west.Detail.Status != "Closed" || west.Detail.RegistryID != "" {
		t.Fatalf("id-less branch = %+v", west)
	}
	nodes, edges := r.graph.Snapshot()
	if len(nodes) != 3 {
		t.Fatalf("the root's own record or a nameless row became a branch: %+v", nodes)
	}
	for _, edge := range edges {
		if edge.Source != RootID || !edge.Confirmed {
			t.Fatalf("edge = %+v", edge)
		}
	}
}

// Only a legal registry name is known: it is the lookup key and confirms.
func TestBranchesUseTheLegalNameWithoutARegistryID(t *testing.T) {
	r, caller := branchRun(&Detail{RegistryName: "Legal Name"}, fixed(`{"items":[{"id":"b","name":"B"}]}`))
	if err := r.branches(t.Context()); err != nil {
		t.Fatal(err)
	}
	if caller.calls[0].Arguments["keyword"] != "Legal Name" || !r.graph.edges[0].Confirmed {
		t.Fatalf("request = %v edges = %+v", caller.calls[0].Arguments, r.graph.edges[0])
	}
}

// A display name may be a brand that resolves to some other registration;
// its branches are clues until a registry identity confirms them.
func TestBranchesOfAnUnverifiedSubjectStayPending(t *testing.T) {
	caller := &fakeCaller{replies: map[string]func(map[string]any) (string, error){RoleBranches: fixed(`{"items":[{"id":"b","name":"B"}]}`)}}
	var out published
	if _, err := Run(t.Context(), Input{Subject: "Brand"}, onlyRoles(RoleBranches), caller, out.publish); err != nil {
		t.Fatal(err)
	}
	state := out.last()
	if caller.calls[0].Arguments["keyword"] != "Brand" || stepStatus(state, StepBranches).Status != statusDone {
		t.Fatalf("request = %v steps = %+v", caller.calls[0].Arguments, state.Collection)
	}
	if edge, ok := edgeOf(state, "company:reg:b"); !ok || edge.Confirmed {
		t.Fatalf("edge = %+v %v", edge, ok)
	}
}

// Relations and branches run in one pipeline: the subject verified by the
// first is the lookup key of the second, and a branch already known as a
// registered company keeps that single node.
func TestBranchesFollowTheRelationsIdentity(t *testing.T) {
	caller := &fakeCaller{replies: map[string]func(map[string]any) (string, error){
		RoleRelations: fixed(`{"root":{"id":"r1","name":"Subject","label":"Company"},"down":[{"id":"c1","name":"Shared","label":"Company"}]}`),
		RoleBranches:  fixed(`{"items":[{"id":"c1","name":"Shared alias","reg_status":"Active"},{"id":"b1","name":"Branch"}]}`),
	}}
	var out published
	if _, err := Run(t.Context(), Input{Subject: "Subject"}, onlyRoles(RoleRelations, RoleBranches), caller, out.publish); err != nil {
		t.Fatal(err)
	}
	state := out.last()
	if caller.calls[1].Arguments["keyword"] != "r1" {
		t.Fatalf("branch request = %v", caller.calls[1].Arguments)
	}
	shared, _ := nodeOf(state, "company:reg:c1")
	if shared.Label != "Shared" || shared.Detail.Status != "Active" || len(shared.Detail.Basis) != 2 {
		t.Fatalf("shared = %+v", shared)
	}
	if len(state.Nodes) != 3 {
		t.Fatalf("nodes = %+v", state.Nodes)
	}
}

// Same-name branches with distinct registrations stay separate, and a unique
// name binds an existing unverified company instead of duplicating it.
func TestBranchIdentitiesAreNotMergedByName(t *testing.T) {
	r, _ := branchRun(&Detail{RegistryID: "p"}, fixed(`{"items":[{"id":"one","name":"Twin"},{"id":"two","name":"Twin"},{"id":"three","name":"Known"},{"name":"Known Too"}]}`))
	r.graph.Upsert(Node{ID: "company:org:1", Kind: "company", Label: "Twin"})
	r.graph.Upsert(Node{ID: "company:org:2", Kind: "company", Label: "Known"})
	r.graph.Upsert(Node{ID: "company:org:3", Kind: "company", Label: "Known Too"})
	if err := r.branches(t.Context()); err != nil {
		t.Fatal(err)
	}
	if r.graph.Node("company:reg:one") == nil || r.graph.Node("company:reg:two") == nil {
		t.Fatal("distinct registrations merged")
	}
	if r.graph.Node("company:org:1").Detail != nil {
		t.Fatal("an ambiguous name bound an existing company")
	}
	if got := r.graph.Node("company:org:2").Detail; got == nil || got.RegistryID != "three" {
		t.Fatalf("unique name not bound: %+v", got)
	}
	if r.graph.Node("company:org:3").Detail == nil || r.graph.Node("company:name:Known Too") != nil {
		t.Fatal("id-less unique name duplicated")
	}
}

// Large registries are read page by page within a fixed bound.
func TestBranchPaginationIsBounded(t *testing.T) {
	page := func(arguments map[string]any) (string, error) {
		n := arguments["page_num"].(int)
		items := make([]string, branchPageSize)
		for i := range items {
			items[i] = fmt.Sprintf(`{"id":"%d-%d","name":"B %d-%d"}`, n, i, n, i)
		}
		return `{"total":1000,"items":[` + strings.Join(items, ",") + `]}`, nil
	}
	r, caller := branchRun(&Detail{RegistryID: "p"}, page)
	if err := r.branches(t.Context()); err != nil {
		t.Fatal(err)
	}
	if nodes, _ := r.graph.Len(); len(caller.calls) != branchMaxPages || nodes != 1+branchMaxPages*branchPageSize {
		t.Fatalf("calls = %d nodes = %d", len(caller.calls), nodes)
	}

	// A total reached on a full page stops without another request.
	r, caller = branchRun(&Detail{RegistryID: "p"}, func(arguments map[string]any) (string, error) {
		body, _ := page(arguments)
		return strings.Replace(body, `"total":1000`, fmt.Sprintf(`"total":%d`, branchPageSize), 1), nil
	})
	if err := r.branches(t.Context()); err != nil || len(caller.calls) != 1 {
		t.Fatalf("err = %v calls = %d", err, len(caller.calls))
	}
}

// A later page failing keeps the branches already read but fails the step so
// the panel does not present an incomplete list as complete.
func TestFailedLaterBranchPageKeepsEarlierBranches(t *testing.T) {
	for _, second := range []string{`{"error":true}`, `{"total":30}`} {
		r, _ := branchRun(&Detail{RegistryID: "p"}, func(arguments map[string]any) (string, error) {
			if arguments["page_num"].(int) == 2 {
				return second, nil
			}
			items := make([]string, branchPageSize)
			for i := range items {
				items[i] = fmt.Sprintf(`{"id":"%d","name":"B %d"}`, i, i)
			}
			return `{"total":30,"items":[` + strings.Join(items, ",") + `]}`, nil
		})
		if err := r.branches(t.Context()); err == nil {
			t.Fatalf("%s: later page failure was hidden", second)
		}
		if nodes, _ := r.graph.Len(); nodes != 1+branchPageSize {
			t.Fatalf("%s: nodes = %d", second, nodes)
		}
	}
}

// Malformed or failed first pages fail the step and add nothing; an empty
// list is a valid answer.
func TestBranchRepliesWithoutItems(t *testing.T) {
	for reply, code := range map[string]string{
		`{"total":0}`:              "malformed_reply",
		`{"items":{}}`:             "malformed_reply",
		`{"total":"x","items":[]}`: "malformed_reply",
		`{"errmsg":"quota"}`:       "tool_error",
		`{"total":0,"items":[]}`:   "",
	} {
		caller := &fakeCaller{replies: map[string]func(map[string]any) (string, error){RoleBranches: fixed(reply)}}
		var out published
		if _, err := Run(t.Context(), Input{Subject: "S"}, onlyRoles(RoleBranches), caller, out.publish); err != nil {
			t.Fatal(err)
		}
		state := out.last()
		if got := stepStatus(state, StepBranches); got.Code != code || len(state.Nodes) != 1 {
			t.Fatalf("%s: step = %+v nodes = %d", reply, got, len(state.Nodes))
		}
	}
}

// Stop during the branch lookup ends the run as interrupted.
func TestBranchLookupHonoursCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	r, _ := branchRun(&Detail{RegistryID: "p"}, fixed(`{"items":[]}`))
	if err := r.branches(ctx); err == nil {
		t.Fatal("cancelled lookup succeeded")
	}
}
