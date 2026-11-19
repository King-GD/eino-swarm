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

const easmCompanyState = `{"schema_version":1,"title":"Fixture Brand","nodes":[{"id":"brand","kind":"root","label":"Fixture Brand"},{"id":"company:fofa-org:different-system-parent-id","kind":"company","label":"Fixture Parent","detail":{"description":"Retain parent description"}},{"id":"legacy-child","kind":"company","label":"Fixture Subsidiary","detail":{"reason":"Retain domain ownership evidence","features":[{"kind":"domain","value":"example.test","node_id":"domain"}]}},{"id":"domain","kind":"domain","label":"example.test","ip_count":9007199254740993}],"edges":[{"source":"brand","target":"company:fofa-org:different-system-parent-id","confirmed":false},{"source":"brand","target":"legacy-child","confirmed":false},{"source":"legacy-child","target":"domain","confirmed":true}],"summary":{"clues":4},"heat":[],"components":[]}`
const easmCompanyArguments = `{"keyword":"Fixture Brand alias","include_human":false,"min_percent":0.05}`
const easmCompanyReply = `{"total":2,"size":2,"root":{"id":"registry-parent-id","name":"Fixture Parent","credit_code":"PARENT-CODE","reg_status":"Active","label":"Company","open":true,"percent":"","pid":""},"down":[{"id":"registry-child-id","name":"Fixture Subsidiary","credit_code":"CHILD-CODE","reg_status":"Active","label":"Company","open":false,"percent":"1","pid":""},{"id":"registry-other-id","name":"Fixture Other Subsidiary","credit_code":"OTHER-CODE","reg_status":"Active","label":"Company","open":false,"percent":"0.9","pid":"different-sentinel"}],"up":[{"id":"outside-investor-id","name":"Outside Investor","label":"Company","percent":"1","children":[{"id":"unrelated-id","name":"Outside Asset Owner","label":"Company"}]}]}`

func TestEASMCompanyRelationsPreserveTheLegalInvestmentDirection(t *testing.T) {
	result := easmOrganizationResult(easmCompanyReply)
	patch := easmCompanyRelationPatch([]byte(easmCompanyState), []byte(easmCompanyArguments), result)
	operations, err := jsonpatch.DecodePatch(patch)
	if err != nil || len(operations) == 0 || len(operations) > 2 {
		t.Fatalf("legal investment path was not published: patch=%s error=%v", patch, err)
	}
	updated, err := operations.Apply([]byte(easmCompanyState))
	if err != nil {
		t.Fatal(err)
	}
	var graph struct {
		Nodes []struct {
			ID, Kind, Label string
			Detail          struct {
				Reason, Description string
				Features            []json.RawMessage
			}
		}
		Edges []struct {
			Source, Target string
			Confirmed      bool
		}
	}
	_ = json.Unmarshal(updated, &graph)
	if len(graph.Nodes) != 5 || len(graph.Edges) != 5 {
		t.Fatalf("source companies were duplicated or investor assets were invented: %s", updated)
	}
	if graph.Nodes[0].ID != "brand" || graph.Nodes[0].Kind != "root" || graph.Nodes[0].Label != "Fixture Brand" {
		t.Fatal("the brand root was reclassified as the legal parent")
	}
	if graph.Nodes[1].ID != "company:fofa-org:different-system-parent-id" || graph.Nodes[1].Detail.Description != "Retain parent description" {
		t.Fatal("a QCC registry ID rejected or replaced the existing FOFA company identity")
	}
	if graph.Nodes[2].ID != "legacy-child" || !strings.Contains(graph.Nodes[2].Detail.Reason, "Retain domain ownership evidence") || len(graph.Nodes[2].Detail.Features) != 1 {
		t.Fatal("the registry observation replaced existing company evidence")
	}
	newChild := graph.Nodes[4]
	if newChild.ID != "company:registry:registry-other-id" || newChild.Kind != "company" || newChild.Label != "Fixture Other Subsidiary" || !strings.Contains(newChild.Detail.Reason, "90%") || !strings.Contains(newChild.Detail.Reason, "OTHER-CODE") || !strings.Contains(newChild.Detail.Reason, "mcp_company_relation") {
		t.Fatalf("the legal company lost its source metadata: %+v", newChild)
	}
	for _, edge := range graph.Edges[3:] {
		if edge.Source != graph.Nodes[1].ID || !edge.Confirmed || edge.Target != "legacy-child" && edge.Target != newChild.ID {
			t.Fatalf("the explicit investment relationship was flattened into a brand association: %+v", edge)
		}
	}
	if repeated := easmCompanyRelationPatch(updated, []byte(easmCompanyArguments), result); repeated != nil {
		t.Fatalf("repeated registry evidence changed the graph: %s", repeated)
	}
	var before, after map[string]json.RawMessage
	_ = json.Unmarshal([]byte(easmCompanyState), &before)
	_ = json.Unmarshal(updated, &after)
	for _, field := range []string{"title", "schema_version", "summary", "heat", "components"} {
		if !reflect.DeepEqual(before[field], after[field]) {
			t.Fatalf("company evidence changed unrelated %s", field)
		}
	}
	if !strings.Contains(string(after["nodes"]), "9007199254740993") || strings.Contains(string(updated), "Fixture Brand alias") {
		t.Fatal("existing numeric identity changed or a search keyword became ownership evidence")
	}
}

func applyEASMCompanyPatch(t *testing.T, state, body string) []byte {
	t.Helper()
	patch := easmCompanyRelationPatch([]byte(state), []byte(easmCompanyArguments), easmOrganizationResult(body))
	operations, err := jsonpatch.DecodePatch(patch)
	if err != nil || len(operations) != 2 {
		t.Fatalf("company patch=%s error=%v", patch, err)
	}
	updated, err := operations.Apply([]byte(state))
	if err != nil {
		t.Fatal(err)
	}
	if repeated := easmCompanyRelationPatch(updated, []byte(easmCompanyArguments), easmOrganizationResult(body)); repeated != nil {
		t.Fatalf("duplicate company source changed graph: %s", repeated)
	}
	return updated
}

func TestEASMCompanyRelationsKeepTheBrandAndLegalParentAsDifferentNodes(t *testing.T) {
	state := `{"nodes":[{"id":"brand","kind":"root","label":"Fixture Brand"}],"edges":[]}`
	body := `{"root":{"id":9007199254740993,"label":"Company","name":"Fixture Parent"},"down":[],"up":[{"id":"investor","name":"Outside Investor","label":"Company"}]}`
	updated := applyEASMCompanyPatch(t, state, body)
	var graph struct {
		Nodes []struct{ ID, Kind, Label string }
		Edges []struct {
			Source, Target string
			Confirmed      bool
		}
	}
	_ = json.Unmarshal(updated, &graph)
	if len(graph.Nodes) != 2 || graph.Nodes[0].Kind != "root" || graph.Nodes[1].Kind != "company" || graph.Nodes[1].ID != "company:registry:9007199254740993" || len(graph.Edges) != 1 || graph.Edges[0].Confirmed {
		t.Fatalf("a legal lookup reclassified the brand or admitted an upstream investor: %s", updated)
	}
}

func TestEASMCompanyRelationsReuseTheKnownLegalSubjectRoot(t *testing.T) {
	for _, test := range []struct{ name, id, label string }{
		{"full_legal_name", "subject-root", "Fixture Parent"},
		{"known_registry_id", "company:registry:registry-parent-id", "Fixture Parent"},
		{"known_registry_id_with_display_alias", "company:registry:registry-parent-id", "Known Display Alias"},
	} {
		t.Run(test.name, func(t *testing.T) {
			state, _ := json.Marshal(map[string]any{
				"nodes": []any{map[string]any{"id": test.id, "kind": "root", "label": test.label}, map[string]any{"id": "domain", "kind": "domain", "label": "example.test"}},
				"edges": []any{map[string]any{"source": test.id, "target": "domain", "confirmed": true}},
			})
			updated := applyEASMCompanyPatch(t, string(state), easmCompanyReply)
			var graph struct {
				Nodes []struct{ ID, Kind, Label string }
				Edges []struct {
					Source, Target string
					Confirmed      bool
				}
			}
			_ = json.Unmarshal(updated, &graph)
			if len(graph.Nodes) != 4 || len(graph.Edges) != 3 || graph.Nodes[0].ID != test.id || graph.Nodes[0].Kind != "root" || graph.Nodes[0].Label != test.label {
				t.Fatalf("a legal subject root was duplicated or reclassified: %s", updated)
			}
			for _, node := range graph.Nodes[1:] {
				if node.Label == "Fixture Parent" {
					t.Fatal("the unique known legal subject gained a duplicate company node")
				}
			}
			for _, edge := range graph.Edges {
				if edge.Source != test.id || edge.Source == edge.Target || !edge.Confirmed {
					t.Fatalf("existing root ownership or legal child direction was lost: %+v", edge)
				}
			}
		})
	}
}

func TestEASMCompanyRelationsDoNotGuessWhichSameNamedRegistryEntityIsTheRoot(t *testing.T) {
	state := `{"nodes":[{"id":"subject-root","kind":"root","label":"Same Legal Name"}],"edges":[]}`
	body := `{"root":{"id":"parent","name":"Same Legal Name","label":"Company"},"down":[{"id":"different-company","name":"Same Legal Name","label":"Company"}]}`
	updated := applyEASMCompanyPatch(t, state, body)
	var graph struct {
		Nodes []struct{ ID, Kind string }
		Edges []struct {
			Source, Target string
			Confirmed      bool
		}
	}
	_ = json.Unmarshal(updated, &graph)
	if len(graph.Nodes) != 3 || len(graph.Edges) != 2 || graph.Nodes[1].ID != "company:registry:parent" || graph.Nodes[2].ID != "company:registry:different-company" || graph.Edges[0].Confirmed {
		t.Fatalf("same-name source ambiguity guessed a root identity: %s", updated)
	}
	state = `{"nodes":[{"id":"subject-root","kind":"root","label":"Same Legal Name"},{"id":"company:fofa-org:one","kind":"company","label":"Same Legal Name"},{"id":"company:fofa-org:two","kind":"company","label":"Same Legal Name"}],"edges":[]}`
	body = `{"root":{"id":"parent","name":"Same Legal Name","label":"Company"},"down":[]}`
	updated = applyEASMCompanyPatch(t, state, body)
	_ = json.Unmarshal(updated, &graph)
	if len(graph.Nodes) != 4 || len(graph.Edges) != 1 || graph.Nodes[3].ID != "company:registry:parent" || graph.Edges[0].Confirmed {
		t.Fatalf("existing identified same-name companies guessed the legal subject root: %s", updated)
	}
}

func TestEASMCompanyRelationsKeepDistinctRegistryCompaniesWithTheSameName(t *testing.T) {
	for _, childCount := range []int{1, 2} {
		t.Run(fmt.Sprintf("source_count_%d", childCount), func(t *testing.T) {
			state := `{"nodes":[{"id":"brand","kind":"root","label":"Fixture"},{"id":"legacy-one","kind":"company","label":"Repeated Legal Name"},{"id":"legacy-two","kind":"company","label":"Repeated Legal Name"}],"edges":[]}`
			down := []any{}
			for i := 0; i < childCount; i++ {
				down = append(down, map[string]any{"id": fmt.Sprintf("source-%d", i), "name": "Repeated Legal Name", "label": "Company"})
			}
			body, _ := json.Marshal(map[string]any{"root": map[string]string{"id": "parent", "name": "Parent", "label": "Company"}, "down": down})
			updated := applyEASMCompanyPatch(t, state, string(body))
			var graph struct {
				Nodes []struct{ ID, Kind, Label string }
			}
			_ = json.Unmarshal(updated, &graph)
			if len(graph.Nodes) != 4+childCount {
				t.Fatalf("ambiguous labels merged different registry IDs: %s", updated)
			}
			for i := 0; i < childCount; i++ {
				if graph.Nodes[4+i].ID != fmt.Sprintf("company:registry:source-%d", i) {
					t.Fatalf("a source company reused an ambiguous historical ID: %+v", graph.Nodes[4+i])
				}
			}
		})
	}
}

func TestEASMCompanyRelationsPromoteOnlyTheDirectedInvestmentEvidence(t *testing.T) {
	state := strings.Replace(easmCompanyState, `"edges":[`, `"edges":[{"source":"company:fofa-org:different-system-parent-id","target":"legacy-child","confirmed":false,"future_evidence":{"keep":true}},`, 1)
	updated := applyEASMCompanyPatch(t, state, easmCompanyReply)
	var graph struct{ Edges []map[string]json.RawMessage }
	_ = json.Unmarshal(updated, &graph)
	if string(graph.Edges[0]["confirmed"]) != "true" || string(graph.Edges[0]["future_evidence"]) != `{"keep":true}` || string(graph.Edges[1]["confirmed"]) != "false" {
		t.Fatalf("explicit ownership erased evidence or upgraded the brand relation: %s", updated)
	}
}

func TestEASMCompanyRelationsIgnoreUpstreamPeopleAndUncontractedChildren(t *testing.T) {
	body := `{"root":{"id":"parent","name":"Legal Parent","label":"Company"},"down":[{"id":"child","name":"Legal Child","label":"Company","children":[{"id":"uncontracted","name":"Nested Candidate","label":"Company"}]},{"id":"child","name":"Legal Child","label":"Company"},{"id":"human","name":"Person","label":"Human"},{"id":"","name":"Missing Identity","label":"Company"},{"id":"missing-name","name":" ","label":"Company"},{"id":"parent","name":"Legal Parent","label":"Company"}],"up":[{"id":"investor","name":"Outside Investor","label":"Company"}]}`
	updated := applyEASMCompanyPatch(t, `{"nodes":[{"id":"brand","kind":"root","label":"Fixture"}],"edges":[]}`, body)
	var graph struct {
		Nodes []struct{ ID string }
		Edges []json.RawMessage
	}
	_ = json.Unmarshal(updated, &graph)
	if len(graph.Nodes) != 3 || len(graph.Edges) != 2 || strings.Contains(string(updated), "Outside Investor") || strings.Contains(string(updated), "Nested Candidate") || strings.Contains(string(updated), "Person") {
		t.Fatalf("the direct-investment response expanded into unverified asset scope: %s", updated)
	}
}

func TestEASMCompanyRelationsPreserveExistingRegistryIdentityDespiteNameVariations(t *testing.T) {
	state := `{"nodes":[{"id":"brand","kind":"root","label":"Fixture"},{"id":"company:registry:parent","kind":"company","label":"Historical Parent Name"}],"edges":[]}`
	body := `{"root":{"id":"parent","name":"Current Legal Parent","label":"Company"},"down":[{"id":"child","name":"Legal Child","label":"Company"},{"id":"child","name":"Returned Child Alias","label":"Company"}]}`
	updated := applyEASMCompanyPatch(t, state, body)
	if !strings.Contains(string(updated), "Current Legal Parent") || !strings.Contains(string(updated), "Returned Child Alias") {
		t.Fatalf("a name rule discarded registry observations: %s", updated)
	}
}

func TestEASMCompanyRelationsRejectInvalidOwnersAndIdentityCollisions(t *testing.T) {
	for _, test := range []struct{ state, args, body string }{
		{easmCompanyState, `{`, easmCompanyReply},
		{easmCompanyState, `{}`, easmCompanyReply},
		{easmCompanyState, easmCompanyArguments, `{`},
		{easmCompanyState, easmCompanyArguments, `{"error":true}`},
		{easmCompanyState, easmCompanyArguments, `{"isError":true}`},
		{easmCompanyState, easmCompanyArguments, `{"errmsg":"Source failed"}`},
		{easmCompanyState, easmCompanyArguments, `{"root":{"id":"person","name":"A person","label":"Human"},"down":[]}`},
		{easmCompanyState, easmCompanyArguments, `{"root":{"id":"parent","name":" ","label":"Company"},"down":[]}`},
		{easmCompanyState, easmCompanyArguments, `{"root":{"id":null,"name":"Parent","label":"Company"},"down":[]}`},
		{easmCompanyState, easmCompanyArguments, `{"root":{"id":"parent","name":"Parent","label":"Company"}}`},
		{`{`, easmCompanyArguments, easmCompanyReply},
		{`{"nodes":[],"edges":[]}`, easmCompanyArguments, easmCompanyReply},
		{`{"nodes":null,"edges":[]}`, easmCompanyArguments, easmCompanyReply},
		{`{"nodes":[],"edges":null}`, easmCompanyArguments, easmCompanyReply},
		{`{"nodes":[null],"edges":[]}`, easmCompanyArguments, easmCompanyReply},
		{`{"nodes":[{"id":"same","kind":"root","label":"Fixture"},{"id":"same","kind":"company","label":"Parent"}],"edges":[]}`, easmCompanyArguments, easmCompanyReply},
		{`{"nodes":[{"id":"root","kind":"root","label":"Fixture"},{"id":"other","kind":"root","label":"Other"}],"edges":[]}`, easmCompanyArguments, easmCompanyReply},
		{`{"nodes":[{"id":"root","kind":"root","label":" "}],"edges":[]}`, easmCompanyArguments, easmCompanyReply},
		{`{"nodes":[{"id":"root","kind":"root","label":"Fixture"}],"edges":[{}]}`, easmCompanyArguments, easmCompanyReply},
		{`{"nodes":[{"id":"root","kind":"root","label":"Fixture"},{"id":"company:registry:registry-parent-id","kind":"domain","label":"example.test"}],"edges":[]}`, easmCompanyArguments, easmCompanyReply},
		{`{"nodes":[{"id":"root","kind":"root","label":"Fixture"},{"id":"company:registry:registry-child-id","kind":"domain","label":"example.test"}],"edges":[]}`, easmCompanyArguments, easmCompanyReply},
		{strings.Replace(easmCompanyState, `"description":"Retain parent description"`, `"description":"Retain parent description","reason":42`, 1), easmCompanyArguments, easmCompanyReply},
		{strings.Replace(easmCompanyState, `"detail":{"description":"Retain parent description"}`, `"detail":[]`, 1), easmCompanyArguments, easmCompanyReply},
	} {
		if patch := easmCompanyRelationPatch([]byte(test.state), []byte(test.args), easmOrganizationResult(test.body)); patch != nil {
			t.Fatalf("unsupported company evidence changed graph: state=%s body=%s patch=%s", test.state, test.body, patch)
		}
	}
	for _, result := range []*mcp.CallToolResult{nil, {IsError: true}, {StructuredContent: func() {}}} {
		if patch := easmCompanyRelationPatch([]byte(easmCompanyState), []byte(easmCompanyArguments), result); patch != nil {
			t.Fatalf("failed MCP result changed company ownership: %s", patch)
		}
	}
}

func TestEASMCompanyRelationsKeepLargeBatchesWithinThePatchOperationLimit(t *testing.T) {
	down := make([]any, 0, 300)
	for i := 0; i < 300; i++ {
		down = append(down, map[string]any{"id": i + 1000, "name": fmt.Sprintf("Legal Child %d", i), "label": "Company", "percent": "1"})
	}
	body, _ := json.Marshal(map[string]any{"root": map[string]any{"id": "parent", "name": "Legal Parent", "label": "Company"}, "down": down})
	var value any
	_ = json.Unmarshal(body, &value)
	result := &mcp.CallToolResult{StructuredContent: value}
	patch := easmCompanyRelationPatch([]byte(`{"nodes":[{"id":"brand","kind":"root","label":"Fixture"}],"edges":[]}`), []byte(easmCompanyArguments), result)
	operations, err := jsonpatch.DecodePatch(patch)
	if err != nil || len(operations) != 2 {
		t.Fatalf("large company tree exceeded operation limit: %s error=%v", patch, err)
	}
}

func TestEASMCompanyRegistryIDsKeepOpaqueAndExactNumericSourceIdentity(t *testing.T) {
	for _, test := range []struct{ raw, want string }{
		{`"registry-parent"`, "company:registry:registry-parent"},
		{`9007199254740993`, "company:registry:9007199254740993"},
		{`null`, ""}, {`" "`, ""}, {`"line\nbreak"`, ""}, {`{}`, ""}, {`-1`, ""},
	} {
		if got := easmCompanyID([]byte(test.raw)); got != test.want {
			t.Fatalf("registry ID %s became %q", test.raw, got)
		}
	}
}

func TestEASMCompanyOwnershipPercentPreservesUnknownSourceFormats(t *testing.T) {
	for _, test := range []struct{ value, want string }{
		{"1", "100%（接口原值 1）"}, {"0.9", "90%（接口原值 0.9）"},
		{"0", "0%（接口原值 0）"}, {"0.005", "0.5%（接口原值 0.005）"},
		{"0.123456789", "12.3456789%（接口原值 0.123456789）"},
		{"unknown", "unknown"}, {"", ""}, {"-0.1", "-0.1"}, {"2", "2"},
		{"0.9e0", "0.9e0"}, {"1/2", "1/2"}, {"0x0.8", "0x0.8"},
	} {
		if got := easmCompanyPercent(test.value); got != test.want {
			t.Fatalf("ownership percent %q became %q", test.value, got)
		}
	}
}
