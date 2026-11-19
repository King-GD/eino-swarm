package engine

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	jsonpatch "github.com/evanphx/json-patch"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const easmICPState = `{"schema_version":1,"title":"portrait","nodes":[{"id":"root-local","kind":"root","label":"Example"},{"id":"company-local","kind":"company","label":"Example Company","detail":{"description":"retain company evidence"}},{"id":"filing-local","kind":"feature","featureKind":"icp","label":"Filing 42-2","detail":{"reason":"retain filing evidence"}},{"id":"domain-local","kind":"domain","label":"EXISTING.test.","ip_count":9007199254740993,"detail":{"reason":"retain evidence","features":[{"kind":"domain","value":"existing.test"},{"kind":"cert","value":"certificate"},{"kind":"icp","value":"Filing 42-2"}]}}],"edges":[{"source":"root-local","target":"company-local","confirmed":true},{"source":"company-local","target":"filing-local","confirmed":true},{"source":"company-local","target":"domain-local","confirmed":true}],"summary":{"fofa_assets":9},"heat":[{"date":"2026-01-02","value":7}],"components":[{"name":"component","count":2}]}`
const easmICPReply = `{"records":[{"org":"Example Company","icp":"Filing 42-2","domain":"existing.test","updated_at":"2026-01-02"}]}`

func applyEASMICPPatch(t *testing.T, state string, result *mcp.CallToolResult) []byte {
	t.Helper()
	patch := easmICPPatch([]byte(state), result)
	operations, err := jsonpatch.DecodePatch(patch)
	if err != nil || len(operations) != 2 {
		t.Fatalf("registration patch=%s error=%v", patch, err)
	}
	updated, err := operations.Apply([]byte(state))
	if err != nil {
		t.Fatal(err)
	}
	return updated
}

func TestEASMICPPreservesTheFilingPathAndExistingEvidence(t *testing.T) {
	result := easmOrganizationResult(easmICPReply)
	updated := applyEASMICPPatch(t, easmICPState, result)
	var before, after map[string]json.RawMessage
	_ = json.Unmarshal([]byte(easmICPState), &before)
	_ = json.Unmarshal(updated, &after)
	for _, key := range []string{"schema_version", "title", "summary", "heat", "components"} {
		if !reflect.DeepEqual(before[key], after[key]) {
			t.Errorf("unrelated %s changed", key)
		}
	}
	var graph struct {
		Nodes []map[string]json.RawMessage
		Edges []struct {
			Source, Target string
			Confirmed      bool
		}
	}
	_ = json.Unmarshal(updated, &graph)
	if len(graph.Nodes) != 4 || len(graph.Edges) != 4 {
		t.Fatalf("existing identities lost: %s", updated)
	}
	if string(graph.Nodes[3]["ip_count"]) != "9007199254740993" {
		t.Fatal("IP count precision changed")
	}
	var filingDetail struct{ Reason string }
	_ = json.Unmarshal(graph.Nodes[2]["detail"], &filingDetail)
	if filingDetail.Reason != "retain filing evidence" {
		t.Fatalf("source-specific judgment was overwritten: %q", filingDetail.Reason)
	}
	var detail struct {
		Reason   string
		Features []struct {
			Kind, Value string
			NodeID      string `json:"node_id"`
		}
	}
	_ = json.Unmarshal(graph.Nodes[3]["detail"], &detail)
	if detail.Reason != "retain evidence" || len(detail.Features) != 2 || detail.Features[0].Kind != "cert" || detail.Features[1].NodeID != "filing-local" || detail.Features[1].Value != "Filing 42-2" {
		t.Fatalf("effective features are not source-based: %+v", detail)
	}
	last := graph.Edges[3]
	if last.Source != "filing-local" || last.Target != "domain-local" || !last.Confirmed {
		t.Fatalf("ICP path missing: %+v", last)
	}
	if repeated := easmICPPatch(updated, result); repeated != nil {
		t.Fatalf("duplicate filing evidence published again: %s", repeated)
	}
}

func TestEASMICPKeepsOldDecisionsWhileAddingTheSourceDirection(t *testing.T) {
	for _, edge := range []string{
		`{"source":"domain-local","target":"filing-local","confirmed":true}`,
		`{"source":"filing-local","target":"domain-local","confirmed":false}`,
	} {
		state := strings.Replace(easmICPState, `"edges":[`, `"edges":[`+edge+`,`, 1)
		updated := applyEASMICPPatch(t, state, easmOrganizationResult(easmICPReply))
		var before, after struct{ Edges []json.RawMessage }
		_ = json.Unmarshal([]byte(state), &before)
		_ = json.Unmarshal(updated, &after)
		if !reflect.DeepEqual(before.Edges, after.Edges[:len(before.Edges)]) {
			t.Fatalf("source publication changed an existing decision: %s", updated)
		}
		if strings.Contains(edge, `"confirmed":false`) {
			if len(after.Edges) != len(before.Edges) {
				t.Fatalf("an explicit decision was bypassed by another edge: %s", updated)
			}
		} else {
			if len(after.Edges) != len(before.Edges)+1 {
				t.Fatalf("reverse edge swallowed source direction: %s", updated)
			}
			var source struct {
				Source, Target string
				Confirmed      bool
			}
			_ = json.Unmarshal(after.Edges[len(before.Edges)], &source)
			if source.Source != "filing-local" || source.Target != "domain-local" || !source.Confirmed {
				t.Fatalf("wrong ICP source path: %+v", source)
			}
		}
	}
}

func TestEASMICPKeepsDifferentSourcesForOneDomain(t *testing.T) {
	reply := `{"records":[{"org":"Example Company","icp":"Filing 42-2","domain":"existing.test"},{"org":"Other Company","icp":"Other Filing 9","domain":"existing.test"},{"org":"Other Company","icp":"Other Filing 9","domain":"new.test"}]}`
	var structured any
	_ = json.Unmarshal([]byte(reply), &structured)
	result := &mcp.CallToolResult{StructuredContent: structured}
	updated := applyEASMICPPatch(t, easmICPState, result)
	var graph struct {
		Nodes []struct {
			ID, Kind, Label string
			Detail          struct {
				Features []struct{ Kind, Value string }
			}
		}
		Edges []struct {
			Source, Target string
			Confirmed      bool
		}
	}
	_ = json.Unmarshal(updated, &graph)
	if len(graph.Nodes) != 7 || len(graph.Edges) != 8 {
		t.Fatalf("multi-source graph=%s", updated)
	}
	for _, n := range graph.Nodes {
		if n.ID == "domain-local" && len(n.Detail.Features) != 3 {
			t.Fatalf("second registration replaced the first: %+v", n)
		}
	}
	if repeat := easmICPPatch(updated, result); repeat != nil {
		t.Fatalf("duplicate multi-source records changed graph: %s", repeat)
	}
}

func TestEASMICPLaterConflictingSubjectsRetainBothPaths(t *testing.T) {
	first := applyEASMICPPatch(t, easmICPState, easmOrganizationResult(easmICPReply))
	result := easmOrganizationResult(`{"records":[{"org":"Other Company","icp":"Filing 42-2","domain":"existing.test"}]}`)
	updated := applyEASMICPPatch(t, string(first), result)
	var graph struct {
		Nodes []struct {
			ID     string
			Detail struct {
				Subject string `json:"subject_name"`
			}
		}
		Edges []struct{ Source, Target string }
	}
	_ = json.Unmarshal(updated, &graph)
	for _, node := range graph.Nodes {
		if node.ID == "filing-local" && node.Detail.Subject != "" {
			t.Fatalf("conflicting filing still claims one subject: %+v", node)
		}
	}
	owners := 0
	for _, edge := range graph.Edges {
		if edge.Target == "filing-local" {
			owners++
		}
	}
	if owners != 2 {
		t.Fatalf("one of the two filing subjects disappeared: %s", updated)
	}
	if repeated := easmICPPatch(updated, result); repeated != nil {
		t.Fatalf("repeated conflicting evidence changed graph: %s", repeated)
	}
}

func TestEASMICPLargeBatchReusesTheKnownRoot(t *testing.T) {
	var records []string
	for i := 0; i < 300; i++ {
		records = append(records, fmt.Sprintf(`{"org":"Example Company","icp":"Filing 42-2","domain":"d%d.test"}`, i))
	}
	result := easmOrganizationResult(`{"records":[` + strings.Join(records, ",") + `]}`)
	updated := applyEASMICPPatch(t, easmGraphState, result)
	var graph struct {
		Nodes []json.RawMessage
		Edges []json.RawMessage
	}
	_ = json.Unmarshal(updated, &graph)
	if len(graph.Nodes) != 303 || len(graph.Edges) != 301 {
		t.Fatalf("large registration batch truncated: nodes=%d edges=%d", len(graph.Nodes), len(graph.Edges))
	}
	if repeat := easmICPPatch(updated, result); repeat != nil {
		t.Fatal("large duplicate batch republished")
	}
}

func TestEASMICPIgnoresIncompleteRecords(t *testing.T) {
	for _, record := range []string{
		`{"org":"","icp":"Filing 42-2","domain":"existing.test"}`,
		`{"org":"Example Company","icp":" ","domain":"existing.test"}`,
		`{"org":"Example Company","icp":"Filing 42-2","domain":"127.0.0.1"}`,
	} {
		if patch := easmICPPatch([]byte(easmICPState), easmOrganizationResult(`{"records":[`+record+`]}`)); patch != nil {
			t.Fatalf("incomplete record became evidence: %s", patch)
		}
	}
}

func TestEASMICPKeepsRegistrationEvidenceWhenOldNodesAreAmbiguous(t *testing.T) {
	for _, duplicate := range []string{
		`{"id":"another-company","kind":"company","label":"Example Company"}`,
		`{"id":"another-filing","kind":"feature","featureKind":"icp","label":"Filing 42-2"}`,
		`{"id":"another-domain","kind":"domain","label":"existing.test"}`,
	} {
		state := strings.Replace(easmICPState, `"nodes":[`, `"nodes":[`+duplicate+`,`, 1)
		result := easmOrganizationResult(easmICPReply)
		updated := applyEASMICPPatch(t, state, result)
		var graph struct {
			Nodes []struct{ ID, Kind, Label, FeatureKind string }
			Edges []struct {
				Source, Target string
				Confirmed      bool
			}
		}
		_ = json.Unmarshal(updated, &graph)
		if len(graph.Nodes) != 5 {
			t.Fatalf("duplicate node handling invented another identity: %s", updated)
		}
		filings, domains := make(map[string]bool), make(map[string]bool)
		for _, node := range graph.Nodes {
			if node.Kind == "feature" && node.FeatureKind == "icp" {
				filings[node.ID] = true
			}
			if node.Kind == "domain" {
				domains[node.ID] = true
			}
		}
		for filing := range filings {
			for domain := range domains {
				found := false
				for _, edge := range graph.Edges {
					found = found || edge.Source == filing && edge.Target == domain && edge.Confirmed
					if duplicate == `{"id":"another-company","kind":"company","label":"Example Company"}` && edge.Source == "another-company" && filings[edge.Target] && edge.Confirmed {
						t.Fatal("same name selected an unproven owner")
					}
				}
				if !found {
					t.Fatalf("unambiguous filing-domain evidence disappeared: %s -> %s in %s", filing, domain, updated)
				}
			}
		}
		if repeat := easmICPPatch(updated, result); repeat != nil {
			t.Fatalf("ambiguous legacy replay republished evidence: %s", repeat)
		}
	}
}

func TestEASMICPErrorsPreserveTheAcceptedGraph(t *testing.T) {
	for _, test := range []struct {
		name, state, reply string
		result             *mcp.CallToolResult
	}{
		{name: "nil result", state: easmICPState},
		{name: "MCP error", state: easmICPState, result: &mcp.CallToolResult{IsError: true}},
		{name: "malformed reply", state: easmICPState, reply: `{`},
		{name: "error flag", state: easmICPState, reply: `{"error":true}`},
		{name: "error object", state: easmICPState, reply: `{"error":{"code":"timeout"}}`},
		{name: "isError", state: easmICPState, reply: `{"isError":true}`},
		{name: "error message", state: easmICPState, reply: `{"errmsg":"timeout"}`},
		{name: "bad state", state: `{`, reply: easmICPReply},
		{name: "no nodes", state: `{"edges":[]}`, reply: easmICPReply},
		{name: "no edges", state: `{"nodes":[]}`, reply: easmICPReply},
		{name: "no root", state: `{"nodes":[],"edges":[]}`, reply: easmICPReply},
		{name: "bad node", state: `{"nodes":[7],"edges":[]}`, reply: easmICPReply},
		{name: "missing id", state: `{"nodes":[{}],"edges":[]}`, reply: easmICPReply},
		{name: "duplicate id", state: strings.Replace(easmICPState, `company-local`, `root-local`, 1), reply: easmICPReply},
		{name: "second root", state: strings.Replace(easmICPState, `"kind":"company"`, `"kind":"root"`, 1), reply: easmICPReply},
		{name: "empty root", state: strings.Replace(easmICPState, `"label":"Example"`, `"label":""`, 1), reply: easmICPReply},
		{name: "malformed edge", state: strings.Replace(easmICPState, `"edges":[`, `"edges":[7,`, 1), reply: easmICPReply},
		{name: "unknown edge endpoint", state: strings.Replace(easmICPState, `"target":"domain-local"`, `"target":"unknown"`, 1), reply: easmICPReply},
		{name: "malformed filing detail", state: strings.Replace(easmICPState, `"detail":{"reason":"retain filing evidence"}`, `"detail":7`, 1), reply: easmICPReply},
		{name: "malformed filing subject", state: strings.Replace(easmICPState, `"detail":{"reason":"retain filing evidence"}`, `"detail":{"subject_name":7}`, 1), reply: easmICPReply},
		{name: "malformed filing reason", state: strings.Replace(easmICPState, `"detail":{"reason":"retain filing evidence"}`, `"detail":{"reason":7}`, 1), reply: easmICPReply},
		{name: "malformed detail", state: strings.Replace(easmICPState, `"detail":{"reason":"retain evidence","features":[{"kind":"domain","value":"existing.test"},{"kind":"cert","value":"certificate"},{"kind":"icp","value":"Filing 42-2"}]}`, `"detail":7`, 1), reply: easmICPReply},
		{name: "malformed features", state: strings.Replace(easmICPState, `"features":[{"kind":"domain","value":"existing.test"},{"kind":"cert","value":"certificate"},{"kind":"icp","value":"Filing 42-2"}]`, `"features":7`, 1), reply: easmICPReply},
		{name: "malformed feature", state: strings.Replace(easmICPState, `{"kind":"cert","value":"certificate"}`, `7`, 1), reply: easmICPReply},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := test.result
			if test.reply != "" {
				result = easmOrganizationResult(test.reply)
			}
			if patch := easmICPPatch([]byte(test.state), result); patch != nil {
				t.Fatalf("untrusted filing data changed graph: %s", patch)
			}
		})
	}
}

func TestEASMICPRefusesGeneratedIdentityCollisions(t *testing.T) {
	for _, id := range []string{"company:icp-org:New Company", "feature:icp:New Filing", "domain:new.test"} {
		node := fmt.Sprintf(`{"id":%q,"kind":"org","label":"another observation"}`, id)
		state := strings.Replace(easmICPState, `"nodes":[`, `"nodes":[`+node+`,`, 1)
		if patch := easmICPPatch([]byte(state), easmOrganizationResult(`{"records":[{"org":"New Company","icp":"New Filing","domain":"new.test"}]}`)); patch != nil {
			t.Fatalf("generated identity overwrote another observation: %s", patch)
		}
	}
}
