package easm

import (
	"encoding/json"
	"testing"

	"github.com/LubyRuffy/eino-swarm/frontend"
	"github.com/LubyRuffy/eino-swarm/internal/taskpanel"
)

func TestPublishedEASMDefinitionsPreserveEarlierStates(t *testing.T) {
	published, err := PanelVersions()
	if err != nil {
		t.Fatal(err)
	}
	if len(published) != 3 {
		t.Fatalf("published contracts = %d", len(published))
	}
	for i, want := range []string{"1", "2", "3"} {
		if published[i].ID != PanelID || published[i].Version != want {
			t.Fatalf("definition %d identity = %s v%s", i, published[i].ID, published[i].Version)
		}
		if err := taskpanel.ValidateDefinition(published[i]); err != nil {
			t.Fatal(err)
		}
	}
	legacy, current := published[0], published[len(published)-1]
	// Each contract must validate the bytes it inherits at upgrade and still
	// distinguish the older phone's more restrictive state shape.
	if err := taskpanel.ValidateState(current, legacy.InitialState); err != nil {
		t.Fatal(err)
	}
	if err := taskpanel.ValidateState(legacy, current.InitialState); err == nil {
		t.Fatal("v1 accepted the current initial state")
	}
	var portrait map[string]any
	if err = json.Unmarshal(current.InitialState, &portrait); err != nil {
		t.Fatal(err)
	}
	portrait["nodes"] = []any{map[string]any{"id": "root", "kind": "root", "label": "Subject",
		"detail": map[string]any{"emails": []any{map[string]any{"address": "contact@example.test"}}}}}
	withEmails, err := json.Marshal(portrait)
	if err != nil {
		t.Fatal(err)
	}
	if err := taskpanel.ValidateState(published[1], withEmails); err == nil {
		t.Fatal("v2 accepted a mailbox introduced in v3")
	}
	if err := taskpanel.ValidateState(current, withEmails); err != nil {
		t.Fatal(err)
	}
}

func TestEASMDefinitionsRefuseMissingOrInvalidEmbeddedContracts(t *testing.T) {
	validLegacy := frontend.EASMV1Definition()
	validCatalog := frontend.TaskPanelCatalog()
	validPrevious := frontend.EASMV2Definition()
	for _, input := range []struct{ legacy, previous, catalog []byte }{
		{nil, validPrevious, validCatalog},
		{validLegacy, nil, validCatalog},
		{validLegacy, []byte(`{"id":"easm-asset-portrait","version":"99"}`), validCatalog},
		{[]byte(`{"id":"easm-asset-portrait","version":"99"}`), validPrevious, validCatalog},
		{validLegacy, validPrevious, nil},
		{validLegacy, validPrevious, []byte("[]")},
	} {
		if _, err := decodePanelDefinitions(input.legacy, input.previous, input.catalog); err == nil {
			t.Fatal("invalid catalog data was accepted")
		}
	}
}

// The manifest and the transfer both name the same contract: an unnamed phone
// gets the oldest still-readable one, never the current schema.
func TestClientVersionNamesTheContractAPhoneReceives(t *testing.T) {
	for _, tc := range []struct{ stored, requested, want string }{
		{"3", "", "1"},
		{"3", "1", "1"},
		{"3", "2", "2"},
		{"3", "3", "3"},
		{"2", "3", "2"},
		{"2", "", "1"},
		{"3", "9", "3"},
		{"custom", "3", ""},
	} {
		if got := ClientVersion(tc.stored, tc.requested); got != tc.want {
			t.Fatalf("ClientVersion(%q, %q) = %q, want %q", tc.stored, tc.requested, got, tc.want)
		}
	}
}
