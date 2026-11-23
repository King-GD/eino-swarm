package config

import (
	"net/url"
	"strings"
)

// The empty level inherits configuration. When it stays empty, the model
// request omits reasoning controls. ReasoningNone explicitly turns thinking off.
const (
	ReasoningDefault = ""
	ReasoningNone    = "none"
	ReasoningLow     = "low"
	ReasoningMedium  = "medium"
	ReasoningHigh    = "high"
)

// ReasoningEfforts is the ordered set of explicit levels the UI offers. The
// empty default is rendered as "Default" and is not one of these values.
func ReasoningEfforts() []string {
	return []string{ReasoningNone, ReasoningLow, ReasoningMedium, ReasoningHigh}
}

// NormalizeReasoning maps any input to a known level, falling back to the
// empty default so a typo or an old value never sends an invalid one on the
// wire.
func NormalizeReasoning(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case ReasoningNone:
		return ReasoningNone
	case ReasoningLow:
		return ReasoningLow
	case ReasoningMedium:
		return ReasoningMedium
	case ReasoningHigh:
		return ReasoningHigh
	default:
		return ReasoningDefault
	}
}

// ChatThinkingOffFields selects the documented Chat Completions dialect for
// the endpoint. A gateway can translate the default field into its own upstream
// dialect, so a model name alone must not select a vendor-specific parameter.
func ChatThinkingOffFields(baseURL string) map[string]any {
	endpoint, _ := url.Parse(baseURL)
	host := ""
	if endpoint != nil {
		host = strings.ToLower(endpoint.Hostname())
	}
	switch {
	case host == "open.bigmodel.cn", host == "api.z.ai":
		return map[string]any{"thinking": map[string]any{"type": "disabled"}}
	case host == "openrouter.ai":
		return map[string]any{"reasoning": map[string]any{"enabled": false}}
	case strings.HasPrefix(host, "dashscope") && strings.HasSuffix(host, ".aliyuncs.com"):
		return map[string]any{"enable_thinking": false}
	default:
		return map[string]any{"reasoning_effort": ReasoningNone}
	}
}
