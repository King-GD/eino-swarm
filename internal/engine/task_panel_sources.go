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

// panelProducer is one registered source. current is the panel state the call
// starts from, so a producer can merge into what the user already sees.
type panelProducer func(context.Context, json.RawMessage, json.RawMessage, config.TaskPanelConfig, string, func(context.Context, json.RawMessage) error) (any, error)

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

func runFOFAWorldSource(ctx context.Context, raw json.RawMessage, _ json.RawMessage, settings config.TaskPanelConfig, workspace string, publish func(context.Context, json.RawMessage) error) (any, error) {
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

func (e *Engine) runEASMSource(ctx context.Context, raw json.RawMessage, current json.RawMessage, settings config.TaskPanelConfig, workspace string, publish func(context.Context, json.RawMessage) error) (any, error) {
	var input easm.Input
	if err := decodeNuwaArgs(string(raw), &input); err != nil {
		return nil, err
	}
	tools := make(map[string]string, len(settings.EASM.Tools))
	for role, name := range settings.EASM.Tools {
		tools[role] = name
	}
	caller := &easmCaller{manager: e.mcp, servers: e.cfg.Snapshot().MCP.Servers, serverID: settings.EASM.ServerID, workspace: workspace, admitted: mcpAdmitted(ctx)}
	return easm.Run(ctx, input, easm.Settings{Tools: tools, MaxDomains: settings.EASM.MaxDomains, MaxComponents: settings.EASM.MaxComponents}, current, caller, publish)
}

// mcpAdmissionKey carries whether the calling turn may use remote MCP tools.
// A producer reached without it is treated as not admitted.
type mcpAdmissionKey struct{}

func withMCPAdmission(ctx context.Context, admitted bool) context.Context {
	return context.WithValue(ctx, mcpAdmissionKey{}, admitted)
}

func mcpAdmitted(ctx context.Context) bool {
	admitted, _ := ctx.Value(mcpAdmissionKey{}).(bool)
	return admitted
}

// easmCaller resolves each tool to one configured MCP server and maps
// transport outcomes to the producer's step results. A turn that does not
// mount MCP tools gets no remote call: every step reports unavailable.
type easmCaller struct {
	manager   *mcpclient.Manager
	servers   []config.MCPServer
	serverID  string
	workspace string
	admitted  bool
}

func (c *easmCaller) server(tool string) (config.MCPServer, bool) {
	if c.manager == nil || !c.admitted {
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
