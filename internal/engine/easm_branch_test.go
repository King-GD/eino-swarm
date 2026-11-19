package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/LubyRuffy/eino-swarm/internal/mcpclient"
	jsonpatch "github.com/evanphx/json-patch"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const easmBranchReply = `{"total":2,"size":20,"page":1,"items":[{"id":123,"name":"Fixture Subsidiary","reg_status":"Active","credit_code":"BRANCH-CODE"},{"name":"Fixture Branch West","reg_status":"Closed"}]}`

func applyEASMBranchPatch(t *testing.T, state string, binding easmBranchBinding, result *mcp.CallToolResult) []byte {
	t.Helper()
	patch := easmCompanyBranchPatch([]byte(state), binding, result)
	operations, err := jsonpatch.DecodePatch(patch)
	if err != nil || len(operations) != 2 {
		t.Fatalf("branch patch=%s error=%v", patch, err)
	}
	updated, err := operations.Apply([]byte(state))
	if err != nil {
		t.Fatal(err)
	}
	if repeated := easmCompanyBranchPatch(updated, binding, result); repeated != nil {
		t.Fatalf("branch retry was not idempotent: %s", repeated)
	}
	return updated
}

func TestEASMCompanyBranchesPreserveTheirLegalOwnerStatusAndExistingEvidence(t *testing.T) {
	binding := easmBranchBinding{ParentID: "company:fofa-org:different-system-parent-id", Keyword: "Fixture Parent"}
	updated := applyEASMBranchPatch(t, easmCompanyState, binding, easmOrganizationResult(easmBranchReply))
	var before, after map[string]json.RawMessage
	_ = json.Unmarshal([]byte(easmCompanyState), &before)
	_ = json.Unmarshal(updated, &after)
	for _, field := range []string{"schema_version", "title", "summary", "heat", "components"} {
		if !reflect.DeepEqual(before[field], after[field]) {
			t.Errorf("branch evidence changed unrelated %s", field)
		}
	}
	var graph struct {
		Nodes []struct {
			ID, Kind, Label string
			Detail          struct {
				Reason   string
				Features []json.RawMessage
			}
		}
		Edges []struct {
			Source, Target string
			Confirmed      bool
		}
	}
	_ = json.Unmarshal(updated, &graph)
	if len(graph.Nodes) != 5 || len(graph.Edges) != 5 {
		t.Fatalf("branches lost stable nodes or legal direction: %s", updated)
	}
	if graph.Nodes[2].ID != "legacy-child" || len(graph.Nodes[2].Detail.Features) != 1 || !strings.Contains(graph.Nodes[2].Detail.Reason, "Retain domain ownership evidence") || !strings.Contains(graph.Nodes[2].Detail.Reason, "Active") || !strings.Contains(graph.Nodes[2].Detail.Reason, "BRANCH-CODE") {
		t.Fatalf("existing branch evidence was overwritten: %+v", graph.Nodes[2])
	}
	if graph.Nodes[4].ID != "company:branch-name:Fixture Branch West" || !strings.Contains(graph.Nodes[4].Detail.Reason, "Closed") || !strings.Contains(graph.Nodes[4].Detail.Reason, "mcp_company_branch") {
		t.Fatalf("a branch without a source ID lost its stable name or status: %+v", graph.Nodes[4])
	}
	for _, edge := range graph.Edges[3:] {
		if edge.Source != binding.ParentID || !edge.Confirmed {
			t.Fatalf("branch was attached to the brand root: %+v", edge)
		}
	}
	if !strings.Contains(string(after["nodes"]), "9007199254740993") {
		t.Fatal("existing count precision changed")
	}
}

func TestEASMCompanyBranchesDoNotInventLegalOwnersFromSearchKeywords(t *testing.T) {
	for _, parent := range []string{"", "missing-parent", "domain"} {
		binding := easmBranchBinding{ParentID: parent, Keyword: "Unresolved search alias"}
		updated := applyEASMBranchPatch(t, easmCompanyState, binding, easmOrganizationResult(easmBranchReply))
		var graph struct {
			Nodes []struct {
				Label  string
				Detail struct{ Reason string }
			}
			Edges []json.RawMessage
		}
		_ = json.Unmarshal(updated, &graph)
		if len(graph.Edges) != 3 {
			t.Fatalf("an unresolved keyword created an ownership edge: %s", updated)
		}
		for _, node := range graph.Nodes {
			if node.Label == binding.Keyword {
				t.Fatal("a search keyword became a legal company")
			}
		}
		if !strings.Contains(graph.Nodes[4].Detail.Reason, "查询对象 Unresolved search alias") || strings.Contains(graph.Nodes[4].Detail.Reason, "所属法人") {
			t.Fatal("the query source was replaced by unproven ownership")
		}
	}
}

func TestEASMCompanyBranchEmptyOwnerBindingUsesTheLegalParentDiscoveredBeforeReturn(t *testing.T) {
	for _, test := range []struct{ name, state, bound, owner string }{
		{"late legal parent", easmCompanyState, "", "company:fofa-org:different-system-parent-id"},
		{"ambiguous late parent", strings.Replace(easmCompanyState, `"nodes":[`, `"nodes":[{"id":"other-parent","kind":"company","label":"Fixture Parent"},`, 1), "", ""},
		{"lost frozen owner", easmCompanyState, "removed-owner", ""},
		{"frozen owner retains identity", strings.Replace(strings.Replace(easmCompanyState, `"label":"Fixture Parent"`, `"label":"Observed parent alias"`, 1), `"nodes":[`, `"nodes":[{"id":"different-parent","kind":"company","label":"Fixture Parent"},`, 1), "company:fofa-org:different-system-parent-id", "company:fofa-org:different-system-parent-id"},
		{"brand root is not a legal owner", `{"nodes":[{"id":"brand","kind":"root","label":"Fixture Parent"}],"edges":[]}`, "", ""},
		{"identified legal root", `{"nodes":[{"id":"company:registry:parent","kind":"root","label":"Fixture Parent"}],"edges":[]}`, "", "company:registry:parent"},
	} {
		t.Run(test.name, func(t *testing.T) {
			binding := easmBranchBinding{ParentID: test.bound, Keyword: "Fixture Parent"}
			updated := applyEASMBranchPatch(t, test.state, binding, easmOrganizationResult(easmBranchReply))
			var before, after struct {
				Edges []struct {
					Source, Target string
					Confirmed      bool
				}
			}
			_ = json.Unmarshal([]byte(test.state), &before)
			_ = json.Unmarshal(updated, &after)
			added := after.Edges[len(before.Edges):]
			if test.owner == "" && len(added) != 0 {
				t.Fatalf("a missing or ambiguous owner gained branch edges: %s", updated)
			}
			if test.owner != "" {
				if len(added) != 2 {
					t.Fatalf("late legal evidence did not gain both branches: %s", updated)
				}
				for _, edge := range added {
					if edge.Source != test.owner || !edge.Confirmed {
						t.Fatalf("branch owner was rebound incorrectly: %+v", edge)
					}
				}
			}
		})
	}
}

func TestEASMCompanyBranchOwnerVerificationDoesNotDuplicateSourceFacts(t *testing.T) {
	binding := easmBranchBinding{Keyword: "Fixture Parent"}
	unknown := applyEASMBranchPatch(t, `{"nodes":[{"id":"brand","kind":"root","label":"Fixture Brand"}],"edges":[]}`, binding, easmOrganizationResult(easmBranchReply))
	var graph map[string]json.RawMessage
	_ = json.Unmarshal(unknown, &graph)
	var nodes []json.RawMessage
	_ = json.Unmarshal(graph["nodes"], &nodes)
	nodes = append(nodes, json.RawMessage(`{"id":"verified-parent","kind":"company","label":"Fixture Parent"}`))
	graph["nodes"], _ = json.Marshal(nodes)
	knownState, _ := json.Marshal(graph)
	binding.ParentID = "verified-parent"
	known := applyEASMBranchPatch(t, string(knownState), binding, easmOrganizationResult(easmBranchReply))
	var before, after struct {
		Nodes []struct {
			ID     string
			Detail struct{ Reason string }
		}
		Edges []struct {
			Source, Target string
			Confirmed      bool
		}
	}
	_ = json.Unmarshal(unknown, &before)
	_ = json.Unmarshal(known, &after)
	if len(after.Edges) != 2 {
		t.Fatalf("verified parent did not gain its branches: %s", known)
	}
	for _, edge := range after.Edges {
		if edge.Source != binding.ParentID || !edge.Confirmed {
			t.Fatalf("owner verification attached a branch to the brand: %+v", edge)
		}
	}
	for i := 1; i < len(before.Nodes); i++ {
		if before.Nodes[i].ID != after.Nodes[i].ID || before.Nodes[i].Detail.Reason != after.Nodes[i].Detail.Reason || strings.Contains(after.Nodes[i].Detail.Reason, "归属主体待核验") {
			t.Fatalf("owner verification rewrote or duplicated source facts: %+v", after.Nodes[i])
		}
	}
}

func TestEASMCompanyBranchBatchesRetainSourceIdentitiesAndPriorDecisions(t *testing.T) {
	binding := easmBranchBinding{ParentID: "company:fofa-org:different-system-parent-id", Keyword: "Fixture Parent"}
	state := strings.Replace(easmCompanyState, `"edges":[`, `"edges":[{"source":"company:fofa-org:different-system-parent-id","target":"legacy-child","confirmed":false},{"source":"company:registry:absent","target":"legacy-child","confirmed":true},`, 1)
	// A reverse edge is valid history, while the absent endpoint above is not.
	if patch := easmCompanyBranchPatch([]byte(state), binding, easmOrganizationResult(easmBranchReply)); patch != nil {
		t.Fatal("a malformed graph accepted new branch evidence")
	}
	state = strings.Replace(easmCompanyState, `"edges":[`, `"edges":[{"source":"company:fofa-org:different-system-parent-id","target":"legacy-child","confirmed":false,"future_evidence":{"keep":true}},{"source":"legacy-child","target":"company:fofa-org:different-system-parent-id","confirmed":true},`, 1)
	first := applyEASMBranchPatch(t, state, binding, easmOrganizationResult(easmBranchReply))
	structured := map[string]any{"items": []any{map[string]any{"id": json.Number("9007199254740993"), "name": "Fixture New Branch", "reg_status": "Active"}}}
	second := applyEASMBranchPatch(t, string(first), binding, &mcp.CallToolResult{StructuredContent: structured})
	var before, after struct {
		Nodes []json.RawMessage
		Edges []json.RawMessage
	}
	_ = json.Unmarshal(first, &before)
	_ = json.Unmarshal(second, &after)
	if len(after.Nodes) != len(before.Nodes)+1 || len(after.Edges) != len(before.Edges)+1 || !reflect.DeepEqual(before.Edges, after.Edges[:len(before.Edges)]) {
		t.Fatalf("a later branch batch replaced prior evidence: %s", second)
	}
	if !strings.Contains(string(second), `company:registry:9007199254740993`) {
		t.Fatal("a numeric registry ID lost precision")
	}
	var confirmed map[string]json.RawMessage
	_ = json.Unmarshal(after.Edges[0], &confirmed)
	if string(confirmed["confirmed"]) != "true" || string(confirmed["future_evidence"]) != `{"keep":true}` {
		t.Fatalf("direct branch evidence did not confirm its edge while retaining prior evidence: %s", after.Edges[0])
	}
}

func TestEASMCompanyBranchesKeepDistinctSameNamedSourceIDs(t *testing.T) {
	binding := easmBranchBinding{ParentID: "company:fofa-org:different-system-parent-id", Keyword: "Fixture Parent"}
	for _, body := range []string{
		`{"items":[{"id":"one","name":"Fixture Subsidiary"},{"id":"two","name":"Fixture Subsidiary"}]}`,
		`{"items":[{"id":123,"name":"Fixture Subsidiary"},{"id":123,"name":"Returned branch alias"}]}`,
	} {
		updated := applyEASMBranchPatch(t, easmCompanyState, binding, easmOrganizationResult(body))
		var graph struct{ Nodes []struct{ ID string } }
		_ = json.Unmarshal(updated, &graph)
		if strings.Contains(body, `"two"`) && len(graph.Nodes) != 6 {
			t.Fatalf("same names merged different registry identities: %s", updated)
		}
		if !strings.Contains(body, `"two"`) && !strings.Contains(string(updated), "Returned branch alias") {
			t.Fatal("an exact source identity lost its returned alias")
		}
	}
	state := strings.Replace(easmCompanyState, `"id":"legacy-child"`, `"id":"company:registry:different"`, 1)
	state = strings.ReplaceAll(state, `"target":"legacy-child"`, `"target":"company:registry:different"`)
	state = strings.ReplaceAll(state, `"source":"legacy-child"`, `"source":"company:registry:different"`)
	updated := applyEASMBranchPatch(t, state, binding, easmOrganizationResult(`{"items":[{"id":"new","name":"Fixture Subsidiary"}]}`))
	if !strings.Contains(string(updated), "company:registry:new") {
		t.Fatal("a different known registry ID was replaced by a matching name")
	}
}

func TestEASMCompanyBranchesReuseExactSourceIdentityDespiteLabelChanges(t *testing.T) {
	binding := easmBranchBinding{ParentID: "company:fofa-org:different-system-parent-id", Keyword: "Fixture Parent"}
	state := strings.Replace(easmCompanyState, `"id":"legacy-child"`, `"id":"company:registry:123"`, 1)
	state = strings.ReplaceAll(state, `"target":"legacy-child"`, `"target":"company:registry:123"`)
	state = strings.ReplaceAll(state, `"source":"legacy-child"`, `"source":"company:registry:123"`)
	updated := applyEASMBranchPatch(t, state, binding, easmOrganizationResult(`{"items":[{"id":123,"name":"Current source branch name","reg_status":"Active"}]}`))
	var graph struct{ Nodes []json.RawMessage }
	_ = json.Unmarshal(updated, &graph)
	if len(graph.Nodes) != 4 || !strings.Contains(string(graph.Nodes[2]), "Current source branch name") || !strings.Contains(string(graph.Nodes[2]), `"label":"Fixture Subsidiary"`) {
		t.Fatalf("a source identity was duplicated or relabeled: %s", updated)
	}
}

func TestEASMCompanyBranchErrorsPreserveTheAcceptedGraph(t *testing.T) {
	binding := easmBranchBinding{ParentID: "company:fofa-org:different-system-parent-id", Keyword: "Fixture Parent"}
	for _, test := range []struct{ state, body string }{
		{easmCompanyState, `{`}, {easmCompanyState, `{"error":true}`}, {easmCompanyState, `{"isError":true}`}, {easmCompanyState, `{"errmsg":"failed"}`}, {easmCompanyState, `{}`},
		{`{`, easmBranchReply}, {`{"nodes":[],"edges":[]}`, easmBranchReply}, {`{"nodes":null,"edges":[]}`, easmBranchReply}, {`{"nodes":[],"edges":null}`, easmBranchReply}, {`{"nodes":[null],"edges":[]}`, easmBranchReply},
		{strings.Replace(easmCompanyState, `"id":"legacy-child"`, `"id":"brand"`, 1), easmBranchReply},
		{strings.Replace(easmCompanyState, `"kind":"company"`, `"kind":"root"`, 1), easmBranchReply},
		{strings.Replace(easmCompanyState, `"label":"Fixture Brand"`, `"label":" "`, 1), easmBranchReply},
		{strings.Replace(easmCompanyState, `"edges":[`, `"edges":[7,`, 1), easmBranchReply},
		{strings.Replace(easmCompanyState, `"detail":{"reason":"Retain domain ownership evidence","features":[{"kind":"domain","value":"example.test","node_id":"domain"}]}`, `"detail":7`, 1), easmBranchReply},
		{strings.Replace(easmCompanyState, `"reason":"Retain domain ownership evidence"`, `"reason":7`, 1), easmBranchReply},
	} {
		if patch := easmCompanyBranchPatch([]byte(test.state), binding, easmOrganizationResult(test.body)); patch != nil {
			t.Fatalf("invalid source changed branch graph: %s", patch)
		}
	}
	for _, result := range []*mcp.CallToolResult{nil, {IsError: true}, {StructuredContent: func() {}}} {
		if patch := easmCompanyBranchPatch([]byte(easmCompanyState), binding, result); patch != nil {
			t.Fatal("failed branch source changed graph")
		}
	}
	if patch := easmCompanyBranchPatch([]byte(easmCompanyState), easmBranchBinding{}, easmOrganizationResult(easmBranchReply)); patch != nil {
		t.Fatal("missing request ownership changed graph")
	}
	if patch := easmCompanyBranchPatch([]byte(easmCompanyState), binding, easmOrganizationResult(`{"items":[{"name":" "},{"id":"different-system-parent-id","name":""}]}`)); patch != nil {
		t.Fatal("empty branch records became companies")
	}
}

func TestEASMCompanyBranchLargeBatchesStayWithinThePatchOperationLimit(t *testing.T) {
	items := make([]any, 300)
	for i := range items {
		items[i] = map[string]any{"name": fmt.Sprintf("Fixture branch %d", i)}
	}
	result := &mcp.CallToolResult{StructuredContent: map[string]any{"items": items}}
	updated := applyEASMBranchPatch(t, easmCompanyState, easmBranchBinding{ParentID: "company:fofa-org:different-system-parent-id", Keyword: "Fixture Parent"}, result)
	var graph struct {
		Nodes []json.RawMessage
		Edges []json.RawMessage
	}
	_ = json.Unmarshal(updated, &graph)
	if len(graph.Nodes) != 304 || len(graph.Edges) != 303 {
		t.Fatalf("large branch batch lost observations: nodes=%d edges=%d", len(graph.Nodes), len(graph.Edges))
	}
}

func TestEASMCompanyBranchCallRequiresAnOwnedRequest(t *testing.T) {
	e := newTestEngine(t)
	for _, args := range []string{`{`, `{}`, `{"keyword":" "}`, `{"keyword":"Fixture Parent"}`} {
		if complete := e.beginEASMBranchCall(t.Context(), mcpclient.ToolCall{Name: "mcp_company_branch", Arguments: json.RawMessage(args)}); complete != nil {
			t.Fatal("unowned branch request acquired a callback")
		}
	}
	ctx := withHistoryReadOwner(context.Background(), "thread", "turn")
	if complete := e.beginEASMBranchCall(ctx, mcpclient.ToolCall{Name: "mcp_company_branch", Arguments: json.RawMessage(`{"keyword":"Fixture Parent"}`)}); complete != nil {
		t.Fatal("missing trusted caller acquired a branch callback")
	}
}
