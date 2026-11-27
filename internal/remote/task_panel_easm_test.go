package remote

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/LubyRuffy/eino-swarm/internal/panelplugins/easm"
	"github.com/LubyRuffy/eino-swarm/internal/taskpanel"
)

func TestPhoneReadsTheNegotiatedEASMDefinitionOverTheSealedLink(t *testing.T) {
	legacy, current, err := easm.PanelDefinitions()
	if err != nil {
		t.Fatal(err)
	}
	t.Run("old and new phones on a host with a v2 panel", func(t *testing.T) {
		e, _, source := phonePanelFixture(t)
		panel, err := e.Store().OpenTaskPanel(t.Context(), current, source)
		if err != nil {
			t.Fatal(err)
		}
		// Keep the old projection large enough to exercise frozen continuation.
		title := strings.Repeat("Portrait-世界", 3000)
		panel = phonePanelPublish(t, e, panel, source, "easm-observation", map[string]any{
			"schema_version": 1, "title": title, "query": "recorded query",
			"collection": map[string]any{"status": "done", "steps": []map[string]string{
				{"id": "summary", "status": "done"}, {"id": "clouds", "status": "done"},
			}},
			"nodes": []any{}, "edges": []any{}, "heat": []any{}, "components": []any{},
			"clouds": []map[string]any{{"name": "Provider", "category": "mail", "assets": 6, "ips": 2,
				"services": []map[string]any{{"name": "SMTP", "count": 4}}}},
		})
		base := panel.Revision
		panel, err = e.Store().PublishTaskPanel(t.Context(), source, taskpanel.Update{
			ProtocolVersion: 1, PanelID: panel.ID, OperationID: "easm-update", BaseRevision: base,
			Kind: "patch", Operations: []byte(`[{"op":"replace","path":"/clouds/0/services/0/count","value":5}]`),
		})
		if err != nil {
			t.Fatal(err)
		}
		relay := newFollowupRelay(t, e)
		oldManifest := readEASMPhoneManifest(t, relay, panel, "")
		if oldManifest != legacy.Version {
			t.Fatalf("old phone was offered plugin version %q", oldManifest)
		}
		body := readEASMPhoneBody(t, relay, panel, "", &base, true)
		if body.Change != nil || body.Panel == nil || body.Panel.PluginVersion != oldManifest || !taskpanel.DefinitionsMatch(body.Panel.Definition, legacy) {
			t.Fatal("old phone did not get a v1 snapshot")
		}
		if err := taskpanel.ValidateState(legacy, body.Panel.State); err != nil {
			t.Fatalf("upstream v1 schema refuses projected state: %v", err)
		}
		var state easm.State
		if err := json.Unmarshal(body.Panel.State, &state); err != nil || state.Title != title || state.Query != "recorded query" || len(state.Clouds) != 0 || len(state.Collection.Steps) != 1 || state.Collection.Steps[0].ID != easm.StepSummary {
			t.Fatal("old phone lost pre-existing portrait data or received new fields", err)
		}

		newManifest := readEASMPhoneManifest(t, relay, panel, current.Version)
		if newManifest != current.Version {
			t.Fatalf("new phone was offered plugin version %q", newManifest)
		}
		body = readEASMPhoneBody(t, relay, panel, current.Version, nil, false)
		if body.Panel == nil || body.Panel.PluginVersion != newManifest || !taskpanel.DefinitionsMatch(body.Panel.Definition, current) || !bytes.Equal(body.Panel.State, panel.State) {
			t.Fatal("new phone did not get the committed v2 snapshot")
		}
		body = readEASMPhoneBody(t, relay, panel, current.Version, &base, false)
		if body.Panel != nil || body.Change == nil || body.Change.BaseRevision != base || !bytes.Contains(body.Change.Operations, []byte(`/clouds/0/services/0/count`)) {
			t.Fatal("new phone did not receive its eligible v2 patch")
		}
		stored, err := e.Store().TaskPanel(t.Context(), panel.ThreadID, panel.ID)
		if err != nil || !bytes.Equal(stored.State, panel.State) || !taskpanel.DefinitionsMatch(stored.Definition, current) {
			t.Fatal("old phone transfer changed durable state", err)
		}
		bad := relay.rpc(t, Request{ID: "unsupported-easm-version", Op: OpTaskPanels, ThreadID: panel.ThreadID, EASMPanelVersion: "unknown"})
		if bad.OK || bad.Code != "bad_request" {
			t.Fatal("unsupported EASM version accepted", bad)
		}
	})

	t.Run("new phone with an upstream v1 panel", func(t *testing.T) {
		e, _, source := phonePanelFixture(t)
		panel, err := e.Store().OpenTaskPanel(t.Context(), legacy, source)
		if err != nil {
			t.Fatal(err)
		}
		panel = phonePanelPublish(t, e, panel, source, "v1-observation", legacy.InitialState)
		relay := newFollowupRelay(t, e)
		if got := readEASMPhoneManifest(t, relay, panel, current.Version); got != legacy.Version {
			t.Fatal("upstream manifest version was rewritten", got)
		}
		body := readEASMPhoneBody(t, relay, panel, current.Version, nil, false)
		if body.Panel == nil || body.Panel.PluginVersion != legacy.Version || !taskpanel.DefinitionsMatch(body.Panel.Definition, legacy) || !bytes.Equal(body.Panel.State, panel.State) {
			t.Fatal("v2 capability lost the upstream v1 portrait")
		}
	})

	t.Run("unknown EASM definition is not relabeled", func(t *testing.T) {
		e, _, source := phonePanelFixture(t)
		custom := current
		custom.Name = "Unrecognized definition"
		panel, err := e.Store().OpenTaskPanel(t.Context(), custom, source)
		if err != nil {
			t.Fatal(err)
		}
		panel = phonePanelPublish(t, e, panel, source, "custom-observation", custom.InitialState)
		relay := newFollowupRelay(t, e)
		reply := relay.rpc(t, Request{ID: "custom", Op: OpTaskPanelRead, ThreadID: panel.ThreadID, PanelID: panel.ID})
		if reply.OK || reply.Code != "read_failed" || reply.PanelTransfer != nil {
			t.Fatal("unknown definition produced a compatible snapshot", reply)
		}
	})
}

func readEASMPhoneManifest(t *testing.T, relay *followupRelay, panel taskpanel.Panel, version string) string {
	t.Helper()
	reply := relay.rpc(t, Request{ID: "easm-manifest", Op: OpTaskPanels, ThreadID: panel.ThreadID, EASMPanelVersion: version})
	if !reply.OK || reply.TaskPanels == nil || len(*reply.TaskPanels) != 1 {
		t.Fatal("manifest did not list exactly the EASM panel", reply)
	}
	item := (*reply.TaskPanels)[0]
	if item.ID != panel.ID || item.PluginID != panel.PluginID || item.Revision != panel.Revision {
		t.Fatal("manifest changed the durable identity", item)
	}
	return item.PluginVersion
}

type easmPhoneBody struct {
	Panel  *taskpanel.Panel  `json:"panel"`
	Change *taskpanel.Change `json:"change"`
}

func readEASMPhoneBody(t *testing.T, relay *followupRelay, panel taskpanel.Panel, version string, base *int64, checkContinuation bool) easmPhoneBody {
	t.Helper()
	request := Request{ID: "easm-read", Op: OpTaskPanelRead, ThreadID: panel.ThreadID, PanelID: panel.ID, EASMPanelVersion: version, PanelBaseRevision: base}
	reply := relay.rpc(t, request)
	raw := panelChunkBytes(t, reply)
	first := reply.PanelTransfer
	if first.Revision != panel.Revision {
		t.Fatal("wrong EASM transfer revision", first.Revision)
	}
	if checkContinuation {
		if !first.More {
			t.Fatal("fixture did not create multiple chunks")
		}
		changedVersion := Request{ID: "switch-easm-mid-transfer", Op: OpTaskPanelRead, ThreadID: panel.ThreadID, PanelID: panel.ID,
			PanelTransferID: first.ID, EASMPanelVersion: easm.PanelVersion, Offset: first.NextOffset}
		if switched := relay.rpc(t, changedVersion); switched.OK || switched.Code != "bad_request" {
			t.Fatal("continuation renegotiated its frozen projection", switched)
		}
	}
	for chunk := first; chunk.More; chunk = reply.PanelTransfer {
		reply = relay.rpc(t, Request{ID: fmt.Sprintf("easm-part-%d", chunk.NextOffset), Op: OpTaskPanelRead,
			ThreadID: panel.ThreadID, PanelID: panel.ID, PanelTransferID: first.ID, Offset: chunk.NextOffset})
		raw = append(raw, panelChunkBytes(t, reply)...)
	}
	sum := sha256.Sum256(raw)
	if int64(len(raw)) != first.Size || hex.EncodeToString(sum[:]) != first.SHA256 {
		t.Fatal("resumed EASM transfer changed its frozen bytes")
	}
	var body easmPhoneBody
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	return body
}

func TestEASMLegacyTransferRefusesMalformedStoredState(t *testing.T) {
	_, current, err := easm.PanelDefinitions()
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{`[]`, `{"collection":[]}`, `{"collection":{"steps":{}}}`, `{"collection":{"steps":null}}`, `{"collection":{"steps":[42]}}`, `{"collection":{"steps":[]}}`} {
		if _, _, err := projectEASMPanel(taskpanel.Panel{PluginVersion: current.Version, Definition: current, State: json.RawMessage(state)}, nil, ""); err == nil {
			t.Fatalf("legacy projection accepted corrupt state: %s", state)
		}
	}
}

// A phone renders the contract it was built for: v3 contact mailboxes must not
// reach a v2 bundle, and the cloud table must not reach v1.
func TestEASMPhonesReceiveTheContractTheirBundleNames(t *testing.T) {
	published, err := easm.PanelVersions()
	if err != nil {
		t.Fatal(err)
	}
	v1, v2, v3 := published[0], published[1], published[2]
	e, _, source := phonePanelFixture(t)
	panel, err := e.Store().OpenTaskPanel(t.Context(), v3, source)
	if err != nil {
		t.Fatal(err)
	}
	panel = phonePanelPublish(t, e, panel, source, "emails", map[string]any{
		"schema_version": 1, "title": "Subject",
		"collection": map[string]any{"status": "done", "steps": []map[string]string{
			{"id": "summary", "status": "done"}, {"id": "clouds", "status": "done"},
		}},
		"nodes": []any{
			map[string]any{"id": "root", "kind": "root", "label": "Subject",
				"detail": map[string]any{"emails": []any{map[string]any{"address": "contact@example.test", "label": "Contact"}}}},
			map[string]any{"id": "domain", "kind": "domain", "label": "example.test"},
		},
		"edges": []any{map[string]any{"source": "root", "target": "domain"}},
		"heat":  []any{}, "components": []any{},
		"clouds": []map[string]any{{"name": "Provider", "category": "mail", "assets": 1, "ips": 1,
			"services": []map[string]any{{"name": "SMTP", "count": 1}}}},
	})
	relay := newFollowupRelay(t, e)
	for _, want := range []struct {
		version    string
		definition taskpanel.Definition
		emails     bool
		clouds     bool
	}{
		{v2.Version, v2, false, true},
		{v1.Version, v1, false, false},
		{v3.Version, v3, true, true},
	} {
		body := readEASMPhoneBody(t, relay, panel, want.version, nil, false)
		if body.Panel == nil || body.Panel.PluginVersion != want.version ||
			!taskpanel.DefinitionsMatch(body.Panel.Definition, want.definition) {
			t.Fatalf("phone v%s received plugin version %q", want.version, body.Panel.PluginVersion)
		}
		if err := taskpanel.ValidateState(want.definition, body.Panel.State); err != nil {
			t.Fatalf("phone v%s state: %v", want.version, err)
		}
		if got := bytes.Contains(body.Panel.State, []byte(`"emails"`)); got != want.emails {
			t.Fatalf("phone v%s emails = %t", want.version, got)
		}
		if got := bytes.Contains(body.Panel.State, []byte(`"clouds"`)); got != want.clouds {
			t.Fatalf("phone v%s clouds = %t", want.version, got)
		}
	}
}
