package engine

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LubyRuffy/eino-swarm/internal/config"
	"github.com/LubyRuffy/eino-swarm/internal/mcpclient"
	"github.com/LubyRuffy/eino-swarm/internal/store"
	"github.com/LubyRuffy/eino-swarm/internal/taskpanel"
	jsonpatch "github.com/evanphx/json-patch"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const easmCertificateState = `{"schema_version":1,"title":"Fixture","nodes":[{"id":"root-local","kind":"root","label":"Fixture"},{"id":"org-local","kind":"feature","featureKind":"org","label":"Fixture Certificate Org"},{"id":"domain-local","kind":"domain","label":"SERVICE.example.test.","ip_count":9007199254740993,"detail":{"reason":"retain domain evidence","features":[{"kind":"icp","value":"Fixture ICP"}]}}],"edges":[{"source":"root-local","target":"org-local","confirmed":false}],"summary":{"fofa_assets":9},"heat":[{"date":"2026-01-01","value":3}],"components":[{"name":"retained component","count":2}]}`

func TestEASMCertificateSearchPreservesExplicitInvalidFlagsAndItsSourcePath(t *testing.T) {
	arguments := json.RawMessage(`{"fields":"domain,cert.subject.cn,cert.subject.org,cert.issuer.org,cert.serial,cert.not_after,cert.is_valid,cert.is_match"}`)
	result := easmOrganizationResult(`{"results":[{"domain":"service.example.test","cert.subject.cn":"*.example.test","cert.subject.org":"Fixture Certificate Org","cert.issuer.org":"Fixture Issuer","cert.serial":"serial-7","cert.not_after":"2030-12-12","cert.is_valid":"false","cert.is_match":false}]}`)
	patch := easmCertificatePatch([]byte(easmCertificateState), arguments, result)
	operations, err := jsonpatch.DecodePatch(patch)
	if err != nil || len(operations) == 0 {
		t.Fatalf("certificate observation was withheld: patch=%s error=%v", patch, err)
	}
	updated, err := operations.Apply([]byte(easmCertificateState))
	if err != nil {
		t.Fatal(err)
	}
	var before, after map[string]json.RawMessage
	_ = json.Unmarshal([]byte(easmCertificateState), &before)
	_ = json.Unmarshal(updated, &after)
	for _, key := range []string{"title", "schema_version", "summary", "heat", "components"} {
		if !jsonpatch.Equal(before[key], after[key]) {
			t.Errorf("certificate observation changed unrelated %s", key)
		}
	}
	var graph struct {
		Nodes []struct {
			ID, Kind, FeatureKind, Label string
			IPCount                      json.RawMessage `json:"ip_count"`
			Detail                       struct {
				Reason, Serial, Issuer string
				NotAfter               string          `json:"not_after"`
				IsValid                json.RawMessage `json:"is_valid"`
				IsMatch                json.RawMessage `json:"is_match"`
				Features               []struct {
					Kind, Value string
					NodeID      string `json:"node_id"`
				}
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
	certificateID := ""
	for _, node := range graph.Nodes {
		if node.Kind == "feature" && node.FeatureKind == "cert" {
			certificateID = node.ID
			if node.ID == "domain-local" || node.Label != "*.example.test" || node.Detail.NotAfter != "2030-12-12" || string(node.Detail.IsValid) != "false" || string(node.Detail.IsMatch) != "false" {
				t.Fatalf("explicit certificate validity was lost or inferred from expiry: %+v", node)
			}
		}
	}
	if certificateID == "" || len(graph.Nodes) != 4 {
		t.Fatalf("certificate/domain identities were confused or duplicated: %s", updated)
	}
	for _, node := range graph.Nodes {
		if node.ID == "domain-local" {
			if string(node.IPCount) != "9007199254740993" || node.Detail.Reason != "retain domain evidence" || len(node.Detail.Features) < 2 || node.Detail.Features[0].Kind != "icp" {
				t.Fatalf("source projection erased existing domain evidence: %+v", node)
			}
			found := false
			for _, feature := range node.Detail.Features {
				found = found || feature.Kind == "cert" && feature.NodeID == certificateID
			}
			if !found {
				t.Error("domain details cannot navigate to the observed certificate")
			}
		}
	}
	certificateDomain, orgCertificate := false, false
	for _, edge := range graph.Edges {
		if edge.Source == certificateID && edge.Target == "domain-local" {
			certificateDomain = !edge.Confirmed
		}
		if edge.Source == "org-local" && edge.Target == certificateID {
			orgCertificate = !edge.Confirmed
		}
		if edge.Source == "root-local" && edge.Target == "domain-local" {
			t.Fatal("certificate evidence fabricated direct target ownership")
		}
	}
	if !certificateDomain || !orgCertificate {
		t.Fatalf("invalid certificate source path was presented as confirmed: %s", updated)
	}
	if repeated := easmCertificatePatch(updated, arguments, result); repeated != nil {
		t.Fatalf("repeated certificate observation changed the graph: %s", repeated)
	}
}

func TestEASMCertificateRowsUseDeclaredColumnsAndExplicitObjectFields(t *testing.T) {
	for _, test := range []struct{ name, arguments, reply string }{
		{"request columns", `{"fields":"domain,cert.subject.cn,cert.subject.org,cert.issuer.org,cert.not_after,cert.is_valid"}`, `{"results":[["service.example.test","*.example.test","Fixture Certificate Org","Fixture Issuer","2030-12-12","false"]]}`},
		{"response columns", `{}`, `{"fields":["cert.is_valid","domain","cert.subject.cn"],"results":[[false,"service.example.test","*.example.test"]]}`},
		{"response CSV columns", `{"fields":"unused"}`, `{"fields":"domain,cert.subject.cn,cert.is_valid","results":[["service.example.test","*.example.test",false]]}`},
		{"nested object", `{}`, `{"results":[{"domain":"service.example.test","cert":{"subject":{"cn":"*.example.test","org":"Fixture Certificate Org"},"is_valid":false,"id":901}}]}`},
		{"fingerprint", `{}`, `{"results":[{"domain":"service.example.test","cert.fingerprint":"fingerprint-one","cert.is_match":"false"}]}`},
		{"sha256", `{}`, `{"results":[{"domain":"service.example.test","cert.sha256":"sha256-one","cert.subject.cn":"*.example.test","cert.is_valid":true,"cert.is_match":"true"}]}`},
		{"alternate serial", `{}`, `{"results":[{"domain":"service.example.test","cert.serial_number":"serial-one","cert.issuer.org":"Fixture Issuer"}]}`},
		{"serial remains local without issuer", `{}`, `{"results":[{"domain":"service.example.test","cert.serial":"serial-one"}]}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := easmOrganizationResult(test.reply)
			patch := easmCertificatePatch([]byte(easmCertificateState), []byte(test.arguments), result)
			operations, err := jsonpatch.DecodePatch(patch)
			if err != nil || len(operations) == 0 {
				t.Fatalf("explicit certificate evidence was lost: %s error=%v", patch, err)
			}
			updated, err := operations.Apply([]byte(easmCertificateState))
			if err != nil {
				t.Fatal(err)
			}
			if repeated := easmCertificatePatch(updated, []byte(test.arguments), result); repeated != nil {
				t.Fatalf("repeat changed a certificate observation: %s", repeated)
			}
		})
	}
}

func TestEASMCertificateObservationsMergeAcrossDomainsOnlyWithCertificateIdentity(t *testing.T) {
	for _, identity := range []string{"", `,"cert.sha256":"same-fingerprint"`} {
		t.Run(identity, func(t *testing.T) {
			result := easmOrganizationResult(`{"results":[{"domain":"service.example.test","cert.subject.cn":"*.example.test","cert.subject.org":"Fixture Certificate Org"` + identity + `},{"domain":"another.example.test","cert.subject.cn":"*.example.test","cert.subject.org":"Fixture Certificate Org"` + identity + `}]}`)
			patch, err := jsonpatch.DecodePatch(easmCertificatePatch([]byte(easmCertificateState), []byte(`{}`), result))
			if err != nil {
				t.Fatal(err)
			}
			updated, err := patch.Apply([]byte(easmCertificateState))
			if err != nil {
				t.Fatal(err)
			}
			var graph struct {
				Nodes []struct{ Kind, FeatureKind string }
			}
			_ = json.Unmarshal(updated, &graph)
			certificates := 0
			for _, node := range graph.Nodes {
				if node.Kind == "feature" && node.FeatureKind == "cert" {
					certificates++
				}
			}
			want := 1
			if identity == "" {
				want = 2
			}
			if certificates != want {
				t.Fatalf("certificate identity crossed an unsupported boundary: certificates=%d want=%d", certificates, want)
			}
		})
	}
}

func TestEASMCertificateSearchReusesLegacyCertificateIDsAndDowngradesInvalidEdges(t *testing.T) {
	var graph map[string]json.RawMessage
	_ = json.Unmarshal([]byte(easmCertificateState), &graph)
	var nodes []json.RawMessage
	_ = json.Unmarshal(graph["nodes"], &nodes)
	nodes = append(nodes, json.RawMessage(`{"id":"cert-local","kind":"feature","featureKind":"cert","label":"*.example.test","detail":{"issuer":"Fixture Issuer","serial":"serial-7","reason":"retain certificate evidence","is_valid":true}}`))
	graph["nodes"], _ = json.Marshal(nodes)
	graph["edges"] = json.RawMessage(`[{"source":"org-local","target":"root-local","confirmed":true},{"source":"cert-local","target":"org-local","confirmed":true},{"source":"domain-local","target":"cert-local","confirmed":true}]`)
	state, _ := json.Marshal(graph)
	result := easmOrganizationResult(`{"results":[{"domain":"service.example.test","cert.subject.cn":"*.example.test","cert.subject.org":"Fixture Certificate Org","cert.issuer.org":"Fixture Issuer","cert.serial":"serial-7","cert.is_valid":false}]}`)
	patch, err := jsonpatch.DecodePatch(easmCertificatePatch(state, []byte(`{}`), result))
	if err != nil {
		t.Fatal(err)
	}
	updated, err := patch.Apply(state)
	if err != nil {
		t.Fatal(err)
	}
	var after struct {
		Nodes []struct {
			ID     string
			Detail struct {
				Reason  string
				IsValid bool `json:"is_valid"`
			}
		}
		Edges []struct {
			Confirmed bool
		}
	}
	_ = json.Unmarshal(updated, &after)
	if len(after.Nodes) != 4 || after.Nodes[3].ID != "cert-local" || after.Nodes[3].Detail.Reason != "retain certificate evidence" || after.Nodes[3].Detail.IsValid || !after.Edges[0].Confirmed || after.Edges[1].Confirmed || after.Edges[2].Confirmed {
		t.Fatalf("legacy certificate evidence or explicit invalidity was lost: %s", updated)
	}
}

func TestEASMCertificateOrganizationAloneDoesNotFabricateACertificate(t *testing.T) {
	result := easmOrganizationResult(`{"results":[{"domain":"another.example.test","cert.subject.org":"New Certificate Organization","cert.is_match":false}]}`)
	patch, err := jsonpatch.DecodePatch(easmCertificatePatch([]byte(easmCertificateState), []byte(`{}`), result))
	if err != nil {
		t.Fatal(err)
	}
	updated, err := patch.Apply([]byte(easmCertificateState))
	if err != nil {
		t.Fatal(err)
	}
	var graph struct {
		Nodes []struct{ Kind, FeatureKind string }
	}
	_ = json.Unmarshal(updated, &graph)
	for _, node := range graph.Nodes {
		if node.Kind == "feature" && node.FeatureKind == "cert" {
			t.Fatal("an organization field became an invented certificate")
		}
	}
}

func TestEASMCertificateMissingFlagsDoNotConfirmAnAlreadyInvalidCertificate(t *testing.T) {
	result := easmOrganizationResult(`{"results":[{"domain":"service.example.test","cert.sha256":"same-source-id","cert.is_valid":false},{"domain":"another.example.test","cert.sha256":"same-source-id"}]}`)
	patch, err := jsonpatch.DecodePatch(easmCertificatePatch([]byte(easmCertificateState), []byte(`{}`), result))
	if err != nil {
		t.Fatal(err)
	}
	updated, err := patch.Apply([]byte(easmCertificateState))
	if err != nil {
		t.Fatal(err)
	}
	var graph struct{ Edges []struct{ Confirmed bool } }
	_ = json.Unmarshal(updated, &graph)
	for _, edge := range graph.Edges {
		if edge.Confirmed {
			t.Fatalf("missing flags overrode an explicit certificate failure: %s", updated)
		}
	}
}

func TestEASMCertificateCanShowAnIPServiceWithoutInventingADomain(t *testing.T) {
	for _, identity := range []string{"", `,"cert.serial":"serial-one"`} {
		result := easmOrganizationResult(`{"results":[{"host":"https://203.0.113.7:443","cert.subject.cn":"*.example.test","cert.subject.org":"Fixture Certificate Org","cert.issuer.org":"Fixture Issuer","cert.is_valid":false` + identity + `}]}`)
		patch, err := jsonpatch.DecodePatch(easmCertificatePatch([]byte(easmCertificateState), []byte(`{}`), result))
		if err != nil || len(patch) == 0 {
			t.Fatalf("IP service certificate evidence was withheld: patch=%v error=%v", patch, err)
		}
		updated, err := patch.Apply([]byte(easmCertificateState))
		if err != nil {
			t.Fatal(err)
		}
		var graph struct {
			Nodes []struct{ ID, Kind, FeatureKind string }
			Edges []struct{ Source, Target string }
		}
		_ = json.Unmarshal(updated, &graph)
		if len(graph.Nodes) != 4 || len(graph.Edges) != 2 {
			t.Fatalf("IP service evidence fabricated a domain or lost its certificate: %s", updated)
		}
		for _, edge := range graph.Edges {
			if edge.Source == "domain-local" || edge.Target == "domain-local" {
				t.Fatal("a service certificate acquired an unrelated domain relationship")
			}
		}
		if repeated := easmCertificatePatch(updated, []byte(`{}`), result); repeated != nil {
			t.Fatalf("repeated service certificate changed its identity: %s", repeated)
		}
	}
}

func TestEASMCertificateSearchRejectsUnknownRowsAndPreservesAcceptedState(t *testing.T) {
	valid := `{"results":[{"domain":"service.example.test","cert.subject.cn":"*.example.test","cert.subject.org":"Fixture Certificate Org"}]}`
	for _, test := range []struct{ name, state, arguments, reply string }{
		{"bad arguments", easmCertificateState, `{`, valid},
		{"bad reply", easmCertificateState, `{}`, `{`},
		{"error flag", easmCertificateState, `{}`, `{"error":true}`},
		{"isError", easmCertificateState, `{}`, `{"isError":true}`},
		{"error message", easmCertificateState, `{}`, `{"errmsg":"failed"}`},
		{"no rows", easmCertificateState, `{}`, `{}`},
		{"tuple without fields", easmCertificateState, `{}`, `{"results":[["service.example.test","*.example.test"]]}`},
		{"tuple with incomplete fields", easmCertificateState, `{"fields":"domain"}`, `{"results":[["service.example.test","*.example.test"]]}`},
		{"duplicate fields", easmCertificateState, `{"fields":"domain,domain"}`, `{"results":[["service.example.test","*.example.test"]]}`},
		{"empty fields", easmCertificateState, `{"fields":"domain,"}`, `{"results":[["service.example.test","*.example.test"]]}`},
		{"invalid fields type", easmCertificateState, `{"fields":7}`, `{"results":[["service.example.test","*.example.test"]]}`},
		{"invalid row", easmCertificateState, `{}`, `{"results":[7]}`},
		{"no certificate evidence", easmCertificateState, `{}`, `{"results":[{"domain":"service.example.test","cert.is_valid":false}]}`},
		{"invalid domain", easmCertificateState, `{}`, `{"results":[{"domain":"127.0.0.1","cert.subject.cn":"Certificate name"}]}`},
		{"bad graph", `{`, `{}`, valid},
		{"no nodes", `{"edges":[]}`, `{}`, valid},
		{"no edges", `{"nodes":[]}`, `{}`, valid},
		{"no root", `{"nodes":[],"edges":[]}`, `{}`, valid},
		{"empty ID", strings.Replace(easmCertificateState, `"id":"root-local"`, `"id":""`, 1), `{}`, valid},
		{"duplicate ID", strings.Replace(easmCertificateState, `"id":"org-local"`, `"id":"root-local"`, 1), `{}`, valid},
		{"empty root label", strings.Replace(easmCertificateState, `"label":"Fixture"`, `"label":""`, 1), `{}`, valid},
		{"duplicate root", strings.Replace(easmCertificateState, `"kind":"domain"`, `"kind":"root"`, 1), `{}`, valid},
		{"bad detail", strings.Replace(easmCertificateState, `"detail":{"reason":"retain domain evidence","features":[{"kind":"icp","value":"Fixture ICP"}]}`, `"detail":7`, 1), `{}`, valid},
		{"bad feature list", strings.Replace(easmCertificateState, `"features":[{"kind":"icp","value":"Fixture ICP"}]`, `"features":7`, 1), `{}`, valid},
		{"missing edge source", strings.Replace(easmCertificateState, `"source":"root-local"`, `"source":"missing"`, 1), `{}`, valid},
		{"missing edge target", strings.Replace(easmCertificateState, `"target":"org-local"`, `"target":"missing"`, 1), `{}`, valid},
	} {
		t.Run(test.name, func(t *testing.T) {
			if patch := easmCertificatePatch([]byte(test.state), []byte(test.arguments), easmOrganizationResult(test.reply)); patch != nil {
				t.Fatalf("unknown certificate data modified the graph: %s", patch)
			}
		})
	}
	if easmCertificatePatch([]byte(easmCertificateState), []byte(`{}`), nil) != nil || easmCertificatePatch([]byte(easmCertificateState), []byte(`{}`), &mcp.CallToolResult{IsError: true}) != nil {
		t.Fatal("a failed remote call modified certificate evidence")
	}
}

func TestEASMCertificateFlagsOnlyRepresentExplicitSourceValues(t *testing.T) {
	for _, raw := range []string{`null`, `7`, `"unknown"`, `{}`} {
		if flag := easmCertificateFlag([]byte(raw)); flag != nil {
			t.Fatalf("unknown validity became an explicit flag: %s", raw)
		}
	}
}

// The fixture uses the source's actual cert.* fields without a fingerprint or
// serial. Its false validity must survive typed MCP, result capping and replay.
func TestEASMCertificateSearchPublishesItsReportedValidityBeforeModelPublication(t *testing.T) {
	for _, mode := range []string{"direct", "progressive"} {
		t.Run(mode, func(t *testing.T) {
			const fields = "domain,cert.subject.cn,cert.subject.org,cert.issuer.org,cert.not_after,cert.is_valid,cert.is_match"
			var calls atomic.Int32
			remote := mcp.NewServer(&mcp.Implementation{Name: "certificate-fixture", Version: "test"}, nil)
			remote.AddTool(&mcp.Tool{Name: "mcp_fofa_search", InputSchema: map[string]any{"type": "object"}}, func(_ context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				var args struct{ Fields string }
				if json.Unmarshal(request.Params.Arguments, &args) != nil || args.Fields != fields {
					t.Error("the search did not declare its returned certificate columns")
				}
				rows := []any{[]any{"first.example.test", "*.example.test", "Fixture Certificate Org", "Fixture Issuer", "2030-12-12", "false", false}}
				if calls.Add(1) > 1 {
					rows = append(rows, []any{"second.example.test", "*.example.test", "Fixture Certificate Org", "Fixture Issuer", "2030-12-12", "false", false})
				}
				if mode == "progressive" {
					for i, row := range rows {
						values := row.([]any)
						object := make(map[string]any)
						for column, field := range strings.Split(fields, ",") {
							object[field] = values[column]
						}
						rows[i] = object
					}
				}
				body := map[string]any{"results": rows, "padding": strings.Repeat("certificate source evidence ", 200)}
				raw, _ := json.Marshal(body)
				result := &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(raw)}}}
				if mode == "progressive" {
					result.StructuredContent = body
				}
				return result, nil
			})
			endpoint := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return remote }, nil))
			t.Cleanup(endpoint.Close)
			server := config.MCPServer{ID: "certificate-observations", Name: "Certificate observations", Transport: "streamable_http", URL: endpoint.URL, Enabled: true, TimeoutSeconds: 5}
			name := mcpclient.DirectToolName(server.ID, "mcp_fofa_search")
			var args any = map[string]any{"query": `domain=="example.test"`, "fields": fields}
			if mode == "progressive" {
				_, name = mcpclient.ProxyNames(server.ID)
				args = map[string]any{"name": "mcp_fofa_search", "arguments": args}
			}
			arguments, _ := json.Marshal(args)
			var e *Engine
			var threadID, panelID string
			var initialRevision int64
			ready, release := make(chan struct{}, 1), make(chan struct{})
			var once sync.Once
			defer once.Do(func() { close(release) })
			check := func(domains int, revision int64) {
				panel, err := e.store.TaskPanel(t.Context(), threadID, panelID)
				if err != nil {
					t.Error(err)
					return
				}
				var graph struct {
					Nodes []struct {
						ID, Kind, FeatureKind string
						Detail                struct {
							IsValid json.RawMessage `json:"is_valid"`
							IsMatch json.RawMessage `json:"is_match"`
						}
					}
					Edges []struct {
						Confirmed bool
					}
				}
				if json.Unmarshal(panel.State, &graph) != nil || panel.Revision != revision || len(graph.Nodes) != 2+domains*2 || len(graph.Edges) != 1+domains*2 {
					t.Errorf("certificate search waited for another model publication: revision=%d state=%s", panel.Revision, panel.State)
				}
				for _, node := range graph.Nodes {
					if node.Kind == "feature" && node.FeatureKind == "cert" && (string(node.Detail.IsValid) != "false" || string(node.Detail.IsMatch) != "false") {
						t.Errorf("the source's explicit certificate flags were dropped: %+v", node)
					}
				}
				for _, edge := range graph.Edges {
					if edge.Confirmed {
						t.Error("invalid certificate evidence was automatically confirmed")
					}
				}
			}
			e, _ = newScheduleCompletionHTTPEngine(t, func(w http.ResponseWriter, request *http.Request, step int, input scheduleCompletionRequest) {
				switch step {
				case 1:
					panelAdmissionCall(w, input, "certificate-open", "open_task_panel", `{"plugin_id":"easm-asset-portrait","subject":"Fixture"}`)
				case 2:
					var opened taskpanel.Panel
					if err := json.Unmarshal([]byte(nuwaValidationReceipt(input, "certificate-open")), &opened); err != nil || opened.ID == "" {
						t.Errorf("open certificate panel: %v", err)
					}
					panelID, initialRevision = opened.ID, opened.Revision
					panelAdmissionCall(w, input, "certificate-search-1", name, string(arguments))
				case 3:
					check(1, initialRevision+1)
					var capped struct{ Truncated bool }
					if json.Unmarshal([]byte(nuwaValidationReceipt(input, "certificate-search-1")), &capped) != nil || !capped.Truncated {
						t.Error("the certificate fixture did not cross model result capping")
					}
					panelAdmissionCall(w, input, "certificate-search-2", name, string(arguments))
				case 4:
					check(2, initialRevision+2)
					panelAdmissionCall(w, input, "certificate-search-repeat", name, string(arguments))
				case 5:
					check(2, initialRevision+2)
					ready <- struct{}{}
					select {
					case <-release:
						writeContextFlowReply(w, input.Stream, map[string]any{"content": "Certificate observations are available."}, "stop")
					case <-request.Context().Done():
					}
				default:
					t.Errorf("unexpected certificate model request %d", step)
					writeContextFlowReply(w, input.Stream, map[string]any{"content": "Fixture failed."}, "stop")
				}
			})
			e.cfg.Memory.Enabled, e.cfg.Memory.AutoReview, e.cfg.Swarm.ComposerPredictions = false, false, false
			e.cfg.Tools.ResultMaxChars = 1024
			e.cfg.MCP.Servers = []config.MCPServer{server}
			e.cfg.MCP.DirectLoadPercent = 100
			if mode == "progressive" {
				e.cfg.MCP.DirectLoadPercent = 0
			}
			thread, err := e.CreateThread("Certificate source observations", "", "")
			if err != nil {
				t.Fatal(err)
			}
			threadID = thread.ID
			turn, err := e.StartTurn(thread.ID, "Inspect the supplied certificate source evidence and retain its reported status.")
			if err != nil {
				t.Fatal(err)
			}
			select {
			case <-ready:
			case <-time.After(10 * time.Second):
				t.Fatal("certificate observations did not reach the final-response gate")
			}
			if pending, err := e.store.GetTurn(turn.ID); err != nil || pending.Status != store.TurnRunning {
				t.Fatalf("certificate projection required task completion: turn=%+v error=%v", pending, err)
			}
			once.Do(func() { close(release) })
			if done := waitForTurn(t, e, turn.ID); done.Status != store.TurnDone {
				t.Fatal(done.Error)
			}
			waitSettled(t, e, thread.ID)
		})
	}
}
