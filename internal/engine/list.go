package engine

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/LubyRuffy/eino-swarm/internal/store"
)

// errNotDirectory is a directory listing asked to read a file or a symlink.
var errNotDirectory = errors.New("not a directory")

// listDirectory reads one directory completely. rel is empty for the
// workspace root and is otherwise an OS path relative to it. Dot-directories,
// node_modules and vendor are omitted; every other direct child is returned.
// A symlink is not followed.
func listDirectory(workspace, rel string) ([]FileEntry, error) {
	root, err := os.OpenRoot(workspace)
	if err != nil {
		return nil, err
	}
	defer root.Close()

	name := "."
	prefix := ""
	if rel != "" {
		name = rel
		prefix = filepath.ToSlash(rel) + "/"
	}
	info, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return nil, errNotDirectory
	}
	f, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	ents, err := f.ReadDir(-1)
	if err != nil {
		// A short read is not a directory listing.
		return nil, err
	}
	out := make([]FileEntry, 0, len(ents))
	for _, ent := range ents {
		if ent.IsDir() && skipListedDir(ent.Name()) {
			continue
		}
		item := FileEntry{
			Path: prefix + ent.Name(),
			Name: ent.Name(),
			Dir:  ent.IsDir(),
		}
		if info, err := ent.Info(); err == nil {
			item.Size = info.Size()
			item.Modified = info.ModTime()
		}
		out = append(out, item)
	}
	return out, nil
}

func skipListedDir(name string) bool {
	if strings.HasPrefix(name, ".") {
		return true
	}
	switch name {
	case "node_modules", "vendor":
		return true
	default:
		return false
	}
}

// ListFiles lists the immediate children of one workspace directory. An empty
// dir is the workspace root. The result is that directory's complete direct
// listing, not a sample of the tree.
func (e *Engine) ListFiles(threadID, dir string) ([]FileEntry, error) {
	root := e.WorkspaceDir(threadID)
	rel := ""
	if strings.TrimSpace(dir) != "" {
		name, err := workspaceRelName(dir)
		if err != nil {
			return nil, err
		}
		rel = name
	} else if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("engine: workspace: %w", err)
	}
	entries, err := listDirectory(root, rel)
	if err != nil {
		if errors.Is(err, errNotDirectory) {
			return nil, fmt.Errorf("engine: %q is not a directory", dir)
		}
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("engine: list %q: %w", dir, store.ErrNotFound)
		}
		return nil, fmt.Errorf("engine: list %q: %w", dir, err)
	}
	if rel == "" {
		// The file list just read the root. Names created since the last scan
		// belong in the next search, so start that scan now and let the query
		// wait for it instead of walking a second time.
		e.invalidateFileScan(root)
	}
	return entries, nil
}

// SearchFiles returns every listed workspace entry whose path contains query.
// It answers from the newest completed scan and waits when that scan is still
// running. Failure, including cancellation, yields no rows.
func (e *Engine) SearchFiles(ctx context.Context, threadID, query string) ([]FileEntry, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("engine: empty file search")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	root := e.WorkspaceDir(threadID)
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("engine: workspace: %w", err)
	}
	listed, err := e.fileScans.paths(ctx, root, e.scanListed)
	if err != nil {
		return nil, fmt.Errorf("engine: search workspace: %w", err)
	}
	entries, err := filterWorkspaceEntries(ctx, root, query, listed)
	if err != nil {
		return nil, fmt.Errorf("engine: search workspace: %w", err)
	}
	return entries, nil
}
