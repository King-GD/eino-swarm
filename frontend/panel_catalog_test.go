package frontend

import (
	"encoding/json"
	"testing"
)

func TestTaskPanelCatalogCannotBeChangedByCaller(t *testing.T) {
	first := TaskPanelCatalog()
	if !json.Valid(first) {
		t.Fatal("plugin definitions must be build-readable JSON")
	}
	first[0] = '!'
	if !json.Valid(TaskPanelCatalog()) {
		t.Fatal("a caller changed the embedded plugin catalog")
	}
}

func TestEASMV1DefinitionCannotBeChangedByCaller(t *testing.T) {
	first := EASMV1Definition()
	first[0] = '!'
	if !json.Valid(EASMV1Definition()) {
		t.Fatal("a caller changed the published EASM definition")
	}
}
