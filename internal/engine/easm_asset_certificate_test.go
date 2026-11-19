package engine

import (
	"encoding/json"
	"strings"
	"testing"

	jsonpatch "github.com/evanphx/json-patch"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The field shape follows asset_lookup's TLS response, with anonymous values.
const easmAssetCertificateDNSRow = `{"id":"asset-dns","domain":"service.example.test","host":"https://service.example.test","tls":{"certificate":{"serial_number":"serial-dns","subject_cn":"*.example.test","subject_orgs":["Fixture Certificate Org"],"root_domains":["example.test"],"issuer_cn":"Fixture Issuer CN","issuer_orgs":["Fixture Issuer Org"],"is_valid":true,"is_expired":false,"is_match":false,"not_before":"2026-01-01","not_after":"2030-12-12"}}}`
const easmAssetCertificateIPRow = `{"id":"asset-ip","domain":"","host":"https://203.0.113.7:443","tls":{"certificate":{"serial_number":"serial-ip","subject_cn":"*.other.test","subject_orgs":["Fixture Certificate Org"],"root_domains":["other.test","second.test"],"issuer_cn":"Fixture Issuer CN","issuer_orgs":["Fixture Issuer Org"],"is_valid":false,"is_expired":false,"is_match":false,"not_after":"2030-12-12"}}}`

type easmAssetCertificateGraph struct {
	Nodes []struct {
		ID, Kind, FeatureKind, Label string
		IPCount                      json.RawMessage `json:"ip_count"`
		Detail                       struct {
			Reason, Serial, Issuer string
			NotAfter               string          `json:"not_after"`
			IsValid                json.RawMessage `json:"is_valid"`
			IsMatch                json.RawMessage `json:"is_match"`
			Observations           []struct {
				Host, Source string
				IsValid      json.RawMessage `json:"is_valid"`
				IsMatch      json.RawMessage `json:"is_match"`
				IsExpired    json.RawMessage `json:"is_expired"`
			}
		}
	}
	Edges []struct {
		Source, Target string
		Confirmed      bool
	}
}

func applyEASMAssetCertificate(t *testing.T, state, reply string) ([]byte, easmAssetCertificateGraph) {
	t.Helper()
	ops := easmAssetCertificatePatch([]byte(state), []byte(`{"include":["tls"]}`), easmOrganizationResult(reply))
	patch, err := jsonpatch.DecodePatch(ops)
	if err != nil || len(patch) == 0 {
		t.Fatalf("TLS evidence was withheld: patch=%s error=%v", ops, err)
	}
	updated, err := patch.Apply([]byte(state))
	if err != nil {
		t.Fatal(err)
	}
	var graph easmAssetCertificateGraph
	if err := json.Unmarshal(updated, &graph); err != nil {
		t.Fatal(err)
	}
	return updated, graph
}

func TestEASMAssetCertificateUsesActualTLSFlagsAndRootDomainClues(t *testing.T) {
	result := `{"results":[` + easmAssetCertificateDNSRow + `,` + easmAssetCertificateIPRow + `]}`
	updated, graph := applyEASMAssetCertificate(t, easmCertificateState, result)
	if len(graph.Nodes) != 7 || len(graph.Edges) != 6 {
		t.Fatalf("TLS observations lost identities or source paths: %s", updated)
	}
	for _, node := range graph.Nodes {
		if node.ID == "domain-local" && (string(node.IPCount) != "9007199254740993" || node.Detail.Reason != "retain domain evidence") {
			t.Fatal("TLS projection erased prior domain evidence")
		}
		if node.FeatureKind != "cert" {
			continue
		}
		wantValid := "true"
		if node.Detail.Serial == "serial-ip" {
			wantValid = "false"
			if !strings.Contains(node.Detail.Reason, "tls.certificate.root_domains") {
				t.Error("an IP response lost its explicit root-domain clue provenance")
			}
		}
		if string(node.Detail.IsValid) != wantValid || string(node.Detail.IsMatch) != "false" || node.Detail.NotAfter != "2030-12-12" || node.Detail.Issuer != "Fixture Issuer CN" || !strings.Contains(node.Detail.Reason, "mcp_fofa_asset_lookup") || len(node.Detail.Observations) != 1 || string(node.Detail.Observations[0].IsExpired) != "false" {
			t.Fatalf("source flags were inferred from the date or issuer changed: %+v", node)
		}
	}
	for _, edge := range graph.Edges {
		if edge.Confirmed || edge.Source == "root-local" && strings.HasPrefix(edge.Target, "domain:") {
			t.Fatal("TLS evidence fabricated target ownership or confirmed a mismatch")
		}
	}
	if repeated := easmAssetCertificatePatch(updated, []byte(`{}`), easmOrganizationResult(result)); repeated != nil {
		t.Fatalf("repeated TLS reply created a new revision: %s", repeated)
	}
}

func TestEASMAssetCertificateRootHintsRemainPendingAndLocalToTheirService(t *testing.T) {
	row := strings.ReplaceAll(easmAssetCertificateIPRow, `"is_valid":false`, `"is_valid":true`)
	row = strings.ReplaceAll(row, `"is_match":false`, `"is_match":true`)
	row = strings.ReplaceAll(row, `"serial_number":"serial-ip",`, ``)
	result := `{"results":[` + row + `,` + strings.ReplaceAll(row, "203.0.113.7", "203.0.113.8") + `]}`
	updated, graph := applyEASMAssetCertificate(t, easmCertificateState, result)
	certificates := map[string]bool{}
	for _, node := range graph.Nodes {
		if node.FeatureKind == "cert" {
			certificates[node.ID] = true
			if string(node.Detail.IsValid) != "true" || string(node.Detail.IsMatch) != "true" {
				t.Error("a pending root hint rewrote source validity flags")
			}
		}
	}
	if len(certificates) != 2 {
		t.Fatalf("root hints merged certificate observations across IP services: %s", updated)
	}
	for _, edge := range graph.Edges {
		if certificates[edge.Source] && edge.Confirmed {
			t.Fatal("root-domain clue was promoted to confirmed domain deployment")
		}
	}
	if repeat := easmAssetCertificatePatch(updated, []byte(`{}`), easmOrganizationResult(result)); repeat != nil {
		t.Fatalf("root clue replay changed graph: %s", repeat)
	}
}

func TestEASMAssetCertificateKeepsEachSubjectAndReusesLegacyIdentities(t *testing.T) {
	row := strings.ReplaceAll(easmAssetCertificateDNSRow, `["Fixture Certificate Org"]`, `["Fixture Certificate Org","Fixture Second Org"]`)
	row = strings.ReplaceAll(row, `"issuer_cn":"Fixture Issuer CN",`, ``)
	row = strings.ReplaceAll(row, `"is_expired":false`, `"is_expired":true`)
	state := strings.Replace(easmCertificateState, `],"edges"`, `,{"id":"cert-old","kind":"feature","featureKind":"cert","label":"Old certificate","detail":{"serial":"serial-dns","issuer":"Fixture Issuer Org","reason":"prior evidence","custom":9007199254740993}}],"edges"`, 1)
	reply := `{"results":[` + row + `]}`
	updated, graph := applyEASMAssetCertificate(t, state, reply)
	certificates, subjectEdges := 0, 0
	for _, node := range graph.Nodes {
		if node.FeatureKind == "cert" {
			certificates++
			if node.ID != "cert-old" || node.Detail.Issuer != "Fixture Issuer Org" || !strings.Contains(node.Detail.Reason, "prior evidence") || !strings.Contains(node.Detail.Reason, "mcp_fofa_asset_lookup") || len(node.Detail.Observations) != 1 || string(node.Detail.Observations[0].IsExpired) != "true" {
				t.Fatalf("TLS lookup replaced a stable certificate or its evidence: %+v", node)
			}
		}
	}
	for _, edge := range graph.Edges {
		if edge.Target == "cert-old" && edge.Source != "root-local" {
			subjectEdges++
		}
	}
	if certificates != 1 || subjectEdges != 2 || !strings.Contains(string(updated), `"custom":9007199254740993`) {
		t.Fatalf("TLS subject array was merged or legacy fields lost: %s", updated)
	}
	if repeated := easmAssetCertificatePatch(updated, []byte(`{}`), easmOrganizationResult(reply)); repeated != nil {
		t.Fatalf("multiple true subjects caused an empty revision on replay: %s", repeated)
	}
}

func TestEASMAssetCertificateCanRetainCertificateOnlyAndExistingConfirmedEvidence(t *testing.T) {
	for _, removeIdentity := range []bool{false, true} {
		row := strings.ReplaceAll(easmAssetCertificateIPRow, `"root_domains":["other.test","second.test"],`, ``)
		if removeIdentity {
			row = strings.ReplaceAll(row, `"serial_number":"serial-ip",`, ``)
		}
		updated, graph := applyEASMAssetCertificate(t, easmCertificateState, `{"results":[`+row+`]}`)
		if len(graph.Nodes) != 4 || len(graph.Edges) != 2 {
			t.Fatalf("IP-only service invented a domain or lost its certificate: %s", updated)
		}
	}
	row := strings.ReplaceAll(easmAssetCertificateIPRow, `"host":"https://203.0.113.7:443",`, ``)
	row = strings.ReplaceAll(row, `"root_domains":["other.test","second.test"],`, ``)
	row = strings.ReplaceAll(row, `"serial_number":"serial-ip",`, ``)
	_, graph := applyEASMAssetCertificate(t, `{"nodes":[{"id":"root","kind":"root","label":"Fixture"}],"edges":[]}`, `{"results":[`+row+`]}`)
	if len(graph.Nodes) != 2 || graph.Nodes[1].FeatureKind != "org" {
		t.Fatal("subject organization alone fabricated a certificate")
	}
	row = strings.ReplaceAll(easmAssetCertificateIPRow, `"is_valid":false`, `"is_valid":true`)
	row = strings.ReplaceAll(row, `"is_match":false`, `"is_match":true`)
	reply := `{"results":[` + row + `]}`
	updated, graph := applyEASMAssetCertificate(t, easmCertificateState, reply)
	var state map[string]json.RawMessage
	_ = json.Unmarshal(updated, &state)
	var edges []map[string]any
	_ = json.Unmarshal(state["edges"], &edges)
	for _, edge := range edges {
		if strings.HasPrefix(edge["source"].(string), "feature:cert:") {
			edge["confirmed"] = true // Independent, already accepted deployment evidence.
		}
	}
	state["edges"], _ = json.Marshal(edges)
	confirmedState, _ := json.Marshal(state)
	if repeated := easmAssetCertificatePatch(confirmedState, []byte(`{}`), easmOrganizationResult(reply)); repeated != nil {
		t.Fatalf("a root-domain clue erased prior confirmed evidence: %s", repeated)
	}
}

func TestEASMAssetCertificateSubjectArrayDescribesOneLocalObservation(t *testing.T) {
	row := strings.ReplaceAll(easmAssetCertificateDNSRow, `["Fixture Certificate Org"]`, `["Fixture Certificate Org","Fixture Second Org"]`)
	row = strings.ReplaceAll(row, `"serial_number":"serial-dns",`, ``)
	reply := `{"results":[` + row + `]}`
	updated, graph := applyEASMAssetCertificate(t, easmCertificateState, reply)
	certificates := 0
	for _, node := range graph.Nodes {
		if node.FeatureKind == "cert" {
			certificates++
		}
	}
	if certificates != 1 || len(graph.Edges) != 5 {
		t.Fatalf("subject array split one asset-local certificate observation: %s", updated)
	}
	if repeat := easmAssetCertificatePatch(updated, []byte(`{}`), easmOrganizationResult(reply)); repeat != nil {
		t.Fatalf("local multi-subject replay added a revision: %s", repeat)
	}
}

func TestEASMAssetCertificateRejectsUnknownSourceShapes(t *testing.T) {
	valid := `{"results":[` + easmAssetCertificateDNSRow + `]}`
	for _, test := range []struct{ name, state, arguments, reply string }{
		{"bad arguments", easmCertificateState, `{`, valid},
		{"null arguments", easmCertificateState, `null`, valid},
		{"bad reply", easmCertificateState, `{}`, `{`},
		{"error", easmCertificateState, `{}`, `{"error":true}`},
		{"isError", easmCertificateState, `{}`, `{"isError":true}`},
		{"errmsg", easmCertificateState, `{}`, `{"errmsg":"failed"}`},
		{"no TLS", easmCertificateState, `{}`, `{"results":[{"domain":"service.example.test"}]}`},
		{"bad subject array", easmCertificateState, `{}`, strings.ReplaceAll(valid, `"subject_orgs":["Fixture Certificate Org"]`, `"subject_orgs":7`)},
		{"bad issuer array", easmCertificateState, `{}`, strings.ReplaceAll(valid, `"issuer_orgs":["Fixture Issuer Org"]`, `"issuer_orgs":7`)},
		{"bad root array", easmCertificateState, `{}`, strings.ReplaceAll(valid, `"root_domains":["example.test"]`, `"root_domains":7`)},
		{"bad state", `{`, `{}`, valid},
		{"missing nodes", `{"edges":[]}`, `{}`, valid},
		{"missing edges", `{"nodes":[]}`, `{}`, valid},
		{"no root", `{"nodes":[],"edges":[]}`, `{}`, valid},
		{"empty ID", strings.Replace(easmCertificateState, `"id":"root-local"`, `"id":""`, 1), `{}`, valid},
		{"duplicate ID", strings.Replace(easmCertificateState, `"id":"org-local"`, `"id":"root-local"`, 1), `{}`, valid},
		{"bad detail", strings.Replace(easmCertificateState, `"detail":{"reason":"retain domain evidence","features":[{"kind":"icp","value":"Fixture ICP"}]}`, `"detail":7`, 1), `{}`, valid},
	} {
		t.Run(test.name, func(t *testing.T) {
			if patch := easmAssetCertificatePatch([]byte(test.state), []byte(test.arguments), easmOrganizationResult(test.reply)); patch != nil {
				t.Fatalf("unknown TLS source modified the graph: %s", patch)
			}
		})
	}
	if easmAssetCertificatePatch([]byte(easmCertificateState), []byte(`{}`), nil) != nil || easmAssetCertificatePatch([]byte(easmCertificateState), []byte(`{}`), &mcp.CallToolResult{IsError: true}) != nil {
		t.Fatal("failed TLS lookup modified panel state")
	}
}

func TestEASMAssetCertificateHostEvidenceOmitsCredentialsAndRequestPaths(t *testing.T) {
	for _, test := range []struct{ input, source, domain string }{
		{"https://private:secret@service.example.test:443/private?token=secret#secret", "https://service.example.test:443", "service.example.test"},
		{"service.example.test:443/path", "service.example.test:443", "service.example.test"},
		{"https://203.0.113.7:443/private", "https://203.0.113.7:443", ""},
		{"http://%", "", ""},
		{"", "", ""},
	} {
		if source, domain := easmAssetCertificateHost(test.input); source != test.source || domain != test.domain {
			t.Fatalf("host evidence exposed request data or fabricated a domain: input=%q source=%q domain=%q", test.input, source, domain)
		}
	}
}
