package backup

import (
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/LubyRuffy/eino-swarm/internal/store"
)

func TestPortableWikiBackupsExcludeDerivedAcknowledgementsAndRollbackKeepsThem(t *testing.T) {
	for _, kind := range []Kind{KindFull, KindProject} {
		t.Run(string(kind), func(t *testing.T) {
			ctx := t.Context()
			src := seed(t)
			peer := &store.Thread{ProjectID: src.other.ID}
			if err := src.st.CreateThread(peer); err != nil {
				t.Fatal(err)
			}
			// Active source receipts retain progress on a full restore, but
			// reusable acknowledgements belong only to the live event IDs.
			for _, thread := range []*store.Thread{src.thread, peer} {
				seedWikiBackupAck(t, src.st, thread)
			}
			scans := func() []store.WikiSourceScan {
				t.Helper()
				var rows []store.WikiSourceScan
				if err := src.st.DB().Order("project_id, event_id").Find(&rows).Error; err != nil {
					t.Fatal(err)
				}
				return rows
			}
			before := scans()
			if len(before) != 2 || !before[0].LocallyIndexed || !before[0].WikiReviewed ||
				!before[1].LocallyIndexed || !before[1].WikiReviewed {
				t.Fatalf("fixture did not acknowledge both sources: %+v", before)
			}
			path := filepath.Join(t.TempDir(), "wiki.zip")
			db, err := src.st.DB().DB()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := Create(ctx, Options{DataDir: src.dir, DB: db, Kind: kind, ProjectID: src.project.ID, Output: path}); err != nil {
				t.Fatal(err)
			}
			arc, err := openArchive(path)
			if err != nil {
				t.Fatal(err)
			}
			defer arc.close()
			uri := url.URL{Scheme: "file", Path: arc.dbPath, RawQuery: "mode=ro"}
			arDB, err := sql.Open("sqlite", uri.String())
			if err != nil {
				t.Fatal(err)
			}
			defer arDB.Close()
			var internalObjects int
			err = arDB.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE name IN
				('wiki_source_scans','wiki_source_scan_changed','wiki_source_scan_deleted')`).Scan(&internalObjects)
			if err != nil || internalObjects != 0 {
				t.Fatalf("portable database retained the derived Wiki cache or dependency: count=%d %v", internalObjects, err)
			}
			if err := arDB.Close(); err != nil {
				t.Fatal(err)
			}
			if got := scans(); !reflect.DeepEqual(got, before) {
				t.Fatalf("creating the archive changed live acknowledgements: %+v", got)
			}

			opts := RestoreOptions{DataDir: src.dir, DB: db, Path: path, Confirm: true, Replace: true, ReplaceProject: true}
			refusal := errors.New("fixture refused file swap")
			beforeFileSwap = func() error { return refusal }
			_, err = Restore(ctx, opts)
			beforeFileSwap = nil
			if !errors.Is(err, refusal) {
				t.Fatalf("expected compensating backup rollback: %v", err)
			}
			if got := scans(); !reflect.DeepEqual(got, before) {
				t.Fatalf("rollback lost exact cached acknowledgements: %+v", got)
			}

			if _, err := Restore(ctx, opts); err != nil {
				t.Fatal(err)
			}
			got := scans()
			if kind == KindFull {
				if len(got) != 0 {
					t.Fatalf("full restore retained old derived state: %+v", got)
				}
			} else if len(got) != 1 || got[0].ProjectID != src.other.ID || !got[0].WikiReviewed {
				t.Fatalf("project restore reused the remapped cache or cleared a peer: %+v", got)
			}
			var triggers int
			err = db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master
				WHERE type='trigger' AND name IN ('wiki_source_scan_changed','wiki_source_scan_deleted')`).Scan(&triggers)
			if err != nil || triggers != 2 {
				t.Fatalf("live cache invalidation triggers were removed with the portable copies: %d %v", triggers, err)
			}
		})
	}
}

func TestWikiProjectRollbackRestoresCacheForAThreadMovedFromAnotherProject(t *testing.T) {
	ctx := t.Context()
	src := seed(t)
	moved := &store.Thread{ProjectID: src.other.ID}
	if err := src.st.CreateThread(moved); err != nil {
		t.Fatal(err)
	}
	eventID := seedWikiBackupAck(t, src.st, moved)
	if err := src.st.SetThreadProject(moved.ID, src.project.ID); err != nil {
		t.Fatal(err)
	}
	var original store.WikiSourceScan
	query := src.st.DB().Where("project_id=? AND event_id=?", src.other.ID, eventID)
	if err := query.Take(&original).Error; err != nil || !original.LocallyIndexed || !original.WikiReviewed {
		t.Fatalf("moved thread never retained its prior owner's acknowledgement: %+v %v", original, err)
	}
	db, err := src.st.DB().DB()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "project.zip")
	if _, err := Create(ctx, Options{DataDir: src.dir, DB: db, Kind: KindProject, ProjectID: src.project.ID, Output: path}); err != nil {
		t.Fatal(err)
	}
	refusal := errors.New("fixture refused moved-thread file swap")
	beforeFileSwap = func() error { return refusal }
	_, err = Restore(ctx, RestoreOptions{DataDir: src.dir, DB: db, Path: path, Confirm: true, ReplaceProject: true})
	beforeFileSwap = nil
	if !errors.Is(err, refusal) {
		t.Fatalf("expected compensating project rollback: %v", err)
	}
	var restored store.WikiSourceScan
	if err := query.Take(&restored).Error; err != nil || !reflect.DeepEqual(original, restored) {
		t.Fatalf("rollback lost the acknowledgement invalidated by event deletion: %+v %v", restored, err)
	}
	thread, err := src.st.GetThread(moved.ID)
	if err != nil || thread.ProjectID != src.project.ID {
		t.Fatalf("rollback did not preserve the moved thread's current owner: %+v %v", thread, err)
	}
	if _, err := Restore(ctx, RestoreOptions{DataDir: src.dir, DB: db, Path: path, Confirm: true, ReplaceProject: true}); err != nil {
		t.Fatal(err)
	}
	var oldCacheRows int64
	if err := src.st.DB().Model(&store.WikiSourceScan{}).Where("event_id=?", eventID).Count(&oldCacheRows).Error; err != nil || oldCacheRows != 0 {
		t.Fatalf("a successful project restore retained acknowledgement for a remapped event: %d %v", oldCacheRows, err)
	}
}

func seedWikiBackupAck(t *testing.T, st *store.Store, thread *store.Thread) uint {
	t.Helper()
	ctx := t.Context()
	turn := store.Turn{ThreadID: thread.ID, Status: store.TurnDone}
	if err := st.CreateTurn(&turn); err != nil {
		t.Fatal(err)
	}
	ev := store.Event{ThreadID: thread.ID, TurnID: turn.ID, Kind: "user_message", Text: "Observed source."}
	if err := st.AppendEvent(&ev); err != nil {
		t.Fatal(err)
	}
	job := store.ProjectDistillation{ProjectID: thread.ProjectID, ID: "wiki-" + thread.ProjectID, State: "running", WikiGraph: true}
	if _, err := st.StartDistillationQueue(ctx, job, thread.ID, func(int) ([]byte, error) { return []byte("{}"), nil }); err != nil {
		t.Fatal(err)
	}
	job.Receipt = []byte(fmt.Sprintf("{\"wiki_scan_cursor\":%d}", ev.ID))
	if err := st.CheckpointWikiScan(ctx, job, 0, []uint{ev.ID}); err != nil {
		t.Fatal(err)
	}
	job.CursorEventID = ev.ID
	if err := st.CheckpointWikiReview(ctx, job, 0, []uint{ev.ID}); err != nil {
		t.Fatal(err)
	}
	return ev.ID
}
