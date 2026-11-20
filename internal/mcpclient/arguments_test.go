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
			result, code := checkArguments(json.RawMessage(tc.schema), args)
			if len(tc.want) == 0 {
				if result != nil {
					t.Fatalf("unexpected local rejection: %s", jsonResult(result))
				}
				return
			}
			if code != missingRequiredArgumentsCode || result == nil || !result.IsError || len(result.Content) != 1 {
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

func TestArgumentTypeErrorsReportPathsWithoutValues(t *testing.T) {
	for _, tc := range []struct {
		name, property, value, want string
	}{
		{"homogeneous array accepts strings", `{"type":"array","items":{"type":"string"}}`, `["first", "second"]`, ""},
		{"array item", `{"type":"array","items":{"type":"string"}}`, `["ok",872343]`, `"record[1]": expected string, got number`},
		{"nested array item", `{"items":{"items":{"type":"boolean"}}}`, `[[false,872343]]`, `"record[0][1]": expected boolean, got number`},
		{"array itself", `{"type":"array","items":{"type":"string"}}`, `"sensitive-value"`, `"record": expected array, got string`},
		{"optional supplied field", `{"type":"string"}`, `{}`, `"record": expected string, got object`},
		{"nullable union accepts null", `{"type":["object","null"]}`, `null`, ""},
		{"union mismatch", `{"type":["object","boolean"]}`, `null`, `"record": expected object or boolean, got null`},
		{"integer exponent", `{"type":"integer"}`, `12.5e1`, ""},
		{"fraction", `{"type":"integer"}`, `12.5`, `"record": expected integer, got number`},
		{"extreme number deferred", `{"type":"integer"}`, `1e1000`, ""},
		{"number includes integer", `{"type":"number"}`, `1`, ""},
		{"ref resolved remotely", `{"$ref":"#/$defs/record","type":"string"}`, `42`, ""},
		{"tuple resolved remotely", `{"items":[{"type":"string"}]}`, `[42]`, ""},
		{"prefix resolved remotely", `{"prefixItems":[{"type":"number"}],"items":{"type":"string"}}`, `[42,"ok"]`, ""},
		{"boolean subschema resolved remotely", `{"items":false}`, `[42]`, ""},
		{"boolean property schema resolved remotely", `true`, `42`, ""},
		{"malformed type", `{"type":42}`, `42`, ""},
		{"unknown type in union", `{"type":["string","custom"]}`, `42`, ""},
		{"different keyword case", `{"Type":"string"}`, `42`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			schema := json.RawMessage(`{"type":"object","properties":{"record":` + tc.property + `}}`)
			args := map[string]json.RawMessage{"record": json.RawMessage(tc.value)}
			result, code := checkArguments(schema, args)
			if tc.want == "" {
				if result != nil || code != "" {
					t.Fatalf("unexpected local rejection: %s", jsonResult(result))
				}
				return
			}
			if code != wrongArgumentTypeCode || result == nil || !result.IsError {
				t.Fatalf("missing local type rejection: %s (%s)", jsonResult(result), code)
			}
			message := result.Content[0].(*mcp.TextContent).Text
			if !strings.Contains(message, tc.want) || strings.Contains(message, "872343") || strings.Contains(message, "sensitive-value") {
				t.Fatalf("unexpected detail: %q", message)
			}
		})
	}
}
