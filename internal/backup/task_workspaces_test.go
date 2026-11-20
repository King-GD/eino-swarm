package backup

import (
	"context"
	"os"
	"path"
	"path/filepath"
	"testing"

	"github.com/LubyRuffy/eino-swarm/internal/config"
	"github.com/LubyRuffy/eino-swarm/internal/store"
)

func TestProjectBackupAndRestoreFollowTaskOwnershipNotPhysicalParent(t *testing.T) {
	src := seed(t)
	db, err := src.st.DB().DB()
	if err != nil {
		t.Fatal(err)
	}
	create := func(owner, origin, value string) (*store.Thread, string) {
		t.Helper()
		thread := &store.Thread{ID: store.NewID("th_"), ProjectID: owner, Title: "Workspace fixture"}
		thread.TaskWorkspace = path.Join("projects", origin, "workspace", config.TaskDirectory, thread.ID)
		if err := src.st.CreateThread(thread); err != nil {
			t.Fatal(err)
		}
		file := filepath.Join(src.dir, filepath.FromSlash(thread.TaskWorkspace), config.TaskResults, "result.txt")
		writeText(t, file, value)
		return thread, file
	}
	incoming, incomingFile := create(src.project.ID, src.other.ID, "moved-in-result")
	_, retainedFile := create(src.other.ID, src.project.ID, "moved-out-result")
	skipped, _ := create(src.project.ID, src.project.ID, "active-task-result")
	archive := filepath.Join(t.TempDir(), "task.zwai-backup")
	opts := Options{DataDir: src.dir, DB: db, Output: archive, Kind: KindProject, ProjectID: src.project.ID,
		OmitThreads: []OmittedThread{{ID: skipped.ID, Title: skipped.Title, Reason: ReasonRunning}}}
	if _, err := Create(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	if !archiveHas(t, archive, "moved-in-result") || archiveHas(t, archive, "moved-out-result") || archiveHas(t, archive, "active-task-result") {
		t.Fatal("project backup stored the wrong task workspaces")
	}
	writeText(t, incomingFile, "changed after backup")
	if _, err := Restore(context.Background(), RestoreOptions{DataDir: src.dir, DB: db, Path: archive, Confirm: true, ReplaceProject: true}); err != nil {
		t.Fatal(err)
	}
	if readText(t, incomingFile) != "moved-in-result" || readText(t, retainedFile) != "moved-out-result" {
		t.Fatal("restore changed files outside its task membership or dropped the moved-in result")
	}
	reloaded, err := src.st.GetThread(incoming.ID)
	if err != nil || reloaded.TaskWorkspace != incoming.TaskWorkspace {
		t.Fatalf("task binding not restored: %+v %v", reloaded, err)
	}
	full := filepath.Join(t.TempDir(), "full.zwai-backup")
	if _, err := Create(context.Background(), Options{DataDir: src.dir, DB: db, Output: full, Kind: KindFull,
		OmitThreads: []OmittedThread{{ID: incoming.ID, Title: incoming.Title, Reason: ReasonRunning}}}); err != nil {
		t.Fatal(err)
	}
	if archiveHas(t, full, "moved-in-result") || !archiveHas(t, full, "moved-out-result") {
		t.Fatal("full backup included an omitted task or skipped an unrelated task")
	}
	if _, err := os.Stat(retainedFile); err != nil {
		t.Fatal(err)
	}
}

func TestOldBackupWithoutTaskBindingRestoresLegacyWorkspace(t *testing.T) {
	src := seed(t)
	db, err := src.st.DB().DB()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("ALTER TABLE threads DROP COLUMN task_workspace"); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "old.zwai-backup")
	if _, err := Create(t.Context(), Options{DataDir: src.dir, DB: db, Kind: KindFull, Output: output}); err != nil {
		t.Fatal(err)
	}
	dest := seed(t)
	destDB, err := dest.st.DB().DB()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Restore(t.Context(), RestoreOptions{DataDir: dest.dir, DB: destDB, Path: output, Confirm: true, Replace: true}); err != nil {
		t.Fatal(err)
	}
	thread, err := dest.st.GetThread(src.thread.ID)
	if err != nil || thread.TaskWorkspace != "" || thread.ProjectID != src.project.ID {
		t.Fatalf("legacy binding changed: %+v %v", thread, err)
	}
	if readText(t, filepath.Join(dest.cfg.WorkspaceDir(src.thread.ID), "out.txt")) != "workspace-out" {
		t.Fatal("the old archive lost its original workspace file")
	}
}
