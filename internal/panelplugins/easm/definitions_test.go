package easm

import (
	"testing"

	"github.com/LubyRuffy/eino-swarm/frontend"
	"github.com/LubyRuffy/eino-swarm/internal/taskpanel"
)

func TestPublishedEASMDefinitionsPreserveV1State(t *testing.T) {
	legacy, current, err := PanelDefinitions()
	if err != nil {
		t.Fatal(err)
	}
	if legacy.ID != "easm-asset-portrait" || legacy.Version != "1" || current.ID != legacy.ID || current.Version != "2" {
		t.Fatalf("definition identity = %s v%s and %s v%s", legacy.ID, legacy.Version, current.ID, current.Version)
	}
	for _, d := range []taskpanel.Definition{legacy, current} {
		if err := taskpanel.ValidateDefinition(d); err != nil {
			t.Fatal(err)
		}
	}
	// The new definition must validate the bytes it inherits at upgrade and
	// continue to distinguish the old phone's more restrictive state shape.
	if err := taskpanel.ValidateState(current, legacy.InitialState); err != nil {
		t.Fatal(err)
	}
	if err := taskpanel.ValidateState(legacy, current.InitialState); err == nil {
		t.Fatal("v1 accepted the v2 initial state")
	}
}

func TestEASMDefinitionsRefuseMissingOrInvalidEmbeddedContracts(t *testing.T) {
	validLegacy := frontend.EASMV1Definition()
	validCatalog := frontend.TaskPanelCatalog()
	for _, input := range []struct{ legacy, catalog []byte }{
		{nil, validCatalog},
		{[]byte(`{"id":"easm-asset-portrait","version":"99"}`), validCatalog},
		{validLegacy, nil},
		{validLegacy, []byte("[]")},
	} {
		if _, _, err := decodePanelDefinitions(input.legacy, input.catalog); err == nil {
			t.Fatal("invalid catalog data was accepted")
		}
	}
}
