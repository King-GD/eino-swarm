package remote

import (
	"encoding/json"
	"fmt"

	"github.com/LubyRuffy/eino-swarm/internal/panelplugins/easm"
	"github.com/LubyRuffy/eino-swarm/internal/taskpanel"
)

// projectEASMPanel serves a stored portrait in the contract the calling phone
// understands. A client never receives a schema newer than its bundle: fields
// introduced later are removed instead of being handed to an older renderer.
func projectEASMPanel(panel taskpanel.Panel, change *taskpanel.Change, version string) (taskpanel.Panel, *taskpanel.Change, error) {
	published, err := easm.PanelVersions()
	if err != nil {
		return taskpanel.Panel{}, nil, err
	}
	known := false
	for _, definition := range published {
		if panel.PluginVersion == definition.Version && taskpanel.DefinitionsMatch(panel.Definition, definition) {
			known = true
			break
		}
	}
	if !known {
		return taskpanel.Panel{}, nil, fmt.Errorf("unknown EASM panel definition")
	}
	target := easm.ClientVersion(panel.PluginVersion, version)
	if target == "" || target == panel.PluginVersion {
		return panel, change, nil
	}
	definition, ok := publishedVersion(published, target)
	if !ok {
		return taskpanel.Panel{}, nil, fmt.Errorf("unknown EASM panel definition")
	}
	state, err := projectEASMState(panel.State, definition)
	if err != nil {
		return taskpanel.Panel{}, nil, err
	}
	panel.Definition, panel.PluginVersion, panel.State = definition, definition.Version, state
	// A patch from the newer definition may refer to fields this one lacks.
	return panel, nil, nil
}

func publishedVersion(published []taskpanel.Definition, version string) (taskpanel.Definition, bool) {
	for _, definition := range published {
		if definition.Version == version {
			return definition, true
		}
	}
	return taskpanel.Definition{}, false
}

func projectEASMState(raw json.RawMessage, target taskpanel.Definition) (json.RawMessage, error) {
	state := raw
	var err error
	if target.Version != easm.PanelVersion {
		if state, err = withoutEASMEmails(state); err != nil {
			return nil, err
		}
	}
	if target.Version == easm.LegacyPanelVersion {
		if state, err = legacyEASMState(state); err != nil {
			return nil, err
		}
	}
	if err := taskpanel.ValidateState(target, state); err != nil {
		return nil, err
	}
	return state, nil
}

// withoutEASMEmails drops the contact mailboxes v3 added to node details.
func withoutEASMEmails(raw json.RawMessage) (json.RawMessage, error) {
	var state map[string]json.RawMessage
	if err := json.Unmarshal(raw, &state); err != nil {
		return nil, err
	}
	var nodes []map[string]json.RawMessage
	if err := json.Unmarshal(state["nodes"], &nodes); err != nil {
		return nil, err
	}
	for _, node := range nodes {
		var detail map[string]json.RawMessage
		if err := json.Unmarshal(node["detail"], &detail); err != nil {
			continue
		}
		if _, ok := detail["emails"]; !ok {
			continue
		}
		delete(detail, "emails")
		encoded, err := json.Marshal(detail)
		if err != nil {
			return nil, err
		}
		node["detail"] = encoded
	}
	encoded, err := json.Marshal(nodes)
	if err != nil {
		return nil, err
	}
	state["nodes"] = encoded
	return json.Marshal(state)
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
