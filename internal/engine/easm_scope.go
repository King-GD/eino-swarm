package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/LubyRuffy/eino-swarm/internal/taskpanel"
)

type easmQueryScope struct {
	ID                 string `json:"id"`
	Query              string `json:"query"`
	ComponentsServerID string `json:"components_server_id,omitempty"`
}

func easmReadQueryScope(state json.RawMessage) (easmQueryScope, bool) {
	var body struct {
		Scope easmQueryScope `json:"query_scope"`
	}
	if json.Unmarshal(state, &body) != nil || body.Scope.ID == "" || strings.TrimSpace(body.Scope.Query) == "" {
		return easmQueryScope{}, false
	}
	return body.Scope, true
}

// A selected range has a separate identity from graph revisions, so an old
// request cannot become current again after visiting another range.
func (e *Engine) selectEASMQueryLocked(ctx context.Context, panel taskpanel.Panel, source taskpanel.Source, query string) (taskpanel.Panel, error) {
	if panel.PluginID != easmPanelID || strings.TrimSpace(query) == "" {
		return panel, fmt.Errorf("%w: a nonempty query is supported only for EASM", taskpanel.ErrInvalid)
	}
	if panel.Source.TurnID != source.TurnID {
		previous, err := e.store.GetTurnContext(ctx, panel.Source.TurnID)
		if err != nil {
			return panel, err
		}
		current, err := e.store.GetTurnContext(ctx, source.TurnID)
		if err != nil {
			return panel, err
		}
		if current.Seq <= previous.Seq {
			return panel, fmt.Errorf("%w: an older turn cannot replace the selected EASM query", taskpanel.ErrConflict)
		}
	}
	scope, selected := easmReadQueryScope(panel.State)
	operations := json.RawMessage(`[]`)
	if !selected || scope.Query != query {
		scope = easmQueryScope{ID: easmOperationID("query", source), Query: query}
		operations, _ = json.Marshal([]map[string]any{
			{"op": "add", "path": "/query_scope", "value": scope},
			{"op": "add", "path": "/summary", "value": map[string]any{}},
			{"op": "replace", "path": "/heat", "value": []any{}},
			{"op": "replace", "path": "/components", "value": []any{}},
		})
		var state map[string]json.RawMessage
		if json.Unmarshal(panel.State, &state) != nil || state == nil {
			return panel, fmt.Errorf("%w: EASM state is not an object", taskpanel.ErrInvalid)
		}
		state["query_scope"], _ = json.Marshal(scope)
		candidate, _ := json.Marshal(state)
		if err := taskpanel.ValidateState(panel.Definition, candidate); err != nil {
			return panel, fmt.Errorf("%w: this panel's pinned schema cannot select an EASM query; open a fresh EASM panel", taskpanel.ErrInvalid)
		}
	} else if panel.Source.TurnID == source.TurnID {
		return panel, nil
	}
	return e.publishTaskPanelLocked(ctx, source, taskpanel.Update{
		ProtocolVersion: taskpanel.ProtocolVersion, PanelID: panel.ID, BaseRevision: panel.Revision,
		OperationID: easmOperationID("query", source), Kind: "patch", Operations: operations,
	})
}
