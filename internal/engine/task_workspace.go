package engine

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/LubyRuffy/eino-swarm/internal/config"
	"github.com/LubyRuffy/eino-swarm/internal/store"
)

const (
	FileScopeTask    = "task"
	FileScopeProject = "project"
)

var (
	ErrFileScope = errors.New("file scope is not available")
	ErrFileRoot  = errors.New("file directory changed; refresh the file list")
)

type WorkspaceView struct {
	Path       string `json:"-"`
	Root       string `json:"root"`
	Isolated   bool   `json:"isolated"`
	HasProject bool   `json:"has_project"`
	info       fs.FileInfo
	project    bool
}

func (e *Engine) initTaskWorkspace(thread *store.Thread, project *store.Project) error {
	base := e.cfg.WorkspacesDir()
	managed := project == nil || strings.TrimSpace(project.Workdir) == ""
	if project != nil {
		base = e.ProjectWorkdir(project)
	} else if err := os.MkdirAll(base, 0o700); err != nil {
		return err
	}
	root, err := os.OpenRoot(base)
	if err != nil {
		return err
	}
	defer root.Close()
	name := thread.ID
	if project != nil {
		if err := root.Mkdir(config.TaskDirectory, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
			return err
		}
		if err := requireDirectory(root, config.TaskDirectory); err != nil {
			return err
		}
		name = filepath.Join(config.TaskDirectory, name)
	}
	binding := filepath.Join(base, name)
	if managed {
		binding, err = filepath.Rel(e.cfg.DataDir(), binding)
		if err != nil {
			return err
		}
		binding = filepath.ToSlash(binding)
	}
	if err := root.Mkdir(name, 0o700); err != nil {
		return err
	}
	for _, section := range []string{config.TaskWork, config.TaskResults, config.TaskUploads} {
		if err := root.Mkdir(filepath.Join(name, section), 0o700); err != nil {
			return errors.Join(err, root.RemoveAll(name))
		}
	}
	thread.TaskWorkspace = binding
	return nil
}

func requireDirectory(root *os.Root, path string) error {
	info, err := root.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%q is not a directory", path)
	}
	return nil
}

func (e *Engine) workspacePath(thread *store.Thread) (string, error) {
	binding := thread.TaskWorkspace
	switch {
	case config.ManagedTaskPath(binding, thread.ID):
		return filepath.Join(e.cfg.DataDir(), filepath.FromSlash(binding)), nil
	case filepath.IsAbs(binding) && filepath.Base(binding) == thread.ID &&
		filepath.Base(filepath.Dir(binding)) == config.TaskDirectory && binding == filepath.Clean(binding):
		return binding, nil
	case binding != "":
		return "", errors.New("invalid task directory binding")
	}
	if thread.ProjectID != "" {
		project, err := e.store.GetProject(thread.ProjectID)
		if err == nil {
			return e.ProjectWorkdir(project), nil
		}
		if !errors.Is(err, store.ErrNotFound) {
			return "", err
		}
	}
	return e.cfg.WorkspaceDir(thread.ID), nil
}

func (e *Engine) validatedTaskWorkspace(thread *store.Thread) (string, error) {
	path, err := e.workspacePath(thread)
	if err != nil || thread.TaskWorkspace == "" {
		return path, err
	}
	base := filepath.Dir(filepath.Dir(path))
	relative := filepath.Join(config.TaskDirectory, thread.ID)
	if config.ManagedTaskPath(thread.TaskWorkspace, thread.ID) {
		base = e.cfg.DataDir()
		relative = filepath.FromSlash(thread.TaskWorkspace)
	}
	root, err := os.OpenRoot(base)
	if err != nil {
		return "", fmt.Errorf("task directory %q: %w", path, err)
	}
	defer root.Close()
	return path, checkWorkspaceDirectories(root, relative, path)
}

func checkWorkspaceDirectories(root *os.Root, relative, path string) error {
	name := ""
	for _, part := range strings.Split(relative, string(filepath.Separator)) {
		name = filepath.Join(name, part)
		if err := requireDirectory(root, name); err != nil {
			return fmt.Errorf("task directory %q: %w", path, err)
		}
	}
	return nil
}

func (e *Engine) executionWorkspace(thread *store.Thread) (string, error) {
	path, err := e.validatedTaskWorkspace(thread)
	if err != nil || thread.TaskWorkspace == "" {
		return path, err
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return "", err
	}
	defer root.Close()
	if err := requireDirectory(root, config.TaskWork); err != nil {
		return "", fmt.Errorf("task work directory %q: %w", path, err)
	}
	return filepath.Join(path, config.TaskWork), nil
}

func (e *Engine) removeManagedTaskWorkspace(thread *store.Thread) {
	binding := thread.TaskWorkspace
	if binding == "" && thread.ProjectID == "" {
		binding = "workspaces/" + thread.ID
	}
	if !config.ManagedTaskPath(binding, thread.ID) {
		return
	}
	root, err := os.OpenRoot(e.cfg.DataDir())
	if err == nil {
		defer root.Close()
		relative := filepath.FromSlash(binding)
		err = checkWorkspaceDirectories(root, filepath.Dir(relative), binding)
		if err == nil {
			err = root.RemoveAll(relative)
		}
	}
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		e.log.Warn("could not remove task workspace", "thread", thread.ID, "err", err)
	}
}

func removeProjectFiles(project, workspace string) error {
	for _, dir := range []string{project, workspace} {
		info, err := os.Lstat(dir)
		if errors.Is(err, fs.ErrNotExist) || (err == nil && !info.IsDir()) {
			return os.RemoveAll(project)
		}
		if err != nil {
			return err
		}
	}
	tasks := filepath.Join(workspace, config.TaskDirectory)
	if err := os.Remove(tasks); err == nil || errors.Is(err, fs.ErrNotExist) {
		return os.RemoveAll(project)
	}
	return errors.Join(clearProjectChildren(project, filepath.Base(workspace)), clearProjectChildren(workspace, config.TaskDirectory))
}

func clearProjectChildren(dir, keep string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var cleanup error
	for _, entry := range entries {
		if entry.Name() != keep {
			cleanup = errors.Join(cleanup, os.RemoveAll(filepath.Join(dir, entry.Name())))
		}
	}
	return cleanup
}

func (e *Engine) FileWorkspace(threadID, scope, expected string) (WorkspaceView, error) {
	thread, err := e.store.GetThread(threadID)
	if err != nil {
		return WorkspaceView{}, err
	}
	view := WorkspaceView{Isolated: thread.TaskWorkspace != "", HasProject: thread.ProjectID != ""}
	identity := ""
	switch scope {
	case "", FileScopeTask:
		scope = FileScopeTask
		view.Path, err = e.validatedTaskWorkspace(thread)
		if err == nil && !view.Isolated && view.Path == e.cfg.WorkspaceDir(thread.ID) {
			err = os.MkdirAll(view.Path, 0o700)
		}
	case FileScopeProject:
		if !view.HasProject {
			return WorkspaceView{}, ErrFileScope
		}
		project, lookupErr := e.store.GetProject(thread.ProjectID)
		if lookupErr != nil {
			return WorkspaceView{}, lookupErr
		}
		view.Path = e.ProjectWorkdir(project)
		view.project = true
		identity = project.ID
	default:
		return WorkspaceView{}, ErrFileScope
	}
	if err != nil {
		return WorkspaceView{}, err
	}
	info, err := os.Stat(view.Path)
	if err != nil {
		return WorkspaceView{}, fmt.Errorf("file directory %q: %w", view.Path, err)
	}
	if !info.IsDir() {
		return WorkspaceView{}, fmt.Errorf("file directory %q is not a directory", view.Path)
	}
	view.info = info
	key := e.fileScans.rootKey(view.Path, info)
	view.Root = fmt.Sprintf("%x", sha256.Sum256([]byte(thread.ID+"\x00"+scope+"\x00"+identity+"\x00"+view.Path+"\x00"+key)))
	if expected != "" && expected != view.Root {
		return WorkspaceView{}, ErrFileRoot
	}
	return view, nil
}

func (view WorkspaceView) openRoot() (*os.Root, error) {
	root, err := os.OpenRoot(view.Path)
	if err != nil {
		return nil, err
	}
	info, err := root.Stat(".")
	if err != nil || view.info == nil || !os.SameFile(view.info, info) {
		_ = root.Close()
		return nil, ErrFileRoot
	}
	return root, nil
}

func (e *Engine) taskWorkspaceInstruction(thread *store.Thread) string {
	if thread == nil || thread.TaskWorkspace == "" {
		return ""
	}
	root, err := e.workspacePath(thread)
	if err != nil {
		return ""
	}
	text := fmt.Sprintf("## Task files\n\nUse the current workspace for intermediate files. User uploads named %s/... are relative to the task root; read them under %s. When delivering files, write them under %s and list their task-relative paths in the final response. Do not create a file for a text-only answer.\n",
		config.TaskUploads, filepath.Join(root, config.TaskUploads), filepath.Join(root, config.TaskResults))
	if thread.ProjectID != "" {
		if project, err := e.store.GetProject(thread.ProjectID); err == nil {
			text += fmt.Sprintf("The shared project directory is %s. Use that explicit path for work that needs the project files; do not put task drafts or deliverables there by default.\n", e.ProjectWorkdir(project))
		}
	}
	return text
}
