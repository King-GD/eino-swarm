package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
)

func TestFileSearchSharesOneScanAndIgnoresAFailedOne(t *testing.T) {
	// Two queries must not walk the workspace twice, and a walk that fails
	// must not become the answer for the next query.
	e := newTestEngine(t)
	th, _ := e.CreateThread("", "", "")
	root := e.WorkspaceDir(th.ID)
	if err := os.WriteFile(filepath.Join(root, "keep.txt"), []byte("ab"), 0o600); err != nil {
		t.Fatal(err)
	}
	var scans atomic.Int32
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	e.scanWorkspace = func(ctx context.Context, workspace string) ([]workspaceEntry, error) {
		if scans.Add(1) == 1 {
			return nil, errors.New("blocked")
		}
		once.Do(func() { close(started) })
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-release:
		}
		return scanListedPaths(ctx, workspace)
	}

	if _, err := e.SearchFiles(context.Background(), th.ID, "keep"); err == nil {
		t.Fatal("a failed scan was treated as an empty workspace")
	}

	var wg sync.WaitGroup
	errCh := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := e.SearchFiles(context.Background(), th.ID, "keep")
			if err != nil || len(got) != 1 || got[0].Path != "keep.txt" || got[0].Size != 2 {
				errCh <- fmt.Errorf("search = %+v, %v", got, err)
				return
			}
			errCh <- nil
		}()
	}
	<-started
	if scans.Load() != 2 {
		t.Fatalf("scans before release = %d, want the failed scan plus one shared scan", scans.Load())
	}
	close(release)
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatal(err)
		}
	}
	if scans.Load() != 2 {
		t.Fatalf("scans = %d, a later waiter started another walk", scans.Load())
	}

	again, err := e.SearchFiles(context.Background(), th.ID, "keep")
	if err != nil || len(again) != 1 {
		t.Fatalf("cached search = %+v, %v", again, err)
	}
	if scans.Load() != 2 {
		t.Fatalf("scans = %d, the finished scan was not reused", scans.Load())
	}
}

func TestCancelledFileSearchReturnsNoRowsWhileTheScanFinishes(t *testing.T) {
	e := newTestEngine(t)
	th, _ := e.CreateThread("", "", "")
	root := e.WorkspaceDir(th.ID)
	if err := os.WriteFile(filepath.Join(root, "keep.txt"), []byte("ab"), 0o600); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	var scans atomic.Int32
	e.scanWorkspace = func(ctx context.Context, workspace string) ([]workspaceEntry, error) {
		scans.Add(1)
		close(started)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-release:
		}
		return scanListedPaths(ctx, workspace)
	}
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		got, err := e.SearchFiles(ctx, th.ID, "keep")
		if got != nil {
			errCh <- fmt.Errorf("cancelled search returned %+v", got)
			return
		}
		errCh <- err
	}()
	<-started
	cancel()
	if err := <-errCh; err == nil {
		t.Fatal("cancelled search succeeded")
	}
	close(release)
	got, err := e.SearchFiles(context.Background(), th.ID, "keep")
	if err != nil || len(got) != 1 || got[0].Path != "keep.txt" {
		t.Fatalf("search after cancel = %+v, %v", got, err)
	}
	if scans.Load() != 1 {
		t.Fatalf("scans = %d, cancellation threw away the walk", scans.Load())
	}
}

func TestDeletingOrReplacingTheWorkspaceDropsTheFileScan(t *testing.T) {
	e := newTestEngine(t)
	th, _ := e.CreateThread("", "", "")
	root := e.WorkspaceDir(th.ID)
	kept := filepath.Join(root, "kept.txt")
	if err := os.WriteFile(kept, []byte("ab"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := e.SearchFiles(context.Background(), th.ID, "kept")
	if err != nil || len(got) != 1 {
		t.Fatalf("first search = %+v, %v", got, err)
	}
	if err := os.Remove(kept); err != nil {
		t.Fatal(err)
	}
	ghost, err := e.SearchFiles(context.Background(), th.ID, "kept")
	if err != nil || len(ghost) != 1 || ghost[0].Size != 0 {
		t.Fatalf("name from the finished scan = %+v, %v", ghost, err)
	}

	if err := e.DeleteFile(th.ID, "kept.txt"); err != nil {
		t.Fatal(err)
	}
	afterDelete, err := e.SearchFiles(context.Background(), th.ID, "kept")
	if err != nil || len(afterDelete) != 0 {
		t.Fatalf("search after delete = %+v, %v", afterDelete, err)
	}

	if _, err := e.SaveUpload(th.ID, "kept.txt", func(dst string) error {
		return os.WriteFile(dst, []byte("xyz"), 0o600)
	}); err != nil {
		t.Fatal(err)
	}
	uploaded, err := e.SearchFiles(context.Background(), th.ID, "kept.txt")
	if err != nil || len(uploaded) != 1 || uploaded[0].Path != "uploads/kept.txt" || uploaded[0].Size != 3 {
		t.Fatalf("search after upload = %+v, %v", uploaded, err)
	}

	if err := os.WriteFile(filepath.Join(root, "later.txt"), []byte("q"), 0o600); err != nil {
		t.Fatal(err)
	}
	e.fileScans.dropAll()
	restored, err := e.SearchFiles(context.Background(), th.ID, "later")
	if err != nil || len(restored) != 1 || restored[0].Path != "later.txt" {
		t.Fatalf("search after the scan was dropped = %+v, %v", restored, err)
	}
}

func TestStoppedFileScanRejectsAnotherWalk(t *testing.T) {
	e := newTestEngine(t)
	th, _ := e.CreateThread("", "", "")
	root := e.WorkspaceDir(th.ID)
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("a"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := e.SearchFiles(context.Background(), th.ID, "a.txt"); err != nil {
		t.Fatal(err)
	}
	// The first scan records names without numbering the root. Dropping every
	// scan still has to forget that root.
	e.fileScans.dropAll()
	if err := os.WriteFile(filepath.Join(root, "b.txt"), []byte("b"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := e.SearchFiles(context.Background(), th.ID, "b.txt")
	if err != nil || len(got) != 1 {
		t.Fatalf("search after drop = %+v, %v", got, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := filterWorkspaceEntries(ctx, root, "a", nil); err == nil {
		t.Fatal("filter accepted a cancelled query")
	}
	if _, err := scanListedPaths(ctx, root); err == nil {
		t.Fatal("scan accepted a cancelled query")
	}
	e.fileScans.stop()
	e.invalidateFileScan("")
	e.fileScans.drop(root)
	e.fileScans.dropAll()
	if _, err := e.fileScans.paths(context.Background(), root, e.scanListed); err == nil {
		t.Fatal("a stopped engine scanned again")
	}
}

func TestFileScanStopsWithTheEngine(t *testing.T) {
	e := newTestEngine(t)
	th, _ := e.CreateThread("", "", "")
	root := e.WorkspaceDir(th.ID)
	if err := os.WriteFile(filepath.Join(root, "keep.txt"), []byte("ab"), 0o600); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	e.scanWorkspace = func(ctx context.Context, _ string) ([]workspaceEntry, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	errCh := make(chan error, 1)
	go func() {
		got, err := e.SearchFiles(context.Background(), th.ID, "keep")
		if got != nil {
			errCh <- fmt.Errorf("stopped search returned %+v", got)
			return
		}
		errCh <- err
	}()
	<-started
	e.fileScans.stop()
	if err := <-errCh; err == nil {
		t.Fatal("search succeeded after the engine stopped scanning")
	}
	if _, err := e.SearchFiles(context.Background(), th.ID, "keep"); err == nil {
		t.Fatal("search started a scan after the engine stopped")
	}
}
