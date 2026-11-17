package swarm

import (
	"context"
	"encoding/json"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
)

type remoteToolRouteKey struct{}

type unknownToolPayload struct {
	OK         bool   `json:"ok"`
	Code       string `json:"code"`
	Tool       string `json:"tool"`
	Error      string `json:"error"`
	CallTool   string `json:"call_tool,omitempty"`
	RemoteName string `json:"remote_name,omitempty"`
}

const (
	unknownToolCode = "tool_not_registered"

	unknownToolError = "This name is not a function in the current tool list. Call only functions provided for this request."

	unknownRemoteError = "This name is not a function in the current tool list. It is a remote MCP tool. Call the call_tool function and pass this exact name in its name argument."

	unknownSharedRemoteError = "This name is not a function in the current tool list. It is a remote MCP tool listed by more than one server. Use that server's call_tool proxy and pass this exact name in its name argument."
)

// WithRemoteToolRoutes records remote catalog names that are not functions in
// this request. A call to one of those names tells the model which call_tool
// proxy must receive it. An empty proxy means the name is listed by more than
// one server, so the result does not choose one.
func WithRemoteToolRoutes(ctx context.Context, routes map[string]string) context.Context {
	if ctx == nil || len(routes) == 0 {
		return ctx
	}
	cloned := make(map[string]string, len(routes))
	for name, call := range routes {
		if name == "" {
			continue
		}
		cloned[name] = call
	}
	if len(cloned) == 0 {
		return ctx
	}
	return context.WithValue(ctx, remoteToolRouteKey{}, cloned)
}

func copyRemoteToolRoutes(parent, detached context.Context) context.Context {
	if parent == nil {
		return detached
	}
	routes, _ := parent.Value(remoteToolRouteKey{}).(map[string]string)
	return WithRemoteToolRoutes(detached, routes)
}

// RegisteredToolsNode executes one model's tools. A name outside that list
// becomes a tool result; it does not fail the agent or cancel the other calls
// in the same reply.
func RegisteredToolsNode(tools []tool.BaseTool, sequential bool) compose.ToolsNodeConfig {
	return compose.ToolsNodeConfig{
		Tools:               tools,
		ExecuteSequentially: sequential,
		UnknownToolsHandler: unknownToolResult,
	}
}

func unknownToolResult(ctx context.Context, name, _ string) (string, error) {
	payload := unknownToolPayload{OK: false, Code: unknownToolCode, Tool: name, Error: unknownToolError}
	if ctx != nil {
		if routes, _ := ctx.Value(remoteToolRouteKey{}).(map[string]string); routes != nil {
			if call, ok := routes[name]; ok {
				payload.RemoteName = name
				payload.Error = unknownSharedRemoteError
				if call != "" {
					payload.CallTool = call
					payload.Error = unknownRemoteError
				}
			}
		}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return `{"ok":false,"code":"tool_not_registered","error":"This name is not a function in the current tool list. Call only functions provided for this request."}`, nil
	}
	return string(body), nil
}
