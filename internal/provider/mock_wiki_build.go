package provider

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/cloudwego/eino/schema"
)

var mockWikiSource = regexp.MustCompile(`(?m)^\[seq=([0-9]+) kind=[^\n]+\] [a-z]+: ([^\n]*)`)

func mockWikiBuild(turn int, messages []*schema.Message) *schema.Message {
	if turn > 1 {
		return schema.AssistantMessage("Wiki draft submitted.", nil)
	}
	reviewed := []int64{}
	nodes := []map[string]any{}
	for _, message := range messages {
		if message == nil || message.Role != schema.User {
			continue
		}
		for _, match := range mockWikiSource.FindAllStringSubmatch(message.Content, -1) {
			seq, _ := strconv.ParseInt(match[1], 10, 64)
			reviewed = append(reviewed, seq)
			quote := strings.TrimSpace(match[2])
			if quote == "" {
				continue
			}
			if len(quote) > 150 {
				end := 150
				for !utf8.RuneStart(quote[end]) {
					end--
				}
				quote = quote[:end]
			}
			nodes = append(nodes, map[string]any{
				"key": match[1], "title": quote, "kind": "entity", "category": "observation",
				"evidence": map[string]any{"seq": seq, "quote": quote},
			})
		}
	}
	raw, _ := json.Marshal(map[string]any{"reviewed": reviewed, "nodes": nodes, "links": []any{}})
	return schema.AssistantMessage("", []schema.ToolCall{call("mock-wiki-build", "submit_wiki_graph", string(raw))})
}
