package engine

import (
	"encoding/json"
	"strings"

	jsonpatch "github.com/evanphx/json-patch"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The host accepts this dedicated IP aggregate only for its bound asset query.
// Product bucket sums and heat scope counters describe different observations.
func easmSummaryPatch(state, arguments json.RawMessage, result *mcp.CallToolResult) json.RawMessage {
	patch, _ := easmSummaryObservation(state, arguments, result)
	return patch
}

func easmSummaryObservation(state, arguments json.RawMessage, result *mcp.CallToolResult) (json.RawMessage, bool) {
	if result == nil || result.IsError {
		return nil, false
	}
	var request struct {
		Query, Fields, Tool string
		UniqCount           string `json:"uniq_count"`
	}
	if json.Unmarshal(arguments, &request) != nil || strings.TrimSpace(request.Query) == "" || request.Fields != "ip" || request.Tool != "fofa" || request.UniqCount != "ip" {
		return nil, false
	}
	var reply struct {
		Error    any
		IsError  bool
		Errmsg   string
		Size     json.RawMessage
		Distinct map[string]json.RawMessage
	}
	if json.Unmarshal(easmOrganizationBody(result), &reply) != nil || reply.IsError || reply.Errmsg != "" || reply.Error != nil && reply.Error != false {
		return nil, false
	}
	observed := make(map[string]json.RawMessage)
	for name, raw := range map[string]json.RawMessage{"fofa_assets": reply.Size, "fofa_ips": reply.Distinct["ip"]} {
		if len(raw) == 0 {
			continue
		}
		value := easmProductID(raw)
		if value == "" {
			return nil, false
		}
		observed[name] = json.RawMessage(value)
	}
	if len(observed) == 0 {
		return nil, false
	}
	var previous map[string]json.RawMessage
	if json.Unmarshal(state, &previous) != nil || previous == nil {
		return nil, false
	}
	var summary map[string]json.RawMessage
	if raw, exists := previous["summary"]; exists && json.Unmarshal(raw, &summary) != nil {
		return nil, false
	}
	if summary == nil {
		summary = make(map[string]json.RawMessage)
	}
	delete(summary, "fofa_assets")
	delete(summary, "fofa_ips")
	for name, raw := range observed {
		summary[name] = raw
	}
	next, _ := json.Marshal(summary)
	if jsonpatch.Equal(previous["summary"], next) {
		return nil, true
	}
	patch, _ := json.Marshal([]map[string]any{{"op": "add", "path": "/summary", "value": json.RawMessage(next)}})
	return patch, true
}
