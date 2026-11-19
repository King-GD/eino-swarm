package engine

import (
	"encoding/json"
	"strings"
	"testing"

	jsonpatch "github.com/evanphx/json-patch"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const easmAssetRuleState = `{"schema_version":1,"title":"Fixture","nodes":[{"id":"root-local","kind":"root","label":"Fixture"},{"id":"company-north","kind":"company","label":"Fixture North Company","detail":{"reason":"original company evidence"}},{"id":"company-south","kind":"company","label":"Fixture South Company"},{"id":"domain-local","kind":"domain","label":"NORTH.example.test.","ip_count":9007199254740993,"detail":{"reason":"original domain evidence","features":[{"kind":"cert","value":"existing certificate"}]}}],"edges":[{"source":"root-local","target":"company-north","confirmed":false},{"source":"company-north","target":"domain-local","confirmed":false}],"summary":{"fofa_assets":9},"heat":[],"components":[]}`
const easmAssetRuleArguments = `{"domains":["north.example.test","south.example.test","product.example.test","unmatched.example.test"]}`
const easmAssetRuleReply = `{"error":false,"errmsg":"","results":[{"domain":"north.example.test","names":[{"name":"Fixture North Company","time":"2026.10.31"}]},{"domain":"south.example.test","names":[{"name":"Fixture South Company","time":"2026.11.19"}]},{"domain":"product.example.test","names":[{"name":"Fixture Product","time":"2026.11.04"}]},{"domain":"unmatched.example.test","names":[]}]}`

func TestEASMAssetRulesPreserveTheirActualSourceAndDoNotInventCompanies(t *testing.T) {
	patch := easmAssetRulePatch([]byte(easmAssetRuleState), []byte(easmAssetRuleArguments), easmOrganizationResult(easmAssetRuleReply))
	operations, err := jsonpatch.DecodePatch(patch)
	if err != nil || len(operations) == 0 {
		t.Fatalf("asset rule evidence was withheld: patch=%s error=%v", patch, err)
	}
	updated, err := operations.Apply([]byte(easmAssetRuleState))
	if err != nil {
		t.Fatal(err)
	}
	var before, after map[string]json.RawMessage
	_ = json.Unmarshal([]byte(easmAssetRuleState), &before)
	_ = json.Unmarshal(updated, &after)
	for _, key := range []string{"schema_version", "title", "summary", "heat", "components"} {
		if !jsonpatch.Equal(before[key], after[key]) {
			t.Errorf("asset rules changed unrelated %s", key)
		}
	}
	var graph struct {
		Nodes []struct {
			ID, Kind, Label string
			IPCount         json.RawMessage `json:"ip_count"`
			Detail          struct {
				Reason   string
				Features []struct{ Kind, Value string }
			}
		}
		Edges []struct {
			Source, Target string
			Confirmed      bool
		}
	}
	if err := json.Unmarshal(updated, &graph); err != nil {
		t.Fatal(err)
	}
	if len(graph.Nodes) != 6 || len(graph.Edges) != 3 {
		t.Fatalf("asset rule mapping created method nodes or lost domain identity: %s", updated)
	}
	for _, node := range graph.Nodes {
		if node.Kind == "feature" || node.Kind == "company" && node.Label == "Fixture Product" {
			t.Fatalf("a rule name became an unsupported entity or source type: %+v", node)
		}
		if node.ID == "domain-local" && (string(node.IPCount) != "9007199254740993" || !strings.HasPrefix(node.Detail.Reason, "original domain evidence") || !strings.Contains(node.Detail.Reason, "mcp_company_asset") || !strings.Contains(node.Detail.Reason, "2026.10.31") || len(node.Detail.Features) != 1) {
			t.Fatalf("asset rules replaced original domain evidence: %+v", node)
		}
		if node.Label == "product.example.test" && (!strings.Contains(node.Detail.Reason, "Fixture Product") || !strings.Contains(node.Detail.Reason, "2026.11.04")) {
			t.Fatalf("untyped product name evidence was lost: %+v", node)
		}
	}
	if graph.Edges[0].Confirmed || !graph.Edges[1].Confirmed || graph.Edges[2].Source != "company-south" || graph.Edges[2].Target != "domain:south.example.test" || !graph.Edges[2].Confirmed {
		t.Fatalf("ownership rules were flattened into a different relationship: %s", updated)
	}
	if repeated := easmAssetRulePatch(updated, []byte(easmAssetRuleArguments), easmOrganizationResult(easmAssetRuleReply)); repeated != nil {
		t.Fatalf("repeated rules changed source evidence or relation identity: %s", repeated)
	}
}

func TestEASMAssetRulesKeepSeveralReportedOwnersAndCreationDates(t *testing.T) {
	result := easmOrganizationResult(`{"results":[{"domain":"north.example.test","names":[{"name":"Fixture North Company","time":"2026.01.01"},{"name":"Fixture South Company","time":"2026.02.02"},{"name":"Fixture Product","time":"2026.03.03"},{"name":"Fixture North Company","time":"2026.01.01"}]}]}`)
	patch, err := jsonpatch.DecodePatch(easmAssetRulePatch([]byte(easmAssetRuleState), []byte(`{"domains":["NORTH.example.test.","north.example.test"]}`), result))
	if err != nil {
		t.Fatal(err)
	}
	updated, err := patch.Apply([]byte(easmAssetRuleState))
	if err != nil {
		t.Fatal(err)
	}
	var graph struct {
		Nodes []struct {
			ID     string
			Detail struct{ Reason string }
		}
		Edges []struct{ Source, Target string }
	}
	_ = json.Unmarshal(updated, &graph)
	if len(graph.Nodes) != 4 || len(graph.Edges) != 3 || graph.Edges[2].Source != "company-south" || graph.Edges[2].Target != "domain-local" {
		t.Fatalf("a shared domain lost its rule sources: %s", updated)
	}
	for _, date := range []string{"2026.01.01", "2026.02.02", "2026.03.03"} {
		if strings.Count(graph.Nodes[3].Detail.Reason, date) != 1 {
			t.Fatalf("creation date was lost or duplicated: %+v", graph.Nodes[3])
		}
	}
	if next := easmAssetRulePatch(updated, []byte(`{"domains":["north.example.test"]}`), result); next != nil {
		t.Fatalf("shared-domain repeat was not idempotent: %s", next)
	}
}

func TestEASMAssetRulesRetainEvidenceOnEveryExistingCanonicalDomainID(t *testing.T) {
	state := strings.Replace(easmAssetRuleState, `"nodes":[`, `"nodes":[{"id":"second-domain","kind":"domain","label":"north.example.test","detail":{"reason":"second source evidence"}},`, 1)
	arguments := []byte(`{"domains":["NORTH.example.test."]}`)
	result := easmOrganizationResult(`{"results":[{"domain":"north.example.test","names":[{"name":"Fixture North Company","time":"2026.01.01"}]}]}`)
	patch, err := jsonpatch.DecodePatch(easmAssetRulePatch([]byte(state), arguments, result))
	if err != nil || len(patch) == 0 {
		t.Fatalf("existing domain identities suppressed an explicit source: patch=%v error=%v", patch, err)
	}
	updated, err := patch.Apply([]byte(state))
	if err != nil {
		t.Fatal(err)
	}
	var graph struct {
		Nodes []struct {
			ID, Kind, Label string
			Detail          struct{ Reason string }
		}
		Edges []struct {
			Source, Target string
			Confirmed      bool
		}
	}
	_ = json.Unmarshal(updated, &graph)
	if len(graph.Nodes) != 5 || len(graph.Edges) != 3 {
		t.Fatalf("canonical-domain identities were selected, merged or duplicated: %s", updated)
	}
	for _, node := range graph.Nodes {
		if node.ID == "domain-local" || node.ID == "second-domain" {
			if !strings.Contains(node.Detail.Reason, "mcp_company_asset") || !strings.Contains(node.Detail.Reason, "2026.01.01") {
				t.Fatalf("source evidence was not copied to %s", node.ID)
			}
			if node.ID == "second-domain" && !strings.HasPrefix(node.Detail.Reason, "second source evidence") || node.ID == "domain-local" && !strings.HasPrefix(node.Detail.Reason, "original domain evidence") {
				t.Fatalf("source evidence replaced retained detail: %+v", node)
			}
		}
	}
	owners := make(map[string]bool)
	for _, edge := range graph.Edges {
		if edge.Source == "company-north" && edge.Confirmed {
			owners[edge.Target] = true
		}
		if edge.Source == "root-local" && (edge.Target == "domain-local" || edge.Target == "second-domain") {
			t.Fatal("canonical-domain preservation invented a root relationship")
		}
	}
	if !owners["domain-local"] || !owners["second-domain"] {
		t.Fatalf("the rule did not retain each existing domain association: %s", updated)
	}
	if repeated := easmAssetRulePatch(updated, arguments, result); repeated != nil {
		t.Fatalf("repeated rule added duplicate evidence or edges: %s", repeated)
	}
}

func TestEASMAssetRulesUseOnlyUniqueKnownSubjectsAndPreserveOtherEdgeDirections(t *testing.T) {
	for _, scenario := range []string{"explicit-root", "ambiguous-company", "reverse-edge", "missing-rule-date"} {
		t.Run(scenario, func(t *testing.T) {
			state := easmAssetRuleState
			reply := `{"results":[{"domain":"north.example.test","names":[{"name":"Fixture North Company","time":"2026.01.01"}]}]}`
			switch scenario {
			case "explicit-root":
				reply = `{"results":[{"domain":"north.example.test","names":[{"name":"Fixture","time":"2026.01.01"}]}]}`
			case "ambiguous-company":
				state = strings.Replace(state, `"nodes":[`, `"nodes":[{"id":"another-company","kind":"company","label":"Fixture North Company"},`, 1)
			case "reverse-edge":
				state = strings.Replace(state, `{"source":"company-north","target":"domain-local","confirmed":false}`, `{"source":"domain-local","target":"company-north","confirmed":true}`, 1)
			case "missing-rule-date":
				reply = `{"results":[{"domain":"north.example.test","names":[{"name":"Fixture North Company","time":""}]}]}`
			}
			patch, err := jsonpatch.DecodePatch(easmAssetRulePatch([]byte(state), []byte(`{"domains":["north.example.test"]}`), easmOrganizationResult(reply)))
			if err != nil || len(patch) == 0 {
				t.Fatalf("explicit asset rule evidence was dropped: patch=%v error=%v", patch, err)
			}
			updated, err := patch.Apply([]byte(state))
			if err != nil {
				t.Fatal(err)
			}
			var graph struct {
				Nodes []struct {
					ID     string
					Detail struct{ Reason string }
				}
				Edges []struct {
					Source, Target string
					Confirmed      bool
				}
			}
			_ = json.Unmarshal(updated, &graph)
			if scenario == "ambiguous-company" && (len(graph.Edges) != 2 || graph.Edges[1].Confirmed) {
				t.Fatal("an ambiguous company name selected an arbitrary source identity")
			}
			if scenario == "explicit-root" && (len(graph.Edges) != 3 || graph.Edges[2].Source != "root-local") {
				t.Fatal("a known exact target lost its own rule association")
			}
			if scenario == "reverse-edge" && (len(graph.Edges) != 3 || graph.Edges[1].Source != "domain-local" || graph.Edges[2].Source != "company-north") {
				t.Fatal("the rule rewrote another source's existing direction")
			}
			if scenario == "missing-rule-date" && strings.Contains(graph.Nodes[3].Detail.Reason, "创建时间") {
				t.Fatal("an unknown rule creation date was invented")
			}
		})
	}
}

func TestEASMAssetRulesIgnoreErrorsUnknownDomainsAndUnusableEvidence(t *testing.T) {
	valid := `{"results":[{"domain":"north.example.test","names":[{"name":"Fixture North Company","time":"2026.01.01"}]}]}`
	for _, test := range []struct{ name, state, arguments, reply string }{
		{"bad arguments", easmAssetRuleState, `{`, valid},
		{"no domains", easmAssetRuleState, `{}`, valid},
		{"IP-only request", easmAssetRuleState, `{"ips":["127.0.0.1"]}`, valid},
		{"invalid domains", easmAssetRuleState, `{"domains":["127.0.0.1"]}`, valid},
		{"foreign domain", easmAssetRuleState, `{"domains":["different.example.test"]}`, valid},
		{"malformed reply", easmAssetRuleState, easmAssetRuleArguments, `{`},
		{"error flag", easmAssetRuleState, easmAssetRuleArguments, `{"error":true}`},
		{"isError", easmAssetRuleState, easmAssetRuleArguments, `{"isError":true}`},
		{"error message", easmAssetRuleState, easmAssetRuleArguments, `{"errmsg":"failed"}`},
		{"no result", easmAssetRuleState, easmAssetRuleArguments, `{}`},
		{"no match is normal", easmAssetRuleState, easmAssetRuleArguments, `{"results":[{"domain":"unmatched.example.test","names":[]}]}`},
		{"blank translated name", easmAssetRuleState, easmAssetRuleArguments, `{"results":[{"domain":"north.example.test","names":[{"name":" ","time":"2026.01.01"}]}]}`},
		{"bad graph", `{`, easmAssetRuleArguments, valid},
		{"missing nodes", `{"edges":[]}`, easmAssetRuleArguments, valid},
		{"missing edges", `{"nodes":[]}`, easmAssetRuleArguments, valid},
		{"missing root", `{"nodes":[],"edges":[]}`, easmAssetRuleArguments, valid},
		{"empty node ID", strings.Replace(easmAssetRuleState, `"id":"root-local"`, `"id":""`, 1), easmAssetRuleArguments, valid},
		{"duplicate node ID", strings.Replace(easmAssetRuleState, `"id":"company-north"`, `"id":"root-local"`, 1), easmAssetRuleArguments, valid},
		{"bad node", strings.Replace(easmAssetRuleState, `"nodes":[`, `"nodes":[7,`, 1), easmAssetRuleArguments, valid},
		{"empty root label", strings.Replace(easmAssetRuleState, `"label":"Fixture"`, `"label":""`, 1), easmAssetRuleArguments, valid},
		{"duplicate root", strings.Replace(easmAssetRuleState, `"kind":"company"`, `"kind":"root"`, 1), easmAssetRuleArguments, valid},
		{"unknown edge source", strings.Replace(easmAssetRuleState, `"source":"root-local"`, `"source":"missing"`, 1), easmAssetRuleArguments, valid},
		{"unknown edge target", strings.Replace(easmAssetRuleState, `"target":"company-north"`, `"target":"missing"`, 1), easmAssetRuleArguments, valid},
		{"bad domain detail", strings.Replace(easmAssetRuleState, `"detail":{"reason":"original domain evidence","features":[{"kind":"cert","value":"existing certificate"}]}`, `"detail":7`, 1), easmAssetRuleArguments, valid},
		{"bad domain reason", strings.Replace(easmAssetRuleState, `"reason":"original domain evidence"`, `"reason":7`, 1), easmAssetRuleArguments, valid},
		{"generated ID collision", strings.Replace(easmAssetRuleState, `"nodes":[`, `"nodes":[{"id":"domain:south.example.test","kind":"org","label":"different source"},`, 1), easmAssetRuleArguments, easmAssetRuleReply},
	} {
		t.Run(test.name, func(t *testing.T) {
			if patch := easmAssetRulePatch([]byte(test.state), []byte(test.arguments), easmOrganizationResult(test.reply)); patch != nil {
				t.Fatalf("unusable asset rule changed the accepted graph: %s", patch)
			}
		})
	}
	if easmAssetRulePatch([]byte(easmAssetRuleState), []byte(easmAssetRuleArguments), nil) != nil || easmAssetRulePatch([]byte(easmAssetRuleState), []byte(easmAssetRuleArguments), &mcp.CallToolResult{IsError: true}) != nil {
		t.Fatal("a failed asset rule request modified the graph")
	}
}
