package engine

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/LubyRuffy/eino-swarm/frontend"
	"github.com/LubyRuffy/eino-swarm/internal/store"
	"github.com/LubyRuffy/eino-swarm/internal/taskpanel"
)

const easmPublicationGraphNodes = `[{"id":"root","kind":"root","label":"Fixture Holdings"},{"id":"legal-source","kind":"company","label":"Fixture Legal Entity"},{"id":"dns-source","kind":"domain","label":"Service.Example.test."},{"id":"cert-source","kind":"feature","featureKind":"cert","label":"*.example.test","detail":{"serial":"serial-a","issuer":"Fixture Issuer","observations":[{"host":"https://service.example.test:443","source":"mcp_fofa_search","is_valid":false}]}},{"id":"local-source","kind":"feature","featureKind":"cert","label":"local.example.test","detail":{"observations":[{"host":"https://local.example.test:443","source":"mcp_fofa_asset_lookup"}]}}]`

func TestEASMAgentReusesReportedGraphIdentities(t *testing.T) {
	var definitions []taskpanel.Definition
	if err := json.Unmarshal(frontend.TaskPanelCatalog(), &definitions); err != nil {
		t.Fatal(err)
	}
	var definition taskpanel.Definition
	for _, candidate := range definitions {
		if candidate.ID == easmPanelID {
			definition = candidate
		}
	}
	for _, test := range []struct {
		name, node, reuse string
		invalid           bool
	}{
		{"legal entity", `{"id":"new","kind":"company","label":"Fixture Legal Entity"}`, "legal-source", true},
		{"legal root", `{"id":"new","kind":"company","label":"Fixture Holdings"}`, "root", true},
		{"trim legal name", `{"id":"new","kind":"company","label":" Fixture Legal Entity "}`, "legal-source", true},
		{"distinct legal entity", `{"id":"new","kind":"company","label":"Another Legal Entity"}`, "", false},
		{"existing ID with a different label", `{"id":"root","kind":"company","label":"Another Legal Entity"}`, "root", true},
		{"DNS identity", `{"id":"new","kind":"domain","label":"service.example.TEST"}`, "dns-source", true},
		{"distinct DNS identity", `{"id":"new","kind":"domain","label":"another.example.test"}`, "", false},
		{"legacy non-DNS label", `{"id":"new","kind":"domain","label":"Legacy domain clue"}`, "", false},
		{"certificate identity", `{"id":"new","kind":"feature","featureKind":"cert","label":"different CN","detail":{"serial":"serial-a","issuer":"Fixture Issuer"}}`, "cert-source", true},
		{"distinct serial", `{"id":"new","kind":"feature","featureKind":"cert","label":"*.example.test","detail":{"serial":"serial-b","issuer":"Fixture Issuer"}}`, "", false},
		{"distinct issuer", `{"id":"new","kind":"feature","featureKind":"cert","label":"*.example.test","detail":{"serial":"serial-a","issuer":"Other Issuer"}}`, "", false},
		{"concept certificate", `{"id":"new","kind":"feature","featureKind":"cert","label":"*.example.test","detail":{"subject_name":"Fixture Certificate Org","issuer":"Fixture Issuer","reason":"Certificate concept"}}`, "", true},
		{"serial alone", `{"id":"new","kind":"feature","featureKind":"cert","label":"*.example.test","detail":{"serial":"serial-c"}}`, "", true},
		{"blank identity", `{"id":"new","kind":"feature","featureKind":"cert","label":"*.example.test","detail":{"serial":" ","issuer":" "}}`, "", true},
		{"empty observations", `{"id":"new","kind":"feature","featureKind":"cert","label":"*.example.test","detail":{"observations":[]}}`, "", true},
		{"blank service", `{"id":"new","kind":"feature","featureKind":"cert","label":"*.example.test","detail":{"observations":[{"host":" "}]}}`, "", true},
		{"invalid service", `{"id":"new","kind":"feature","featureKind":"cert","label":"*.example.test","detail":{"observations":[{"host":"certificate concept"}]}}`, "", true},
		{"invalid port", `{"id":"new","kind":"feature","featureKind":"cert","label":"*.example.test","detail":{"observations":[{"host":"https://service.example.test:invalid"}]}}`, "", true},
		{"multiple services", `{"id":"new","kind":"feature","featureKind":"cert","label":"*.example.test","detail":{"observations":[{"host":"https://one.example.test:443"},{"host":"https://two.example.test:443"}]}}`, "", true},
		{"existing global service", `{"id":"new","kind":"feature","featureKind":"cert","label":"*.EXAMPLE.test.","detail":{"observations":[{"host":"https://SERVICE.EXAMPLE.TEST.:443/"}]}}`, "cert-source", true},
		{"existing local service", `{"id":"new","kind":"feature","featureKind":"cert","label":"local.example.test","detail":{"observations":[{"host":"https://local.example.test:443"}]}}`, "local-source", true},
		{"existing service in multiple-host summary", `{"id":"new","kind":"feature","featureKind":"cert","label":"*.example.test","detail":{"observations":[{"host":"https://service.example.test:443"},{"host":"https://another.example.test:443"}]}}`, "cert-source", true},
		{"new local service", `{"id":"new","kind":"feature","featureKind":"cert","label":"*.example.test","detail":{"observations":[{"host":"https://another.example.test:443","is_valid":false}]}}`, "", false},
		{"same service multiple sources", `{"id":"new","kind":"feature","featureKind":"cert","label":"*.example.test","detail":{"observations":[{"host":"https://another.example.test:443","source":"mcp_fofa_search"},{"host":"https://ANOTHER.EXAMPLE.TEST.:443/","source":"mcp_fofa_asset_lookup"}]}}`, "", false},
		{"IP service", `{"id":"new","kind":"feature","featureKind":"cert","label":"*.example.test","detail":{"observations":[{"host":"https://203.0.113.17:8443"}]}}`, "", false},
		{"IPv6 service", `{"id":"new","kind":"feature","featureKind":"cert","label":"*.example.test","detail":{"observations":[{"host":"https://[2001:db8::17]:8443"}]}}`, "", false},
		{"bare domain service", `{"id":"new","kind":"feature","featureKind":"cert","label":"*.example.test","detail":{"observations":[{"host":"another.example.test"}]}}`, "", false},
		{"unrelated feature", `{"id":"new","kind":"feature","featureKind":"org","label":"Fixture Certificate Org"}`, "", false},
	} {
		for _, kind := range []string{"patch", "snapshot"} {
			t.Run(test.name+"/"+kind, func(t *testing.T) {
				state := json.RawMessage(`{"schema_version":1,"title":"Fixture Holdings","nodes":` + easmPublicationGraphNodes + `,"edges":[],"heat":[],"components":[]}`)
				update := taskpanel.Update{ProtocolVersion: 1, PanelID: "panel", OperationID: "graph-identity", Kind: kind}
				if kind == "patch" {
					update.Operations = json.RawMessage(`[{"op":"add","path":"/nodes/-","value":` + test.node + `}]`)
				} else {
					update.State = json.RawMessage(strings.Replace(string(state), easmPublicationGraphNodes, strings.TrimSuffix(easmPublicationGraphNodes, "]")+","+test.node+"]", 1))
				}
				err := easmValidateAgentPublication(taskpanel.Panel{Definition: definition, State: state}, update)
				if (err != nil) != test.invalid || test.invalid && !errors.Is(err, taskpanel.ErrInvalid) {
					t.Fatalf("invalid=%v, got %v", test.invalid, err)
				}
				if test.reuse != "" && !strings.Contains(err.Error(), test.reuse) {
					t.Fatalf("validation did not identify reusable ID %q: %v", test.reuse, err)
				}
			})
		}
	}
}

func TestEASMAgentGraphGuardHandlesOldMalformedDataAndBatchDuplicates(t *testing.T) {
	for _, pair := range [][2]string{{`{`, `{}`}, {`{}`, `{`}} {
		if err := easmValidateAgentGraph(json.RawMessage(pair[0]), json.RawMessage(pair[1])); !errors.Is(err, taskpanel.ErrInvalid) {
			t.Fatalf("malformed graph was accepted: %v", err)
		}
	}
	for _, observations := range []string{`{}`, `[{"host":7}]`, `[{"host":"https://user@service.example.test"}]`, `[{"host":"single-label"}]`} {
		if _, valid := easmPublicationCertificateHosts(json.RawMessage(observations)); valid {
			t.Fatalf("invalid service evidence was accepted: %s", observations)
		}
	}
	// Old malformed or absent observations do not prevent evidence enrichment on
	// existing IDs, and two new identities in one batch cannot duplicate either.
	old := json.RawMessage(`{"nodes":[{"id":"old","kind":"feature","featureKind":"cert","label":"Old clue","detail":{"observations":{}}}]}`)
	if err := easmValidateAgentGraph(old, old); err != nil {
		t.Fatalf("old evidence was forced through new-node admission: %v", err)
	}
	for _, nodes := range []string{
		`[{"id":"first","kind":"company","label":"New Entity"},{"id":"first","kind":"company","label":"Other Entity"}]`,
		`[{"id":"first","kind":"company","label":"New Entity"},{"id":"second","kind":"company","label":"New Entity"}]`,
		`[{"id":"first","kind":"domain","label":"new.example.test"},{"id":"second","kind":"domain","label":"NEW.example.test."}]`,
		`[{"id":"first","kind":"feature","featureKind":"cert","label":"A","detail":{"serial":"serial","issuer":"Issuer"}},{"id":"second","kind":"feature","featureKind":"cert","label":"B","detail":{"serial":"serial","issuer":"Issuer"}}]`,
	} {
		if err := easmValidateAgentGraph(json.RawMessage(`{"nodes":[]}`), json.RawMessage(`{"nodes":`+nodes+`}`)); !errors.Is(err, taskpanel.ErrInvalid) || !strings.Contains(err.Error(), "first") {
			t.Fatalf("batch duplicates were accepted: %v", err)
		}
	}
	before := json.RawMessage(`{"nodes":[{"id":"one","kind":"feature","featureKind":"cert","label":"*.example.test","detail":{"serial":"a","issuer":"Issuer","observations":[{"host":"https://one.example.test"},{"host":"https://two.example.test"}]}},{"id":"two","kind":"feature","featureKind":"cert","label":"*.example.test","detail":{"serial":"b","issuer":"Issuer","observations":[{"host":"https://three.example.test"}]}}]}`)
	after := json.RawMessage(strings.TrimSuffix(string(before), "]}") + `,{"id":"summary","kind":"feature","featureKind":"cert","label":"*.example.test","detail":{"observations":[{"host":"https://one.example.test"},{"host":"https://two.example.test"},{"host":"https://three.example.test"}]}}]}`)
	if err := easmValidateAgentGraph(before, after); !errors.Is(err, taskpanel.ErrInvalid) || !strings.Contains(err.Error(), "one, two") || strings.Contains(err.Error(), "one, one") {
		t.Fatalf("multi-service summary did not identify each reusable certificate once: %v", err)
	}
}

func TestEASMGraphPublicationKeepsAllReferencesResolvable(t *testing.T) {
	for _, test := range []struct {
		name, next string
		invalid    bool
	}{
		{"missing edge source", `{"nodes":[{"id":"root","kind":"root","label":"Fixture"}],"edges":[{"source":"missing","target":"root"}]}`, true},
		{"missing edge target", `{"nodes":[{"id":"root","kind":"root","label":"Fixture"}],"edges":[{"source":"root","target":"missing"}]}`, true},
		{"missing feature reference", `{"nodes":[{"id":"root","kind":"root","label":"Fixture","detail":{"features":[{"kind":"org","value":"Evidence","node_id":"missing"}]}}],"edges":[]}`, true},
		{"unlinked feature value", `{"nodes":[{"id":"root","kind":"root","label":"Fixture","detail":{"features":[{"kind":"org","value":"Evidence"}]}}],"edges":[]}`, false},
		{"optional empty reference", `{"nodes":[{"id":"root","kind":"root","label":"Fixture","detail":{"features":[{"kind":"org","value":"Evidence","node_id":""}]}}],"edges":[]}`, false},
		{"self reference stays valid", `{"nodes":[{"id":"root","kind":"root","label":"Fixture","detail":{"features":[{"kind":"org","value":"Evidence","node_id":"root"}]}}],"edges":[{"source":"root","target":"root"}]}`, false},
		{"same-name entities remain distinct", `{"nodes":[{"id":"root","kind":"root","label":"Fixture"},{"id":"one","kind":"company","label":"Ambiguous Entity"},{"id":"two","kind":"company","label":"Ambiguous Entity"}],"edges":[{"source":"two","target":"one"}]}`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Existing ambiguous source entities are valid; this check adds no
			// label, relation-direction or self-reference restrictions.
			before := json.RawMessage(`{"nodes":[{"id":"root","kind":"root","label":"Fixture"},{"id":"one","kind":"company","label":"Ambiguous Entity"},{"id":"two","kind":"company","label":"Ambiguous Entity"}],"edges":[]}`)
			err := easmValidateAgentGraph(before, json.RawMessage(test.next))
			if (err != nil) != test.invalid || test.invalid && !errors.Is(err, taskpanel.ErrInvalid) {
				t.Fatalf("invalid=%v, got %v", test.invalid, err)
			}
		})
	}
}

// Agent evidence can enrich existing nodes and remove an excluded relationship;
// automatic MCP publication remains free to preserve ambiguous source entities.
func TestEASMGraphGuardAtTaskPanelToolBoundary(t *testing.T) {
	var e *Engine
	var threadID, panelID, acceptedArgs string
	var observed, accepted taskpanel.Panel
	read := func() taskpanel.Panel {
		panel, err := e.store.TaskPanel(t.Context(), threadID, panelID)
		if err != nil {
			t.Fatal(err)
		}
		return panel
	}
	e, _ = newScheduleCompletionHTTPEngine(t, func(w http.ResponseWriter, _ *http.Request, step int, request scheduleCompletionRequest) {
		switch step {
		case 1:
			panelAdmissionCall(w, request, "identity-open", "open_task_panel", `{"plugin_id":"easm-asset-portrait","subject":"Fixture Holdings"}`)
		case 2:
			var opened taskpanel.Panel
			if err := json.Unmarshal([]byte(nuwaValidationReceipt(request, "identity-open")), &opened); err != nil || opened.ID == "" {
				t.Fatalf("open receipt: %v", err)
			}
			panelID = opened.ID
			current := read()
			e.recordMu.Lock()
			var err error
			observed, err = e.publishTaskPanelLocked(t.Context(), current.Source, taskpanel.Update{ProtocolVersion: 1, PanelID: panelID, OperationID: "source-identities", BaseRevision: current.Revision, Kind: "patch", Operations: json.RawMessage(`[{"op":"replace","path":"/nodes","value":` + strings.TrimSuffix(easmPublicationGraphNodes, "]") + `,{"id":"ambiguous-source","kind":"company","label":"Fixture Legal Entity"}]},{"op":"add","path":"/edges/-","value":{"source":"root","target":"legal-source","confirmed":false}},{"op":"add","path":"/nodes/0/detail","value":{"features":[{"kind":"org","value":"Fixture Legal Entity","node_id":"legal-source"}]}}]`)})
			e.recordMu.Unlock()
			if err != nil {
				t.Fatalf("automatic source could not retain ambiguous entities: %v", err)
			}
			panelAdmissionCall(w, request, "identity-duplicate", "publish_panel_update", fmt.Sprintf(`{"protocol_version":1,"panel_id":%q,"operation_id":"identity-duplicate","base_revision":%d,"kind":"patch","operations":[{"op":"add","path":"/nodes/-","value":{"id":"agent-copy","kind":"company","label":"Fixture Legal Entity"}}]}`, panelID, observed.Revision))
		case 3:
			if receipt := nuwaValidationReceipt(request, "identity-duplicate"); !strings.Contains(receipt, "legal-source") || !strings.Contains(receipt, "ambiguous-source") || read().Revision != observed.Revision {
				t.Errorf("duplicate identity was not refused with reusable IDs: %s", receipt)
			}
			panelAdmissionCall(w, request, "identity-duplicate-id", "publish_panel_update", fmt.Sprintf(`{"protocol_version":1,"panel_id":%q,"operation_id":"identity-duplicate-id","base_revision":%d,"kind":"patch","operations":[{"op":"add","path":"/nodes/-","value":{"id":"root","kind":"company","label":"Another Legal Entity"}}]}`, panelID, observed.Revision))
		case 4:
			if receipt := nuwaValidationReceipt(request, "identity-duplicate-id"); !strings.Contains(receipt, "duplicate node ID") || !strings.Contains(receipt, "root") || read().Revision != observed.Revision {
				t.Errorf("existing ID append was not refused without a revision change: %s", receipt)
			}
			panelAdmissionCall(w, request, "identity-batch-id", "publish_panel_update", fmt.Sprintf(`{"protocol_version":1,"panel_id":%q,"operation_id":"identity-batch-id","base_revision":%d,"kind":"patch","operations":[{"op":"add","path":"/nodes/-","value":{"id":"agent-batch","kind":"company","label":"New Entity"}},{"op":"add","path":"/nodes/-","value":{"id":"agent-batch","kind":"company","label":"Other Entity"}}]}`, panelID, observed.Revision))
		case 5:
			if receipt := nuwaValidationReceipt(request, "identity-batch-id"); !strings.Contains(receipt, "duplicate node ID") || !strings.Contains(receipt, "agent-batch") || read().Revision != observed.Revision {
				t.Errorf("same-batch duplicate ID was not refused without a revision change: %s", receipt)
			}
			panelAdmissionCall(w, request, "identity-concept", "publish_panel_update", fmt.Sprintf(`{"protocol_version":1,"panel_id":%q,"operation_id":"identity-concept","base_revision":%d,"kind":"patch","operations":[{"op":"add","path":"/nodes/-","value":{"id":"agent-concept","kind":"feature","featureKind":"cert","label":"*.example.test","detail":{"subject_name":"Fixture Certificate Org"}}}]}`, panelID, observed.Revision))
		case 6:
			if receipt := nuwaValidationReceipt(request, "identity-concept"); !strings.Contains(receipt, "serial") || !strings.Contains(receipt, "host") || read().Revision != observed.Revision {
				t.Errorf("certificate concept was not refused: %s", receipt)
			}
			panelAdmissionCall(w, request, "identity-dangling-edge", "publish_panel_update", fmt.Sprintf(`{"protocol_version":1,"panel_id":%q,"operation_id":"identity-dangling-edge","base_revision":%d,"kind":"patch","operations":[{"op":"add","path":"/edges/-","value":{"source":"root","target":"missing-entity","confirmed":false}}]}`, panelID, observed.Revision))
		case 7:
			if receipt := nuwaValidationReceipt(request, "identity-dangling-edge"); !strings.Contains(receipt, "missing node") || !strings.Contains(receipt, "missing-entity") || read().Revision != observed.Revision {
				t.Errorf("dangling edge was not refused without a revision change: %s", receipt)
			}
			panelAdmissionCall(w, request, "identity-dangling-feature", "publish_panel_update", fmt.Sprintf(`{"protocol_version":1,"panel_id":%q,"operation_id":"identity-dangling-feature","base_revision":%d,"kind":"patch","operations":[{"op":"add","path":"/nodes/0/detail/features/-","value":{"kind":"org","value":"Evidence","node_id":"missing-feature"}}]}`, panelID, read().Revision))
		case 8:
			if receipt := nuwaValidationReceipt(request, "identity-dangling-feature"); !strings.Contains(receipt, "missing node") || !strings.Contains(receipt, "missing-feature") || read().Revision != observed.Revision {
				t.Errorf("dangling feature was not refused without a revision change: %s", receipt)
			}
			acceptedArgs = fmt.Sprintf(`{"protocol_version":1,"panel_id":%q,"operation_id":"identity-evidence","base_revision":%d,"kind":"patch","operations":[{"op":"add","path":"/nodes/3/detail/reason","value":"Independent evidence"},{"op":"remove","path":"/edges/0"},{"op":"remove","path":"/nodes/0/detail/features/0"},{"op":"remove","path":"/nodes/1"},{"op":"add","path":"/nodes/-","value":{"id":"agent-distinct","kind":"company","label":"Independent Entity"}}]}`, panelID, observed.Revision)
			panelAdmissionCall(w, request, "identity-evidence", "publish_panel_update", acceptedArgs)
		case 9:
			if err := json.Unmarshal([]byte(nuwaValidationReceipt(request, "identity-evidence")), &accepted); err != nil || accepted.Revision != observed.Revision+1 {
				t.Fatalf("existing evidence or exclusion was refused: %v", err)
			}
			current := read()
			if strings.Contains(string(current.State), `"legal-source"`) {
				t.Error("removing an excluded node did not remove its edge and feature references")
			}
			if _, err := e.store.PublishTaskPanel(t.Context(), current.Source, taskpanel.Update{ProtocolVersion: 1, PanelID: panelID, OperationID: "source-later", BaseRevision: current.Revision, Kind: "patch", Operations: json.RawMessage(`[{"op":"add","path":"/nodes/0/detail","value":{"reason":"Later source evidence"}}]`)}); err != nil {
				t.Fatal(err)
			}
			panelAdmissionCall(w, request, "identity-retry", "publish_panel_update", acceptedArgs)
		default:
			var retry taskpanel.Panel
			if err := json.Unmarshal([]byte(nuwaValidationReceipt(request, "identity-retry")), &retry); err != nil || retry.Revision != accepted.Revision || read().Revision != accepted.Revision+1 {
				t.Errorf("accepted retry lost precedence over graph validation: %v", err)
			}
			writeContextFlowReply(w, request.Stream, map[string]any{"content": "The evidence is available."}, "stop")
		}
	})
	e.cfg.Memory.Enabled, e.cfg.Memory.AutoReview, e.cfg.Swarm.ComposerPredictions = false, false, false
	thread, err := e.CreateThread("Graph identity validation", "", "")
	if err != nil {
		t.Fatal(err)
	}
	threadID = thread.ID
	turn, err := e.StartTurn(thread.ID, "Keep reported identities while adding evidence.")
	if err != nil {
		t.Fatal(err)
	}
	if got := waitForTurn(t, e, turn.ID); got.Status != store.TurnDone {
		t.Fatalf("graph validation failed: %s", got.Error)
	}
	waitSettled(t, e, thread.ID)
}
