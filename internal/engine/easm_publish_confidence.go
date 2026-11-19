package engine

import (
	"encoding/json"
	"fmt"

	"github.com/LubyRuffy/eino-swarm/internal/taskpanel"
)

// Agent publications can add pending evidence or lower confidence; structured
// source projections alone can establish a confirmed relationship.
func easmValidateAgentConfidence(before, after json.RawMessage) error {
	var current, proposed struct {
		Edges []struct {
			Source, Target string
			Confirmed      *bool
		}
	}
	if json.Unmarshal(before, &current) != nil || json.Unmarshal(after, &proposed) != nil {
		return fmt.Errorf("%w: malformed EASM relations", taskpanel.ErrInvalid)
	}
	previous := make(map[[2]string]*bool)
	for _, edge := range current.Edges {
		previous[[2]string{edge.Source, edge.Target}] = edge.Confirmed
	}
	for _, edge := range proposed.Edges {
		prior, exists := previous[[2]string{edge.Source, edge.Target}]
		allowed := edge.Confirmed != nil && !*edge.Confirmed
		if exists && (prior == nil && edge.Confirmed == nil || prior != nil && edge.Confirmed != nil && *prior == *edge.Confirmed) {
			allowed = true
		}
		if !allowed {
			return fmt.Errorf("%w: relation %q -> %q: submit new or unconfirmed evidence as pending (confirmed:false); confirmations belong to structured source updates; retain existing explicit confirmation or downgrade true to false", taskpanel.ErrInvalid, edge.Source, edge.Target)
		}
	}
	return nil
}
