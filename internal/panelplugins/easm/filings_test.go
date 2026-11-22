package easm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// stepRun builds a run around an existing graph so a step can be exercised
// after the earlier steps have already placed companies and domains.
func stepRun(graph *Graph, settings Settings, caller *fakeCaller, out *published) *run {
	return &run{graph: graph, caller: caller, settings: settings, publish: out.publish, state: State{Heat: []HeatPoint{}, Components: []Component{}}}
}

func graphState(r *run) State {
	nodes, edges := r.graph.Snapshot()
	return State{Nodes: nodes, Edges: edges}
}

func hasFeature(node Node, kind, value, nodeID string) bool {
	if node.Detail == nil {
		return false
	}
	for _, feature := range node.Detail.Features {
		if feature.Kind == kind && feature.Value == value && feature.NodeID == nodeID {
			return true
		}
	}
	return false
}

// A filing is the path that makes a domain the subject's: the registrant
// owns the filing and the filing owns the domain, both confirmed, so the
// panel shows why the domain belongs to the target.
func TestFilingsHangDomainsUnderTheirRegistration(t *testing.T) {
	caller := &fakeCaller{replies: map[string]func(map[string]any) (string, error){
		RoleFilings: fixed(`{"records":[{"org":"Subject Co","icp":"F-1","domain":"One.Example.TEST.","updated_at":"2026-01-02"},{"org":"Subject Co","icp":"F-1","domain":"two.example.test"},{"org":"","icp":"F-2","domain":"x.test"},{"org":"Subject Co","icp":"F-3","domain":"bad_domain"}]}`),
	}}
	var out published
	if _, err := Run(t.Context(), Input{Subject: "Subject Co"}, onlyRoles(RoleFilings), caller, out.publish); err != nil {
		t.Fatal(err)
	}
	state := out.last()
	if got := stepStatus(state, StepFilings); got.Status != statusDone {
		t.Fatalf("filings = %+v", got)
	}
	filing, ok := nodeOf(state, "feature:icp:F-1")
	if !ok || filing.Kind != "feature" || filing.FeatureKind != "icp" || filing.Label != "F-1" || filing.Detail.SubjectName != "Subject Co" {
		t.Fatalf("filing node = %+v", filing)
	}
	if edge, ok := edgeOf(state, filing.ID); !ok || edge.Source != RootID || !edge.Confirmed {
		t.Fatalf("registrant edge = %+v %v", edge, ok)
	}
	for _, domain := range []string{"one.example.test", "two.example.test"} {
		id := DomainID(domain)
		if edge, ok := edgeOf(state, id); !ok || edge.Source != filing.ID || !edge.Confirmed {
			t.Fatalf("%s edge = %+v %v", domain, edge, ok)
		}
		node, _ := nodeOf(state, id)
		if !hasFeature(node, "icp", "F-1", filing.ID) || !hasFeature(filing, "domain", domain, id) {
			t.Fatalf("filing and domain do not link each other: %+v / %+v", node.Detail, filing.Detail)
		}
	}
	one, _ := nodeOf(state, "domain:one.example.test")
	if got := one.Detail.Basis; len(got) != 1 || got[0] != (Basis{Source: RoleFilings, Via: "F-1", Date: "2026-01-02"}) {
		t.Fatalf("domain basis must be structured evidence: %+v", got)
	}
	if len(state.Nodes) != 4 {
		t.Fatalf("incomplete records entered the graph: %+v", state.Nodes)
	}
	if args := caller.calls[0].Arguments; args["query_type"] != "org" || args["value"] != "Subject Co" || args["size"] != filingPageSize {
		t.Fatalf("filing query = %v", args)
	}
}

// A domain another source already placed keeps that source; the filing only
// adds its evidence, so a later lookup never re-parents the canvas.
func TestFilingsKeepAnEarlierDomainSource(t *testing.T) {
	graph := NewGraph("Subject Co", nil)
	graph.AddDomain(RootID, "known.test", true, Basis{Source: RoleOrganizations})
	caller := &fakeCaller{replies: map[string]func(map[string]any) (string, error){
		RoleFilings: fixed(`{"records":[{"org":"Subject Co","icp":"F-1","domain":"known.test"}]}`),
	}}
	var out published
	r := stepRun(graph, onlyRoles(RoleFilings), caller, &out)
	if err := r.filings(t.Context()); err != nil {
		t.Fatal(err)
	}
	state := graphState(r)
	if edge, _ := edgeOf(state, "domain:known.test"); edge.Source != RootID {
		t.Fatalf("domain was re-parented: %+v", edge)
	}
	node, _ := nodeOf(state, "domain:known.test")
	if len(node.Detail.Basis) != 2 || !hasFeature(node, "icp", "F-1", "feature:icp:F-1") {
		t.Fatalf("filing evidence lost on the existing domain: %+v", node.Detail)
	}
}

// A registrant unknown to the graph is a pending clue, a registrant with
// several same-name registrations owns nothing, and one filing reported for
// two organizations shows no registrant at all.
func TestFilingsDoNotGuessTheRegistrant(t *testing.T) {
	graph := NewGraph("Subject Co", nil)
	for _, id := range []string{"company:a", "company:b"} {
		graph.Upsert(Node{ID: id, Kind: "company", Label: "Twin Co"})
		graph.Link(RootID, id, false)
	}
	caller := &fakeCaller{replies: map[string]func(map[string]any) (string, error){
		RoleFilings: fixed(`{"records":[{"org":"Other Co","icp":"F-9","domain":"other.test"},{"org":"Twin Co","icp":"F-7","domain":"twin.test"},{"org":"Subject Co","icp":"F-9","domain":"other2.test"}]}`),
	}}
	var out published
	r := stepRun(graph, onlyRoles(RoleFilings), caller, &out)
	if err := r.filings(t.Context()); err != nil {
		t.Fatal(err)
	}
	state := graphState(r)
	if edge, ok := edgeOf(state, "company:icp-org:Other Co"); !ok || edge.Source != RootID || edge.Confirmed {
		t.Fatalf("unknown registrant = %+v %v", edge, ok)
	}
	if edge, _ := edgeOf(state, "feature:icp:F-7"); edge.Source != RootID || edge.Confirmed {
		t.Fatalf("ambiguous registrant claimed the filing: %+v", edge)
	}
	if edge, _ := edgeOf(state, "feature:icp:F-9"); edge.Source != "company:icp-org:Other Co" {
		t.Fatalf("first registrant source lost: %+v", edge)
	}
	if filing, _ := nodeOf(state, "feature:icp:F-9"); filing.Detail.SubjectName != "" {
		t.Fatalf("conflicting registrants shown as one: %q", filing.Detail.SubjectName)
	}
}

// Confirmed companies are looked up too, but the number of lookups is bounded
// so a large group cannot spend the whole quota on filings.
func TestFilingLookupsAreBoundedAndPublishProgress(t *testing.T) {
	graph := NewGraph("Subject Co", nil)
	for i := 0; i < 8; i++ {
		id := fmt.Sprintf("company:%d", i)
		graph.Upsert(Node{ID: id, Kind: "company", Label: fmt.Sprintf("Sub %d", i)})
		graph.Link(RootID, id, i != 0)
	}
	caller := &fakeCaller{replies: map[string]func(map[string]any) (string, error){RoleFilings: fixed(`{"records":[]}`)}}
	var out published
	settings := onlyRoles(RoleFilings)
	settings.MaxDomains = 4
	r := stepRun(graph, settings, caller, &out)
	if err := r.filings(t.Context()); err != nil {
		t.Fatal(err)
	}
	var values []string
	for _, call := range caller.calls {
		values = append(values, call.Arguments["value"].(string))
	}
	if strings.Join(values, "|") != "Subject Co|Sub 1|Sub 2|Sub 3" {
		t.Fatalf("lookups = %v", values)
	}
	if len(out.states) != 1 {
		t.Fatalf("progress publications = %d", len(out.states))
	}
}

// A malformed or failed reply fails the step instead of drawing guesses.
func TestFilingReplyFailures(t *testing.T) {
	for _, tc := range []struct {
		body string
		code string
	}{
		{`{"records":"none"}`, "malformed_reply"},
		{`{"error":true,"errmsg":"quota"}`, "tool_error"},
	} {
		caller := &fakeCaller{replies: map[string]func(map[string]any) (string, error){RoleFilings: fixed(tc.body)}}
		var out published
		if _, err := Run(t.Context(), Input{Subject: "S"}, onlyRoles(RoleFilings), caller, out.publish); err != nil {
			t.Fatal(err)
		}
		if got := stepStatus(out.last(), StepFilings); got.Status != statusFailed || got.Code != tc.code {
			t.Fatalf("%s: %+v", tc.body, got)
		}
	}
}

// Stop during the lookups ends the step with the cancellation; a progress
// publication failure ends it too.
func TestFilingsStopOnCancellationAndPublishFailure(t *testing.T) {
	graph := NewGraph("Subject Co", nil)
	for i := 0; i < 4; i++ {
		id := fmt.Sprintf("company:%d", i)
		graph.Upsert(Node{ID: id, Kind: "company", Label: fmt.Sprintf("Sub %d", i)})
		graph.Link(RootID, id, true)
	}
	ctx, cancel := context.WithCancel(t.Context())
	caller := &fakeCaller{replies: map[string]func(map[string]any) (string, error){
		RoleFilings: func(map[string]any) (string, error) { cancel(); return `{"records":[]}`, nil },
	}}
	var out published
	r := stepRun(graph, onlyRoles(RoleFilings), caller, &out)
	if err := r.filings(ctx); !errors.Is(err, context.Canceled) || len(caller.calls) != 2 {
		t.Fatalf("err=%v calls=%d", err, len(caller.calls))
	}
	want := errors.New("store closed")
	caller = &fakeCaller{replies: map[string]func(map[string]any) (string, error){RoleFilings: fixed(`{"records":[]}`)}}
	r = stepRun(graph, onlyRoles(RoleFilings), caller, &out)
	r.publish = func(context.Context, json.RawMessage) error { return want }
	if err := r.filings(t.Context()); !errors.Is(err, want) {
		t.Fatalf("publish err = %v", err)
	}
}
