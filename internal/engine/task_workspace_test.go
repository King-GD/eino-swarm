package engine

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LubyRuffy/eino-swarm/internal/config"
	"github.com/LubyRuffy/eino-swarm/internal/store"
)

func TestProjectAgentsKeepSameNamedProcessAndResultFilesApart(t *testing.T) {
	e, _ := newScheduleCompletionHTTPEngine(t, func(w http.ResponseWriter, _ *http.Request, step int, request scheduleCompletionRequest) {
		slot := (step - 1) / 3
		var path string
		switch (step - 1) % 3 {
		case 0:
			path = "notes.txt"
		case 1:
			path = "../results/report.txt"
		default:
			writeContextFlowReply(w, request.Stream, map[string]any{"content": "Delivered results/report.txt."}, "stop")
			return
		}
		value := fmt.Sprintf("task %d", slot)
		args, _ := json.Marshal(map[string]string{"file_path": path, "content": value})
		call := map[string]any{
			"index": 0, "id": fmt.Sprintf("task-write-%d", step), "type": "function",
			"function": map[string]any{"name": "write", "arguments": string(args)},
		}
		writeContextFlowReply(w, request.Stream, map[string]any{"tool_calls": []any{call}}, "tool_calls")
	})
	e.cfg.Memory.Enabled, e.cfg.Memory.AutoReview = false, false
	e.cfg.Swarm.ComposerPredictions = false
	project, err := e.CreateProject("Shared project", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	var threads []*store.Thread
	for slot := range 2 {
		thread, err := e.CreateThread(fmt.Sprintf("Task %d", slot), "", project.ID)
		if err != nil {
			t.Fatal(err)
		}
		threads = append(threads, thread)
		turn, err := e.StartTurn(thread.ID, "Produce separate working notes and a deliverable.")
		if err != nil {
			t.Fatal(err)
		}
		if waited := waitForTurn(t, e, turn.ID); waited.Status != store.TurnDone {
			t.Fatalf("turn did not finish: %+v", waited)
		}
		waitSettled(t, e, thread.ID)
	}
	for slot, thread := range threads {
		for _, path := range []string{"work/notes.txt", "results/report.txt"} {
			data, err := os.ReadFile(filepath.Join(e.TaskFileDir(thread.ID), filepath.FromSlash(path)))
			if err != nil || strings.TrimSpace(string(data)) != fmt.Sprintf("task %d", slot) {
				t.Fatalf("task %d %s: %q %v", slot, path, data, err)
			}
		}
		if dir, err := e.TerminalDir(thread.ID, ""); err != nil || dir != filepath.Join(e.TaskFileDir(thread.ID), config.TaskWork) {
			t.Fatalf("terminal did not follow task execution: %q %v", dir, err)
		}
	}
	if _, err := os.Stat(filepath.Join(e.ProjectWorkdir(project), "notes.txt")); !os.IsNotExist(err) {
		t.Fatalf("working notes escaped into the project root: %v", err)
	}
}

func TestTaskBindingSurvivesMoveWorkdirChangeAndOriginDeletion(t *testing.T) {
	e := newTestEngine(t)
	origin, err := e.CreateProject("Origin", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	dest, err := e.CreateProject("Destination", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	thread, err := e.CreateThread("Kept task", "", origin.ID)
	if err != nil {
		t.Fatal(err)
	}
	root := e.TaskFileDir(thread.ID)
	result := filepath.Join(root, "results", "kept.txt")
	if err := os.WriteFile(result, []byte("kept"), 0o600); err != nil {
		t.Fatal(err)
	}
	oldView, err := e.FileWorkspace(thread.ID, FileScopeProject, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := e.MoveThread(thread.ID, dest.ID); err != nil {
		t.Fatal(err)
	}
	newRoot := t.TempDir()
	if _, err := e.UpdateProject(dest.ID, nil, nil, &newRoot, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := e.FileWorkspace(thread.ID, FileScopeProject, oldView.Root); !errors.Is(err, ErrFileRoot) {
		t.Fatalf("old project file root remained valid: %v", err)
	}
	if view, err := e.FileWorkspace(thread.ID, FileScopeProject, ""); err != nil || view.Path != newRoot {
		t.Fatalf("project view did not follow the edited project: %+v %v", view, err)
	}
	if err := e.DeleteProject(origin.ID); err != nil {
		t.Fatal(err)
	}
	if e.TaskFileDir(thread.ID) != root {
		t.Fatal("move changed the task directory")
	}
	if data, err := os.ReadFile(result); err != nil || string(data) != "kept" {
		t.Fatalf("deleting the origin lost an unrelated task result: %q %v", data, err)
	}
	if err := e.DeleteThread(thread.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("explicit deletion did not remove the managed task: %v", err)
	}
}

func TestNewProjectTasksRefuseMissingOrLinkedTaskContainer(t *testing.T) {
	e := newTestEngine(t)
	external := t.TempDir()
	project, err := e.CreateProject("External", "", external, false)
	if err != nil {
		t.Fatal(err)
	}
	container := filepath.Join(external, config.TaskDirectory)
	outside := t.TempDir()
	if err := os.Symlink(outside, container); err != nil {
		t.Skipf("symlinks not available: %v", err)
	}
	if _, err := e.CreateThread("Refused", "", project.ID); err == nil {
		t.Fatal("task creation must not follow a redirected container")
	}
	ids, err := e.Store().ListThreadIDsByProject(project.ID)
	if err != nil || len(ids) != 0 {
		t.Fatalf("refusal left a conversation behind: %v %v", ids, err)
	}
	if err := os.Remove(container); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(external); err != nil {
		t.Fatal(err)
	}
	if _, err := e.CreateThread("Still refused", "", project.ID); err == nil {
		t.Fatal("task creation must not recreate the vanished external root")
	}
}

func TestLegacyProjectAttachmentStaysAtItsExistingRelativePath(t *testing.T) {
	e := newTestEngine(t)
	project, err := e.CreateProject("Legacy", "", t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	thread := &store.Thread{ProjectID: project.ID, Title: "Existing task"}
	if err := e.store.CreateThread(thread); err != nil {
		t.Fatal(err)
	}
	attachment, err := e.SaveUpload(thread.ID, "existing.txt", writeUpload("original bytes"))
	if err != nil {
		t.Fatal(err)
	}
	view, err := e.FileWorkspace(thread.ID, "", "")
	if err != nil || view.Isolated || !view.HasProject || view.Path != project.Workdir {
		t.Fatalf("legacy file view: %+v %v", view, err)
	}
	if attachment.RelPath != "uploads/existing.txt" || e.WorkspaceDir(thread.ID) != project.Workdir {
		t.Fatal("legacy attachment or tool base was moved")
	}
	if err := e.DeleteThread(thread.ID); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(project.Workdir, attachment.RelPath))
	if err != nil || string(data) != "original bytes" {
		t.Fatalf("legacy user files were removed: %q %v", data, err)
	}
}
