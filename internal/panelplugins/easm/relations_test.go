package easm

import (
	"encoding/json"
	"testing"
)

func runRelations(t *testing.T, subject, reply string, roles ...string) (State, *fakeCaller) {
	t.Helper()
	caller := &fakeCaller{replies: map[string]func(map[string]any) (string, error){
		RoleRelations:     fixed(reply),
		RoleOrganizations: fixed(`{"results":[]}`),
	}}
	var out published
	if _, err := Run(t.Context(), Input{Subject: subject}, onlyRoles(append(roles, RoleRelations)...), caller, out.publish); err != nil {
		t.Fatal(err)
	}
	return out.last(), caller
}

// The registry's legal entity with the subject's name is the subject: its
// direct investments must hang under the root with confirmed edges and keep
// their share and registration status, while up-tree investors and people
// never enter the portrait.
func TestSubjectRelationsPutDirectSubsidiariesUnderTheRoot(t *testing.T) {
	reply := `{"root":{"id":"9007199254740993","name":"Subject Co","label":"Company","reg_status":"Active","percent":""},
	"down":[{"id":9007199254740995,"name":"Child A","label":"Company","reg_status":"Active","percent":"0.9"},
	{"id":"c-b","name":"Child B","label":"Company","percent":"1","children":[{"id":"nested","name":"Nested","label":"Company"}]},
	{"id":"c-b","name":"Child B alias","label":"Company"},
	{"id":"h","name":"Person","label":"Human","percent":"0.1"},
	{"id":"9007199254740993","name":"Subject Co","label":"Company"},
	{"id":1.5,"name":"Rounded","label":"Company"},
	{"id":"","name":"No Identity","label":"Company"},
	{"id":"blank","name":" ","label":"Company"}],
	"up":[{"id":"investor","name":"Investor","label":"Company","percent":"1"}]}`
	state, caller := runRelations(t, "Subject Co", reply)
	if got := caller.calls[0].Arguments["keyword"]; got != "Subject Co" {
		t.Fatalf("relations request = %v", caller.calls[0].Arguments)
	}
	root, _ := nodeOf(state, RootID)
	if root.Detail == nil || root.Detail.RegistryID != "9007199254740993" || root.Detail.RegistryName != "Subject Co" || root.Detail.Status != "Active" {
		t.Fatalf("root registry identity = %+v", root.Detail)
	}
	child, ok := nodeOf(state, "company:reg:9007199254740995")
	if !ok || child.Kind != "company" || child.Detail.Share != "90%" || child.Detail.Status != "Active" || child.Detail.RegistryID != "9007199254740995" {
		t.Fatalf("exact numeric child = %+v %v", child, ok)
	}
	if len(child.Detail.Basis) != 1 || child.Detail.Basis[0] != (Basis{Source: "relations", Via: "Subject Co"}) {
		t.Fatalf("child basis = %+v", child.Detail.Basis)
	}
	if edge, ok := edgeOf(state, child.ID); !ok || edge.Source != RootID || !edge.Confirmed {
		t.Fatalf("subsidiary edge = %+v %v", edge, ok)
	}
	second, _ := nodeOf(state, "company:reg:c-b")
	if second.Label != "Child B" || second.Detail.Share != "100%" {
		t.Fatalf("repeated identity must keep its first record: %+v", second)
	}
	// Root, two subsidiaries; nothing from investors, people, nested rows,
	// rounded IDs or rows without identity.
	if len(state.Nodes) != 3 || len(state.Edges) != 2 {
		raw, _ := json.Marshal(state.Nodes)
		t.Fatalf("unexpected nodes: %s", raw)
	}
	if got := stepStatus(state, StepRelations); got.Status != statusDone {
		t.Fatalf("relations = %+v", got)
	}
}

// A brand subject resolves to a differently named legal parent. That parent
// is related to the brand but not proven to be it, so it is a pending clue,
// while its own subsidiaries keep the legal parent → child direction.
func TestDifferentlyNamedLegalParentStaysPendingWithItsSubsidiaries(t *testing.T) {
	reply := `{"root":{"id":"p","name":"Legal Parent","label":"Company"},"down":[{"id":"c","name":"Legal Child","label":"Company"}]}`
	state, _ := runRelations(t, "Brand", reply)
	if edge, ok := edgeOf(state, "company:reg:p"); !ok || edge.Source != RootID || edge.Confirmed {
		t.Fatalf("legal parent edge = %+v %v", edge, ok)
	}
	if edge, ok := edgeOf(state, "company:reg:c"); !ok || edge.Source != "company:reg:p" || !edge.Confirmed {
		t.Fatalf("legal child edge = %+v %v", edge, ok)
	}
	root, _ := nodeOf(state, RootID)
	if root.Detail != nil && root.Detail.RegistryID != "" {
		t.Fatalf("brand root took the parent's registry identity: %+v", root.Detail)
	}
}

// Different registrations that share a name are different companies; merging
// them would attribute one company's assets to another.
func TestSameNameRegistrationsStaySeparate(t *testing.T) {
	reply := `{"root":{"id":"p1","name":"Same","label":"Company"},"down":[{"id":"p2","name":"Same","label":"Company"},{"id":"x1","name":"Twin","label":"Company"},{"id":"x2","name":"Twin","label":"Company"}]}`
	state, _ := runRelations(t, "Same", reply)
	if _, ok := edgeOf(state, "company:reg:p1"); !ok {
		t.Fatal("an ambiguous same-name record must not become the root")
	}
	for _, id := range []string{"company:reg:p2", "company:reg:x1", "company:reg:x2"} {
		if edge, ok := edgeOf(state, id); !ok || edge.Source != "company:reg:p1" || !edge.Confirmed {
			t.Fatalf("%s = %+v %v", id, edge, ok)
		}
	}
}

// An organization clue with the same unique name is the same legal company:
// it gains the registry identity instead of appearing twice on the canvas.
func TestRelationsBindAnUnverifiedOrganizationClue(t *testing.T) {
	caller := &fakeCaller{replies: map[string]func(map[string]any) (string, error){
		RoleOrganizations: fixed(`{"results":[{"id":"7","name":"Legal Parent"},{"id":"8","name":"Legal Child"}]}`),
		RoleRelations:     fixed(`{"root":{"id":"p","name":"Legal Parent","label":"Company"},"down":[{"id":"c","name":"Legal Child","label":"Company","percent":"0.505"}]}`),
	}}
	var out published
	if _, err := Run(t.Context(), Input{Subject: "Brand"}, onlyRoles(RoleOrganizations, RoleRelations), caller, out.publish); err != nil {
		t.Fatal(err)
	}
	state := out.last()
	parent, _ := nodeOf(state, "company:org:7")
	child, _ := nodeOf(state, "company:org:8")
	if parent.Detail.RegistryID != "p" || child.Detail.RegistryID != "c" || child.Detail.Share != "50.5%" {
		t.Fatalf("organization clues not bound: %+v %+v", parent.Detail, child.Detail)
	}
	if _, ok := nodeOf(state, "company:reg:p"); ok {
		t.Fatal("bound company duplicated")
	}
	// The organization step reached the child first; the tree keeps that
	// first source rather than re-parenting.
	if edge, _ := edgeOf(state, "company:org:8"); edge.Source != RootID {
		t.Fatalf("child re-parented: %+v", edge)
	}
}

// A root already verified as another registration keeps that identity: a
// same-name relations root is then a separate pending company.
func TestVerifiedRootIsNotRebound(t *testing.T) {
	var out published
	r := &run{graph: NewGraph("Same", &Detail{RegistryID: "first"}), caller: &fakeCaller{replies: map[string]func(map[string]any) (string, error){
		RoleRelations: fixed(`{"root":{"id":"second","name":"Same","label":"Company"},"down":[{"id":"first","name":"Same","label":"Company"}]}`),
	}}, settings: onlyRoles(RoleRelations), publish: out.publish}
	if err := r.relations(t.Context()); err != nil {
		t.Fatal(err)
	}
	if source, ok := r.graph.Parent("company:reg:second"); !ok || source != RootID {
		t.Fatalf("second registration parent = %q %v", source, ok)
	}
	if r.graph.Root().Detail.RegistryID != "first" {
		t.Fatalf("root identity changed: %+v", r.graph.Root().Detail)
	}
	if _, ok := r.graph.Parent(RootID); ok {
		t.Fatal("root gained a parent")
	}
}

// A malformed reply fails the relations step only and adds nothing.
func TestMalformedRelationsReplyFailsOnlyItsStep(t *testing.T) {
	for _, reply := range []string{
		`[]`,
		`{"down":[]}`,
		`{"root":{"id":"p","name":"P","label":"Company"}}`,
		`{"root":{"id":"p","name":"P","label":"Human"},"down":[]}`,
		`{"root":{"id":1e3,"name":"P","label":"Company"},"down":[]}`,
		`{"root":{"id":"p","name":" ","label":"Company"},"down":[]}`,
		`{"root":{"id":"p\u0000","name":"P","label":"Company"},"down":[]}`,
		`{"root":{"id":"p","name":"P","label":"Company"},"down":{}}`,
	} {
		state, _ := runRelations(t, "S", reply)
		if got := stepStatus(state, StepRelations); got.Status != statusFailed || got.Code != "malformed_reply" {
			t.Fatalf("%s: relations = %+v", reply, got)
		}
		if len(state.Nodes) != 1 {
			t.Fatalf("%s: nodes = %+v", reply, state.Nodes)
		}
	}
	state, _ := runRelations(t, "S", `{"error":true,"errmsg":"x"}`)
	if got := stepStatus(state, StepRelations); got.Status != statusFailed || got.Code != "tool_error" {
		t.Fatalf("error envelope = %+v", got)
	}
}

// An empty investment list is a valid answer: the subject is still verified.
func TestRelationsWithoutSubsidiariesStillVerifyTheSubject(t *testing.T) {
	state, _ := runRelations(t, "S", `{"root":{"id":42,"name":"S","label":"Company"},"down":[]}`)
	root, _ := nodeOf(state, RootID)
	if root.Detail == nil || root.Detail.RegistryID != "42" || len(state.Edges) != 0 || stepStatus(state, StepRelations).Status != statusDone {
		t.Fatalf("state = %+v", state)
	}
}

func TestSharePercentKeepsOnlyExactRatios(t *testing.T) {
	for value, want := range map[string]string{
		"1": "100%", "0.9": "90%", "0.505": "50.5%", "0.12345": "12.345%", "0": "0%", " 0.5 ": "50%",
		"": "", "1.5": "", "-0.1": "", "1e-1": "", "1/2": "", "half": "", "+0.1": "",
	} {
		if got := sharePercent(value); got != want {
			t.Errorf("sharePercent(%q) = %q, want %q", value, got, want)
		}
	}
}

func TestRegistryIDRejectsInexactNumbers(t *testing.T) {
	for raw, want := range map[string]string{
		`"abc"`: "abc", `" 12 "`: "12", `9007199254740993`: "9007199254740993", `0`: "0",
		``: "", `null`: "", `1.0`: "", `1e3`: "", `-1`: "", `true`: "", `"a\nb"`: "",
	} {
		if got := registryID(json.RawMessage(raw)); got != want {
			t.Errorf("registryID(%s) = %q, want %q", raw, got, want)
		}
	}
}

// Two organization clues already carry the subject's name, so the graph
// cannot tell which registration the subject is; the relations root then
// stays a separate pending company instead of claiming the root.
func TestRelationsDoNotBindTheRootAmongSameNameClues(t *testing.T) {
	caller := &fakeCaller{replies: map[string]func(map[string]any) (string, error){
		RoleOrganizations: fixed(`{"results":[{"id":"1","name":"Same"},{"id":"2","name":"Same"}]}`),
		RoleRelations:     fixed(`{"root":{"id":"p","name":"Same","label":"Company"},"down":[]}`),
	}}
	var out published
	if _, err := Run(t.Context(), Input{Subject: "Same"}, onlyRoles(RoleOrganizations, RoleRelations), caller, out.publish); err != nil {
		t.Fatal(err)
	}
	state := out.last()
	if edge, ok := edgeOf(state, "company:reg:p"); !ok || edge.Source != RootID || edge.Confirmed {
		t.Fatalf("edge = %+v %v", edge, ok)
	}
	if root, _ := nodeOf(state, RootID); root.Detail != nil && root.Detail.RegistryID != "" {
		t.Fatalf("root = %+v", root.Detail)
	}
}
