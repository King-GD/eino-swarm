package mcpclient

import (
	"regexp"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const rateLimitedCode = "rate_limited"

// MCP tool results have no standard structured HTTP status. Recognize an
// explicit HTTP 429 or its status reason in a failed text result.
var rateLimitStatus = regexp.MustCompile(`(?i)\b(?:HTTP[ \t]+(?:status[ \t]+)?429|429[ \t:-]+Too Many Requests)\b`)

func addRateLimitNotice(result *mcp.CallToolResult) bool {
	for _, content := range result.Content {
		if text, ok := content.(*mcp.TextContent); ok && rateLimitStatus.MatchString(text.Text) {
			notice := &mcp.TextContent{Text: "429 Too Many Requests. The remote operation's outcome may be unknown. Verify it before repeating the operation."}
			result.Content = append([]mcp.Content{notice}, result.Content...)
			return true
		}
	}
	return false
}
