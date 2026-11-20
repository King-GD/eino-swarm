package engine

import (
	"fmt"
	"os"
	"strings"
)

// TerminalDir is where a human terminal for this conversation or project
// starts. The client names the conversation or the project; it does not
// name a path. The conversation terminal follows the agents' working directory.
func (e *Engine) TerminalDir(threadID, projectID string) (string, error) {
	threadID = strings.TrimSpace(threadID)
	projectID = strings.TrimSpace(projectID)
	var dir string
	switch {
	case threadID != "":
		thread, err := e.store.GetThread(threadID)
		if err != nil {
			return "", err
		}
		dir, err = e.executionWorkspace(thread)
		if err != nil {
			return "", err
		}
		if thread.TaskWorkspace == "" && dir == e.cfg.WorkspaceDir(thread.ID) {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				return "", fmt.Errorf("engine: create workspace: %w", err)
			}
		}
	case projectID != "":
		p, err := e.store.GetProject(projectID)
		if err != nil {
			return "", err
		}
		dir = e.ProjectWorkdir(p)
	default:
		return "", fmt.Errorf("engine: a conversation or a project is required")
	}
	return ensureTerminalDir(dir)
}

func ensureTerminalDir(dir string) (string, error) {
	info, err := os.Stat(dir)
	if err != nil {
		return "", fmt.Errorf("engine: working directory %s: %w", dir, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("engine: working directory %s is not a directory", dir)
	}
	return dir, nil
}
