package remote

import (
	"encoding/json"
	"fmt"

	"github.com/LubyRuffy/eino-swarm/internal/panelplugins/easm"
	"github.com/LubyRuffy/eino-swarm/internal/taskpanel"
)

func projectEASMPanel(panel taskpanel.Panel, change *taskpanel.Change, version string) (taskpanel.Panel, *taskpanel.Change, error) {
	legacy, current, err := easm.PanelDefinitions()
	if err != nil {
		return taskpanel.Panel{}, nil, err
	}
	if panel.PluginVersion == legacy.Version && taskpanel.DefinitionsMatch(panel.Definition, legacy) {
		return panel, change, nil
	}
	if panel.PluginVersion != current.Version || !taskpanel.DefinitionsMatch(panel.Definition, current) {
		return taskpanel.Panel{}, nil, fmt.Errorf("unknown EASM panel definition")
	}
	if version == current.Version {
		return panel, change, nil
	}
	state, err := legacyEASMState(panel.State)
	if err != nil {
		return taskpanel.Panel{}, nil, err
	}
	if err := taskpanel.ValidateState(legacy, state); err != nil {
		return taskpanel.Panel{}, nil, err
	}
	panel.Definition, panel.PluginVersion, panel.State = legacy, legacy.Version, state
	// A patch from the stored definition may refer to fields absent in v1.
	return panel, nil, nil
}

func legacyEASMState(raw json.RawMessage) (json.RawMessage, error) {
	var state, collection map[string]json.RawMessage
	if err := json.Unmarshal(raw, &state); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(state["collection"], &collection); err != nil {
		return nil, err
	}
	var steps []json.RawMessage
	if err := json.Unmarshal(collection["steps"], &steps); err != nil {
		return nil, err
	}
	if steps == nil {
		return nil, fmt.Errorf("EASM collection steps must be an array")
	}
	retained := make([]json.RawMessage, 0, len(steps))
	for _, rawStep := range steps {
		var step struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(rawStep, &step); err != nil {
			return nil, err
		}
		if step.ID != string(easm.StepClouds) {
			retained = append(retained, rawStep)
		}
	}
	var err error
	collection["steps"], err = json.Marshal(retained)
	if err != nil {
		return nil, err
	}
	state["collection"], err = json.Marshal(collection)
	if err != nil {
		return nil, err
	}
	delete(state, "clouds")
	return json.Marshal(state)
}
