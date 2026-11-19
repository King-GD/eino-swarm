package mcpclient

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestRequiredArgumentsReportMissingFieldsBeforeDispatch(t *testing.T) {
	for _, tc := range []struct {
		name, schema, args string
		want               []string
	}{
		{"declared fields only", `{"type":"object","required":["scope","record","region"],"properties":{"region":{"default":"all"},"optional":{}}}`,
			`{"secret":"sentinel-value"}`, []string{`"scope"`, `"record"`}},
		{"already supplied", `{"type":"object","required":["scope","region"],"properties":{"region":{"default":null}}}`, `{"scope":null}`, nil},
		{"explicit empty name", `{"type":"object","required":[""]}`, `{}`, []string{`""`}},
		{"optional input", `{"type":"object","properties":{"scope":{"type":"string"}}}`, `{}`, nil},
		{"schema keywords are case-sensitive", `{"type":"object","Required":["scope"]}`, `{}`, nil},
		{"nested validation stays remote", `{"type":"object","properties":{"filter":{"required":["kind"]}}}`, `{"filter":{}}`, nil},
		{"root ref can replace required", `{"type":"object","$ref":"#/definitions/custom","required":["scope"]}`, `{}`, nil},
		{"malformed required values", `{"type":"object","required":["scope",null]}`, `{}`, nil},
		{"malformed required keyword", `{"type":"object","required":true}`, `{}`, nil},
		{"malformed properties", `{"type":"object","required":["scope"],"properties":true}`, `{}`, nil},
		{"non-object schema", `true`, `{}`, nil},
		{"invalid schema input", `!`, `{}`, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var args map[string]json.RawMessage
			if err := json.Unmarshal([]byte(tc.args), &args); err != nil {
				t.Fatal(err)
			}
			result := checkRequiredArguments(json.RawMessage(tc.schema), args)
			if len(tc.want) == 0 {
				if result != nil {
					t.Fatalf("unexpected local rejection: %s", jsonResult(result))
				}
				return
			}
			if result == nil || !result.IsError || len(result.Content) != 1 {
				t.Fatalf("missing structured rejection: %+v", result)
			}
			content := result.Content[0].(*mcp.TextContent).Text
			fields := strings.TrimSuffix(strings.TrimPrefix(content, "Missing required MCP arguments: ["), "]. Supply these fields in a new call.")
			if !slices.Equal(strings.Fields(fields), tc.want) {
				t.Fatalf("receipt=%q, want only the missing fields %v", content, tc.want)
			}
		})
	}
}
