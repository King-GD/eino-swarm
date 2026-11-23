package mcpclient

import (
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	missingRequiredArgumentsCode = "missing_required_arguments"
	wrongArgumentTypeCode        = "wrong_argument_type"
)

// checkArguments checks explicit top-level requirements and property types
// from the connected session's catalog. It leaves defaults, references and
// other schema constraints to the server.
func checkArguments(inputSchema any, arguments map[string]json.RawMessage) (*mcp.CallToolResult, string) {
	raw, err := json.Marshal(inputSchema)
	if err != nil {
		return nil, ""
	}
	var schema map[string]json.RawMessage
	if json.Unmarshal(raw, &schema) != nil || schema["$ref"] != nil {
		return nil, ""
	}
	var properties map[string]json.RawMessage
	if raw := schema["properties"]; raw != nil && json.Unmarshal(raw, &properties) != nil {
		return nil, ""
	}
	if missing := missingFields(schema["required"], properties, arguments); len(missing) != 0 {
		return argumentRejection(fmt.Sprintf("Missing required MCP arguments: %q. Supply these fields in a new call.", missing)), missingRequiredArgumentsCode
	}
	fields := make([]string, 0, len(arguments))
	for field := range arguments {
		fields = append(fields, field)
	}
	slices.Sort(fields)
	for _, field := range fields {
		if detail := checkArgumentType(properties[field], arguments[field], field); detail != "" {
			return argumentRejection("Invalid MCP argument " + detail + ". Supply a value with the declared type in a new call."), wrongArgumentTypeCode
		}
	}
	return nil, ""
}

func missingFields(requiredSchema json.RawMessage, properties, arguments map[string]json.RawMessage) []string {
	var required []*string
	if json.Unmarshal(requiredSchema, &required) != nil {
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
	return missing
}

func argumentRejection(detail string) *mcp.CallToolResult {
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: detail}}}
}

func checkArgumentType(rawSchema, value json.RawMessage, path string) string {
	var schema map[string]json.RawMessage
	if json.Unmarshal(rawSchema, &schema) != nil || schema["$ref"] != nil {
		return ""
	}
	actual := argumentType(value)
	if types := declaredArgumentTypes(schema["type"]); len(types) != 0 && !slices.Contains(types, actual) {
		if actual != "number" || !slices.Contains(types, "integer") || isFraction(value) {
			return fmt.Sprintf("%q: expected %s, got %s", path, strings.Join(types, " or "), actual)
		}
	}
	// A tuple can assign a separate schema to each slot. Only the
	// homogeneous form is interpreted here.
	items := schema["items"]
	if actual != "array" || len(items) == 0 || items[0] != '{' || schema["prefixItems"] != nil {
		return ""
	}
	var elements []json.RawMessage
	if json.Unmarshal(value, &elements) != nil {
		return ""
	}
	for i, element := range elements {
		if detail := checkArgumentType(items, element, fmt.Sprintf("%s[%d]", path, i)); detail != "" {
			return detail
		}
	}
	return ""
}

func declaredArgumentTypes(raw json.RawMessage) []string {
	var types []string
	var single string
	if json.Unmarshal(raw, &single) == nil {
		types = []string{single}
	} else if json.Unmarshal(raw, &types) != nil {
		return nil
	}
	for _, name := range types {
		switch name {
		case "array", "boolean", "integer", "null", "number", "object", "string":
		default:
			return nil
		}
	}
	return types
}

// value was decoded as a member of a JSON object or array, so its first byte
// identifies its type. Numeric values retain their original bytes.
func argumentType(value json.RawMessage) string {
	switch value[0] {
	case '[':
		return "array"
	case 't', 'f':
		return "boolean"
	case 'n':
		return "null"
	case '{':
		return "object"
	case '"':
		return "string"
	default:
		return "number"
	}
}

// Leave extreme values and fractions rounded to an integer to the server.
func isFraction(value json.RawMessage) bool {
	number, err := strconv.ParseFloat(string(value), 64)
	return err == nil && number != math.Trunc(number)
}
