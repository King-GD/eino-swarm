package engine

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/LubyRuffy/eino-swarm/internal/taskpanel"
	jsonpatch "github.com/evanphx/json-patch"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const easmGraphState = `{"schema_version":1,"title":"portrait","nodes":[{"id":"chosen-root","kind":"root","label":"Example Company","detail":{"description":"Retain this evidence"}},{"id":"old-domain","kind":"domain","label":"EXISTING.test","ip_count":7}],"edges":[],"summary":{"clues":2},"heat":[],"components":[]}`

func easmOrganizationResult(body string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: body}}}
}

func TestEASMOrganizationLookupDrawsCandidatesBeforeTheirRelationshipIsConfirmed(t *testing.T) {
	state := strings.Replace(easmGraphState, `"label":"Example Company"`, `"label":"Example"`, 1)
	result := easmOrganizationResult(`{"results":[{"id":"north","name":"Example North Company","domains":[{"domain":"existing.test"},{"domain":"shared.test"}]},{"id":"south","name":"Example South Company","domains":[{"domain":"shared.test"},{"domain":"south.test"}]}]}`)
	patch := easmOrganizationPatch([]byte(state), result)
	operations, err := jsonpatch.DecodePatch(patch)
	if err != nil || len(operations) == 0 {
		t.Fatalf("known candidate organizations were withheld: patch=%s error=%v", patch, err)
	}
	updated, err := operations.Apply([]byte(state))
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
	if err := json.Unmarshal(updated, &graph); err != nil {
		t.Fatal(err)
	}
	if len(graph.Nodes) != 6 || len(graph.Edges) != 6 || graph.Nodes[1].ID != "old-domain" {
		t.Fatalf("candidate graph lost identities or duplicated a shared domain: %s", updated)
	}
	companies := make(map[string]string)
	for _, node := range graph.Nodes {
		if node.Kind == "company" {
			if node.Detail.Reason == "" {
				t.Error("candidate organization has no explanation of the lookup evidence")
			}
			companies[node.ID] = node.Label
		}
	}
	if len(companies) != 2 {
		t.Fatalf("different legal entities were merged: %s", updated)
	}
	sharedOwners := 0
	for _, edge := range graph.Edges {
		if edge.Source == "chosen-root" {
			if edge.Confirmed || companies[edge.Target] == "" {
				t.Fatalf("candidate relationship was presented as target ownership: %+v", edge)
			}
		} else if !edge.Confirmed || companies[edge.Source] == "" {
			t.Fatalf("observed organization/domain association was lost: %+v", edge)
		}
		if edge.Target == "domain:shared.test" {
			sharedOwners++
		}
	}
	if sharedOwners != 2 {
		t.Fatalf("shared domain retained %d organization associations, want 2", sharedOwners)
	}
	if next := easmOrganizationPatch(updated, result); next != nil {
		t.Fatalf("repeated candidate lookup changed identities or duplicated evidence: %s", next)
	}
}

func TestEASMOrganizationDomainsAppearWithoutAnotherModelPublication(t *testing.T) {
	result := easmOrganizationResult(`{"results":[{"id":"match","name":"Example Company","domains":[{"domain":"existing.test"},{"domain":"NEW.test."},{"domain":"new.test"},{"domain":"*.invalid.test"}]}]}`)
	patch := easmOrganizationPatch(json.RawMessage(easmGraphState), result)
	operations, err := jsonpatch.DecodePatch(patch)
	if err != nil || len(operations) != 3 {
		t.Fatalf("patch=%s error=%v", patch, err)
	}
	updated, err := operations.Apply([]byte(easmGraphState))
	if err != nil {
		t.Fatal(err)
	}
	var before, after map[string]any
	_ = json.Unmarshal([]byte(easmGraphState), &before)
	_ = json.Unmarshal(updated, &after)
	oldNodes := before["nodes"].([]any)
	nodes := after["nodes"].([]any)
	if len(nodes) != 3 || !reflect.DeepEqual(oldNodes, nodes[:2]) {
		t.Fatalf("existing nodes changed or duplicates appeared: %s", updated)
	}
	if !reflect.DeepEqual(nodes[2], map[string]any{"id": "domain:new.test", "kind": "domain", "label": "new.test"}) {
		t.Fatalf("new domain=%v", nodes[2])
	}
	for _, key := range []string{"title", "schema_version", "summary", "heat", "components"} {
		if !reflect.DeepEqual(before[key], after[key]) {
			t.Fatalf("unrelated field %s changed", key)
		}
	}
	edges := after["edges"].([]any)
	for i, id := range []string{"old-domain", "domain:new.test"} {
		if !reflect.DeepEqual(edges[i], map[string]any{"source": "chosen-root", "target": id, "confirmed": true}) {
			t.Fatalf("edge=%v", edges[i])
		}
	}
	if next := easmOrganizationPatch(updated, result); next != nil {
		t.Fatalf("repeated observations added duplicates: %s", next)
	}
}

func TestEASMOrganizationKeepsExistingDecisionsAndAddsSourceDirection(t *testing.T) {
	result := easmOrganizationResult(`{"results":[{"name":"Example Company","domains":[{"domain":"existing.test"}]}]}`)
	for _, edge := range []string{
		`{"source":"chosen-root","target":"old-domain","confirmed":false}`,
		`{"source":"old-domain","target":"chosen-root","confirmed":true}`,
	} {
		state := strings.Replace(easmGraphState, `"edges":[]`, `"edges":[`+edge+`]`, 1)
		patch := easmOrganizationPatch([]byte(state), result)
		if strings.Contains(edge, `"confirmed":false`) {
			if patch != nil {
				t.Fatalf("existing decision changed: %s", patch)
			}
			continue
		}
		operations, err := jsonpatch.DecodePatch(patch)
		if err != nil || len(operations) != 1 {
			t.Fatalf("source direction missing: patch=%s error=%v", patch, err)
		}
		updated, err := operations.Apply([]byte(state))
		if err != nil {
			t.Fatal(err)
		}
		var graph struct {
			Edges []struct {
				Source, Target string
				Confirmed      bool
			}
		}
		_ = json.Unmarshal(updated, &graph)
		if len(graph.Edges) != 2 || graph.Edges[0].Source != "old-domain" || !graph.Edges[0].Confirmed || graph.Edges[1].Source != "chosen-root" || graph.Edges[1].Target != "old-domain" || !graph.Edges[1].Confirmed {
			t.Fatalf("source direction rewrote an earlier relation: %s", updated)
		}
	}
}

func TestEASMOrganizationRefusesUnknownOrAmbiguousEvidence(t *testing.T) {
	valid := `{"results":[{"name":"Example Company","domains":[{"domain":"new.test"}]}]}`
	for _, test := range []struct {
		name   string
		state  string
		result *mcp.CallToolResult
	}{
		{"no result", easmGraphState, nil},
		{"tool error", easmGraphState, &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: valid}}}},
		{"invalid state", "{", easmOrganizationResult(valid)},
		{"missing nodes", `{"edges":[]}`, easmOrganizationResult(valid)},
		{"missing edges", `{"nodes":[]}`, easmOrganizationResult(valid)},
		{"no root", `{"nodes":[],"edges":[]}`, easmOrganizationResult(valid)},
		{"empty node ID", strings.Replace(easmGraphState, "chosen-root", "", 1), easmOrganizationResult(valid)},
		{"duplicate node ID", strings.Replace(easmGraphState, "old-domain", "chosen-root", 1), easmOrganizationResult(valid)},
		{"empty root label", strings.Replace(easmGraphState, "Example Company", "", 1), easmOrganizationResult(valid)},
		{"two roots", strings.Replace(easmGraphState, `"kind":"domain"`, `"kind":"root"`, 1), easmOrganizationResult(valid)},
		{"no content", easmGraphState, &mcp.CallToolResult{}},
		{"multiple texts", easmGraphState, &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: valid}, &mcp.TextContent{Text: valid}}}},
		{"image content", easmGraphState, &mcp.CallToolResult{Content: []mcp.Content{&mcp.ImageContent{MIMEType: "image/png"}}}},
		{"nil text", easmGraphState, &mcp.CallToolResult{Content: []mcp.Content{(*mcp.TextContent)(nil)}}},
		{"malformed JSON", easmGraphState, easmOrganizationResult("{")},
		{"wrong domains shape", easmGraphState, easmOrganizationResult(`{"results":[{"name":"Example Company","domains":"new.test"}]}`)},
		{"embedded error", easmGraphState, easmOrganizationResult(`{"error":true,"results":[{"name":"Example Company","domains":[{"domain":"new.test"}]}]}`)},
		{"embedded error object", easmGraphState, easmOrganizationResult(`{"error":{"code":"timeout"}}`)},
		{"embedded IsError", easmGraphState, easmOrganizationResult(`{"isError":true}`)},
		{"error message", easmGraphState, easmOrganizationResult(`{"errmsg":"missing keyword"}`)},
		{"empty organization name", easmGraphState, easmOrganizationResult(`{"results":[{"id":"empty","name":"  ","domains":[{"domain":"new.test"}]}]}`)},
		{"invalid organization ID", easmGraphState, easmOrganizationResult(`{"results":[{"id":true,"name":"Example Company","domains":[{"domain":"new.test"}]}]}`)},
		{"company ID collision", strings.Replace(easmGraphState, "old-domain", "company:fofa-org:candidate", 1), easmOrganizationResult(`{"results":[{"id":"candidate","name":"Related Company","domains":[{"domain":"new.test"}]}]}`)},
		{"no domains", easmGraphState, easmOrganizationResult(`{"results":[{"name":"Example Company","domains":[]}]}`)},
		{"ID collision", strings.Replace(easmGraphState, "old-domain", "domain:new.test", 1), easmOrganizationResult(valid)},
		{"invalid structured content does not fall back", easmGraphState, &mcp.CallToolResult{StructuredContent: func() {}, Content: []mcp.Content{&mcp.TextContent{Text: valid}}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if patch := easmOrganizationPatch([]byte(test.state), test.result); patch != nil {
				t.Fatalf("unreliable evidence produced a patch: %s", patch)
			}
		})
	}
}

func TestEASMOrganizationIdentityRetainsExactSourceIDs(t *testing.T) {
	for _, test := range []struct {
		id, name, want string
	}{
		{`"123"`, "Company", "company:fofa-org:123"},
		{`123`, "Company", "company:fofa-org:123"},
		{`1912436834906935296`, "Company", "company:fofa-org:1912436834906935296"},
		{`" 123 "`, "Company", "company:fofa-org: 123 "},
		{"", "Company", "company:fofa-org-name:Company"},
		{`null`, "Company", "company:fofa-org-name:Company"},
		{`" "`, "Company", ""},
		{`false`, "Company", ""},
		{`{}`, "Company", ""},
		{`-1`, "Company", ""},
		{`1.5`, "Company", ""},
	} {
		t.Run(test.id, func(t *testing.T) {
			if got := easmOrganizationIdentity([]byte(test.id), test.name); got != test.want {
				t.Fatalf("identity=%q, want %q", got, test.want)
			}
		})
	}
}

func TestEASMOrganizationLookupKeepsAmbiguousNamesAsSeparateIdentifiedCompanies(t *testing.T) {
	result := easmOrganizationResult(`{"results":[{"id":"north","name":"Example Company","domains":[{"domain":"shared.test"}]},{"id":"south","name":"Example Company","domains":[{"domain":"shared.test"}]}]}`)
	patch, err := jsonpatch.DecodePatch(easmOrganizationPatch([]byte(easmGraphState), result))
	if err != nil {
		t.Fatal(err)
	}
	updated, err := patch.Apply([]byte(easmGraphState))
	if err != nil {
		t.Fatal(err)
	}
	var graph struct {
		Nodes []struct{ ID, Kind string }
		Edges []struct {
			Source, Target string
			Confirmed      bool
		}
	}
	if err := json.Unmarshal(updated, &graph); err != nil || len(graph.Nodes) != 5 || len(graph.Edges) != 4 {
		t.Fatalf("same-name source identities were merged: %s error=%v", updated, err)
	}
	for _, edge := range graph.Edges {
		if edge.Source == "chosen-root" && edge.Confirmed {
			t.Fatalf("same-name entities were attributed to the root: %+v", edge)
		}
	}
}

func TestEASMOrganizationLookupReusesAnIdentifiedCompanyAndItsRelationships(t *testing.T) {
	var graph map[string]any
	_ = json.Unmarshal([]byte(easmGraphState), &graph)
	graph["nodes"] = append(graph["nodes"].([]any), map[string]any{"id": "company:fofa-org:retained", "kind": "company", "label": "Previously observed legal name", "detail": map[string]string{"reason": "Prior evidence"}})
	graph["edges"] = []any{
		map[string]any{"source": "company:fofa-org:retained", "target": "chosen-root", "confirmed": true},
		map[string]any{"source": "old-domain", "target": "company:fofa-org:retained", "confirmed": false},
	}
	state, _ := json.Marshal(graph)
	result := easmOrganizationResult(`{"results":[{"id":"retained","name":"Current legal name","domains":[{"domain":"existing.test"}]}]}`)
	patch, err := jsonpatch.DecodePatch(easmOrganizationPatch(state, result))
	if err != nil || len(patch) != 1 {
		t.Fatalf("expected the direct source edge: %v", err)
	}
	updated, err := patch.Apply(state)
	if err != nil {
		t.Fatal(err)
	}
	var before, after map[string]any
	_ = json.Unmarshal(state, &before)
	_ = json.Unmarshal(updated, &after)
	if !reflect.DeepEqual(before["nodes"], after["nodes"]) || !reflect.DeepEqual(before["edges"], after["edges"].([]any)[:2]) {
		t.Fatalf("adding source direction changed prior identity or relationship: %s", updated)
	}
	if repeat := easmOrganizationPatch(updated, result); repeat != nil {
		t.Fatalf("source edge replay changed graph: %s", repeat)
	}
	result = easmOrganizationResult(`{"results":[{"name":"example company","domains":[]}]}`)
	if patch := easmOrganizationPatch(state, result); patch == nil {
		t.Fatal("a reported candidate without domains was withheld")
	}
}

func TestEASMOrganizationLookupKeepsLegacyCompanyIDsWithoutMergingKnownDifferentIdentities(t *testing.T) {
	for _, companyID := range []string{"legacy-company", "company:fofa-org:different"} {
		t.Run(companyID, func(t *testing.T) {
			var graph map[string]any
			_ = json.Unmarshal([]byte(easmGraphState), &graph)
			graph["nodes"] = append(graph["nodes"].([]any), map[string]any{"id": companyID, "kind": "company", "label": "Reported Legal Entity", "detail": map[string]string{"reason": "Previously observed evidence"}})
			state, _ := json.Marshal(graph)
			result := easmOrganizationResult(`{"results":[{"id":"reported","name":"Reported Legal Entity","domains":[{"domain":"existing.test"}]}]}`)
			patch, err := jsonpatch.DecodePatch(easmOrganizationPatch(state, result))
			if err != nil {
				t.Fatal(err)
			}
			updated, err := patch.Apply(state)
			if err != nil {
				t.Fatal(err)
			}
			var after struct {
				Nodes []struct{ ID, Kind string }
				Edges []struct{ Source, Target string }
			}
			if err := json.Unmarshal(updated, &after); err != nil {
				t.Fatal(err)
			}
			wantOwner, wantNodes := companyID, 3
			if companyID == "company:fofa-org:different" {
				wantOwner, wantNodes = "company:fofa-org:reported", 4
			}
			if len(after.Nodes) != wantNodes || len(after.Edges) != 2 || after.Edges[0].Target != wantOwner || after.Edges[1].Source != wantOwner || after.Edges[1].Target != "old-domain" {
				t.Fatalf("legacy reuse lost or conflated organization identity: %s", updated)
			}
			if next := easmOrganizationPatch(updated, result); next != nil {
				t.Fatalf("a repeated lookup duplicated the retained entity: %s", next)
			}
		})
	}
}

func TestEASMOrganizationLookupKeepsRepeatedSourceRecords(t *testing.T) {
	result := easmOrganizationResult(`{"results":[{"id":"same","name":"Different Legal Entity","domains":[{"domain":"first.test"}]},{"id":"same","name":"Different Legal Entity","domains":[{"domain":"second.test"}]}]}`)
	if patch := easmOrganizationPatch([]byte(easmGraphState), result); patch == nil {
		t.Fatal("consistent repeated source identity lost its observations")
	}
}

func TestEASMOrganizationUsesStructuredContent(t *testing.T) {
	result := &mcp.CallToolResult{StructuredContent: map[string]any{"error": false, "results": []any{map[string]any{"name": "Example Company", "domains": []any{map[string]any{"domain": "new.test"}}}}}}
	if patch := easmOrganizationPatch([]byte(easmGraphState), result); patch == nil {
		t.Fatal("structured observations did not reach the graph")
	}
}

func TestEASMOrganizationRetainsRepeatedNamesAndLegacyDomainNodes(t *testing.T) {
	state := strings.Replace(easmGraphState, `"nodes":[`, `"nodes":[{"id":"duplicate-domain","kind":"domain","label":"existing.TEST."},`, 1)
	result := easmOrganizationResult(`{"results":[{"name":"Example Company","domains":[{"domain":"existing.test"}]},{"name":"Example Company","domains":[{"domain":"new.test"}]}]}`)
	patch, err := jsonpatch.DecodePatch(easmOrganizationPatch([]byte(state), result))
	if err != nil {
		t.Fatal(err)
	}
	updated, err := patch.Apply([]byte(state))
	if err != nil {
		t.Fatal(err)
	}
	var graph struct {
		Nodes []struct{ ID, Kind string }
		Edges []struct {
			Source, Target string
			Confirmed      bool
		}
	}
	_ = json.Unmarshal(updated, &graph)
	if len(graph.Nodes) != 4 || len(graph.Edges) != 3 {
		t.Fatalf("source observations were lost or duplicated: %s", updated)
	}
	for _, target := range []string{"old-domain", "duplicate-domain", "domain:new.test"} {
		found := false
		for _, edge := range graph.Edges {
			found = found || edge.Source == "chosen-root" && edge.Target == target && edge.Confirmed
		}
		if !found {
			t.Fatalf("domain source association missing for %s", target)
		}
	}
	if repeat := easmOrganizationPatch(updated, result); repeat != nil {
		t.Fatalf("repeated named source changed graph: %s", repeat)
	}
}

func TestEASMOrganizationNameConflictKeepsSourceDomainEvidence(t *testing.T) {
	result := easmOrganizationResult(`{"results":[{"id":"conflict","name":"Example Company","domains":[{"domain":"first.test"}]},{"id":"conflict","name":"Different Legal Entity","domains":[{"domain":"second.test"}]}]}`)
	patch, err := jsonpatch.DecodePatch(easmOrganizationPatch([]byte(easmGraphState), result))
	if err != nil {
		t.Fatal(err)
	}
	updated, err := patch.Apply([]byte(easmGraphState))
	if err != nil {
		t.Fatal(err)
	}
	var graph struct {
		Nodes []struct{ ID, Kind string }
		Edges []struct {
			Source, Target string
			Confirmed      bool
		}
	}
	_ = json.Unmarshal(updated, &graph)
	if len(graph.Nodes) != 5 || len(graph.Edges) != 3 {
		t.Fatalf("conflicting names lost source-backed domains: %s", updated)
	}
	for _, edge := range graph.Edges {
		if edge.Source == "chosen-root" && (edge.Target != "company:fofa-org:conflict" || edge.Confirmed) {
			t.Fatalf("name conflict identified the root as the source company: %+v", edge)
		}
		if edge.Source != "chosen-root" && edge.Source != "company:fofa-org:conflict" {
			t.Fatalf("domain attached to an unsupported source: %+v", edge)
		}
	}
	if repeated := easmOrganizationPatch(updated, result); repeated != nil {
		t.Fatalf("conflicting repeated observations changed graph: %s", repeated)
	}
}

func TestEASMOrganizationNameConflictDoesNotChooseBetweenExistingCompanies(t *testing.T) {
	state := strings.Replace(easmGraphState, `"nodes":[`, `"nodes":[{"id":"old-one","kind":"company","label":"Example Company"},{"id":"old-two","kind":"company","label":"Different Legal Entity"},`, 1)
	result := easmOrganizationResult(`{"results":[{"id":"conflict","name":"Example Company","domains":[{"domain":"first.test"}]},{"id":"conflict","name":"Different Legal Entity","domains":[{"domain":"second.test"}]}]}`)
	patch, err := jsonpatch.DecodePatch(easmOrganizationPatch([]byte(state), result))
	if err != nil {
		t.Fatal(err)
	}
	updated, err := patch.Apply([]byte(state))
	if err != nil {
		t.Fatal(err)
	}
	var graph struct {
		Edges []struct {
			Source, Target string
			Confirmed      bool
		}
	}
	_ = json.Unmarshal(updated, &graph)
	if len(graph.Edges) != 3 {
		t.Fatalf("source identity was split by name: %s", updated)
	}
	for _, edge := range graph.Edges {
		if edge.Source == "old-one" || edge.Source == "old-two" {
			t.Fatalf("a conflicting name selected an existing entity: %+v", edge)
		}
	}
}

func TestEASMOrganizationKnownSameNameIdentitiesDoNotBecomeTheRoot(t *testing.T) {
	state := strings.Replace(easmGraphState, `"nodes":[`, `"nodes":[{"id":"company:fofa-org:old","kind":"company","label":"Example Company"},`, 1)
	result := easmOrganizationResult(`{"results":[{"id":"new","name":"Example Company","domains":[{"domain":"new.test"}]}]}`)
	patch, err := jsonpatch.DecodePatch(easmOrganizationPatch([]byte(state), result))
	if err != nil {
		t.Fatal(err)
	}
	updated, err := patch.Apply([]byte(state))
	if err != nil {
		t.Fatal(err)
	}
	var graph struct {
		Edges []struct {
			Source, Target string
			Confirmed      bool
		}
	}
	_ = json.Unmarshal(updated, &graph)
	if len(graph.Edges) != 2 || graph.Edges[0].Target != "company:fofa-org:new" || graph.Edges[0].Confirmed || graph.Edges[1].Source != "company:fofa-org:new" || graph.Edges[1].Target != "domain:new.test" {
		t.Fatalf("an observed same-name identity was collapsed into the target: %s", updated)
	}
}

func TestEASMOrganizationLargeBatchesRetainAllExistingGraphFields(t *testing.T) {
	// Larger observations still need to appear when individual appends exceed
	// the protocol operation limit, including graphs whose nodes already exist.
	for _, existingDomains := range []bool{false, true} {
		t.Run(fmt.Sprintf("existing_domains=%t", existingDomains), func(t *testing.T) {
			state := strings.Replace(easmGraphState, `"ip_count":7`, `"ip_count":9007199254740993`, 1)
			state = strings.Replace(state, `"edges":[]`, `"edges":[{"source":"old-domain","target":"chosen-root","confirmed":false}]`, 1)
			var original map[string]json.RawMessage
			_ = json.Unmarshal([]byte(state), &original)
			var originalNodes, originalEdges []json.RawMessage
			_ = json.Unmarshal(original["nodes"], &originalNodes)
			_ = json.Unmarshal(original["edges"], &originalEdges)
			var domains []map[string]string
			for i := 0; i <= taskpanel.MaxPatchOperations; i++ {
				domain := fmt.Sprintf("domain-%d.test", i)
				domains = append(domains, map[string]string{"domain": domain})
				if existingDomains {
					node, _ := json.Marshal(map[string]any{"id": "retained:" + domain, "kind": "domain", "label": domain, "detail": map[string]string{"reason": "Retain this domain evidence"}})
					originalNodes = append(originalNodes, node)
				}
			}
			original["nodes"], _ = json.Marshal(originalNodes)
			before, _ := json.Marshal(original)
			result := &mcp.CallToolResult{StructuredContent: map[string]any{"results": []any{map[string]any{"name": "Example Company", "domains": domains}}}}
			patch := easmOrganizationPatch(before, result)
			operations, err := jsonpatch.DecodePatch(patch)
			if err != nil || len(operations) == 0 || len(operations) > 2 {
				t.Fatalf("large observation was lost or exceeded replacement count: patch=%s error=%v", patch, err)
			}
			for _, operation := range operations {
				path, _ := operation.Path()
				if operation.Kind() != "replace" || (path != "/nodes" && path != "/edges") {
					t.Fatalf("unexpected large-batch operation: %v", operation)
				}
			}
			updated, err := operations.Apply(before)
			if err != nil {
				t.Fatal(err)
			}
			var after map[string]json.RawMessage
			_ = json.Unmarshal(updated, &after)
			var nodes, edges []json.RawMessage
			_ = json.Unmarshal(after["nodes"], &nodes)
			_ = json.Unmarshal(after["edges"], &edges)
			if len(nodes) != 2+len(domains) || len(edges) != len(originalEdges)+len(domains) {
				t.Fatalf("lost or duplicated observations: nodes=%d edges=%d", len(nodes), len(edges))
			}
			if !reflect.DeepEqual(nodes[:len(originalNodes)], originalNodes) || !reflect.DeepEqual(edges[:len(originalEdges)], originalEdges) {
				t.Fatal("replacing collections changed existing raw graph entries")
			}
			for _, key := range []string{"title", "schema_version", "summary", "heat", "components"} {
				if !reflect.DeepEqual(original[key], after[key]) {
					t.Fatalf("replacing collections changed %s", key)
				}
			}
			if repeated := easmOrganizationPatch(updated, result); repeated != nil {
				t.Fatalf("large repeated observations added duplicates: %s", repeated)
			}
		})
	}
}

func TestEASMDomainsUseCanonicalDNSNames(t *testing.T) {
	for _, test := range []struct{ input, want string }{
		{" EXAMPLE.Test. ", "example.test"},
		{"例子.测试", "xn--fsqu00a.xn--0zwm56d"},
		{"xn--fsqu00a.xn--0zwm56d", "xn--fsqu00a.xn--0zwm56d"},
		{"", ""}, {"localhost", ""}, {"127.0.0.1", ""}, {"::1", ""},
		{"https://example.test", ""}, {"example.test/path", ""}, {"example.test:443", ""},
		{"*.example.test", ""}, {"bad_name.test", ""}, {"a..test", ""},
		{"-a.test", ""}, {"a-.test", ""}, {"example.test..", ""},
		{strings.Repeat("a", 64) + ".test", ""},
		{strings.Repeat("a.", 127) + "test", ""},
	} {
		t.Run(test.input, func(t *testing.T) {
			if got := easmDomain(test.input); got != test.want {
				t.Fatalf("domain=%q want=%q", got, test.want)
			}
		})
	}
}
