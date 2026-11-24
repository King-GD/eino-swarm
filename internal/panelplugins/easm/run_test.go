package easm

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeCaller answers by tool name and records every request, so tests assert
// on the actual arguments a step sends and the state it publishes.
type fakeCaller struct {
	mu      sync.Mutex
	replies map[string]func(map[string]any) (string, error)
	calls   []fakeCall
}

type fakeCall struct {
	Tool      string
	Arguments map[string]any
}

func (f *fakeCaller) Call(ctx context.Context, tool string, arguments map[string]any) (json.RawMessage, error) {
	f.mu.Lock()
	f.calls = append(f.calls, fakeCall{tool, arguments})
	reply := f.replies[tool]
	f.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if reply == nil {
		return nil, ErrUnavailable
	}
	body, err := reply(arguments)
	return json.RawMessage(body), err
}

// requests copies the recorded requests of one tool: the aggregations run
// concurrently, so their recorded order carries no meaning.
func (f *fakeCaller) requests(tool string) []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []map[string]any
	for _, call := range f.calls {
		if call.Tool == tool {
			out = append(out, call.Arguments)
		}
	}
	return out
}

func fixed(body string) func(map[string]any) (string, error) {
	return func(map[string]any) (string, error) { return body, nil }
}

// testSettings binds every role to a tool of the same name.
func testSettings() Settings {
	tools := map[string]string{}
	for _, role := range Roles {
		tools[role] = role
	}
	return Settings{Tools: tools, MaxDomains: 5, MaxComponents: 20}
}

func onlyRoles(roles ...string) Settings {
	settings := testSettings()
	settings.Tools = map[string]string{}
	for _, role := range roles {
		settings.Tools[role] = role
	}
	return settings
}

// published records what the producer sent. Steps publish from their own
// goroutines, so every read and write goes through the lock.
type published struct {
	mu     sync.Mutex
	states []State
	fail   error
}

func (p *published) publish(_ context.Context, body json.RawMessage) error {
	var state State
	if err := json.Unmarshal(body, &state); err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.fail != nil {
		return p.fail
	}
	p.states = append(p.states, state)
	return nil
}

func (p *published) snapshot() []State {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]State(nil), p.states...)
}

func (p *published) last() State {
	states := p.snapshot()
	if len(states) == 0 {
		return State{}
	}
	return states[len(states)-1]
}

// push runs one call against the state the panel holds, the way a later
// run_panel_source call starts.
func push(t *testing.T, current json.RawMessage, input Input, settings Settings, caller Caller) (State, Result, error) {
	t.Helper()
	var out published
	result, err := Run(t.Context(), input, settings, current, caller, out.publish)
	return out.last(), result, err
}

// stateJSON turns a published state into the panel state a later call reads.
func stateJSON(t *testing.T, state State) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func boolean(value bool) *bool { return &value }

func stepStatus(state State, step Step) StepStatus {
	for _, status := range state.Collection.Steps {
		if status.ID == step {
			return status
		}
	}
	return StepStatus{}
}

func edgeOf(state State, target string) (Edge, bool) {
	for _, edge := range state.Edges {
		if edge.Target == target {
			return edge, true
		}
	}
	return Edge{}, false
}

func nodeOf(state State, id string) (Node, bool) {
	for _, node := range state.Nodes {
		if node.ID == id {
			return node, true
		}
	}
	return Node{}, false
}

func stepStatuses(state State) []StepStatus { return state.Collection.Steps }

// A first call without a query only records the graph: the metrics stay
// pending and the collection is idle, never running with nothing to collect.
func TestFirstBatchWithoutAQueryStaysIdle(t *testing.T) {
	state, result, err := push(t, nil, Input{Subject: "Subject Co", Companies: []CompanyInput{{Name: "Peer", Source: "org_tool"}}}, onlyRoles(), &fakeCaller{})
	if err != nil {
		t.Fatal(err)
	}
	if state.Collection.Status != statusIdle {
		t.Fatalf("collection = %+v", state.Collection)
	}
	for _, step := range Steps {
		if got := stepStatus(state, step); got.Status != statusPending {
			t.Fatalf("%s = %+v", step, got)
		}
	}
	if result.Nodes != 2 || result.Edges != 1 || result.UnresolvedParents != 0 || result.RefusedDomains != 0 {
		t.Fatalf("result = %+v", result)
	}
	if len(stepStatuses(state)) != 3 {
		t.Fatalf("steps = %+v", state.Collection.Steps)
	}
}

// Every finished batch arrives as its own call, so the portrait must accumulate
// instead of replacing the nodes the user already sees.
func TestBatchesAccumulateAcrossCalls(t *testing.T) {
	settings := onlyRoles()
	var current json.RawMessage
	batches := []Input{
		{Subject: "Subject Co", Description: "d", Website: "https://subject.test", Companies: []CompanyInput{{Name: "Parent Co", RegistryID: "9", Relation: "investment", Share: "51%", Source: "relation_tool", Via: "registry"}}},
		{Subject: "Subject Co", Domains: []DomainInput{{Domain: "Subject.TEST.", Parent: "Parent Co", Confirmed: true, Source: "org_tool"}}},
		{Subject: "Subject Co", Domains: []DomainInput{{Domain: "subject.test", Confirmed: true, Source: "second_tool", Via: "record-1"}}},
	}
	for i, batch := range batches {
		state, _, err := push(t, current, batch, settings, &fakeCaller{})
		if err != nil {
			t.Fatalf("batch %d: %v", i, err)
		}
		current = stateJSON(t, state)
	}
	state, _, err := push(t, current, Input{Subject: "Subject Co"}, settings, &fakeCaller{})
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Nodes) != 3 || len(state.Edges) != 2 {
		t.Fatalf("nodes=%v edges=%v", state.Nodes, state.Edges)
	}
	company, ok := nodeOf(state, registryCompanyID("9"))
	if !ok || company.Detail == nil || company.Detail.Relation != "investment" || company.Detail.Share != "51%" || company.Detail.RegistryName != "Parent Co" {
		t.Fatalf("company = %+v %v", company, ok)
	}
	if edge, ok := edgeOf(state, company.ID); !ok || edge.Source != RootID || edge.Confirmed {
		t.Fatalf("company edge = %+v %v", edge, ok)
	}
	domain, ok := nodeOf(state, DomainID("subject.test"))
	if !ok || domain.Label != "subject.test" {
		t.Fatalf("domain = %+v %v", domain, ok)
	}
	if edge, ok := edgeOf(state, domain.ID); !ok || edge.Source != company.ID || !edge.Confirmed {
		t.Fatalf("domain edge = %+v %v", edge, ok)
	}
	// Two tools reported the same domain; both stay on the card as evidence.
	if got := len(domain.Detail.Basis); got != 2 {
		t.Fatalf("domain basis = %+v", domain.Detail)
	}
	root, _ := nodeOf(state, RootID)
	if root.Detail == nil || root.Detail.Description != "d" || root.Detail.Website != "https://subject.test" {
		t.Fatalf("root = %+v", root.Detail)
	}
}

// A later batch may confirm an edge the earlier one only suspected, but nothing
// may take that confirmation away or move the node to another parent.
func TestConfirmationOnlyUpgradesAndNeverReparents(t *testing.T) {
	settings := onlyRoles()
	first, _, err := push(t, nil, Input{
		Subject:   "Subject Co",
		Companies: []CompanyInput{{Name: "Owner", Source: "a"}, {Name: "Other", Source: "a"}},
		Domains:   []DomainInput{{Domain: "child.test", Parent: "Owner", Source: "a"}},
	}, settings, &fakeCaller{})
	if err != nil {
		t.Fatal(err)
	}
	if edge, ok := edgeOf(first, DomainID("child.test")); !ok || edge.Confirmed {
		t.Fatalf("initial edge = %+v %v", edge, ok)
	}
	current := stateJSON(t, first)
	upgraded, _, err := push(t, current, Input{Subject: "Subject Co", Domains: []DomainInput{{Domain: "child.test", Parent: "Owner", Confirmed: true, Source: "b"}}}, settings, &fakeCaller{})
	if err != nil {
		t.Fatal(err)
	}
	edge, ok := edgeOf(upgraded, DomainID("child.test"))
	if !ok || !edge.Confirmed || edge.Source != companyNameID("Owner") {
		t.Fatalf("upgraded edge = %+v %v", edge, ok)
	}
	current = stateJSON(t, upgraded)
	// Another owner and an unconfirmed report must not move or downgrade it.
	for _, entry := range []DomainInput{
		{Domain: "child.test", Parent: "Other", Source: "c"},
		{Domain: "child.test", Confirmed: false, Source: "d"},
	} {
		kept, _, err := push(t, current, Input{Subject: "Subject Co", Domains: []DomainInput{entry}}, settings, &fakeCaller{})
		if err != nil {
			t.Fatal(err)
		}
		if edge, ok := edgeOf(kept, DomainID("child.test")); !ok || !edge.Confirmed || edge.Source != companyNameID("Owner") {
			t.Fatalf("edge after %+v = %+v %v", entry, edge, ok)
		}
		current = stateJSON(t, kept)
	}
}

// A parent the agent named but never pushed cannot become this entry's owner:
// the entry hangs under the subject as a clue and the receipt says so.
func TestUnresolvedParentsHangUnderTheSubjectAndAreCounted(t *testing.T) {
	state, result, err := push(t, nil, Input{
		Subject:   "Subject Co",
		Companies: []CompanyInput{{Name: "Orphan Co", Parent: "Missing Co", Confirmed: true, Source: "a"}},
		Domains:   []DomainInput{{Domain: "orphan.test", Parent: "Missing Co", Confirmed: true, Source: "a"}},
	}, onlyRoles(), &fakeCaller{})
	if err != nil {
		t.Fatal(err)
	}
	if result.UnresolvedParents != 2 {
		t.Fatalf("result = %+v", result)
	}
	for _, id := range []string{companyNameID("Orphan Co"), DomainID("orphan.test")} {
		edge, ok := edgeOf(state, id)
		if !ok || edge.Source != RootID || edge.Confirmed {
			t.Fatalf("%s edge = %+v %v", id, edge, ok)
		}
	}
}

// A batch with the same name in two registrations cannot say which one owns the
// entry, so the entry stays a clue under the subject.
func TestAmbiguousCompanyNamesCannotOwnAnEntry(t *testing.T) {
	state, result, err := push(t, nil, Input{
		Subject:   "Subject Co",
		Companies: []CompanyInput{{Name: "Same", RegistryID: "1", Source: "a"}, {Name: "Same", RegistryID: "2", Source: "a"}},
		Domains:   []DomainInput{{Domain: "same.test", Parent: "Same", Confirmed: true, Source: "a"}},
	}, onlyRoles(), &fakeCaller{})
	if err != nil {
		t.Fatal(err)
	}
	if result.UnresolvedParents != 1 {
		t.Fatalf("result = %+v", result)
	}
	if edge, ok := edgeOf(state, DomainID("same.test")); !ok || edge.Source != RootID || edge.Confirmed {
		t.Fatalf("edge = %+v %v", edge, ok)
	}
}

func TestNameOnlyOwnerStaysPendingWhenARegisteredCompanySharesItsName(t *testing.T) {
	state, result, err := push(t, nil, Input{
		Subject:   "Chosen Org",
		Companies: []CompanyInput{{Name: "Identical", RegistryID: "1", Source: "registry"}, {Name: "Identical", Source: "name_lookup"}},
		Domains:   []DomainInput{{Domain: "same.test", Parent: "Identical", Confirmed: true, Source: "name_lookup"}},
	}, onlyRoles(), &fakeCaller{})
	if err != nil {
		t.Fatal(err)
	}
	edge, found := edgeOf(state, DomainID("same.test"))
	if result.UnresolvedParents != 1 || !found || edge.Source != RootID || edge.Confirmed {
		t.Fatalf("result = %+v, edge = %+v", result, edge)
	}
}

// A domain can name the subject itself as its owner; that is the root, not a
// company node duplicating it.
func TestSubjectNameAsParentResolvesToTheRoot(t *testing.T) {
	state, result, err := push(t, nil, Input{Subject: "Subject Co", Domains: []DomainInput{{Domain: "subject.test", Parent: "Subject Co", Confirmed: true, Source: "a"}}}, onlyRoles(), &fakeCaller{})
	if err != nil {
		t.Fatal(err)
	}
	if result.UnresolvedParents != 0 || result.Nodes != 2 {
		t.Fatalf("result = %+v", result)
	}
	if edge, ok := edgeOf(state, DomainID("subject.test")); !ok || edge.Source != RootID || !edge.Confirmed {
		t.Fatalf("edge = %+v %v", edge, ok)
	}
}

// A portrait describes one subject. Another subject must not inherit the old
// graph or the numbers collected for it.
func TestAnotherSubjectStartsANewPortrait(t *testing.T) {
	current, _, err := push(t, nil, Input{Subject: "First Co", Companies: []CompanyInput{{Name: "Old Co", Source: "a"}}, Domains: []DomainInput{{Domain: "old.test", Source: "a"}}}, onlyRoles(), &fakeCaller{})
	if err != nil {
		t.Fatal(err)
	}
	state, result, err := push(t, stateJSON(t, current), Input{Subject: "Second Co", Domains: []DomainInput{{Domain: "new.test", Source: "a"}}}, onlyRoles(), &fakeCaller{})
	if err != nil {
		t.Fatal(err)
	}
	if state.Title != "Second Co" || result.Nodes != 2 || result.Edges != 1 {
		t.Fatalf("state=%+v result=%+v", state, result)
	}
	if _, ok := nodeOf(state, DomainID("old.test")); ok {
		t.Fatal("the previous subject's domain survived the reset")
	}
	root, _ := nodeOf(state, RootID)
	if root.Label != "Second Co" {
		t.Fatalf("root = %+v", root)
	}
}

// The domain budget bounds the whole portrait, so a later batch cannot grow the
// graph past it and the receipt reports what it refused.
func TestDomainBudgetRefusesEntriesAndCountsThem(t *testing.T) {
	settings := onlyRoles()
	settings.MaxDomains = 2
	state, result, err := push(t, nil, Input{Subject: "Subject Co", Domains: []DomainInput{
		{Domain: "a.test", Source: "t"}, {Domain: "b.test", Source: "t"}, {Domain: "c.test", Source: "t"},
	}}, settings, &fakeCaller{})
	if err != nil {
		t.Fatal(err)
	}
	if result.RefusedDomains != 1 || len(state.Nodes) != 3 {
		t.Fatalf("result=%+v nodes=%v", result, state.Nodes)
	}
	if _, ok := nodeOf(state, DomainID("c.test")); ok {
		t.Fatal("the refused domain entered the graph")
	}
	// Re-reporting a domain already on the graph is never refused.
	state, result, err = push(t, stateJSON(t, state), Input{Subject: "Subject Co", Domains: []DomainInput{
		{Domain: "a.test", Source: "t2"}, {Domain: "d.test", Source: "t"},
	}}, settings, &fakeCaller{})
	if err != nil {
		t.Fatal(err)
	}
	if result.RefusedDomains != 1 || len(state.Nodes) != 3 {
		t.Fatalf("second call result=%+v nodes=%v", result, state.Nodes)
	}
	if node, ok := nodeOf(state, DomainID("a.test")); !ok || len(node.Detail.Basis) != 2 {
		t.Fatalf("existing domain = %+v %v", node, ok)
	}
}

// A certificate hangs under the domain it was observed on, and only its own
// validity confirms that edge.
func TestCertificatesHangUnderTheirDomain(t *testing.T) {
	state, _, err := push(t, nil, Input{Subject: "Subject Co", Certificates: []CertificateInput{{
		Serial: " 0a1b ", Domain: "subject.test", Issuer: "CA", SubjectCN: "subject.test", SubjectOrg: "Subject Co",
		Names: []string{"subject.test", "www.subject.test"}, NotBefore: "2026-01-01", NotAfter: "2027-01-01",
		Valid: boolean(true), Match: boolean(true), Expired: boolean(false), Source: "stats_tool", Via: "certs",
	}}}, onlyRoles(), &fakeCaller{})
	if err != nil {
		t.Fatal(err)
	}
	// The domain was not pushed yet: it enters as a pending clue under the root.
	domain, ok := nodeOf(state, DomainID("subject.test"))
	if !ok {
		t.Fatal("the certificate's domain is missing")
	}
	if edge, ok := edgeOf(state, domain.ID); !ok || edge.Source != RootID || edge.Confirmed {
		t.Fatalf("domain edge = %+v %v", edge, ok)
	}
	id := certificateID("0a1b", "CA")
	certificate, ok := nodeOf(state, id)
	if !ok || certificate.FeatureKind != "cert" || certificate.Label != "subject.test" || certificate.Detail.Serial != "0a1b" {
		t.Fatalf("certificate = %+v %v", certificate, ok)
	}
	if edge, ok := edgeOf(state, id); !ok || edge.Source != domain.ID || !edge.Confirmed {
		t.Fatalf("certificate edge = %+v %v", edge, ok)
	}
	if _, ok := nodeOf(state, DomainID("www.subject.test")); ok {
		t.Fatal("a certificate name became a domain node")
	}
}

// A certificate that is not valid, not matching or expired stays a clue.
func TestUnusableCertificatesStayPendingClues(t *testing.T) {
	cases := map[string]*CertificateInput{
		"expired":    {Valid: boolean(true), Match: boolean(true), Expired: boolean(true)},
		"invalid":    {Valid: boolean(false), Match: boolean(true)},
		"mismatch":   {Valid: boolean(true), Match: boolean(false)},
		"unreported": {},
	}
	for name, entry := range cases {
		entry.Serial = name
		entry.Domain = "subject.test"
		entry.Source = "stats_tool"
		state, _, err := push(t, nil, Input{Subject: "Subject Co", Certificates: []CertificateInput{*entry}}, onlyRoles(), &fakeCaller{})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if edge, ok := edgeOf(state, certificateID(name, "")); !ok || edge.Confirmed {
			t.Fatalf("%s edge = %+v %v", name, edge, ok)
		}
	}
}

// A second tool or domain cannot replace the first discovery of a serial.
func TestASharedCertificateKeepsItsFirstDomainAcrossBatches(t *testing.T) {
	state, _, err := push(t, nil, Input{
		Subject: "Subject Co",
		Domains: []DomainInput{{Domain: "a.test", Source: "t"}, {Domain: "b.test", Source: "t"}},
		Certificates: []CertificateInput{
			{Serial: "s1", Domain: "a.test", Issuer: "First CA", SubjectCN: "a.test", Valid: boolean(true), Match: boolean(true), Source: "first_tool"},
			{Serial: "s1", Domain: "b.test", Issuer: "Different CA", SubjectCN: "b.test", Valid: boolean(true), Match: boolean(true), Source: "second_tool"},
		},
	}, onlyRoles(), &fakeCaller{})
	if err != nil {
		t.Fatal(err)
	}
	next, _, err := push(t, stateJSON(t, state), Input{Subject: "Subject Co", Certificates: []CertificateInput{
		{Serial: " s1 ", Domain: "never-added.test", Issuer: "Third CA", Source: "third_tool"},
	}}, onlyRoles(), &fakeCaller{})
	if err != nil {
		t.Fatal(err)
	}
	if len(next.Edges) != 3 || len(next.Nodes) != 4 {
		t.Fatalf("nodes = %+v, edges = %+v", next.Nodes, next.Edges)
	}
	if edge, ok := edgeOf(next, certificateID("s1", "First CA")); !ok || edge.Source != DomainID("a.test") || !edge.Confirmed {
		t.Fatalf("certificate edge = %+v %v", edge, ok)
	}
	cert, _ := nodeOf(next, certificateID("s1", "First CA"))
	if cert.Detail == nil || cert.Detail.Issuer != "First CA" || len(cert.Detail.Basis) != 1 || cert.Detail.Basis[0].Source != "first_tool" {
		t.Fatalf("certificate = %+v", cert)
	}
	second, ok := nodeOf(next, DomainID("b.test"))
	if !ok || second.Detail != nil && len(second.Detail.Features) != 0 {
		t.Fatalf("second domain = %+v %v", second, ok)
	}
}

// A certificate without a usable domain, or one over the domain budget, hangs
// under the subject as a clue rather than creating an unrequested domain.
func TestCertificatesWithoutADomainStayUnderTheSubject(t *testing.T) {
	settings := onlyRoles()
	settings.MaxDomains = 1
	state, result, err := push(t, nil, Input{Subject: "Subject Co", Certificates: []CertificateInput{
		{Serial: "none", Domain: "", Valid: boolean(true), Match: boolean(true), Source: "t"},
		{Serial: "bad", Domain: "not a domain", Valid: boolean(true), Match: boolean(true), Source: "t"},
		{Serial: "kept", Domain: "kept.test", Source: "t"},
		{Serial: "over", Domain: "over.test", Valid: boolean(true), Match: boolean(true), Source: "t"},
	}}, settings, &fakeCaller{})
	if err != nil {
		t.Fatal(err)
	}
	for _, serial := range []string{"none", "bad"} {
		if edge, ok := edgeOf(state, certificateID(serial, "")); !ok || edge.Source != RootID || edge.Confirmed {
			t.Fatalf("%s edge = %+v %v", serial, edge, ok)
		}
	}
	if edge, ok := edgeOf(state, certificateID("kept", "")); !ok || edge.Source != DomainID("kept.test") {
		t.Fatalf("kept edge = %+v %v", edge, ok)
	}
	if edge, ok := edgeOf(state, certificateID("over", "")); !ok || edge.Source != RootID || edge.Confirmed {
		t.Fatalf("over-budget edge = %+v %v", edge, ok)
	}
	if result.RefusedDomains != 1 {
		t.Fatalf("result = %+v", result)
	}
}

// Blank or malformed entries are dropped instead of becoming anonymous nodes.
func TestBlankEntriesAreIgnored(t *testing.T) {
	state, result, err := push(t, nil, Input{
		Subject:      "Subject Co",
		Companies:    []CompanyInput{{Name: "  ", Source: "t"}, {Name: "Kept", Source: "t"}},
		Domains:      []DomainInput{{Domain: "bad_domain", Source: "t"}},
		Certificates: []CertificateInput{{Serial: " ", Domain: "kept.test", Source: "t"}},
	}, onlyRoles(), &fakeCaller{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Nodes != 2 || len(state.Edges) != 1 {
		t.Fatalf("result=%+v state=%+v", result, state)
	}
}

// A stored state that breaks the tree invariants cannot be continued without
// inventing a parent, so the call fails and leaves the panel untouched.
func TestACorruptPanelStateStopsTheMerge(t *testing.T) {
	broken := []string{
		`{"schema_version":1,"title":"Subject Co","collection":{"status":"idle","steps":[]},"nodes":[{"id":"company:name:A","kind":"company","label":"A"}],"edges":[],"heat":[],"components":[]}`,
		`{"schema_version":1,"title":"Subject Co","collection":{"status":"idle","steps":[]},"nodes":[{"id":"root","kind":"root","label":"Subject Co"},{"id":"domain:a.test","kind":"domain","label":"a.test"}],"edges":[{"source":"root","target":"domain:a.test"},{"source":"root","target":"domain:a.test"}],"heat":[],"components":[]}`,
		`{"schema_version":1,"title":"Subject Co","collection":{"status":"idle","steps":[]},"nodes":[{"id":"root","kind":"root","label":"Subject Co"}],"edges":[{"source":"root","target":"missing"}],"heat":[],"components":[]}`,
		`{"schema_version":1,"title":"Subject Co","collection":{"status":"idle","steps":[]},"nodes":[{"id":"root","kind":"root","label":"Subject Co"},{"id":"","kind":"domain","label":"a.test"}],"edges":[],"heat":[],"components":[]}`,
		`{"schema_version":1,"title":"Subject Co","collection":{"status":"idle","steps":[]},"nodes":[{"id":"root","kind":"root","label":"Subject Co"},{"id":"root","kind":"root","label":"Subject Co"}],"edges":[],"heat":[],"components":[]}`,
		`{"schema_version":1,"title":"Subject Co","collection":{"status":"idle","steps":[]},"nodes":[{"id":"root","kind":"root","label":"Subject Co"},{"id":"domain:a.test","kind":"domain","label":"a.test"}],"edges":[{"source":"domain:a.test","target":"domain:a.test"}],"heat":[],"components":[]}`,
		`{"schema_version":1,"title":"Subject Co","nodes":`,
	}
	for _, current := range broken {
		var out published
		if _, err := Run(t.Context(), Input{Subject: "Subject Co"}, onlyRoles(), json.RawMessage(current), &fakeCaller{}, out.publish); err == nil {
			t.Fatalf("accepted %s", current)
		}
		if len(out.snapshot()) != 0 {
			t.Fatalf("published for %s", current)
		}
	}
}

func TestRunRejectsMissingSubjectAndWiring(t *testing.T) {
	var out published
	for _, tc := range []struct {
		input    Input
		settings Settings
		caller   Caller
	}{
		{Input{Subject: " "}, testSettings(), &fakeCaller{}},
		{Input{Subject: "S"}, testSettings(), nil},
		{Input{Subject: "S"}, Settings{Tools: map[string]string{}}, &fakeCaller{}},
	} {
		if _, err := Run(t.Context(), tc.input, tc.settings, nil, tc.caller, out.publish); err == nil {
			t.Fatalf("accepted %+v", tc.input)
		}
	}
}

// A failed publication ends the run: the host could not record progress.
func TestPublishFailureEndsTheRun(t *testing.T) {
	want := errors.New("store closed")
	_, err := Run(t.Context(), Input{Subject: "S"}, testSettings(), nil, &fakeCaller{}, func(context.Context, json.RawMessage) error { return want })
	if !errors.Is(err, want) {
		t.Fatalf("err = %v", err)
	}
}

// Agent-supplied root details are kept only when they use safe URL schemes.
func TestRootDetailsKeepOnlySafeLinks(t *testing.T) {
	state, _, err := push(t, nil, Input{Subject: "S", Description: " d ", Logo: "javascript:x", Website: "https://s.test"}, onlyRoles(), &fakeCaller{})
	if err != nil {
		t.Fatal(err)
	}
	root, _ := nodeOf(state, RootID)
	if root.Detail == nil || root.Detail.Logo != "" || root.Detail.Website != "https://s.test" || root.Detail.Description != "d" {
		t.Fatalf("root detail = %+v", root.Detail)
	}
}

// gatedCaller holds every aggregation until all of them have arrived. A
// producer that queues the steps cannot fill the gate and reports it.
type gatedCaller struct {
	inner  *fakeCaller
	want   int
	mu     sync.Mutex
	count  int
	gate   chan struct{}
	serial bool
}

func newGatedCaller(inner *fakeCaller, want int) *gatedCaller {
	return &gatedCaller{inner: inner, gate: make(chan struct{}), want: want}
}

func (g *gatedCaller) Call(ctx context.Context, tool string, arguments map[string]any) (json.RawMessage, error) {
	g.mu.Lock()
	g.count++
	ready := g.count == g.want
	if ready {
		close(g.gate)
	}
	g.mu.Unlock()
	if !ready {
		select {
		case <-g.gate:
		case <-time.After(5 * time.Second):
			g.mu.Lock()
			g.serial = true
			g.mu.Unlock()
		}
	}
	return g.inner.Call(ctx, tool, arguments)
}

// Three aggregations that queue behind each other would make a demo wait for the
// slowest three times over, and a single final publication would leave the panel
// empty until the last one answered.
func TestQueryStepsRunConcurrentlyAndPublishEachCompletion(t *testing.T) {
	inner := &fakeCaller{replies: map[string]func(map[string]any) (string, error){
		RoleStats: func(arguments map[string]any) (string, error) {
			if arguments["fields"] == "ip" {
				return `{"query":"org=\"Subject\"","size":7,"distinct":{"ip":"3"}}`, nil
			}
			return `{"aggs":{"product":[{"name":"nginx","count":4}]}}`, nil
		},
		RoleHeat: fixed(`{"hot_trend":[{"date":"2026-01-02","value":1}]}`),
	}}
	// The gate only needs the metric steps, and the caller is called once per
	// step here because product rules are not configured.
	caller := newGatedCaller(inner, 3)
	var out published
	if _, err := Run(t.Context(), Input{Subject: "Subject", Query: `org="Subject"`}, onlyRoles(RoleStats, RoleHeat), nil, caller, out.publish); err != nil {
		t.Fatal(err)
	}
	if caller.serial {
		t.Fatal("the aggregations ran one after another instead of concurrently")
	}
	states := out.snapshot()
	if len(states) != 5 {
		t.Fatalf("publications = %d", len(states))
	}
	done := make([]int, len(states))
	for i, state := range states {
		for _, status := range state.Collection.Steps {
			if status.Status == statusDone {
				done[i]++
			}
		}
	}
	// The merge publication, one publication per finished step, then the final one.
	if got := done; len(got) != 5 || got[1]-got[0] != 1 || got[2]-got[1] != 1 || got[3]-got[2] != 1 || got[4] != 3 {
		t.Fatalf("finished steps per publication = %v", done)
	}
	last := out.last()
	if last.Collection.Status != statusDone || stepStatus(last, StepSummary).Status != statusDone || stepStatus(last, StepHeat).Status != statusDone || stepStatus(last, StepComponents).Status != statusDone {
		t.Fatalf("final collection = %+v", last.Collection)
	}
	if last.Summary == nil || *last.Summary.FOFAAssets != 7 || *last.Summary.FOFAIPs != 3 || *last.Summary.Clues != 0 {
		t.Fatalf("summary = %+v", last.Summary)
	}
	if len(last.Heat) != 1 || len(last.Components) != 1 {
		t.Fatalf("heat=%+v components=%+v", last.Heat, last.Components)
	}
}

// A cancelled aggregation must not leave a spinner behind: every step that
// never finished is recorded as interrupted on a context that outlives the run.
func TestCancellationMarksUnfinishedStepsInterrupted(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	caller := &fakeCaller{replies: map[string]func(map[string]any) (string, error){
		RoleStats: func(map[string]any) (string, error) { cancel(); return `{"size":1}`, nil },
	}}
	// Heat is left unconfigured, so its status is decided before the run starts.

	var out published
	_, err := Run(ctx, Input{Subject: "Subject", Query: `org="Subject"`}, onlyRoles(RoleStats), nil, caller, out.publish)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	state := out.last()
	if state.Collection.Status != statusInterrupted {
		t.Fatalf("collection = %+v", state.Collection)
	}
	for _, step := range Steps {
		switch got := stepStatus(state, step).Status; got {
		case statusInterrupted, statusDone, statusUnavailable:
		default:
			t.Fatalf("%s = %q left a spinner on a stopped panel", step, got)
		}
	}
}

// The query in the panel is the range its numbers describe. A call without one
// keeps the previous range and its numbers; a new range clears them before the
// new aggregation, so old-range totals never appear under a new query.
func TestQueryChangesClearAndRerunTheMetrics(t *testing.T) {
	reply := func(value any) (string, error) {
		raw, err := json.Marshal(value)
		return string(raw), err
	}
	// Every role answers with the query it was asked about, so a reply for
	// another range is refused by the producer instead of shown.
	portraitCaller := func(query string) *fakeCaller {
		return &fakeCaller{replies: map[string]func(map[string]any) (string, error){
			RoleStats: func(arguments map[string]any) (string, error) {
				if arguments["fields"] == "ip" {
					return reply(map[string]any{"query": query, "size": 4, "distinct": map[string]int{"ip": 1}})
				}
				return reply(map[string]any{"query": query, "aggs": map[string]any{"product": []any{}}})
			},
			RoleHeat: func(map[string]any) (string, error) {
				return reply(map[string]any{"query": query, "hot_trend": []any{map[string]any{"date": "2026-01-02", "value": 1}}})
			},
		}}
	}
	settings := onlyRoles(RoleStats, RoleHeat)
	first, _, err := push(t, nil, Input{Subject: "Subject", Query: `org="Subject"`}, settings, portraitCaller(`org="Subject"`))
	if err != nil {
		t.Fatal(err)
	}
	if first.Query != `org="Subject"` || first.Summary == nil || first.Summary.FOFAAssets == nil || *first.Summary.FOFAAssets != 4 {
		t.Fatalf("first = %+v", first)
	}

	// Nothing changed, so the finished aggregations are not repeated.
	caller := portraitCaller(`org="Subject"`)
	same, _, err := push(t, stateJSON(t, first), Input{Subject: "Subject", Query: `org="Subject"`}, settings, caller)
	if err != nil {
		t.Fatal(err)
	}
	if len(caller.requests(RoleStats))+len(caller.requests(RoleHeat)) != 0 || same.Collection.Status != statusDone {
		t.Fatalf("calls=%v collection=%+v", caller.requests(RoleStats), same.Collection)
	}

	// A batch without a query keeps the range and its numbers.
	kept, _, err := push(t, stateJSON(t, same), Input{Subject: "Subject"}, settings, &fakeCaller{})
	if err != nil {
		t.Fatal(err)
	}
	// All three aggregations finished, so the collection stays done.
	if kept.Query != `org="Subject"` || kept.Summary == nil || *kept.Summary.FOFAAssets != 4 || kept.Collection.Status != statusDone || len(kept.Heat) != 1 {
		t.Fatalf("kept = %+v", kept)
	}

	// A new range leaves nothing of the previous one behind.
	changed, _, err := push(t, stateJSON(t, kept), Input{Subject: "Subject", Query: `org="Other"`}, settings, portraitCaller(`org="Other"`))
	if err != nil {
		t.Fatal(err)
	}
	if changed.Query != `org="Other"` || changed.Summary == nil || changed.Summary.FOFAAssets == nil || *changed.Summary.FOFAAssets != 4 {
		t.Fatalf("changed = %+v", changed.Summary)
	}

	// A new range whose aggregation fails keeps no number from the old range.
	failing, _, err := push(t, stateJSON(t, kept), Input{Subject: "Subject", Query: `org="Broken"`}, settings, &fakeCaller{replies: map[string]func(map[string]any) (string, error){
		RoleStats: fixed(`{"error":true}`),
		RoleHeat:  fixed(`{"error":true}`),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if failing.Query != `org="Broken"` || failing.Summary == nil || failing.Summary.FOFAAssets != nil || failing.Summary.FOFAIPs != nil || len(failing.Heat) != 0 {
		t.Fatalf("failing state = %+v %+v", failing.Summary, failing.Heat)
	}
	if got := stepStatus(failing, StepSummary); got.Status != statusFailed || got.Code != "tool_error" {
		t.Fatalf("summary step = %+v", got)
	}
}

// A failed aggregation leaves its own step failed while the others finish; the
// panel reports all three rather than dropping the ones that never ran.
func TestAFailedAggregationMarksOnlyItsStep(t *testing.T) {
	var out published
	if _, err := Run(t.Context(), Input{Subject: "Subject", Query: `org="Subject"`}, onlyRoles(RoleStats), nil, &fakeCaller{replies: map[string]func(map[string]any) (string, error){
		RoleStats: fixed(`{"error":true,"errmsg":"quota for key abc"}`),
	}}, out.publish); err != nil {
		t.Fatal(err)
	}
	state := out.last()
	if state.Collection.Status != statusDone {
		t.Fatalf("collection = %+v", state.Collection)
	}
	if got := stepStatus(state, StepSummary); got.Status != statusFailed || got.Code != "tool_error" {
		t.Fatalf("summary = %+v", got)
	}
	if got := stepStatus(state, StepHeat); got.Status != statusUnavailable || got.Code != "not_configured" {
		t.Fatalf("heat = %+v", got)
	}
	raw, _ := json.Marshal(state)
	if strings.Contains(string(raw), "abc") {
		t.Fatal("remote error text leaked into the panel")
	}
}
