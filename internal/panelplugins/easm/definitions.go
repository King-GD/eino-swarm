package easm

import (
	"encoding/json"
	"fmt"

	"github.com/LubyRuffy/eino-swarm/frontend"
	"github.com/LubyRuffy/eino-swarm/internal/taskpanel"
)

const (
	PanelID            = "easm-asset-portrait"
	LegacyPanelVersion = "1"
	PanelVersion       = "2"
)

// PanelDefinitions returns the published EASM v1 and the current bundled
// definition for the host's migration and paired phone reads.
func PanelDefinitions() (legacy, current taskpanel.Definition, err error) {
	return decodePanelDefinitions(frontend.EASMV1Definition(), frontend.TaskPanelCatalog())
}

func decodePanelDefinitions(legacyJSON, catalogJSON []byte) (legacy, current taskpanel.Definition, err error) {
	if err = json.Unmarshal(legacyJSON, &legacy); err != nil {
		return legacy, current, fmt.Errorf("decode EASM v1 panel definition: %w", err)
	}
	if legacy.ID != PanelID || legacy.Version != LegacyPanelVersion {
		return legacy, current, fmt.Errorf("unexpected EASM v1 panel identity %q v%s", legacy.ID, legacy.Version)
	}
	var catalog []taskpanel.Definition
	if err = json.Unmarshal(catalogJSON, &catalog); err != nil {
		return legacy, current, fmt.Errorf("decode task panel catalog: %w", err)
	}
	for _, definition := range catalog {
		if definition.ID == PanelID && definition.Version == PanelVersion {
			return legacy, definition, nil
		}
	}
	return legacy, current, fmt.Errorf("EASM v%s panel definition is missing from the catalog", PanelVersion)
}
