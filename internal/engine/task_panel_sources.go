package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/LubyRuffy/eino-swarm/internal/config"
	"github.com/LubyRuffy/eino-swarm/internal/panelplugins/fofaworld"
)

type panelProducer func(context.Context, json.RawMessage, config.TaskPanelConfig, string, func(context.Context, json.RawMessage) error) (any, error)

// This adapter registry is the only host wiring that knows the bundled examples.
// A new producer does not add business fields to the publication protocol.
func panelProducerFor(id string) panelProducer {
	if id == "fofa-world-map" {
		return runFOFAWorldSource
	}
	return nil
}

func runFOFAWorldSource(ctx context.Context, raw json.RawMessage, settings config.TaskPanelConfig, workspace string, publish func(context.Context, json.RawMessage) error) (any, error) {
	var input struct {
		Query     string `json:"query"`
		MaxAssets int    `json:"max_assets,omitempty"`
	}
	if err := decodeNuwaArgs(string(raw), &input); err != nil {
		return nil, err
	}
	maxAssets := settings.MaxAssets
	if input.MaxAssets < 0 || input.MaxAssets > maxAssets {
		return nil, fmt.Errorf("max_assets exceeds configured source limit")
	}
	if input.MaxAssets > 0 {
		maxAssets = input.MaxAssets
	}
	return fofaworld.Run(ctx, fofaworld.Input{Query: input.Query, ClientPath: settings.FOFAClient, Workspace: workspace, MaxAssets: maxAssets, BatchSize: min(settings.BatchSize, maxAssets), Concurrency: settings.Concurrency, Timeout: time.Duration(settings.TimeoutSeconds) * time.Second, FlushInterval: time.Duration(settings.FlushIntervalMS) * time.Millisecond}, publish)
}
