package easm

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// certBucket builds one cert.sn aggregation bucket as the FOFA stats tool
// returns it with detail:true.
func certBucket(serial, cn, issuer string, domains []string, valid, match, expired bool) map[string]any {
	return map[string]any{"name": serial, "count": 3, "detail": map[string]any{
		"domain": domains, "is_valid": valid, "is_match": match, "is_expired": expired,
		"not_before": "2026-01-01 00:00:00", "not_after": "2027-01-01 00:00:00",
		"issuer": map[string]any{"cn": issuer, "org": []string{issuer + " Org"}}, "subject": map[string]any{"cn": cn, "org": []string{"Subject Org"}},
	}}
}

// certCaller answers organizations with the subject's domains and the stats
// tool with per-domain certificate buckets; it records every query.
func certCaller(domains []string, byDomain map[string][]map[string]any) *fakeCaller {
	entries := make([]map[string]string, len(domains))
	for i, domain := range domains {
		entries[i] = map[string]string{"domain": domain}
	}
	org, _ := json.Marshal(map[string]any{"results": []any{map[string]any{"id": "1", "name": "Subject", "domains": entries}}})
	return &fakeCaller{replies: map[string]func(map[string]any) (string, error){
		RoleOrganizations: fixed(string(org)),
		RoleStats: func(args map[string]any) (string, error) {
			query, _ := args["query"].(string)
			domain := strings.TrimSuffix(strings.TrimPrefix(query, `domain="`), `"`)
			buckets, ok := byDomain[domain]
			if !ok {
				return "", errors.New("unexpected query " + query)
			}
			raw, _ := json.Marshal(map[string]any{"error": false, "aggs": map[string]any{"cert.sn": buckets}})
			return string(raw), nil
		},
	}}
}

func runCertificates(t *testing.T, caller *fakeCaller, maxDomains int) State {
	t.Helper()
	settings := onlyRoles(RoleOrganizations, RoleStats)
	settings.MaxDomains = maxDomains
	var out published
	if _, err := Run(t.Context(), Input{Subject: "Subject"}, settings, caller, out.publish); err != nil {
		t.Fatal(err)
	}
	return out.last()
}

// The search tool rejects cert.* return fields, so certificates must come
// from the aggregation with detail; a valid, matching certificate confirms
// its domain and keeps its issuer, validity and names on the card.
func TestEffectiveCertificateFromAggregationIsConfirmed(t *testing.T) {
	caller := certCaller([]string{"a.test"}, map[string][]map[string]any{"a.test": {certBucket("1058863643458816854384477497218834472", "a.test", "Fixture CA", []string{"a.test"}, true, true, false)}})
	state := runCertificates(t, caller, 5)
	id := "feature:cert:1058863643458816854384477497218834472@Fixture CA"
	edge, ok := edgeOf(state, id)
	if !ok || edge.Source != "domain:a.test" || !edge.Confirmed {
		t.Fatalf("edge=%+v ok=%v", edge, ok)
	}
	node, _ := nodeOf(state, id)
	if node.Label != "a.test" || node.Detail.Issuer != "Fixture CA" || node.Detail.NotAfter == "" || node.Detail.SubjectName != "Subject Org" || len(node.Detail.Names) != 1 {
		t.Fatalf("node=%+v detail=%+v", node, node.Detail)
	}
	var request map[string]any
	for _, call := range caller.calls {
		if call.Tool == RoleStats {
			request = call.Arguments
		}
	}
	if request["fields"] != "cert.sn" || request["detail"] != true || request["tool"] != fofaSource || request["query"] != `domain="a.test"` {
		t.Fatalf("aggregation request = %v", request)
	}
	if stepStatus(state, StepCertificates).Status != statusDone {
		t.Fatalf("steps = %+v", state.Collection.Steps)
	}
}

// Expired or mismatched certificates are evidence, not proof of ownership.
func TestIneffectiveCertificatesStayPending(t *testing.T) {
	caller := certCaller([]string{"a.test"}, map[string][]map[string]any{"a.test": {
		certBucket("11", "a.test", "CA", []string{"a.test"}, true, true, true),
		certBucket("12", "a.test", "CA", []string{"a.test"}, true, false, false),
	}})
	state := runCertificates(t, caller, 5)
	for _, id := range []string{"feature:cert:11@CA", "feature:cert:12@CA"} {
		if edge, ok := edgeOf(state, id); !ok || edge.Confirmed {
			t.Fatalf("%s edge=%+v ok=%v", id, edge, ok)
		}
	}
}

// One certificate served on two domains stays one node; the second domain
// lists it as an effective feature, and a name seen only on the certificate
// becomes a pending clue under the target.
func TestSharedCertificateKeepsOneIdentityAndAddsClues(t *testing.T) {
	shared := certBucket("99", "*.a.test", "CA", []string{"a.test", "b.test", "only-cert.test"}, true, true, false)
	caller := certCaller([]string{"a.test", "b.test"}, map[string][]map[string]any{"a.test": {shared}, "b.test": {shared}})
	state := runCertificates(t, caller, 5)
	count := 0
	for _, node := range state.Nodes {
		if node.FeatureKind == "cert" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("certificate nodes = %d", count)
	}
	b, _ := nodeOf(state, "domain:b.test")
	if b.Detail == nil || len(b.Detail.Features) != 1 || b.Detail.Features[0].NodeID != "feature:cert:99@CA" {
		t.Fatalf("second domain features = %+v", b.Detail)
	}
	if edge, ok := edgeOf(state, "domain:only-cert.test"); !ok || edge.Source != RootID || edge.Confirmed {
		t.Fatalf("certificate-only domain = %+v %v", edge, ok)
	}
}

// Lookups are bounded and a bucket without a serial adds nothing.
func TestCertificateLookupsAreBoundedAndNeedASerial(t *testing.T) {
	caller := certCaller([]string{"a.test", "b.test", "c.test"}, map[string][]map[string]any{"a.test": {certBucket("", "x", "CA", nil, true, true, false)}})
	state := runCertificates(t, caller, 1)
	stats := 0
	for _, call := range caller.calls {
		if call.Tool == RoleStats {
			stats++
		}
	}
	if stats != 1 {
		t.Fatalf("aggregations = %d", stats)
	}
	for _, node := range state.Nodes {
		if node.FeatureKind == "cert" {
			t.Fatalf("certificate without serial: %+v", node)
		}
	}
}

// A refused aggregation fails the step after the others are applied; a reply
// without aggregations is malformed rather than an empty certificate list.
func TestCertificateFailuresKeepOtherDomains(t *testing.T) {
	caller := certCaller([]string{"a.test", "b.test"}, map[string][]map[string]any{"a.test": {certBucket("7", "a.test", "CA", []string{"a.test"}, true, true, false)}})
	state := runCertificates(t, caller, 5)
	if _, ok := nodeOf(state, "feature:cert:7@CA"); !ok {
		t.Fatal("successful domain lost its certificate")
	}
	if got := stepStatus(state, StepCertificates); got.Status != statusFailed {
		t.Fatalf("status = %+v", got)
	}
	caller.replies[RoleStats] = fixed(`{"error":false}`)
	state = runCertificates(t, caller, 5)
	if got := stepStatus(state, StepCertificates); got.Status != statusFailed || got.Code != "malformed_reply" {
		t.Fatalf("missing aggs = %+v", got)
	}
}

// Stop during the parallel lookups interrupts the step instead of failing it.
func TestCertificateLookupsStopOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	caller := certCaller([]string{"a.test"}, nil)
	caller.replies[RoleStats] = func(map[string]any) (string, error) { cancel(); return "", context.Canceled }
	settings := onlyRoles(RoleOrganizations, RoleStats)
	var out published
	if _, err := Run(ctx, Input{Subject: "Subject"}, settings, caller, out.publish); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	if got := stepStatus(out.last(), StepCertificates); got.Status != statusInterrupted {
		t.Fatalf("status = %+v", got)
	}
}

func TestCertificateSerialReadsTextAndNumbers(t *testing.T) {
	for raw, want := range map[string]string{`"12"`: "12", `34`: "34", `null`: "", `{}`: ""} {
		if got := certificateSerial(json.RawMessage(raw)); got != want {
			t.Fatalf("%s = %q", raw, got)
		}
	}
}
