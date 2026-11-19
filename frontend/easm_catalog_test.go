package frontend

import (
	"encoding/json"
	"testing"

	"github.com/LubyRuffy/eino-swarm/internal/taskpanel"
)

func TestEASMPanelSchemaRejectsInvalidSnapshots(t *testing.T) {
	var definitions []taskpanel.Definition
	if err := json.Unmarshal(TaskPanelCatalog(), &definitions); err != nil {
		t.Fatal(err)
	}
	var definition taskpanel.Definition
	for _, candidate := range definitions {
		if candidate.ID == "easm-asset-portrait" {
			definition = candidate
		}
	}
	if err := taskpanel.ValidateDefinition(definition); err != nil {
		t.Fatal(err)
	}
	if definition.Source != nil {
		t.Fatal("the EASM panel receives published snapshots")
	}
	if definition.Entry != "" {
		t.Fatal("the EASM panel uses the bundled host renderer")
	}
	// The bundled contract stays covered when prose examples are removed.
	example := json.RawMessage(`{"schema_version":1,"title":"Synthetic inventory","nodes":[{"id":"subject","kind":"root","label":"Fixture organization","ip_count":1}],"edges":[],"heat":[],"components":[]}`)
	if err := taskpanel.ValidateState(definition, example); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name string
		edit func(map[string]any)
	}{
		{"schema version", func(s map[string]any) { s["schema_version"] = 2 }},
		{"missing collection", func(s map[string]any) { delete(s, "edges") }},
		{"node kind", func(s map[string]any) { s["nodes"].([]any)[0].(map[string]any)["kind"] = "unsupported" }},
		{"negative count", func(s map[string]any) { s["nodes"].([]any)[0].(map[string]any)["ip_count"] = -1 }},
		{"feature type", func(s map[string]any) {
			s["nodes"] = []any{map[string]any{"id": "root", "kind": "root", "label": "root"}, map[string]any{"id": "feature", "kind": "feature", "label": "feature"}}
		}},
		{"multiple roots", func(s map[string]any) {
			s["nodes"] = []any{map[string]any{"id": "a", "kind": "root", "label": "a"}, map[string]any{"id": "b", "kind": "root", "label": "b"}}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var changed map[string]any
			if err := json.Unmarshal(example, &changed); err != nil {
				t.Fatal(err)
			}
			test.edit(changed)
			raw, err := json.Marshal(changed)
			if err != nil {
				t.Fatal(err)
			}
			if taskpanel.ValidateState(definition, raw) == nil {
				t.Fatal("invalid snapshot passed the host Schema")
			}
		})
	}
}
