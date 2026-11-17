package engine

import (
	"context"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
)

// workspaceEntry is one path the Files panel is willing to show. Size and
// modification time stay off this list: stating every name costs more than
// the walk that produced it, and a query only needs them for its matches.
type workspaceEntry struct {
	path string
	dir  bool
}

type workspaceScanResult struct {
	gen     uint64
	entries []workspaceEntry
}

type workspaceScanFlight struct {
	gen     uint64
	done    chan struct{}
	entries []workspaceEntry
	err     error
}

// workspaceScans remembers one complete listing of a workspace. A file query
// walks the tree only when that listing is missing; later queries match the
// names already in memory. Listing the root, uploading, or deleting advances
// the generation so the next query waits for a new complete walk instead of
// mixing in names discovered halfway through one.
type workspaceScans struct {
	mu      sync.Mutex
	stopped bool
	gen     map[string]uint64
	current map[string]*workspaceScanResult
	flight  map[string]*workspaceScanFlight
	wg      sync.WaitGroup
	ctx     context.Context
	cancel  context.CancelFunc
}

func (s *workspaceScans) initLocked() {
	if s.gen != nil {
		return
	}
	s.ctx, s.cancel = context.WithCancel(context.Background())
	s.gen = map[string]uint64{}
	s.current = map[string]*workspaceScanResult{}
	s.flight = map[string]*workspaceScanFlight{}
}

func (s *workspaceScans) stop() {
	s.mu.Lock()
	s.initLocked()
	s.stopped = true
	s.cancel()
	s.mu.Unlock()
	s.wg.Wait()
}

func (s *workspaceScans) drop(root string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.initLocked()
	if s.stopped {
		return
	}
	s.gen[root]++
	delete(s.current, root)
}

func (s *workspaceScans) dropAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.initLocked()
	if s.stopped {
		return
	}
	next := make(map[string]uint64, len(s.gen)+len(s.current))
	for root, gen := range s.gen {
		next[root] = gen + 1
	}
	for root := range s.current {
		if _, ok := next[root]; !ok {
			next[root] = 1
		}
	}
	s.gen = next
	s.current = map[string]*workspaceScanResult{}
}

func (s *workspaceScans) paths(ctx context.Context, root string, scan func(context.Context, string) ([]workspaceEntry, error)) ([]workspaceEntry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.initLocked()
	if s.stopped {
		err := s.ctx.Err()
		s.mu.Unlock()
		return nil, err
	}
	want := s.gen[root]
	if cur := s.current[root]; cur != nil && cur.gen == want {
		entries := cur.entries
		s.mu.Unlock()
		return entries, nil
	}
	if f := s.flight[root]; f != nil && f.gen == want {
		s.mu.Unlock()
		return waitWorkspaceScan(ctx, f)
	}
	f := &workspaceScanFlight{gen: want, done: make(chan struct{})}
	s.flight[root] = f
	scanCtx := s.ctx
	s.wg.Add(1)
	s.mu.Unlock()

	go func() {
		defer s.wg.Done()
		entries, err := scan(scanCtx, root)
		s.mu.Lock()
		f.entries, f.err = entries, err
		if err == nil && !s.stopped && s.gen[root] == want {
			s.current[root] = &workspaceScanResult{gen: want, entries: entries}
		}
		if s.flight[root] == f {
			delete(s.flight, root)
		}
		close(f.done)
		s.mu.Unlock()
	}()
	return waitWorkspaceScan(ctx, f)
}

func waitWorkspaceScan(ctx context.Context, f *workspaceScanFlight) ([]workspaceEntry, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-f.done:
		if f.err != nil {
			return nil, f.err
		}
		return f.entries, nil
	}
}

func (e *Engine) scanListed(ctx context.Context, root string) ([]workspaceEntry, error) {
	if e.scanWorkspace != nil {
		return e.scanWorkspace(ctx, root)
	}
	return scanListedPaths(ctx, root)
}

// invalidateFileScan forgets the listing for root and starts the replacement
// walk without waiting. A query that arrives during that walk shares it.
func (e *Engine) invalidateFileScan(root string) {
	if root == "" {
		return
	}
	e.fileScans.drop(root)
	go func() {
		_, _ = e.fileScans.paths(context.Background(), root, e.scanListed)
	}()
}

func scanListedPaths(ctx context.Context, workspace string) ([]workspaceEntry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out := make([]workspaceEntry, 0)
	err := filepath.WalkDir(workspace, func(abs string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if abs == workspace {
			return nil
		}
		if d.IsDir() && skipListedDir(d.Name()) {
			return filepath.SkipDir
		}
		rel, err := filepath.Rel(workspace, abs)
		if err != nil {
			return err
		}
		out = append(out, workspaceEntry{path: filepath.ToSlash(rel), dir: d.IsDir()})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func filterWorkspaceEntries(ctx context.Context, workspace, query string, entries []workspaceEntry) ([]FileEntry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	needle := strings.ToLower(query)
	out := make([]FileEntry, 0)
	for _, ent := range entries {
		if !strings.Contains(strings.ToLower(ent.path), needle) {
			continue
		}
		item := FileEntry{Path: ent.path, Name: path.Base(ent.path), Dir: ent.dir}
		info, err := os.Lstat(filepath.Join(workspace, filepath.FromSlash(ent.path)))
		if err == nil {
			item.Size = info.Size()
			item.Modified = info.ModTime()
		}
		out = append(out, item)
	}
	return out, nil
}
