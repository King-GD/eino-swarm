package easm

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func certGraph(domains ...string) *Graph {
	graph := NewGraph("Subject Co", nil)
	for _, domain := range domains {
		graph.AddDomain(RootID, domain, true, Basis{Source: RoleOrganizations})
	}
	return graph
}

// searchReply answers the certificate search per queried domain.
func searchReply(rows map[string]string) func(map[string]any) (string, error) {
	return func(args map[string]any) (string, error) {
		query := args["query"].(string)
		for domain, body := range rows {
			if query == fmt.Sprintf("domain=%q", domain) {
				return body, nil
			}
		}
		return `{"results":[]}`, nil
	}
}

func certNodes(state State) []Node {
	var out []Node
	for _, node := range state.Nodes {
		if node.Kind == "feature" && node.FeatureKind == "cert" {
			out = append(out, node)
		}
	}
	return out
}

// A valid, matching certificate served by the domain is confirmed under it,
// keeps its subject organization as an attribute rather than a company, and
// the domain card links it as an effective feature.
func TestEffectiveCertificateIsConfirmedUnderItsDomain(t *testing.T) {
	caller := &fakeCaller{replies: map[string]func(map[string]any) (string, error){
		RoleCertificateSearch: searchReply(map[string]string{"a.test": `{"results":[{"host":"https://a.test:443","domain":"a.test","cert.subject.cn":"*.a.test","cert.subject.org":"Cert Org","cert.issuer.org":"Issuer","cert.serial":"S1","cert.not_before":"2026-01-01","cert.not_after":"2030-01-01","cert.is_valid":true,"cert.is_match":"true","cert.is_expired":false}]}`}),
	}}
	var out published
	r := stepRun(certGraph("a.test"), onlyRoles(RoleCertificateSearch), caller, &out)
	if err := r.certificates(t.Context()); err != nil {
		t.Fatal(err)
	}
	state := graphState(r)
	certs := certNodes(state)
	if len(certs) != 1 {
		t.Fatalf("certificates = %+v", certs)
	}
	cert := certs[0]
	if cert.Label != "*.a.test" || cert.Detail.Serial != "S1" || cert.Detail.Issuer != "Issuer" || cert.Detail.NotBefore != "2026-01-01" || cert.Detail.NotAfter != "2030-01-01" || cert.Detail.SubjectName != "Cert Org" {
		t.Fatalf("certificate detail = %+v", cert.Detail)
	}
	if !hasFeature(cert, "org", "Cert Org", "") || *cert.Detail.IsValid != true || *cert.Detail.IsMatch != true {
		t.Fatalf("certificate attributes = %+v", cert.Detail)
	}
	if got := cert.Detail.Observations; len(got) != 1 || got[0].Host != "https://a.test:443" || got[0].Domain != "a.test" || got[0].Source != RoleCertificateSearch {
		t.Fatalf("observations = %+v", got)
	}
	if edge, _ := edgeOf(state, cert.ID); edge.Source != "domain:a.test" || !edge.Confirmed {
		t.Fatalf("certificate edge = %+v", edge)
	}
	domain, _ := nodeOf(state, "domain:a.test")
	if !hasFeature(domain, "cert", "*.a.test", cert.ID) {
		t.Fatalf("domain card lost its effective certificate: %+v", domain.Detail)
	}
	for _, node := range state.Nodes {
		if node.Kind == "company" {
			t.Fatalf("certificate subject became a company: %+v", node)
		}
	}
	args := caller.calls[0].Arguments
	if args["query"] != `domain="a.test"` || args["fields"] != certificateSearchFields || args["size"] != certificatePageSize {
		t.Fatalf("search arguments = %v", args)
	}
	for _, disabled := range []string{"banner", "header", "body"} {
		if strings.Contains(","+certificateSearchFields+",", ","+disabled+",") {
			t.Fatalf("requested disabled field %s", disabled)
		}
	}
}

// Explicit false flags stay visible and keep the association pending; the
// domain card does not list the certificate as effective.
func TestInvalidCertificateStaysPending(t *testing.T) {
	caller := &fakeCaller{replies: map[string]func(map[string]any) (string, error){
		RoleCertificateSearch: fixed(`{"results":[{"domain":"a.test","cert.subject.cn":"*.a.test","cert.issuer.org":"I","cert.serial":"S","cert.is_valid":"false","cert.is_match":true}]}`),
	}}
	var out published
	r := stepRun(certGraph("a.test"), onlyRoles(RoleCertificateSearch), caller, &out)
	if err := r.certificates(t.Context()); err != nil {
		t.Fatal(err)
	}
	state := graphState(r)
	cert := certNodes(state)[0]
	if cert.Detail.IsValid == nil || *cert.Detail.IsValid {
		t.Fatalf("explicit false flag lost: %+v", cert.Detail)
	}
	if edge, _ := edgeOf(state, cert.ID); edge.Confirmed {
		t.Fatalf("invalid certificate confirmed: %+v", edge)
	}
	if domain, _ := nodeOf(state, "domain:a.test"); hasFeature(domain, "cert", cert.Label, cert.ID) {
		t.Fatal("invalid certificate listed as effective")
	}
}

// One certificate served on two domains keeps one identity: its first
// domain is the source edge and the second domain still links it as an
// effective feature. Mixed validity drops the top-level flag.
func TestSharedCertificateKeepsOneIdentity(t *testing.T) {
	row := `{"results":[{"host":"%s","domain":"%s","cert.subject.cn":"shared","cert.issuer.org":"I","cert.serial":"S","cert.is_valid":%s,"cert.is_match":true}]}`
	caller := &fakeCaller{replies: map[string]func(map[string]any) (string, error){
		RoleCertificateSearch: searchReply(map[string]string{
			"a.test": fmt.Sprintf(row, "a.test", "a.test", "true"),
			"b.test": fmt.Sprintf(row, "https://b.test", "b.test", "true"),
			"c.test": fmt.Sprintf(row, "c.test", "c.test", "false"),
		}),
	}}
	var out published
	r := stepRun(certGraph("a.test", "b.test", "c.test"), onlyRoles(RoleCertificateSearch), caller, &out)
	if err := r.certificates(t.Context()); err != nil {
		t.Fatal(err)
	}
	state := graphState(r)
	certs := certNodes(state)
	if len(certs) != 1 || len(certs[0].Detail.Observations) != 3 {
		t.Fatalf("shared certificate split: %+v", certs)
	}
	if certs[0].Detail.IsValid != nil || certs[0].Detail.IsMatch == nil {
		t.Fatalf("flag consensus = %+v", certs[0].Detail)
	}
	if edge, _ := edgeOf(state, certs[0].ID); edge.Source != "domain:a.test" || !edge.Confirmed {
		t.Fatalf("first source lost: %+v", edge)
	}
	b, _ := nodeOf(state, "domain:b.test")
	c, _ := nodeOf(state, "domain:c.test")
	if !hasFeature(b, "cert", "shared", certs[0].ID) || hasFeature(c, "cert", "shared", certs[0].ID) {
		t.Fatalf("effective features: b=%+v c=%+v", b.Detail, c.Detail)
	}
}

// Without a certificate identifier, two services with the same names are
// kept apart, and a repeated observation of one service replaces itself.
func TestAssetLocalCertificatesAreNotMergedByName(t *testing.T) {
	row := `{"domain":"%s","cert.subject.cn":"same-name","cert.is_valid":true}`
	caller := &fakeCaller{replies: map[string]func(map[string]any) (string, error){
		RoleCertificateSearch: searchReply(map[string]string{
			"a.test": `{"results":[` + fmt.Sprintf(row, "a.test") + `,` + fmt.Sprintf(row, "a.test") + `]}`,
			"b.test": `{"results":[` + fmt.Sprintf(row, "b.test") + `]}`,
		}),
	}}
	var out published
	r := stepRun(certGraph("a.test", "b.test"), onlyRoles(RoleCertificateSearch), caller, &out)
	if err := r.certificates(t.Context()); err != nil {
		t.Fatal(err)
	}
	certs := certNodes(graphState(r))
	if len(certs) != 2 || len(certs[0].Detail.Observations) != 1 {
		t.Fatalf("asset-local certificates = %+v", certs)
	}
}

// A domain first seen in a certificate reply hangs under the root as a
// pending clue, never as a detached island; the certificate hangs under it.
func TestCertificateOnlyDomainIsAPendingClue(t *testing.T) {
	caller := &fakeCaller{replies: map[string]func(map[string]any) (string, error){
		RoleCertificateSearch: fixed(`{"results":[{"host":"new.other.test:8443","cert.subject.cn":"x","cert.sha256":"ab12","cert.is_valid":true,"cert.is_match":true}]}`),
	}}
	var out published
	r := stepRun(certGraph("a.test"), onlyRoles(RoleCertificateSearch), caller, &out)
	if err := r.certificates(t.Context()); err != nil {
		t.Fatal(err)
	}
	state := graphState(r)
	if edge, ok := edgeOf(state, "domain:new.other.test"); !ok || edge.Source != RootID || edge.Confirmed {
		t.Fatalf("certificate-only domain = %+v %v", edge, ok)
	}
	cert := certNodes(state)[0]
	if edge, _ := edgeOf(state, cert.ID); edge.Source != "domain:new.other.test" || !edge.Confirmed {
		t.Fatalf("certificate edge = %+v", edge)
	}
	if cert.Detail.Observations[0].Host != "new.other.test:8443" {
		t.Fatalf("service host = %+v", cert.Detail.Observations)
	}
}

// Rows that only describe an asset, malformed rows and tuple rows without a
// usable column list produce no certificate; a reply with only totals is
// not certificate evidence and fails the step.
func TestRowsWithoutCertificateIdentityAddNothing(t *testing.T) {
	caller := &fakeCaller{replies: map[string]func(map[string]any) (string, error){
		RoleCertificateSearch: fixed(`{"fields":"domain,domain","results":[{"domain":"a.test","host":"a.test"},{"domain":"bad_domain","cert.subject.cn":"x"},["a.test","x"],7,{"cert.subject.cn":"x"}]}`),
	}}
	var out published
	r := stepRun(certGraph("a.test"), onlyRoles(RoleCertificateSearch), caller, &out)
	if err := r.certificates(t.Context()); err != nil {
		t.Fatal(err)
	}
	if certs := certNodes(graphState(r)); len(certs) != 0 {
		t.Fatalf("certificates without identity: %+v", certs)
	}
	caller.replies[RoleCertificateSearch] = fixed(`{"size":0,"total":12}`)
	if err := r.certificates(t.Context()); err == nil {
		t.Fatal("a total-only reply was accepted")
	}
}

// Tuple rows follow the reply's own column list, as an array or a comma
// list, and nested cert objects are read like flat dotted columns.
func TestTupleAndNestedCertificateRows(t *testing.T) {
	caller := &fakeCaller{replies: map[string]func(map[string]any) (string, error){
		RoleCertificateSearch: searchReply(map[string]string{
			"a.test": `{"fields":["cert.is_valid","domain","cert.subject.cn"],"results":[[false,"a.test","tuple-a"]]}`,
			"b.test": `{"fields":"domain, cert.subject.cn","results":[["b.test","tuple-b"]]}`,
			"c.test": `{"results":[{"domain":"c.test","cert":{"id":9007199254740993,"subject":{"cn":"nested"}}}]}`,
			"d.test": `{"fields":7,"results":[["d.test","x"]]}`,
		}),
	}}
	var out published
	r := stepRun(certGraph("a.test", "b.test", "c.test", "d.test"), onlyRoles(RoleCertificateSearch), caller, &out)
	if err := r.certificates(t.Context()); err != nil {
		t.Fatal(err)
	}
	labels := map[string]Node{}
	for _, cert := range certNodes(graphState(r)) {
		labels[cert.Label] = cert
	}
	if len(labels) != 3 || labels["tuple-a"].Detail.IsValid == nil || labels["tuple-b"].ID == "" {
		t.Fatalf("tuple rows = %+v", labels)
	}
	nested := labels["nested"]
	if nested.ID != fmt.Sprintf("feature:cert:%x", sha256Of("cert.id:9007199254740993")) {
		t.Fatalf("exact numeric id lost: %s", nested.ID)
	}
}

// The TLS lookup supplies certificates for services whose search rows have
// none. An IP-only service keeps its certificate and observation without a
// domain edge of its own: it stays a pending clue under the queried domain.
// Several subject organizations stay attributes; root-domain hints are names.
func TestTLSLookupAddsServiceCertificates(t *testing.T) {
	caller := &fakeCaller{replies: map[string]func(map[string]any) (string, error){
		RoleCertificateSearch: fixed(`{"results":[]}`),
		RoleAssetLookup: fixed(`{"results":[
			{"domain":"","host":"https://203.0.113.7:443","tls":{"certificate":{"serial_number":"S-IP","subject_cn":"*.other.test","subject_orgs":["Org A","Org B"],"root_domains":["other.test","bad_domain"],"issuer_orgs":["I1","I2"],"is_valid":true,"is_match":true}}},
			{"domain":"a.test","host":"https://a.test","tls":{"certificate":{"serial_number":"S-A","subject_cn":"a.test","issuer_cn":"Issuer CN","is_valid":true,"is_match":true,"is_expired":true}}},
			{"host":"https://a.test","tls":{"certificate":{"subject_orgs":"not a list"}}},
			{"host":"https://a.test","tls":{}}]}`),
	}}
	var out published
	r := stepRun(certGraph("a.test"), onlyRoles(RoleCertificateSearch, RoleAssetLookup), caller, &out)
	if err := r.certificates(t.Context()); err != nil {
		t.Fatal(err)
	}
	state := graphState(r)
	certs := certNodes(state)
	if len(certs) != 2 {
		t.Fatalf("certificates = %+v", certs)
	}
	var ip, expired Node
	for _, cert := range certs {
		if cert.Detail.Serial == "S-IP" {
			ip = cert
		} else {
			expired = cert
		}
	}
	if ip.Detail.Issuer != "I1 / I2" || !hasFeature(ip, "org", "Org B", "") || ip.Detail.SubjectName != "Org A" || strings.Join(ip.Detail.Names, ",") != "*.other.test,other.test" {
		t.Fatalf("TLS certificate detail = %+v", ip.Detail)
	}
	if got := ip.Detail.Observations; len(got) != 1 || got[0].Host != "https://203.0.113.7:443" || got[0].Domain != "" || got[0].Source != RoleAssetLookup {
		t.Fatalf("IP observation = %+v", got)
	}
	if edge, _ := edgeOf(state, ip.ID); edge.Source != "domain:a.test" || edge.Confirmed {
		t.Fatalf("IP-only certificate edge = %+v", edge)
	}
	if _, ok := nodeOf(state, "domain:other.test"); ok {
		t.Fatal("a root-domain hint became a domain")
	}
	if edge, _ := edgeOf(state, expired.ID); edge.Confirmed || expired.Detail.Issuer != "Issuer CN" {
		t.Fatalf("expired certificate = %+v %+v", edge, expired.Detail)
	}
	if args := caller.calls[1].Arguments; args["include"].([]string)[0] != "tls" || args["domains"].([]string)[0] != "a.test" || args["size"] != certificatePageSize {
		t.Fatalf("lookup arguments = %v", args)
	}
}

// A certificate first identified by fingerprint is found again by issuer and
// serial when a later row lacks the fingerprint.
func TestIssuerAndSerialReachAnEarlierCertificate(t *testing.T) {
	graph := certGraph("a.test", "b.test")
	graph.Upsert(Node{ID: "feature:cert:earlier", Kind: "feature", FeatureKind: "cert", Label: "old", Detail: &Detail{Issuer: "I", Serial: "S"}})
	graph.Link("domain:a.test", "feature:cert:earlier", false)
	caller := &fakeCaller{replies: map[string]func(map[string]any) (string, error){
		RoleCertificateSearch: searchReply(map[string]string{"b.test": `{"results":[{"domain":"b.test","cert.issuer.org":"I","cert.sn":"S","cert.is_valid":true,"cert.is_match":true}]}`}),
	}}
	var out published
	r := stepRun(graph, onlyRoles(RoleCertificateSearch), caller, &out)
	if err := r.certificates(t.Context()); err != nil {
		t.Fatal(err)
	}
	state := graphState(r)
	if certs := certNodes(state); len(certs) != 1 || len(certs[0].Detail.Observations) != 1 {
		t.Fatalf("certificate identity split: %+v", certs)
	}
	if b, _ := nodeOf(state, "domain:b.test"); !hasFeature(b, "cert", "old", "feature:cert:earlier") {
		t.Fatalf("second domain lost the effective certificate: %+v", b.Detail)
	}
}

// Per-domain lookups are bounded and publish progress while they run.
func TestCertificateLookupsAreBounded(t *testing.T) {
	var domains []string
	for i := 0; i < 9; i++ {
		domains = append(domains, fmt.Sprintf("d%d.test", i))
	}
	caller := &fakeCaller{replies: map[string]func(map[string]any) (string, error){RoleCertificateSearch: fixed(`{"results":[]}`)}}
	var out published
	settings := onlyRoles(RoleCertificateSearch)
	settings.MaxDomains = 7
	if err := stepRun(certGraph(domains...), settings, caller, &out).certificates(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(caller.calls) != 7 || len(out.states) != 2 {
		t.Fatalf("calls=%d publications=%d", len(caller.calls), len(out.states))
	}
}

// The optional TLS lookup going away does not stop the search; its tool
// failure and a progress publication failure do fail the step.
func TestCertificateLookupFailures(t *testing.T) {
	graph := certGraph("a.test", "b.test", "c.test", "d.test")
	unavailable := &fakeCaller{replies: map[string]func(map[string]any) (string, error){RoleCertificateSearch: fixed(`{"results":[]}`)}}
	var out published
	if err := stepRun(graph, onlyRoles(RoleCertificateSearch, RoleAssetLookup), unavailable, &out).certificates(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(unavailable.calls) != 5 {
		t.Fatalf("unavailable lookup retried: %d calls", len(unavailable.calls))
	}
	for name, replies := range map[string]map[string]func(map[string]any) (string, error){
		"lookup error":   {RoleCertificateSearch: fixed(`{"results":[]}`), RoleAssetLookup: fixed(`{"error":true}`)},
		"lookup totals":  {RoleCertificateSearch: fixed(`{"results":[]}`), RoleAssetLookup: fixed(`{"total":3}`)},
		"search failure": {RoleCertificateSearch: fixed(`{"errmsg":"quota"}`)},
	} {
		caller := &fakeCaller{replies: replies}
		if err := stepRun(graph, onlyRoles(RoleCertificateSearch, RoleAssetLookup), caller, &out).certificates(t.Context()); err == nil {
			t.Fatalf("%s accepted", name)
		}
	}
	want := errors.New("store closed")
	r := stepRun(graph, onlyRoles(RoleCertificateSearch), unavailable, &out)
	r.publish = func(context.Context, json.RawMessage) error { return want }
	if err := r.certificates(t.Context()); !errors.Is(err, want) {
		t.Fatalf("publish err = %v", err)
	}
}

// Stop in the middle of the domain loop ends the run as interrupted and
// keeps the certificates already found on the published panel.
func TestCertificateLoopStopsOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	calls := 0
	caller := &fakeCaller{replies: map[string]func(map[string]any) (string, error){
		RoleOrganizations: fixed(`{"results":[{"id":"1","name":"S","domains":[{"domain":"a.test"},{"domain":"b.test"},{"domain":"c.test"}]}]}`),
		RoleCertificateSearch: func(map[string]any) (string, error) {
			if calls++; calls == 1 {
				return `{"results":[{"domain":"a.test","cert.subject.cn":"a","cert.is_valid":true}]}`, nil
			}
			cancel()
			return "", context.Canceled
		},
	}}
	var out published
	_, err := Run(ctx, Input{Subject: "S"}, onlyRoles(RoleOrganizations, RoleCertificateSearch), caller, out.publish)
	if !errors.Is(err, context.Canceled) || calls != 2 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
	state := out.last()
	if got := stepStatus(state, StepCertificates); got.Status != statusInterrupted {
		t.Fatalf("certificates = %+v", got)
	}
	if len(certNodes(state)) != 1 {
		t.Fatalf("found certificates lost: %+v", state.Nodes)
	}
}

// The step needs the search; the TLS lookup alone leaves it unavailable
// rather than half-configured.
func TestCertificatesNeedTheSearchTool(t *testing.T) {
	var out published
	if _, err := Run(t.Context(), Input{Subject: "S"}, onlyRoles(RoleAssetLookup), &fakeCaller{}, out.publish); err != nil {
		t.Fatal(err)
	}
	if got := stepStatus(out.last(), StepCertificates); got.Status != statusUnavailable {
		t.Fatalf("certificates = %+v", got)
	}
}

func TestCertificateValueReaders(t *testing.T) {
	for raw, want := range map[string]string{`"x"`: "x", `12`: "12", `true`: "", `{}`: "", ``: ""} {
		if got := certScalar(json.RawMessage(raw)); got != want {
			t.Fatalf("certScalar(%s) = %q", raw, got)
		}
	}
	for raw, want := range map[string]string{`true`: "true", `"false"`: "false", `null`: "nil", `"yes"`: "nil", `1`: "nil"} {
		got := "nil"
		if value := certFlag(json.RawMessage(raw)); value != nil {
			got = fmt.Sprint(*value)
		}
		if got != want {
			t.Fatalf("certFlag(%s) = %s", raw, got)
		}
	}
	if host, domain := certServiceHost("http://[::1"); host != "" || domain != "" {
		t.Fatalf("broken host = %q %q", host, domain)
	}
	if certField(map[string]json.RawMessage{"cert": json.RawMessage(`1`)}, "cert.x") != nil || certField(nil, "plain") != nil {
		t.Fatal("unreadable nested field returned a value")
	}
}

func sha256Of(value string) [32]byte { return sha256.Sum256([]byte(value)) }
