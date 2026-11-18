package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/LubyRuffy/eino-swarm/internal/store"
	"github.com/cloudwego/eino/components/tool"
)

func TestProjectDescriptionToolRequiresAnExplicitDescription(t *testing.T) {
	e := newTestEngine(t)
	project, err := e.CreateProject("Instructions", "Keep this policy", "", false)
	if err != nil {
		t.Fatal(err)
	}
	thread := &store.Thread{Title: "Update instructions", ProjectID: project.ID}
	if err := e.store.CreateThread(thread); err != nil {
		t.Fatal(err)
	}
	turn := &store.Turn{ThreadID: thread.ID, ProjectID: project.ID}
	if err := e.store.CreateTurn(turn); err != nil {
		t.Fatal(err)
	}
	writer := e.projectDescriptionTool(project, turn).(tool.InvokableTool)
	info, err := writer.Info(context.Background())
	if err != nil || !strings.Contains(info.Desc, "no separate tool") || !strings.Contains(info.Desc, "saved description") {
		t.Fatalf("description tool does not say the success result is the saved text: %+v %v", info, err)
	}
	ctx := context.Background()
	for _, args := range []string{`{}`, `{"description":null}`, `{"description":3}`, `{"description":"partial"`} {
		out, err := writer.InvokableRun(ctx, args)
		if err != nil || !strings.Contains(out, `"ok":false`) {
			t.Fatalf("malformed input %q: %s %v", args, out, err)
		}
	}
	got, err := e.GetProject(project.ID)
	if err != nil || got.SystemPrompt != project.SystemPrompt {
		t.Fatalf("invalid inputs changed project: %+v %v", got, err)
	}

	const revised = "Keep the revised policy."
	out, err := writer.InvokableRun(ctx, `{"description":"`+revised+`"}`)
	if err != nil || out != `{"ok":true,"description":"`+revised+`"}` {
		t.Fatalf("saved description was not returned: %s %v", out, err)
	}
	got, err = e.GetProject(project.ID)
	if err != nil || got.SystemPrompt != revised {
		t.Fatalf("description did not persist: %+v %v", got, err)
	}
	writer = e.projectDescriptionTool(got, turn).(tool.InvokableTool)
	out, err = writer.InvokableRun(ctx, `{"description":""}`)
	if err != nil || out != `{"ok":true,"description":""}` {
		t.Fatalf("explicit clear: %s %v", out, err)
	}
	got, err = e.GetProject(project.ID)
	if err != nil || got.SystemPrompt != "" {
		t.Fatalf("clear did not persist: %+v %v", got, err)
	}
	out, err = writer.InvokableRun(ctx, `{"description":"Overwrite from the stale snapshot"}`)
	if err != nil || !strings.Contains(out, `"ok":false`) {
		t.Fatalf("stale overwrite: %s %v", out, err)
	}
}
