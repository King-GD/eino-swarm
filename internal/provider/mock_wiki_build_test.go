package provider

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
)

func TestMockWikiBuildKeepsClippedNodeTitlesValid(t *testing.T) {
	spaceBoundary := strings.Repeat("a", 149)
	unicodeBoundary := strings.Repeat("b", 149)
	messages := []*schema.Message{
		schema.SystemMessage("Call submit_wiki_graph."),
		schema.UserMessage("[seq=1 kind=tool_result] tool: " + spaceBoundary + " tail\n" +
			"[seq=2 kind=tool_result] tool: " + unicodeBoundary + "界 begins here\n" +
			"[seq=3 kind=tool_result] tool:   "),
	}
	response := reviewerScript(1, messages)
	if len(response.ToolCalls) != 1 || response.ToolCalls[0].Function.Name != "submit_wiki_graph" {
		t.Fatalf("missing offline graph submission: %+v", response)
	}
	var draft struct {
		Reviewed []int64
		Nodes    []struct {
			Title    string
			Evidence struct {
				Seq   int64
				Quote string
			}
		}
	}
	if err := json.Unmarshal([]byte(response.ToolCalls[0].Function.Arguments), &draft); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(draft.Reviewed, []int64{1, 2, 3}) || len(draft.Nodes) != 2 {
		t.Fatalf("unexpected reviewed sources or nodes: %+v", draft)
	}
	for i, want := range []string{spaceBoundary, unicodeBoundary} {
		node := draft.Nodes[i]
		if node.Title != want || node.Evidence.Quote != want || node.Evidence.Seq != int64(i+1) {
			t.Errorf("node %d does not preserve a valid title and exact quote: %+v", i+1, node)
		}
	}
	if followup := reviewerScript(2, messages); len(followup.ToolCalls) != 0 {
		t.Fatal("offline reviewer repeated the graph submission")
	}
}
