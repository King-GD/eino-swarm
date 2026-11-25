package easm

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

const metricQuery = `org="Subject"`

type metricReplies = map[string]func(map[string]any) (string, error)

// runMetrics collects the aggregations for one portrait. current carries a graph
// the agent pushed earlier; nil starts from an empty panel.
func runMetrics(t *testing.T, current json.RawMessage, settings Settings, caller Caller) State {
	t.Helper()
	var out published
	if _, err := Run(t.Context(), Input{Subject: "Subject", Query: metricQuery}, settings, current, caller, out.publish); err != nil {
		t.Fatal(err)
	}
	return out.last()
}

// portrait pushes a graph without a query, so metric tests can start from it.
func portrait(t *testing.T, input Input) json.RawMessage {
	t.Helper()
	state, _, err := push(t, nil, input, onlyRoles(), &fakeCaller{})
	if err != nil {
		t.Fatal(err)
	}
	return stateJSON(t, state)
}

// findRequest returns the recorded request carrying this fields value; the
// aggregations run concurrently, so their order is not a contract.
func findRequest(t *testing.T, caller *fakeCaller, tool, fields string) map[string]any {
	t.Helper()
	for _, arguments := range caller.requests(tool) {
		if arguments["fields"] == fields {
			return arguments
		}
	}
	t.Fatalf("no %s request for fields %q", tool, fields)
	return nil
}

// The summary row shows FOFA's own totals for the confirmed query; sending
// another backend or field would report different numbers under FOFA's name.
// Clues count what the agent pushed into the graph, not its own prose.
func TestSummaryShowsFOFATotalsAndGraphClues(t *testing.T) {
	caller := &fakeCaller{replies: metricReplies{
		RoleStats: func(arguments map[string]any) (string, error) {
			if arguments["fields"] == "ip" {
				return `{"query":"org=\"Subject\"","size":9007199254740993,"distinct":{"ip":"76"}}`, nil
			}
			return `{"aggs":{"product":[]}}`, nil
		},
	}}
	current := portrait(t, Input{Subject: "Subject", Companies: []CompanyInput{{Name: "A", Source: "t"}, {Name: "B", Source: "t"}}})
	state := runMetrics(t, current, onlyRoles(RoleStats), caller)
	summary := state.Summary
	if summary == nil || *summary.Clues != 2 || *summary.FOFAAssets != 9007199254740993 || *summary.FOFAIPs != 76 {
		t.Fatalf("summary = %+v", summary)
	}
	want := map[string]any{"query": metricQuery, "fields": "ip", "uniq_count": "ip", "tool": "fofa"}
	if got := findRequest(t, caller, RoleStats, "ip"); !reflect.DeepEqual(got, want) {
		t.Fatalf("summary arguments = %v", got)
	}
	if stepStatus(state, StepSummary).Status != statusDone {
		t.Fatalf("summary step = %+v", stepStatus(state, StepSummary))
	}
}

// A reply that carries only one total keeps the other absent instead of
// showing a zero the source never reported.
func TestSummaryKeepsAnAbsentTotalAbsent(t *testing.T) {
	caller := &fakeCaller{replies: metricReplies{RoleStats: func(arguments map[string]any) (string, error) {
		if arguments["fields"] == "ip" {
			return `{"distinct":{"ip":0}}`, nil
		}
		return `{"aggs":{"product":[]}}`, nil
	}}}
	state := runMetrics(t, nil, onlyRoles(RoleStats), caller)
	if s := state.Summary; s == nil || s.FOFAAssets != nil || s.FOFAIPs == nil || *s.FOFAIPs != 0 || *s.Clues != 0 {
		t.Fatalf("summary = %+v", s)
	}
}

// Rounded, negative or missing totals would be shown as exact counts; the
// step fails instead and the clue count found by the graph stays visible.
func TestSummaryRefusesInexactTotals(t *testing.T) {
	for _, body := range []string{
		`{"size":12.5}`, `{"size":1e3}`, `{"size":-1}`, `{"size":"many"}`, `{"size":true}`,
		`{"distinct":{"ip":4.0}}`, `{"size":99999999999999999999}`, `{}`, `{"size":null}`,
		`{"query":"other","size":3}`, `{"query":5,"size":3}`, `[1]`,
	} {
		caller := &fakeCaller{replies: metricReplies{RoleStats: fixed(body)}}
		state := runMetrics(t, nil, onlyRoles(RoleStats), caller)
		if got := stepStatus(state, StepSummary); got.Status != statusFailed || got.Code != "malformed_reply" {
			t.Fatalf("%s: summary step = %+v", body, got)
		}
		if s := state.Summary; s == nil || s.Clues == nil || s.FOFAAssets != nil || s.FOFAIPs != nil {
			t.Fatalf("%s: summary = %+v", body, s)
		}
	}
}

// Error envelopes and unavailable tools fail only the step that hit them, with
// a stable code instead of the remote's message.
func TestMetricToolFailuresMarkOnlyTheirStep(t *testing.T) {
	caller := &fakeCaller{replies: metricReplies{
		RoleStats: fixed(`{"error":true,"errmsg":"quota for key abc"}`),
		RoleHeat:  func(map[string]any) (string, error) { return "", ErrUnavailable },
	}}
	state := runMetrics(t, nil, onlyRoles(RoleStats, RoleHeat), caller)
	for step, want := range map[Step]StepStatus{
		StepSummary:    {ID: StepSummary, Status: statusFailed, Code: "tool_error"},
		StepHeat:       {ID: StepHeat, Status: statusUnavailable, Code: "unavailable"},
		StepComponents: {ID: StepComponents, Status: statusFailed, Code: "tool_error"},
	} {
		if got := stepStatus(state, step); got != want {
			t.Fatalf("%s = %+v", step, got)
		}
	}
	if len(state.Heat) != 0 || len(state.Components) != 0 || state.Collection.Status != statusDone {
		t.Fatalf("state = %+v", state)
	}
}

// The trend chart plots exactly the returned points, including fractional
// and zero values, in the order the source reported them.
func TestHeatKeepsReturnedPoints(t *testing.T) {
	caller := &fakeCaller{replies: metricReplies{RoleHeat: fixed(`{"hot_trend":[{"date":"2026-01-02","value":0},{"date":"2026-01-09","value":"8"},{"date":"2026-01-16","value":2.5}]}`)}}
	state := runMetrics(t, nil, onlyRoles(RoleHeat), caller)
	want := []HeatPoint{{"2026-01-02", 0}, {"2026-01-09", 8}, {"2026-01-16", 2.5}}
	if !reflect.DeepEqual(state.Heat, want) {
		t.Fatalf("heat = %+v", state.Heat)
	}
	if got := caller.requests(RoleHeat); len(got) != 1 || !reflect.DeepEqual(got[0], map[string]any{"query": metricQuery}) {
		t.Fatalf("heat arguments = %v", got)
	}
	empty := runMetrics(t, nil, onlyRoles(RoleHeat), &fakeCaller{replies: metricReplies{RoleHeat: fixed(`{"hot_trend":[]}`)}})
	if stepStatus(empty, StepHeat).Status != statusDone || empty.Heat == nil || len(empty.Heat) != 0 {
		t.Fatalf("empty trend = %+v", empty.Heat)
	}
}

// The renderer rejects a whole panel whose trend is unordered or has an
// invalid date, so such a reply fails the step and publishes no points.
func TestHeatRefusesPointsTheChartCannotPlot(t *testing.T) {
	for _, body := range []string{
		`{"hot_trend":[{"date":"2026-01-09","value":1},{"date":"2026-01-02","value":1}]}`,
		`{"hot_trend":[{"date":"2026-01-02","value":1},{"date":"2026-01-02","value":1}]}`,
		`{"hot_trend":[{"date":"2026-02-30","value":1}]}`,
		`{"hot_trend":[{"date":"2026-1-2","value":1}]}`,
		`{"hot_trend":[{"date":"2026-01-02"}]}`,
		`{"hot_trend":[{"date":"2026-01-02","value":-1}]}`,
		`{"hot_trend":[{"date":"2026-01-02","value":"x"}]}`,
		`{"hot_trend":[{"date":"2026-01-02","value":1e400}]}`,
		`{"hot_trend":null}`, `{}`, `{"hot_trend":{}}`,
		`{"query":"other","hot_trend":[]}`,
	} {
		state := runMetrics(t, nil, onlyRoles(RoleHeat), &fakeCaller{replies: metricReplies{RoleHeat: fixed(body)}})
		if got := stepStatus(state, StepHeat); got.Status != statusFailed || got.Code != "malformed_reply" || len(state.Heat) != 0 {
			t.Fatalf("%s: heat = %+v %+v", body, got, state.Heat)
		}
	}
}

func componentCaller(products string, rules func(map[string]any) (string, error)) *fakeCaller {
	return &fakeCaller{replies: metricReplies{
		RoleStats: func(arguments map[string]any) (string, error) {
			switch arguments["fields"] {
			case "ip":
				return `{"size":1,"distinct":{"ip":1}}`, nil
			case "cloud_name":
				return `{"aggs":{"cloud_name":[]}}`, nil
			default:
				return products, nil
			}
		},
		RoleProductRules: rules,
	}}
}

// Without a rule lookup a numeric product ID is still a usable, stable name;
// repeated buckets with the same count are one product.
func TestComponentsKeepSourceNamesAndIDsWithoutRuleLookup(t *testing.T) {
	caller := componentCaller(`{"aggs":{"product":[{"name":"Product A","count":5},{"name":9007199254740993,"count":"4"},{"name":"Product A","count":5},{"name":7,"count":0}]}}`, nil)
	state := runMetrics(t, nil, onlyRoles(RoleStats), caller)
	want := []Component{{Name: "Product A", Count: 5}, {Name: "9007199254740993", Count: 4}, {Name: "7", Count: 0}}
	if !reflect.DeepEqual(state.Components, want) {
		t.Fatalf("components = %+v", state.Components)
	}
	if got := findRequest(t, caller, RoleStats, componentLayers); got["tool"] != "fofa" || got["query"] != metricQuery || got["size"] != testSettings().MaxComponents {
		t.Fatalf("component arguments = %v", got)
	}
	if len(caller.requests(RoleProductRules)) != 0 {
		t.Fatal("an unconfigured rule lookup was called")
	}
}

// A configured rule lookup names numeric products for the reader while the
// ID stays the component's identity; only requested, unambiguous rules apply.
func TestComponentsUseRuleNamesWhenConfigured(t *testing.T) {
	var requested any
	caller := componentCaller(`{"aggs":{"product":[{"name":"Named","count":3},{"name":901,"count":9},{"name":902,"count":6},{"name":903,"count":4},{"name":904,"count":1}]}}`,
		func(arguments map[string]any) (string, error) {
			requested = arguments["rule_ids"]
			return `{"results":[{"rule_id":901,"product":"Rule A"},{"rule_id":"902","product":"","en_product":"Rule B"},{"rule_id":903,"product":"One"},{"rule_id":903,"product":"Two"},{"rule_id":999,"product":"Unrequested"},{"product":"No id"}]}`, nil
		})
	state := runMetrics(t, nil, onlyRoles(RoleStats, RoleProductRules), caller)
	labels := map[string]string{}
	for _, component := range state.Components {
		labels[component.Name] = component.DisplayName
	}
	want := map[string]string{"Named": "", "901": "Rule A", "902": "Rule B", "903": "", "904": ""}
	if !reflect.DeepEqual(labels, want) {
		t.Fatalf("labels = %v", labels)
	}
	if got, _ := json.Marshal(requested); string(got) != `[901,902,903,904]` {
		t.Fatalf("rule lookup arguments = %s", got)
	}
	if stepStatus(state, StepComponents).Status != statusDone {
		t.Fatalf("components step = %+v", stepStatus(state, StepComponents))
	}
}

// Names only decorate collected components: a failed rule lookup keeps the
// buckets, and a reply without numeric IDs needs no lookup at all.
func TestComponentsSurviveAFailedRuleLookup(t *testing.T) {
	for _, rules := range []func(map[string]any) (string, error){
		fixed(`{"error":true}`),
		func(map[string]any) (string, error) { return "", errors.New("transport down") },
	} {
		caller := componentCaller(`{"aggs":{"product":[{"name":901,"count":9}]}}`, rules)
		state := runMetrics(t, nil, onlyRoles(RoleStats, RoleProductRules), caller)
		if len(state.Components) != 1 || state.Components[0].DisplayName != "" || stepStatus(state, StepComponents).Status != statusDone {
			t.Fatalf("components = %+v step=%+v", state.Components, stepStatus(state, StepComponents))
		}
	}
	caller := componentCaller(`{"aggs":{"product":[{"name":"Named","count":9}]}}`, fixed(`{"results":[]}`))
	runMetrics(t, nil, onlyRoles(RoleStats, RoleProductRules), caller)
	if len(caller.requests(RoleProductRules)) != 0 {
		t.Fatal("rule lookup called without numeric product IDs")
	}
}

// Inexact counts, unusable names or conflicting duplicates would misreport
// the product table, so the step fails and publishes no components.
func TestComponentsRefuseMalformedBuckets(t *testing.T) {
	for _, body := range []string{
		`{"aggs":{"product":[{"name":"A","count":1.5}]}}`,
		`{"aggs":{"product":[{"name":"A","count":-1}]}}`,
		`{"aggs":{"product":[{"name":"A"}]}}`,
		`{"aggs":{"product":[{"name":" ","count":1}]}}`,
		`{"aggs":{"product":[{"name":-7,"count":1}]}}`,
		`{"aggs":{"product":[{"name":7.0,"count":1}]}}`,
		`{"aggs":{"product":[{"count":1}]}}`,
		`{"aggs":{"product":[{"name":"A","count":1},{"name":"A","count":2}]}}`,
		`{"aggs":{"product":null}}`, `{"aggs":{"ip":[]}}`, `{"aggs":{"product":{}}}`,
		`{"query":"other","aggs":{"product":[]}}`,
	} {
		state := runMetrics(t, nil, onlyRoles(RoleStats), componentCaller(body, nil))
		if got := stepStatus(state, StepComponents); got.Status != statusFailed || got.Code != "malformed_reply" || len(state.Components) != 0 {
			t.Fatalf("%s: components = %+v %+v", body, got, state.Components)
		}
	}
}
