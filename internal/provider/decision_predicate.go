package provider

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

var (
	ErrDecisionModelUnavailable = errors.New("provider: no authorized Gateway decision model")
	ErrInvalidDecisionResponse  = errors.New("provider: invalid decision predicate response")
)

// DecidePredicate uses the authorized Gateway source serving the conversation.
// A personal provider in LOCAL mode keeps its own route.
func (p *Pool) DecidePredicate(ctx context.Context, providerID, input, name, instructions string, rec Recorder) (probability float64, err error) {
	release, err := p.BeginModelUse()
	if err != nil {
		return 0, err
	}
	defer release()
	source := p.activeAccount()
	if source == nil {
		source = p.accountForExecution(providerID)
	}
	modelName := decisionModelName(source)
	if modelName == "" {
		return 0, ErrDecisionModelUnavailable
	}
	prov := source.provider("")
	started := time.Now()
	var raw []byte
	if rec != nil {
		defer func() {
			rec(CallRecord{ProviderID: prov.ID, Model: modelName, InputMsgs: 1, InputChars: len(input),
				OutputChars: len(raw), Duration: time.Since(started), Err: err})
		}()
	}
	raw, err = requestDecision(ctx, prov, decisionRequest{
		Model: modelName, Input: input,
		Questions: []decisionQuestion{{Type: "predicate", Name: name, Instructions: instructions}},
	})
	if err != nil {
		return 0, err
	}
	return parseDecisionPredicate(raw, name)
}

func decisionModelName(source *AccountSource) string {
	const role = "decisions"
	if source == nil || source.unavailable || !source.allowsRole(role) {
		return ""
	}
	enabled, configured := source.Role(role)
	if configured && !enabled {
		return ""
	}
	if name := source.defaults[role]; name != "" {
		return name
	}
	if enabled {
		return role
	}
	for _, name := range source.names {
		if source.types[name] == role {
			return name
		}
	}
	return ""
}

func parseDecisionPredicate(raw []byte, name string) (float64, error) {
	var response struct {
		Answers []struct {
			Name        string   `json:"name"`
			Probability *float64 `json:"probability"`
		} `json:"answers"`
	}
	if err := json.Unmarshal(raw, &response); err != nil || len(response.Answers) != 1 {
		return 0, ErrInvalidDecisionResponse
	}
	answer := response.Answers[0]
	if answer.Name != name || answer.Probability == nil || *answer.Probability < 0 || *answer.Probability > 1 {
		return 0, ErrInvalidDecisionResponse
	}
	return *answer.Probability, nil
}
