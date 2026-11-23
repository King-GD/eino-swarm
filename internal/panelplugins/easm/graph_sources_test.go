package easm

import (
	"reflect"
	"testing"
)

// An older detached cycle stays available as raw evidence, but its nodes
// cannot become parents of new findings or place a certificate in the graph.
func TestSavedDetachedSourcesStayRawAndCannotOwnLaterFindings(t *testing.T) {
	prior := State{SchemaVersion: 1, Title: "Subject", Nodes: []Node{
		{ID: RootID, Kind: "root", Label: "Subject"},
		{ID: companyNameID("Lost"), Kind: "company", Label: "Lost"},
		{ID: DomainID("lost.test"), Kind: "domain", Label: "lost.test", Detail: &Detail{Basis: []Basis{{Source: "original"}}}},
		{ID: companyNameID("Missing source"), Kind: "company", Label: "Missing source"},
	}, Edges: []Edge{
		{Source: companyNameID("Lost"), Target: DomainID("lost.test")},
		{Source: DomainID("lost.test"), Target: companyNameID("Lost")},
		{Source: "source-no-longer-saved", Target: companyNameID("Missing source")},
	}}
	raw := stateJSON(t, prior)
	g, err := Restore(prior)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := g.Parent(companyNameID("Lost")); ok {
		t.Fatal("the disconnected source became an active parent")
	}
	if _, ok := g.Parent(companyNameID("Missing source")); ok {
		t.Fatal("the missing source became an active parent")
	}
	next, receipt, err := push(t, raw, Input{Subject: prior.Title,
		Domains: []DomainInput{
			{Domain: "new.test", Parent: "Lost", Confirmed: true, Source: "lookup"},
			{Domain: "other.test", Parent: "Missing source", Confirmed: true, Source: "lookup"},
		},
		Certificates: []CertificateInput{{Serial: "kept", Domain: "lost.test", Source: "certificate_lookup"}},
	}, onlyRoles(), &fakeCaller{})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(next.Nodes[:len(prior.Nodes)], prior.Nodes) || !reflect.DeepEqual(next.Edges[:len(prior.Edges)], prior.Edges) {
		t.Fatalf("the original observations or sources changed: %+v", next)
	}
	for _, domain := range []string{"new.test", "other.test"} {
		edge, found := edgeOf(next, DomainID(domain))
		if !found || edge.Source != RootID || edge.Confirmed || receipt.UnresolvedParents != 2 {
			t.Fatalf("new clue = %+v, receipt = %+v", edge, receipt)
		}
	}
	if _, found := edgeOf(next, certificateID("kept", "")); found {
		t.Fatal("new certificate was attached to the invalid source")
	}
	if _, found := nodeOf(next, certificateID("kept", "")); !found {
		t.Fatal("the raw certificate observation was lost")
	}
	if err := ValidateGraphUpdate(raw, stateJSON(t, next)); err != nil {
		t.Fatalf("the valid continuation was refused: %v", err)
	}
}

// Restoring the canonical source can recover the original link. A matching
// display name or an unrooted copy of the source cannot do that.
func TestSavedSourceIsReusedOnlyWhenItsExactIDBecomesRooted(t *testing.T) {
	source := companyNameID("Owner")
	target := DomainID("legacy.test")
	original := Edge{Source: source, Target: target, Confirmed: true}
	stored := State{Title: "Subject", Nodes: []Node{
		{ID: RootID, Kind: "root", Label: "Subject"},
		{ID: target, Kind: "domain", Label: "legacy.test"},
	}, Edges: []Edge{original}}
	ambiguous, _, err := push(t, stateJSON(t, stored), Input{Subject: stored.Title, Companies: []CompanyInput{
		{Name: "Owner", RegistryID: "alternative", Confirmed: true, Source: "registry"},
	}}, onlyRoles(), &fakeCaller{})
	if err != nil {
		t.Fatal(err)
	}
	g, err := Restore(ambiguous)
	if err != nil {
		t.Fatal(err)
	}
	if g.reachable()[target] || g.Node(registryCompanyID("alternative")) == nil {
		t.Fatalf("same-name observation revived the unrelated source: %+v", ambiguous)
	}
	unrooted := ambiguous
	unrooted.Nodes = append(unrooted.Nodes, Node{ID: source, Kind: "company", Label: "Owner"})
	if err := ValidateGraphUpdate(stateJSON(t, ambiguous), stateJSON(t, unrooted)); err != nil {
		t.Fatal(err)
	}
	g, err = Restore(unrooted)
	if err != nil {
		t.Fatal(err)
	}
	if g.reachable()[target] {
		t.Fatal("saving the exact source without a path to the subject revived its clue")
	}
	restored, _, err := push(t, stateJSON(t, unrooted), Input{Subject: stored.Title, Companies: []CompanyInput{
		{Name: "Owner", Confirmed: true, Source: "original_registry"},
	}}, onlyRoles(), &fakeCaller{})
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateGraphUpdate(stateJSON(t, unrooted), stateJSON(t, restored)); err != nil {
		t.Fatal(err)
	}
	g, err = Restore(restored)
	if err != nil {
		t.Fatal(err)
	}
	if parent, found := g.Parent(target); !found || parent != source || !g.reachable()[target] {
		t.Fatalf("the exact rooted source did not restore the observation: %+v", restored)
	}
	if edge, found := edgeOf(restored, target); !found || edge != original {
		t.Fatalf("historical source changed: %+v", edge)
	}
}

func TestSameBatchSourceCycleKeepsOnlyRootedRelationships(t *testing.T) {
	state, _, err := push(t, nil, Input{Subject: "Subject", Companies: []CompanyInput{
		{Name: "Loop 1", Parent: "Loop 2", Source: "registry"},
		{Name: "Branch", Parent: "Owner", Source: "registry"},
		{Name: "Loop 2", Parent: "Loop 1", Source: "registry"},
		{Name: "Owner", Source: "registry"},
	}}, onlyRoles(), &fakeCaller{})
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Nodes) != 5 || len(state.Edges) != 2 {
		t.Fatalf("clues = %+v, sources = %+v", state.Nodes, state.Edges)
	}
	if edge, found := edgeOf(state, companyNameID("Branch")); !found || edge.Source != companyNameID("Owner") {
		t.Fatalf("forward reference to a valid parent was lost: %+v", edge)
	}
	if err := ValidateGraphUpdate([]byte("{}"), stateJSON(t, state)); err != nil {
		t.Fatalf("new batch still contains an invalid source: %v", err)
	}
}

func TestPublishCannotRewriteFirstSourcesOrCreateLegacyDuplicateCertificates(t *testing.T) {
	root := Node{ID: RootID, Kind: "root", Label: "Subject"}
	first := Node{ID: "cert:first", Kind: "feature", FeatureKind: "cert", Label: "first", Detail: &Detail{Serial: "certificate"}}
	duplicate := Node{ID: "cert:duplicate", Kind: "feature", FeatureKind: "cert", Label: "duplicate", Detail: &Detail{Serial: " certificate "}}
	other := Node{ID: "domain:other.test", Kind: "domain", Label: "other.test"}
	firstSource := Edge{Source: RootID, Target: first.ID}
	duplicateSource := Edge{Source: other.ID, Target: duplicate.ID}
	otherSource := Edge{Source: RootID, Target: other.ID}
	valid := State{Nodes: []Node{root, first, other}, Edges: []Edge{firstSource, otherSource}}
	legacy := State{Nodes: []Node{root, first, other, duplicate}, Edges: []Edge{firstSource, otherSource, duplicateSource}}
	legacyMissingSource := State{Nodes: []Node{root, duplicate}, Edges: []Edge{duplicateSource}}
	beforeSourceRemoved := State{Nodes: []Node{root, other, duplicate}, Edges: []Edge{otherSource, duplicateSource}}
	cases := []struct {
		name         string
		before, next State
		wantError    bool
	}{
		{"empty panel", State{}, State{}, false},
		{"first observation", State{}, valid, false},
		{"unchanged old duplicate", legacy, legacy, false},
		{"new duplicate serial", valid, legacy, true},
		{"reordered legacy duplicate", legacy, State{Nodes: []Node{root, duplicate, first, other}, Edges: legacy.Edges}, true},
		{"changed first source", valid, State{Nodes: valid.Nodes, Edges: []Edge{otherSource, {Source: other.ID, Target: first.ID}}}, true},
		{"orphaned parent", State{}, State{Nodes: valid.Nodes, Edges: []Edge{{Source: other.ID, Target: first.ID}}}, true},
		{"missing source retained", legacyMissingSource, legacyMissingSource, false},
		{"new missing source", State{}, legacyMissingSource, true},
		{"referenced source removed", beforeSourceRemoved, legacyMissingSource, true},
		{"missing node", State{}, State{Nodes: []Node{root}, Edges: []Edge{firstSource}}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateGraphUpdate(stateJSON(t, tc.before), stateJSON(t, tc.next))
			if (err != nil) != tc.wantError {
				t.Fatalf("validation error = %v, want refusal = %v", err, tc.wantError)
			}
		})
	}
	for _, args := range [][2]string{{"{", "{}"}, {"{}", "{"}} {
		if err := ValidateGraphUpdate([]byte(args[0]), []byte(args[1])); err == nil {
			t.Fatal("malformed graph JSON was accepted")
		}
	}
}
