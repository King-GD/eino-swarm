package config

import (
	"encoding/json"
	"testing"
)

func TestChatThinkingOffFollowsEndpointDialect(t *testing.T) {
	for endpoint, want := range map[string]string{
		"https://open.bigmodel.cn/api/paas/v4":                   `{"thinking":{"type":"disabled"}}`,
		"https://api.z.ai:443/api/coding/paas/v4":                `{"thinking":{"type":"disabled"}}`,
		"https://openrouter.ai/api/v1":                           `{"reasoning":{"enabled":false}}`,
		"https://dashscope-intl.aliyuncs.com/compatible-mode/v1": `{"enable_thinking":false}`,
		"https://gateway.example/v1":                             `{"reasoning_effort":"none"}`,
		"https://openrouter.ai.example/v1":                       `{"reasoning_effort":"none"}`,
		":invalid":                                               `{"reasoning_effort":"none"}`,
	} {
		raw, err := json.Marshal(ChatThinkingOffFields(endpoint))
		if err != nil || string(raw) != want {
			t.Errorf("%s: got %s, %v; want %s", endpoint, raw, err, want)
		}
	}
}
