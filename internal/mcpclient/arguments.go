package mcpclient

import (
	"encoding/json"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const missingRequiredArgumentsCode = "missing_required_arguments"

// checkRequiredArguments checks explicit, top-level requirements from the
// connected session's catalog. Servers may supply default values themselves.
// A root reference can replace its sibling keywords in older schema drafts.
func checkRequiredArguments(inputSchema any, arguments map[string]json.RawMessage) *mcp.CallToolResult {
	raw, err := json.Marshal(inputSchema)
	if err != nil {
		return nil
	}
	var schema map[string]json.RawMessage
	if json.Unmarshal(raw, &schema) != nil || schema["$ref"] != nil {
		return nil
	}
	var required []*string
	if json.Unmarshal(schema["required"], &required) != nil || len(required) == 0 {
		return nil
	}
	var properties map[string]json.RawMessage
	if raw := schema["properties"]; raw != nil && json.Unmarshal(raw, &properties) != nil {
		return nil
	}
	var missing []string
	for _, field := range required {
		if field == nil {
			return nil // Malformed required entries do not establish a constraint.
		}
		if _, present := arguments[*field]; present {
			continue
		}
		var property map[string]json.RawMessage
		_ = json.Unmarshal(properties[*field], &property)
		if property["default"] == nil {
			missing = append(missing, *field)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{
		&mcp.TextContent{Text: fmt.Sprintf("Missing required MCP arguments: %q. Supply these fields in a new call.", missing)},
	}}
}
