package mcpclient

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestRateLimitNoticeRequiresExplicitHTTPStatus(t *testing.T) {
	for _, tc := range []struct {
		text string
		want bool
	}{
		{"429 Too Many Requests", true},
		{"Upstream HTTP status 429", true},
		{"vendor returned 429: too many requests", true},
		{"query includes record 429", false},
		{"upstream HTTP 4290", false},
		{"error 1429 Too Many Requests", false},
		{"Too Many Requests", false},
	} {
		t.Run(tc.text, func(t *testing.T) {
			image := &mcp.ImageContent{MIMEType: "image/png"}
			original := &mcp.TextContent{Text: tc.text}
			result := &mcp.CallToolResult{IsError: true, Content: []mcp.Content{image, original}}
			if got := addRateLimitNotice(result); got != tc.want {
				t.Fatalf("rate limit classified as %v, want %v", got, tc.want)
			}
			if tc.want {
				if !strings.Contains(result.Content[0].(*mcp.TextContent).Text, "Verify it before repeating") {
					t.Fatal("outcome verification is missing")
				}
				result.Content = result.Content[1:]
			}
			if len(result.Content) != 2 || result.Content[0] != image || result.Content[1] != original || !result.IsError {
				t.Fatalf("original MCP result changed: %+v", result)
			}
		})
	}
}

func TestMCPRateLimitedProxyReturnsErrorWithoutBlockingAdmission(t *testing.T) {
	var calls atomic.Int32
	remote := progressFixture(t, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		calls.Add(1)
		return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{
			&mcp.TextContent{Text: "HTTP 429"},
		}}, nil
	})

	ctx := WithExecution(t.Context(), NewExecution(nil))
	result, err := remote.InvokableRun(ctx, `{"name":"work","arguments":{}}`)
	if err != nil || !strings.Contains(result, `"isError":true`) || !strings.Contains(result, "429 Too Many Requests") {
		t.Fatalf("lost the tool error: result=%q error=%v", result, err)
	}
	if failure := ExecutionFailure(ctx); failure != nil || calls.Load() != 1 {
		t.Fatalf("rate limit blocked admission or replayed the call: failure=%v calls=%d", failure, calls.Load())
	}
}
