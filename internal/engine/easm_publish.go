package engine

import (
	"encoding/json"
	"fmt"
	"math/big"
	"sort"

	"github.com/LubyRuffy/eino-swarm/internal/taskpanel"
)

func easmValidateAgentPublication(panel taskpanel.Panel, update taskpanel.Update) error {
	next, err := taskpanel.Apply(panel.Definition, panel.State, update)
	if err != nil {
		return err
	}
	before, err := easmPublicationValues(panel.State)
	if err != nil {
		return err
	}
	after, err := easmPublicationValues(next)
	if err != nil {
		return err
	}
	beforeHash, _ := taskpanel.Digest(before)
	afterHash, _ := taskpanel.Digest(after)
	if beforeHash != afterHash {
		return fmt.Errorf("%w: EASM query scope and observed metrics are maintained by Go; select the current range with open_task_panel(query)", taskpanel.ErrInvalid)
	}
	if err := easmValidateAgentGraph(panel.State, next); err != nil {
		return err
	}
	return easmValidateAgentConfidence(panel.State, next)
}

// Descriptive enrichment may change, while the exact source values stay owned
// by the host. Rational comparison retains large integers without float loss.
func easmPublicationValues(raw json.RawMessage) (map[string]any, error) {
	var state struct {
		Scope json.RawMessage `json:"query_scope"`
		Heat  []struct {
			Date  string
			Value json.RawMessage
		}
		Summary    map[string]json.RawMessage
		Components []struct {
			Name  string
			Count json.RawMessage
		}
	}
	if err := json.Unmarshal(raw, &state); err != nil {
		return nil, err
	}
	values := map[string]any{"query_scope": state.Scope}
	if len(state.Scope) == 0 {
		return values, nil
	}
	heat := make([][2]string, 0, len(state.Heat))
	for _, point := range state.Heat {
		value, err := easmPublicationNumber(point.Value)
		if err != nil {
			return nil, err
		}
		heat = append(heat, [2]string{point.Date, value})
	}
	components := make([][2]string, 0, len(state.Components))
	for _, component := range state.Components {
		count, err := easmPublicationNumber(component.Count)
		if err != nil {
			return nil, err
		}
		components = append(components, [2]string{component.Name, count})
	}
	sort.Slice(components, func(i, j int) bool {
		if components[i][0] != components[j][0] {
			return components[i][0] < components[j][0]
		}
		return components[i][1] < components[j][1]
	})
	values["heat"], values["components"] = heat, components
	for _, key := range []string{"fofa_assets", "fofa_ips"} {
		number, err := easmPublicationNumber(state.Summary[key])
		if err != nil {
			return nil, err
		}
		values[key] = number
	}
	return values, nil
}

func easmPublicationNumber(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", nil
	}
	var number json.Number
	if err := json.Unmarshal(raw, &number); err != nil {
		return "", err
	}
	value, ok := new(big.Rat).SetString(number.String())
	if !ok {
		return "", fmt.Errorf("invalid source metric number")
	}
	return value.RatString(), nil
}
