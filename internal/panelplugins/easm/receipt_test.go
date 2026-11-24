package easm

import (
	"strings"
	"testing"
)

// The receipt drives the agent's closing summary, so it may name only what the
// panel really holds. A completion sentence sent alongside unavailable or
// failed aggregations made the agent report numbers the panel never collected.
func TestReceiptNamesEveryAggregationThatDidNotFinish(t *testing.T) {
	settings := onlyRoles()
	settings.Tools = map[string]string{RoleHeat: RoleHeat}
	caller := &fakeCaller{replies: metricReplies{RoleHeat: fixed(`{"error":true,"errmsg":"quota for key abc"}`)}}
	_, result, err := push(t, nil, Input{Subject: "Subject Co", Query: metricQuery}, settings, caller)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"summary (unavailable)", "heat (failed)", "components (unavailable)", "end the turn"} {
		if !strings.Contains(result.Next, want) {
			t.Fatalf("next = %q, want %q", result.Next, want)
		}
	}
	if result.Next == finishedNext || strings.Contains(result.Next, "holds the totals") {
		t.Fatalf("next claims completion: %q", result.Next)
	}
}

// Completion is worded only when every step reported a result for this query;
// the same call repeated for an already collected query stays just as honest.
func TestReceiptClaimsCompletionOnlyWhenEveryAggregationFinished(t *testing.T) {
	caller := componentCaller(`{"aggs":{"product":[]}}`, nil)
	caller.replies[RoleHeat] = fixed(`{"hot_trend":[]}`)
	state, result, err := push(t, nil, Input{Subject: "Subject Co", Query: metricQuery}, onlyRoles(RoleStats, RoleHeat), caller)
	if err != nil || result.Next != finishedNext {
		t.Fatalf("next=%q err=%v", result.Next, err)
	}
	_, again, err := push(t, stateJSON(t, state), Input{Subject: "Subject Co", Query: metricQuery}, onlyRoles(RoleStats, RoleHeat), caller)
	if err != nil || again.Next != finishedNext {
		t.Fatalf("repeated next=%q err=%v", again.Next, err)
	}
}

// A batch without a query only feeds the graph. The panel still waits for the
// final aggregation, so the receipt must not tell the agent to stop.
func TestReceiptStaysSilentWithoutAQuery(t *testing.T) {
	settings := onlyRoles()
	settings.Tools = map[string]string{RoleHeat: RoleHeat}
	caller := &fakeCaller{replies: metricReplies{RoleHeat: fixed(`{"hot_trend":[]}`)}}
	_, result, err := push(t, nil, Input{Subject: "Subject Co"}, settings, caller)
	if err != nil || result.Next != "" {
		t.Fatalf("next=%q err=%v", result.Next, err)
	}
}
