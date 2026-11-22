package easm

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
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

type published struct{ states []State }

func (p *published) publish(_ context.Context, body json.RawMessage) error {
	var state State
	if err := json.Unmarshal(body, &state); err != nil {
		return err
	}
	p.states = append(p.states, state)
	return nil
}

func (p *published) last() State { return p.states[len(p.states)-1] }

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

func onlyRoles(roles ...string) Settings {
	settings := testSettings()
	settings.Tools = map[string]string{}
	for _, role := range roles {
		settings.Tools[role] = role
	}
	return settings
}

// The subject's own organization record is the root: its domains must hang
// directly under the target rather than under a duplicate company node.
func TestSubjectOrganizationDomainsHangUnderTheRoot(t *testing.T) {
	caller := &fakeCaller{replies: map[string]func(map[string]any) (string, error){
		RoleOrganizations: fixed(`{"results":[{"id":"9007199254740993","name":"Subject Co","domains":[{"domain":"Example.TEST."},{"domain":"bad_domain"}]},{"id":12,"name":"Other Co","domains":[{"domain":"other.test"}]}]}`),
	}}
	var out published
	result, err := Run(t.Context(), Input{Subject: "Subject Co"}, onlyRoles(RoleOrganizations), caller, out.publish)
	if err != nil {
		t.Fatal(err)
	}
	state := out.last()
	if edge, ok := edgeOf(state, "domain:example.test"); !ok || edge.Source != RootID || !edge.Confirmed {
		t.Fatalf("subject domain edge = %+v %v", edge, ok)
	}
	root, _ := nodeOf(state, RootID)
	if root.Detail == nil || root.Detail.OrganizationID != "9007199254740993" {
		t.Fatalf("exact organization id lost: %+v", root.Detail)
	}
	if edge, ok := edgeOf(state, "company:org:12"); !ok || edge.Confirmed {
		t.Fatalf("other organization must be a pending clue: %+v %v", edge, ok)
	}
	if edge, ok := edgeOf(state, "domain:other.test"); !ok || edge.Source != "company:org:12" || !edge.Confirmed {
		t.Fatalf("other organization's domain = %+v", edge)
	}
	if _, ok := nodeOf(state, "domain:bad_domain"); ok {
		t.Fatal("malformed domain entered the graph")
	}
	if got := caller.calls[0].Arguments["names"].([]string); len(got) != 1 || got[0] != "Subject Co" {
		t.Fatalf("lookup arguments = %v", caller.calls[0].Arguments)
	}
	if state.Collection.Status != statusDone || stepStatus(state, StepOrganizations).Status != statusDone || result.Nodes != 4 {
		t.Fatalf("collection = %+v result=%+v", state.Collection, result)
	}
}

// Two registrations sharing the subject's name cannot both be the subject.
func TestAmbiguousSameNameOrganizationsStayPendingClues(t *testing.T) {
	caller := &fakeCaller{replies: map[string]func(map[string]any) (string, error){
		RoleOrganizations: fixed(`{"results":[{"id":"1","name":"Same","domains":[{"domain":"a.test"}]},{"id":"2","name":"Same","domains":[{"domain":"b.test"}]}]}`),
	}}
	var out published
	if _, err := Run(t.Context(), Input{Subject: "Same"}, onlyRoles(RoleOrganizations), caller, out.publish); err != nil {
		t.Fatal(err)
	}
	state := out.last()
	for _, id := range []string{"company:org:1", "company:org:2"} {
		if edge, ok := edgeOf(state, id); !ok || edge.Confirmed {
			t.Fatalf("%s = %+v %v", id, edge, ok)
		}
	}
}

// A tool-level error envelope fails only its step; the run still completes
// and other configured steps are reported rather than silently dropped.
func TestFailedLookupMarksOnlyItsStep(t *testing.T) {
	caller := &fakeCaller{replies: map[string]func(map[string]any) (string, error){
		RoleOrganizations: fixed(`{"error":true,"errmsg":"quota exceeded for key abc"}`),
	}}
	var out published
	result, err := Run(t.Context(), Input{Subject: "S", Query: `org="S"`}, onlyRoles(RoleOrganizations), caller, out.publish)
	if err != nil {
		t.Fatal(err)
	}
	state := out.last()
	if got := stepStatus(state, StepOrganizations); got.Status != statusFailed || got.Code != "tool_error" {
		t.Fatalf("organizations = %+v", got)
	}
	if got := stepStatus(state, StepRelations); got.Status != statusUnavailable {
		t.Fatalf("unconfigured relations = %+v", got)
	}
	raw, _ := json.Marshal(result)
	if strings.Contains(string(raw), "abc") {
		t.Fatal("remote error text leaked into the receipt")
	}
}

// Metrics need an asset query; without one they are skipped, not failed.
func TestMetricsWithoutQueryAreSkipped(t *testing.T) {
	var out published
	if _, err := Run(t.Context(), Input{Subject: "S"}, testSettings(), &fakeCaller{}, out.publish); err != nil {
		t.Fatal(err)
	}
	for _, step := range []Step{StepSummary, StepHeat, StepComponents} {
		if got := stepStatus(out.last(), step); got.Status != statusSkipped || got.Code != "no_query" {
			t.Fatalf("%s = %+v", step, got)
		}
	}
}

// Stop must not leave a spinner on a reloaded panel: the steps that never
// finished are recorded as interrupted and the run returns the cancellation.
func TestCancellationMarksUnfinishedStepsInterrupted(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	caller := &fakeCaller{replies: map[string]func(map[string]any) (string, error){
		RoleOrganizations: func(map[string]any) (string, error) { cancel(); return `{"results":[]}`, nil },
	}}
	var out published
	_, err := Run(ctx, Input{Subject: "S"}, onlyRoles(RoleOrganizations), caller, out.publish)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	state := out.last()
	if state.Collection.Status != statusInterrupted || stepStatus(state, StepOrganizations).Status != statusInterrupted || stepStatus(state, StepHeat).Status != statusInterrupted {
		t.Fatalf("collection = %+v", state.Collection)
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
		if _, err := Run(t.Context(), tc.input, tc.settings, tc.caller, out.publish); err == nil {
			t.Fatalf("accepted %+v", tc.input)
		}
	}
}

// A failed publication ends the run: the host could not record progress.
func TestPublishFailureEndsTheRun(t *testing.T) {
	want := errors.New("store closed")
	_, err := Run(t.Context(), Input{Subject: "S"}, testSettings(), &fakeCaller{}, func(context.Context, json.RawMessage) error { return want })
	if !errors.Is(err, want) {
		t.Fatalf("err = %v", err)
	}
}

// Agent-supplied root details are kept only when they use safe URL schemes.
func TestRootDetailsKeepOnlySafeLinks(t *testing.T) {
	var out published
	if _, err := Run(t.Context(), Input{Subject: "S", Description: "d", Logo: "javascript:x", Website: "https://s.test"}, onlyRoles(), &fakeCaller{}, out.publish); err != nil {
		t.Fatal(err)
	}
	root, _ := nodeOf(out.last(), RootID)
	if root.Detail == nil || root.Detail.Logo != "" || root.Detail.Website != "https://s.test" || root.Detail.Description != "d" {
		t.Fatalf("root detail = %+v", root.Detail)
	}
}
