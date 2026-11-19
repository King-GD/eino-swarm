package engine

import (
	"encoding/json"
	"strings"
	"testing"

	jsonpatch "github.com/evanphx/json-patch"
)

// A certificate identity is shared across services, but validity and hostname
// matching describe one observed service and must survive independently.
func TestEASMCertificateObservationsRetainMixedAndUnknownServiceFlags(t *testing.T) {
	apply := func(state []byte, reply string) []byte {
		t.Helper()
		patch, err := jsonpatch.DecodePatch(easmCertificatePatch(state, []byte(`{}`), easmOrganizationResult(reply)))
		if err != nil || len(patch) == 0 {
			t.Fatalf("certificate service observations were withheld: %v", err)
		}
		updated, err := patch.Apply(state)
		if err != nil {
			t.Fatal(err)
		}
		return updated
	}
	first := `{"results":[{"host":"https://one.example.test:443","domain":"example.test","cert.serial":"shared-serial","cert.issuer.org":"Fixture Issuer CN","cert.is_valid":true,"cert.is_match":false},{"host":"https://two.example.test:443","domain":"example.test","cert.serial":"shared-serial","cert.issuer.org":"Fixture Issuer CN","cert.is_valid":true,"cert.is_match":true}]}`
	updated := apply([]byte(easmCertificateState), first)
	detail := certificateObservationDetail(t, updated)
	if string(detail["is_valid"]) != "true" || len(detail["is_match"]) != 0 {
		t.Fatalf("mixed service matching was collapsed into one certificate flag: %s", updated)
	}
	var observations []map[string]json.RawMessage
	_ = json.Unmarshal(detail["observations"], &observations)
	if len(observations) != 2 || string(observations[0]["is_match"]) != "false" || string(observations[1]["is_match"]) != "true" || string(observations[0]["source"]) != `"mcp_fofa_search"` {
		t.Fatalf("service observations lost their actual source flags: %s", detail["observations"])
	}
	second := `{"results":[{"host":"https://one.example.test:443","domain":"example.test","cert.serial":"shared-serial","cert.issuer.org":"Fixture Issuer CN"}]}`
	updated = apply(updated, second)
	detail = certificateObservationDetail(t, updated)
	observations = nil
	_ = json.Unmarshal(detail["observations"], &observations)
	if len(observations) != 2 || len(observations[0]["is_valid"]) != 0 || len(observations[0]["is_match"]) != 0 || len(detail["is_valid"]) != 0 || len(detail["is_match"]) != 0 {
		t.Fatalf("latest unknown flags inherited an earlier service value: %s", updated)
	}
	if repeat := easmCertificatePatch(updated, []byte(`{}`), easmOrganizationResult(second)); repeat != nil {
		t.Fatalf("service observation replay created an empty revision: %s", repeat)
	}
	third := `{"results":[{"host":"https://one.example.test:443","domain":"example.test","cert.serial":"shared-serial","cert.issuer.org":"Fixture Issuer CN","cert.is_valid":true,"cert.is_match":true}]}`
	updated = apply(updated, third)
	detail = certificateObservationDetail(t, updated)
	if string(detail["is_valid"]) != "true" || string(detail["is_match"]) != "true" {
		t.Fatalf("complete uniform observations failed to produce a truthful summary: %s", updated)
	}
}

func TestEASMCertificateObservationsKeepIndependentSourcesAndDoNotInventServiceHosts(t *testing.T) {
	state, _ := applyEASMAssetCertificate(t, easmCertificateState, `{"results":[`+easmAssetCertificateDNSRow+`]}`)
	result := easmOrganizationResult(`{"results":[{"host":"https://service.example.test","domain":"service.example.test","cert.serial":"serial-dns","cert.issuer.org":"Fixture Issuer CN","cert.is_valid":true,"cert.is_match":true,"_zwai_certificate_source":"forged-source","_zwai_certificate_host":"forged-host"}]}`)
	patch, err := jsonpatch.DecodePatch(easmCertificatePatch(state, []byte(`{}`), result))
	if err != nil || len(patch) == 0 {
		t.Fatalf("independent source evidence was withheld: %v", err)
	}
	updated, err := patch.Apply(state)
	if err != nil {
		t.Fatal(err)
	}
	detail := certificateObservationDetail(t, updated)
	var observations []easmCertificateServiceObservation
	_ = json.Unmarshal(detail["observations"], &observations)
	if len(observations) != 2 || observations[0].Source != "mcp_fofa_asset_lookup" || observations[1].Source != "mcp_fofa_search" || observations[0].Host != observations[1].Host || observations[0].IsExpired == nil || *observations[0].IsExpired || observations[1].IsExpired != nil || len(detail["is_match"]) != 0 {
		t.Fatalf("one source overwrote or impersonated another source's service flags: %s", updated)
	}
	row := `{"domain":"","tls":{"certificate":{"serial_number":"hostless","issuer_cn":"Fixture Issuer CN","root_domains":["candidate.example.test"],"is_valid":true,"is_match":true}}}`
	hostless, _ := applyEASMAssetCertificate(t, easmCertificateState, `{"results":[`+row+`]}`)
	detail = certificateObservationDetail(t, hostless)
	if len(detail["observations"]) != 0 || len(detail["is_valid"]) != 0 || len(detail["is_match"]) != 0 {
		t.Fatalf("root-domain hints fabricated an observed service or certificate-wide flag: %s", hostless)
	}
}

func TestEASMCertificateObservationsRefuseMalformedExistingEvidence(t *testing.T) {
	row := map[string]json.RawMessage{"host": json.RawMessage(`"https://one.example.test"`)}
	for _, raw := range []string{`7`, `[{}]`, `[{"host":"service.example.test","is_valid":"false"}]`} {
		detail := map[string]json.RawMessage{"observations": json.RawMessage(raw)}
		if changed, valid := easmCertificateObserve(detail, row, ""); changed || valid || string(detail["observations"]) != raw {
			t.Fatalf("malformed service evidence was overwritten: %s", detail["observations"])
		}
	}
	state := `{"nodes":[{"id":"root","kind":"root","label":"Fixture"},{"id":"legacy","kind":"feature","featureKind":"cert","label":"Fixture certificate","detail":{"serial":"same","issuer":"Fixture Issuer","observations":7}}],"edges":[]}`
	result := easmOrganizationResult(`{"results":[{"domain":"example.test","cert.serial":"same","cert.issuer.org":"Fixture Issuer"}]}`)
	if patch := easmCertificatePatch([]byte(state), []byte(`{}`), result); patch != nil {
		t.Fatalf("bad existing observations were erased by a source publication: %s", patch)
	}
}

func TestEASMAssetCertificateLatestExpiryStaysWithItsServiceObservation(t *testing.T) {
	state, _ := applyEASMAssetCertificate(t, easmCertificateState, `{"results":[`+easmAssetCertificateDNSRow+`]}`)
	before := certificateObservationDetail(t, state)
	row := strings.ReplaceAll(easmAssetCertificateDNSRow, `"is_expired":false`, `"is_expired":true`)
	updated, _ := applyEASMAssetCertificate(t, string(state), `{"results":[`+row+`]}`)
	detail := certificateObservationDetail(t, updated)
	var observations []easmCertificateServiceObservation
	_ = json.Unmarshal(detail["observations"], &observations)
	if len(observations) != 1 || observations[0].IsExpired == nil || !*observations[0].IsExpired || !jsonpatch.Equal(before["reason"], detail["reason"]) {
		t.Fatalf("expiry changed certificate identity or appended conflicting reason prose: %s", updated)
	}
	row = strings.ReplaceAll(row, `"is_expired":true,`, ``)
	updated, _ = applyEASMAssetCertificate(t, string(updated), `{"results":[`+row+`]}`)
	detail = certificateObservationDetail(t, updated)
	observations = nil
	_ = json.Unmarshal(detail["observations"], &observations)
	if len(observations) != 1 || observations[0].IsExpired != nil || !jsonpatch.Equal(before["reason"], detail["reason"]) {
		t.Fatalf("latest missing expiry inherited a previous source flag: %s", updated)
	}
}

func certificateObservationDetail(t *testing.T, state []byte) map[string]json.RawMessage {
	t.Helper()
	var graph struct{ Nodes []map[string]json.RawMessage }
	if err := json.Unmarshal(state, &graph); err != nil {
		t.Fatal(err)
	}
	var detail map[string]json.RawMessage
	certificates := 0
	for _, node := range graph.Nodes {
		if easmCertificateString(node, "featureKind") == "cert" {
			certificates++
			_ = json.Unmarshal(node["detail"], &detail)
		}
	}
	if certificates != 1 {
		t.Fatalf("service flags split a real certificate identity: %s", state)
	}
	return detail
}
