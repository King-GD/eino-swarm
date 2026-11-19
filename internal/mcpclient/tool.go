package mcpclient

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/LubyRuffy/eino-swarm/internal/config"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/eino-contrib/jsonschema"
)

// ProxyNames are the two model-facing tools one configured server contributes.
func ProxyNames(serverID string) (list, call string) {
	return toolName(serverID, "list_tools"), toolName(serverID, "call_tool")
}

// ProxyOffered reports whether a model-facing name belongs to an enabled server.
// Names this process did not mint for a configured server stay offered.
func ProxyOffered(servers []config.MCPServer, name string) bool {
	matched := false
	for _, server := range servers {
		list, call := ProxyNames(server.ID)
		if name != list && name != call {
			continue
		}
		matched = true
		if server.Enabled {
			return true
		}
	}
	return !matched
}

func slug(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
		if b.Len() >= 18 {
			break
		}
	}
	return b.String()
}

func toolName(server, name string) string {
	hash := sha256.Sum256([]byte(server + "\x00" + name))
	return fmt.Sprintf("mcp_%s_%s_%x", slug(server), slug(name), hash[:6])
}

// DirectToolName is the model-facing function for one remote tool. Its hash
// input is distinct from list_tools and call_tool, so a remote tool with one
// of those names does not collide with the catalog proxies.
func DirectToolName(server, remote string) string {
	hash := sha256.Sum256([]byte(server + "\x00direct\x00" + remote))
	return fmt.Sprintf("mcp_%s_%s_%x", slug(server), slug(remote), hash[:6])
}

func toolInfo(d ToolDescriptor) (*schema.ToolInfo, error) {
	raw, err := json.Marshal(d.InputSchema)
	if err != nil {
		return nil, err
	}
	var params jsonschema.Schema
	if err = json.Unmarshal(raw, &params); err != nil {
		return nil, err
	}
	if params.Type != "object" && (len(params.TypeEnhanced) != 1 || params.TypeEnhanced[0] != "object") {
		return nil, fmt.Errorf("MCP tool schema must be an object")
	}
	return &schema.ToolInfo{Name: d.Name, Desc: d.Description, ParamsOneOf: schema.NewParamsOneOfByJSONSchema(&params)}, nil
}

// A toolset binds the first session it uses. A later call cannot silently
// replace server-side state after eviction, configuration change or failure.
type sessionHolder struct {
	mu        sync.Mutex
	manager   *Manager
	server    config.MCPServer
	workspace string
	entry     *entry
	renewed   *config.MCPServer
}

func (h *sessionHolder) acquire() (*entry, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.entry != nil {
		return h.entry, h.manager.retain(h.entry)
	}
	server := h.server
	if h.renewed != nil {
		server = *h.renewed
	}
	e, err := h.manager.acquire(server, h.workspace)
	if err == nil {
		h.entry = e
	}
	return e, err
}

// Refresh only the binding this call observed. A sibling may already have
// installed the replacement while the expired request was unwinding.
func (h *sessionHolder) refresh(stale *entry) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.entry != stale {
		return true
	}
	server, ok := h.manager.refreshServer(h.server)
	if ok {
		h.entry = nil
		h.renewed = &server
	}
	return ok
}

func (h *sessionHolder) renewExpired(ctx context.Context, stale *entry, code string) bool {
	return code == "session_expired" && execution(ctx) != nil && ctx.Err() == nil &&
		ExecutionFailure(ctx) == nil && h.refresh(stale)
}

type externalTool struct {
	holder *sessionHolder
	list   bool
	index  string
}

const mcpRouteGuidance = "Before a local command or a direct API call, query this catalog for the task. The server title is not evidence that no tool matches. Prefer a matching MCP tool unless the user specifies another route. A past failure alone does not justify bypassing this server. "

func (t *externalTool) Info(context.Context) (*schema.ToolInfo, error) {
	name := "call_tool"
	description := "Call one tool on this configured MCP server. First use its list_tools proxy to inspect exact remote names and JSON input schemas. Copy a returned remote name into name and supply its arguments object. An expired session may be renewed once when the call was refused before execution."
	input := map[string]any{"type": "object", "properties": map[string]any{"name": map[string]any{"type": "string", "description": "Exact remote name returned by this server's list_tools proxy."}, "arguments": map[string]any{"type": "object", "description": "Arguments conforming to the remote tool's input_schema."}}, "required": []string{"name", "arguments"}}
	if t.list {
		name = "list_tools"
		description = fmt.Sprintf("Query this configured MCP server's tool catalog without executing a remote tool. A query returns matching names and descriptions. Pass name to retrieve one complete JSON input schema before using this server's call_tool proxy. Use offset/limit to page (default limit %d). If a result is truncated, narrow the query or one name; an individual schema that still exceeds the result budget requires increasing tools.result_max_chars in Settings.", config.DefaultMCPToolPageSize)
		input = map[string]any{"type": "object", "properties": map[string]any{
			"query":  map[string]any{"type": "string", "description": "Optional case-insensitive match against remote names and descriptions. Omit it to list the enabled catalog."},
			"name":   map[string]any{"type": "string", "description": "Optional exact remote tool name; returns only that tool's complete schema."},
			"offset": map[string]any{"type": "integer", "minimum": 0, "description": "First tool index in the filtered catalogue."},
			"limit":  map[string]any{"type": "integer", "minimum": 1, "description": fmt.Sprintf("Maximum tools to return; default %d.", config.DefaultMCPToolPageSize)},
		}}
	}
	label := t.holder.server.Name
	if label == "" {
		label = t.holder.server.ID
	}
	description = fmt.Sprintf("MCP server %q (id %q). %s%s", label, t.holder.server.ID, mcpRouteGuidance, description)
	if t.index != "" {
		description += " Enabled tools: " + t.index + "."
	}
	return toolInfo(ToolDescriptor{Name: toolName(t.holder.server.ID, name), Description: description, InputSchema: input})
}

func (t *externalTool) failure(code string) string {
	return jsonResult(map[string]any{"error": &Error{Code: code, ServerID: t.holder.server.ID}})
}

func (t *externalTool) switchedOff(name string) bool {
	if t == nil || t.holder == nil || t.holder.manager == nil {
		return false
	}
	return t.holder.manager.remoteDisabled(t.holder.server.ID, name)
}

func switchedOffRemoteResult(name string) string {
	encoded, _ := json.Marshal(name)
	return `{"ok":false,"code":"tool_switched_off","tool":` + string(encoded) + `}`
}

func withoutSwitchedOffTools(tools []ToolDescriptor, off func(string) bool) []ToolDescriptor {
	selected := make([]ToolDescriptor, 0, len(tools))
	for _, remote := range tools {
		if off(remote.Name) || off(remote.OriginalName) {
			continue
		}
		selected = append(selected, remote)
	}
	return selected
}

func (t *externalTool) InvokableRun(ctx context.Context, args string, _ ...tool.Option) (string, error) {
	if ctx.Err() != nil {
		return t.failure("cancelled"), nil
	}
	var input map[string]json.RawMessage
	if json.Unmarshal([]byte(args), &input) != nil || input == nil {
		markExecutionFailed(ctx, "invalid_arguments", t.holder.server.ID)
		return t.failure("invalid_arguments"), nil
	}
	var request struct {
		Name      string                     `json:"name"`
		Arguments map[string]json.RawMessage `json:"arguments"`
	}
	var listing struct {
		Query  string `json:"query"`
		Name   string `json:"name"`
		Offset int    `json:"offset"`
		Limit  *int   `json:"limit"`
	}
	if t.list && (json.Unmarshal([]byte(args), &listing) != nil || listing.Offset < 0 || listing.Limit != nil && *listing.Limit < 1) {
		markExecutionFailed(ctx, "invalid_arguments", t.holder.server.ID)
		return t.failure("invalid_arguments"), nil
	}
	if !t.list && (json.Unmarshal([]byte(args), &request) != nil || request.Name == "" || request.Arguments == nil) {
		markExecutionFailed(ctx, "invalid_arguments", t.holder.server.ID)
		return t.failure("invalid_arguments"), nil
	}
	if !t.list {
		if t.switchedOff(request.Name) {
			markExecutionFailed(ctx, "tool_switched_off", t.holder.server.ID)
			return switchedOffRemoteResult(request.Name), nil
		}
		return t.holder.runCall(ctx, request.Name, request.Arguments, false), nil
	}
	e, catalog, code := t.holder.listOnce(ctx)
	if t.holder.renewExpired(ctx, e, code) {
		e, catalog, code = t.holder.listOnce(ctx)
	}
	if code != "ok" {
		markExecutionFailed(ctx, code, t.holder.server.ID)
		return t.failure(code), nil
	}
	selected := withoutSwitchedOffTools(catalog.Tools, t.switchedOff)
	if listing.Name != "" {
		matched := []ToolDescriptor{}
		for _, d := range selected {
			if d.Name == listing.Name || d.OriginalName == listing.Name {
				matched = append(matched, d)
			}
		}
		selected = matched
	} else {
		selected = filterToolQuery(selected, listing.Query)
	}
	total := len(selected)
	offset := min(listing.Offset, total)
	limit := config.DefaultMCPToolPageSize
	if listing.Limit != nil {
		limit = *listing.Limit
	}
	end := offset + min(limit, total-offset)
	page := append([]ToolDescriptor(nil), selected[offset:end]...)
	if listing.Name == "" {
		for i := range page {
			page[i].InputSchema = nil
		}
	}
	t.holder.manager.RememberCatalog(e.server, catalog)
	return jsonResult(struct {
		Catalog
		Total  int  `json:"total"`
		Offset int  `json:"offset"`
		More   bool `json:"more"`
	}{Catalog{ServerID: catalog.ServerID, Tools: page}, total, offset, end < total}), nil
}

func (h *sessionHolder) listOnce(ctx context.Context) (*entry, Catalog, string) {
	e, err := h.acquire()
	if err != nil {
		return e, Catalog{}, err.(*Error).Code
	}
	_, catalog, err := e.connect(ctx)
	var waiting *waiterCancellation
	defer func() { h.manager.release(e, err != nil && !errors.As(err, &waiting)) }()
	if err == nil {
		return e, catalog, "ok"
	}
	if ctx.Err() != nil {
		return e, Catalog{}, "cancelled"
	}
	code := "connection_failed"
	if failure, ok := err.(*Error); ok {
		code = failure.Code
	}
	if code != "session_expired" {
		markExecutionFailed(ctx, code, h.server.ID)
	}
	return e, Catalog{}, code
}
