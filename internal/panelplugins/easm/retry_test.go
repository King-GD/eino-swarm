package easm

import (
	"strings"
	"testing"
)

func aggregationReplies(summary, heat string) *fakeCaller {
	return &fakeCaller{replies: metricReplies{
		RoleStats: func(arguments map[string]any) (string, error) {
			switch arguments["fields"] {
			case "ip":
				return summary, nil
			case "cloud_name":
				return `{"aggs":{"cloud_name":[]}}`, nil
			default:
				return `{"aggs":{"product":[{"name":"Component A","count":4}]}}`, nil
			}
		},
		RoleHeat: fixed(heat),
	}}
}

// requestsFor counts one tool's requests for a fields value: the aggregations
// share the stats tool, so a bare call count would mix them.
func requestsFor(caller *fakeCaller, tool, fields string) int {
	count := 0
	for _, request := range caller.requests(tool) {
		if request["fields"] == fields {
			count++
		}
	}
	return count
}

// A trend outage must not make the user lose totals from the same query.
func TestRepeatingQueryRetriesOnlyUnfinishedAggregations(t *testing.T) {
	settings := onlyRoles(RoleStats, RoleHeat)
	input := Input{Subject: "Subject", Query: metricQuery}
	first, _, err := push(t, nil, input, settings, aggregationReplies(`{"size":7,"distinct":{"ip":3}}`, `{"error":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if stepStatus(first, StepHeat).Status != statusFailed || stepStatus(first, StepSummary).Status != statusDone || stepStatus(first, StepComponents).Status != statusDone {
		t.Fatalf("initial steps = %+v", first.Collection.Steps)
	}

	input.Domains = []DomainInput{{Domain: "service.test", Source: "fixture"}}
	retry := aggregationReplies(`{"error":true}`, `{"hot_trend":[{"date":"2026-01-02","value":2}]}`)
	var out published
	result, err := Run(t.Context(), input, settings, stateJSON(t, first), retry, out.publish)
	if err != nil {
		t.Fatal(err)
	}
	if len(retry.requests(RoleStats)) != 0 || len(retry.requests(RoleHeat)) != 1 {
		t.Errorf("stats requests = %v, heat requests = %v", retry.requests(RoleStats), retry.requests(RoleHeat))
	}
	for _, state := range out.snapshot() {
		summary := state.Summary
		if summary == nil || summary.FOFAAssets == nil || *summary.FOFAAssets != 7 || summary.FOFAIPs == nil || *summary.FOFAIPs != 3 || summary.Clues == nil || *summary.Clues != 1 {
			t.Fatalf("repeated query published summary %+v", summary)
		}
		if stepStatus(state, StepSummary).Status != statusDone || stepStatus(state, StepComponents).Status != statusDone || len(state.Components) != 1 {
			t.Fatalf("repeated query published steps %+v and components %+v", state.Collection.Steps, state.Components)
		}
	}
	if last := out.last(); len(last.Heat) != 1 || result.Next != finishedNext {
		t.Fatalf("trend = %+v, next = %q", last.Heat, result.Next)
	}
}

// Counts seen separately stay useful, but they cannot prove that one response
// supplied a complete summary. The valid complete answer may contain zeroes.
func TestSummaryNeedsBothTotalsInOneResponse(t *testing.T) {
	settings := onlyRoles(RoleStats, RoleHeat)
	input := Input{Subject: "Subject", Query: metricQuery}
	state, result, err := push(t, nil, input, settings, aggregationReplies(`{"size":7}`, `{"hot_trend":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	if stepStatus(state, StepSummary).Status != statusFailed || !strings.Contains(result.Next, "missing the distinct IP total") || state.Summary == nil || state.Summary.FOFAAssets == nil || *state.Summary.FOFAAssets != 7 || state.Summary.FOFAIPs != nil {
		t.Fatalf("initial summary = %+v, receipt = %+v", state.Summary, result)
	}

	for _, attempt := range []struct {
		reply, status, next string
		assets, ips         int64
	}{
		{`{"distinct":{"ip":3}}`, statusFailed, "missing the asset total", 7, 3},
		{`{"error":true}`, statusFailed, "summary (failed)", 7, 3},
		{`{"size":0,"distinct":{"ip":0}}`, statusDone, finishedNext, 0, 0},
	} {
		caller := aggregationReplies(attempt.reply, `{"error":true}`)
		state, result, err = push(t, stateJSON(t, state), input, settings, caller)
		if err != nil {
			t.Fatal(err)
		}
		if summary := requestsFor(caller, RoleStats, "ip"); summary != 1 || len(caller.requests(RoleHeat)) != 0 {
			t.Fatalf("%s: summary requests = %d, heat requests = %v", attempt.reply, summary, caller.requests(RoleHeat))
		}
		if g := state.Summary; g == nil || g.FOFAAssets == nil || *g.FOFAAssets != attempt.assets || g.FOFAIPs == nil || *g.FOFAIPs != attempt.ips {
			t.Fatalf("%s: summary = %+v", attempt.reply, g)
		}
		if stepStatus(state, StepSummary).Status != attempt.status || !strings.Contains(result.Next, attempt.next) {
			t.Fatalf("%s: receipt = %+v", attempt.reply, result)
		}
	}
}

// A saved partial summary from the older producer must be repairable, even if
// every old step claimed it was done.
func TestRepeatingQueryRepairsLegacyPartialSummary(t *testing.T) {
	settings := onlyRoles(RoleStats, RoleHeat)
	input := Input{Subject: "Subject", Query: metricQuery}
	state, _, err := push(t, nil, input, settings, aggregationReplies(`{"size":7,"distinct":{"ip":3}}`, `{"hot_trend":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	state.Summary.FOFAIPs = nil
	caller := aggregationReplies(`{"size":7,"distinct":{"ip":4}}`, `{"error":true}`)
	state, result, err := push(t, stateJSON(t, state), input, settings, caller)
	if err != nil {
		t.Fatal(err)
	}
	if summary := requestsFor(caller, RoleStats, "ip"); summary != 1 || len(caller.requests(RoleHeat)) != 0 {
		t.Errorf("summary requests = %d, heat requests = %v", summary, caller.requests(RoleHeat))
	}
	if state.Summary == nil || state.Summary.FOFAIPs == nil || *state.Summary.FOFAIPs != 4 || result.Next != finishedNext {
		t.Fatalf("repaired summary = %+v, receipt = %+v", state.Summary, result)
	}
}
