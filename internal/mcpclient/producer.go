package mcpclient

import (
	"context"
	"encoding/json"

	"github.com/LubyRuffy/eino-swarm/internal/config"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// CallTool runs one remote tool for a host-owned panel producer. Unlike the
// model-facing tools, a failure here belongs to that producer step alone: the
// call runs outside the task's execution admission, so a quota error in a
// panel lookup cannot block the agent's own work.
//
// The reply body prefers the single text content when it is JSON, because the
// SDK decodes structured content into float64 and would round large IDs.
func (m *Manager) CallTool(ctx context.Context, server config.MCPServer, workspace, name string, arguments map[string]any) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	encoded := make(map[string]json.RawMessage, len(arguments))
	for key, value := range arguments {
		raw, err := json.Marshal(value)
		if err != nil {
			return nil, &Error{Code: "invalid_arguments", ServerID: server.ID}
		}
		encoded[key] = raw
	}
	holder := &sessionHolder{manager: m, server: cloneServer(server), workspace: workspace}
	isolated := WithExecution(ctx, nil)
	e, result, code := holder.callOnce(isolated, name, encoded, false)
	if holder.renewExpired(ctx, e, code) {
		_, result, code = holder.callOnce(isolated, name, encoded, false)
	}
	if code != "ok" {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return nil, &Error{Code: code, ServerID: server.ID}
	}
	return replyBody(result), nil
}

func replyBody(result *mcp.CallToolResult) json.RawMessage {
	if result == nil {
		return nil
	}
	if len(result.Content) == 1 {
		if text, ok := result.Content[0].(*mcp.TextContent); ok && text != nil && json.Valid([]byte(text.Text)) {
			return json.RawMessage(text.Text)
		}
	}
	if result.StructuredContent != nil {
		body, _ := json.Marshal(result.StructuredContent)
		return body
	}
	return nil
}

// ServerOffering returns the enabled server whose saved catalog lists name.
// It reports ambiguity as a miss: a producer must not pick between two
// servers that happen to expose the same tool name.
func (m *Manager) ServerOffering(servers []config.MCPServer, name string) (config.MCPServer, bool) {
	var found []config.MCPServer
	for _, server := range servers {
		if !server.Enabled || disabled(server, name) {
			continue
		}
		catalog, ok := LoadCatalog(m.catalogDirectory(), server)
		if !ok {
			continue
		}
		for _, tool := range catalog.Tools {
			if tool.OriginalName == name || tool.OriginalName == "" && tool.Name == name {
				found = append(found, server)
				break
			}
		}
	}
	if len(found) != 1 {
		return config.MCPServer{}, false
	}
	return found[0], true
}

func disabled(server config.MCPServer, name string) bool {
	for _, off := range server.DisabledTools {
		if off == name {
			return true
		}
	}
	return false
}
