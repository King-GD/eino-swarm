package swarm

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

// A name the model invented must come back as a tool result. The other calls
// in that same reply still run, and the run continues.
func TestUnknownToolCallKeepsTheRegisteredToolAndTheRun(t *testing.T) {
	const secret = "argument-must-not-be-echoed"
	var ran atomic.Int32
	reg := NewRegistry()
	reg.ModelBuilder = func(role, agentID string) model.BaseChatModel {
		return &chunkedModel{turns: []turnScript{
			{calls: []schema.ToolCall{
				{ID: "known-call", Type: "function", Function: schema.FunctionCall{Name: "read_note", Arguments: `{}`}},
				{ID: "missing-call", Type: "function", Function: schema.FunctionCall{Name: "missing_tool", Arguments: `{"token":"` + secret + `"}`}},
			}},
			{inspect: func(msgs []*schema.Message) {
				var found string
				for _, msg := range msgs {
					if msg != nil && msg.Role == schema.Tool && msg.ToolCallID == "missing-call" {
						found = msg.Content
					}
				}
				if !strings.Contains(found, `"code":"tool_not_registered"`) || !strings.Contains(found, "missing_tool") {
					t.Errorf("next model request lost the unknown-tool result: %s", found)
				}
				if strings.Contains(found, "toolsNode") || strings.Contains(found, secret) || strings.Contains(found, "read_note") {
					t.Errorf("unknown-tool result leaked the failure, the arguments, or another tool name: %s", found)
				}
			}, content: []string{"continued"}},
		}}
	}
	res, err := reg.RunWith(context.Background(), RunConfig{
		Task: "Check the note.",
		ManagerTools: []tool.BaseTool{&fnTool{name: "read_note", fn: func(context.Context, string) (string, error) {
			ran.Add(1)
			return "note-body", nil
		}}},
	}, nil)
	if err != nil || res.Final != "continued" || ran.Load() != 1 {
		t.Fatalf("run err=%v final=%q executed=%d", err, res.Final, ran.Load())
	}
}

func TestWorkerUnknownToolCallDoesNotFailTheWorker(t *testing.T) {
	const proxy = "mcp_example_call_tool_abcdef"
	var ran atomic.Int32
	reg := NewRegistry()
	modelOpt := func(role, agentID string) model.BaseChatModel {
		return &chunkedModel{turns: []turnScript{
			{calls: []schema.ToolCall{
				{ID: "known-call", Type: "function", Function: schema.FunctionCall{Name: "read_note", Arguments: `{}`}},
				{ID: "remote-call", Type: "function", Function: schema.FunctionCall{Name: "remote_record", Arguments: `{}`}},
			}},
			{inspect: func(msgs []*schema.Message) {
				var found string
				for _, msg := range msgs {
					if msg != nil && msg.Role == schema.Tool && msg.ToolCallID == "remote-call" {
						found = msg.Content
					}
				}
				if !strings.Contains(found, `"code":"tool_not_registered"`) || !strings.Contains(found, `"call_tool":"`+proxy+`"`) || !strings.Contains(found, "remote_record") {
					t.Errorf("worker did not receive the catalog route: %s", found)
				}
			}, content: []string{"continued"}},
		}}
	}
	ctx := WithRemoteToolRoutes(context.Background(), map[string]string{"remote_record": proxy})
	handle, err := reg.Spawn(ctx, "reader", "Read the assigned record.", modelOpt, &fnTool{name: "read_note", fn: func(context.Context, string) (string, error) {
		ran.Add(1)
		return "note-body", nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-handle.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not finish")
	}
	result, err, finished := handle.Result()
	if !finished || err != nil || result != "continued" || ran.Load() != 1 {
		t.Fatalf("worker result=%q err=%v finished=%t executed=%d", result, err, finished, ran.Load())
	}
}

func TestUnknownToolResultNamesOneProxyAndRefusesASharedName(t *testing.T) {
	const proxy = "mcp_example_call_tool_abcdef"
	ctx := WithRemoteToolRoutes(context.Background(), map[string]string{
		"remote_record": proxy,
		"shared_record": "",
	})
	one, err := unknownToolResult(ctx, "remote_record", `{"token":"secret"}`)
	if err != nil || !strings.Contains(one, `"call_tool":"`+proxy+`"`) || strings.Contains(one, "secret") {
		t.Fatalf("single route: %s %v", one, err)
	}
	shared, err := unknownToolResult(ctx, "shared_record", "")
	if err != nil || strings.Contains(shared, `"call_tool"`) || !strings.Contains(shared, "more than one server") {
		t.Fatalf("shared route chose a proxy: %s %v", shared, err)
	}
	plain, err := unknownToolResult(context.Background(), "missing_tool", "")
	if err != nil || strings.Contains(plain, "remote MCP") || !strings.Contains(plain, `"ok":false`) {
		t.Fatalf("unlisted name: %s %v", plain, err)
	}
}

// Catalog refreshes and detached worker setup must not mutate another request's
// correction hint or carry one into an unrelated run.
func TestRemoteToolHintsAreCopiedAndEmptyRoutesDoNotInventAProxy(t *testing.T) {
	base := context.Background()
	routes := map[string]string{"remote_record": "original_proxy", "": "ignored_proxy"}
	ctx := WithRemoteToolRoutes(base, routes)
	routes["remote_record"] = "changed_proxy"
	worker := copyRemoteToolRoutes(ctx, base)
	for _, request := range []context.Context{ctx, worker} {
		out, err := unknownToolResult(request, "remote_record", "")
		if err != nil || !strings.Contains(out, `"call_tool":"original_proxy"`) || strings.Contains(out, "changed_proxy") {
			t.Fatalf("a catalog mutation changed another request: %s %v", out, err)
		}
	}
	for _, request := range []context.Context{
		nil, WithRemoteToolRoutes(nil, routes), WithRemoteToolRoutes(base, nil),
		WithRemoteToolRoutes(base, map[string]string{"": "ignored_proxy"}),
		copyRemoteToolRoutes(nil, base),
	} {
		out, err := unknownToolResult(request, "remote_record", "")
		if err != nil || strings.Contains(out, "call_tool") || strings.Contains(out, "remote MCP") {
			t.Fatalf("an unbound request received a remote hint: %s %v", out, err)
		}
	}
}
