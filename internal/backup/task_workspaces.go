package backup

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/LubyRuffy/eino-swarm/internal/config"
)

func managedProjectTask(rel string) string {
	parts := strings.SplitN(rel, "/", 6)
	if len(parts) >= 5 && parts[0] == "projects" && parts[2] == "workspace" && parts[3] == config.TaskDirectory {
		return strings.Join(parts[:5], "/")
	}
	return ""
}

func projectTaskRoots(ctx context.Context, conn queryer, schema, projectID string) ([]string, error) {
	var roots []string
	err := readStrings(ctx, conn, "SELECT id, task_workspace FROM "+schema+".threads WHERE project_id = ? ORDER BY id", []any{projectID}, 2, func(row []string) error {
		if config.ManagedTaskPath(row[1], row[0]) && managedProjectTask(row[1]) != "" {
			roots = append(roots, row[1])
		}
		return nil
	})
	return roots, err
}

func projectSwapRoots(project, live, extracted string, taskRoots []string) ([]string, error) {
	workspace := path.Join(project, "workspace")
	taskContainer := path.Join(workspace, config.TaskDirectory)
	roots := map[string]struct{}{}
	for _, base := range []string{live, extracted} {
		for _, layout := range []struct{ parent, skip string }{
			{project, "workspace"},
			{workspace, config.TaskDirectory},
		} {
			parent := filepath.Join(base, filepath.FromSlash(layout.parent))
			if err := checkBackupParents(base, layout.parent+"/child"); err != nil {
				return nil, err
			}
			children, err := os.ReadDir(parent)
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			if err != nil {
				return nil, err
			}
			for _, child := range children {
				if child.Name() != layout.skip {
					roots[path.Join(layout.parent, child.Name())] = struct{}{}
				}
			}
		}
	}
	for _, rel := range taskRoots {
		for _, base := range []string{live, extracted} {
			if err := checkBackupParents(base, rel); err != nil {
				return nil, err
			}
		}
		roots[rel] = struct{}{}
	}
	if _, err := os.Lstat(filepath.Join(live, filepath.FromSlash(taskContainer))); errors.Is(err, fs.ErrNotExist) {
		if _, err := os.Lstat(filepath.Join(extracted, filepath.FromSlash(taskContainer))); errors.Is(err, fs.ErrNotExist) {
			out := []string{project}
			for _, rel := range taskRoots {
				if !strings.HasPrefix(rel, project+"/") {
					out = append(out, rel)
				}
			}
			return out, nil
		}
	}
	out := make([]string, 0, len(roots))
	for rel := range roots {
		out = append(out, rel)
	}
	sort.Strings(out)
	return out, nil
}

func checkBackupParents(base, rel string) error {
	for _, part := range strings.Split(path.Dir(rel), "/") {
		base = filepath.Join(base, part)
		info, err := os.Lstat(base)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return invalidf("not a managed directory: %s", base)
		}
	}
	return nil
}
