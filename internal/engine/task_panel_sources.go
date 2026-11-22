package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/LubyRuffy/eino-swarm/internal/config"
	"github.com/LubyRuffy/eino-swarm/internal/mcpclient"
	"github.com/LubyRuffy/eino-swarm/internal/panelplugins/easm"
	"github.com/LubyRuffy/eino-swarm/internal/panelplugins/fofaworld"
)

type panelProducer func(context.Context, json.RawMessage, config.TaskPanelConfig, string, func(context.Context, json.RawMessage) error) (any, error)

// This registry is the only host wiring that knows the bundled producers.
// A producer receives its input, settings and a publisher; it never adds
// business fields to the publication protocol or the turn lifecycle.
func (e *Engine) panelProducerFor(id string) panelProducer {
	switch id {
	case "fofa-world-map":
		return runFOFAWorldSource
	case "easm-asset-portrait":
		return e.runEASMSource
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

func (e *Engine) runEASMSource(ctx context.Context, raw json.RawMessage, settings config.TaskPanelConfig, workspace string, publish func(context.Context, json.RawMessage) error) (any, error) {
	var input easm.Input
	if err := decodeNuwaArgs(string(raw), &input); err != nil {
		return nil, err
	}
	tools := make(map[string]string, len(settings.EASM.Tools))
	for role, name := range settings.EASM.Tools {
		tools[role] = name
	}
	caller := &easmCaller{manager: e.mcp, servers: e.cfg.Snapshot().MCP.Servers, serverID: settings.EASM.ServerID, workspace: workspace}
	return easm.Run(ctx, input, easm.Settings{Tools: tools, MaxDomains: settings.EASM.MaxDomains, MaxComponents: settings.EASM.MaxComponents}, caller, publish)
}

// easmCaller resolves each tool to one configured MCP server and maps
// transport outcomes to the producer's step results.
type easmCaller struct {
	manager   *mcpclient.Manager
	servers   []config.MCPServer
	serverID  string
	workspace string
}

func (c *easmCaller) server(tool string) (config.MCPServer, bool) {
	if c.manager == nil {
		return config.MCPServer{}, false
	}
	if c.serverID == "" {
		return c.manager.ServerOffering(c.servers, tool)
	}
	for _, server := range c.servers {
		if server.ID == c.serverID && server.Enabled {
			return server, true
		}
	}
	return config.MCPServer{}, false
}

func (c *easmCaller) Call(ctx context.Context, tool string, arguments map[string]any) (json.RawMessage, error) {
	server, ok := c.server(tool)
	if !ok {
		return nil, easm.ErrUnavailable
	}
	body, err := c.manager.CallTool(ctx, server, c.workspace, tool, arguments)
	var failure *mcpclient.Error
	switch {
	case err == nil:
		return body, nil
	case errors.As(err, &failure) && (failure.Code == "unknown_tool" || failure.Code == "tool_switched_off"):
		return nil, easm.ErrUnavailable
	case errors.As(err, &failure):
		return nil, &easm.ToolError{Code: failure.Code}
	}
	return nil, err
}
