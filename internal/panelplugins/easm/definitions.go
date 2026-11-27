package easm

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/LubyRuffy/eino-swarm/frontend"
	"github.com/LubyRuffy/eino-swarm/internal/taskpanel"
)

const (
	PanelID              = "easm-asset-portrait"
	LegacyPanelVersion   = "1"
	PreviousPanelVersion = "2"
	PanelVersion         = "3"
)

// PanelVersions lists every published EASM contract, oldest first. The first
// entry is what a client that names no version receives.
func PanelVersions() ([]taskpanel.Definition, error) {
	return decodePanelDefinitions(frontend.EASMV1Definition(), frontend.EASMV2Definition(), frontend.TaskPanelCatalog())
}

// PanelDefinitions returns the first published contract and the current one
// for the host's migration and paired phone reads.
func PanelDefinitions() (legacy, current taskpanel.Definition, err error) {
	published, err := PanelVersions()
	if err != nil {
		return legacy, current, err
	}
	return published[0], published[len(published)-1], nil
}

func decodePanelDefinitions(v1JSON, v2JSON, catalogJSON []byte) ([]taskpanel.Definition, error) {
	var published []taskpanel.Definition
	for _, legacy := range []struct {
		name, version string
		body          []byte
	}{{"v1", LegacyPanelVersion, v1JSON}, {"v2", PreviousPanelVersion, v2JSON}} {
		var definition taskpanel.Definition
		if err := json.Unmarshal(legacy.body, &definition); err != nil {
			return nil, fmt.Errorf("decode EASM %s panel definition: %w", legacy.name, err)
		}
		if definition.ID != PanelID || definition.Version != legacy.version {
			return nil, fmt.Errorf("unexpected EASM %s panel identity %q v%s", legacy.name, definition.ID, definition.Version)
		}
		published = append(published, definition)
	}
	var catalog []taskpanel.Definition
	if err := json.Unmarshal(catalogJSON, &catalog); err != nil {
		return nil, fmt.Errorf("decode task panel catalog: %w", err)
	}
	for _, definition := range catalog {
		if definition.ID == PanelID && definition.Version == PanelVersion {
			return append(published, definition), nil
		}
	}
	return nil, fmt.Errorf("EASM v%s panel definition is missing from the catalog", PanelVersion)
}

// ClientVersion names the contract a paired client that asked for requested
// gets for a panel stored at storedVersion. An unmatched request is not
// negotiated here; panelRequestValid refuses it first. An empty request is an
// older phone and receives the oldest published contract.
func ClientVersion(storedVersion, requestedVersion string) string {
	stored, ok := panelVersionNumber(storedVersion)
	if !ok {
		return ""
	}
	if requestedVersion == "" {
		return LegacyPanelVersion
	}
	requested, ok := panelVersionNumber(requestedVersion)
	if !ok || requested >= stored {
		return storedVersion
	}
	return requestedVersion
}

func panelVersionNumber(version string) (int, bool) {
	number, err := strconv.Atoi(version)
	return number, err == nil && number > 0
}
