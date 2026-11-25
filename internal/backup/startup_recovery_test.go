package backup

import (
	"database/sql"
	"net/url"
	"path/filepath"
	"testing"

	"github.com/LubyRuffy/eino-swarm/internal/store"
)

func TestPortableBackupRecreatesRecoveryPointersForImportedEvents(t *testing.T) {
	for _, kind := range []Kind{KindFull, KindProject} {
		t.Run(string(kind), func(t *testing.T) {
			ctx := t.Context()
			src := seed(t)
			turn := &store.Turn{ThreadID: src.thread.ID, Status: store.TurnDone}
			if err := src.st.CreateTurn(turn); err != nil {
				t.Fatal(err)
			}
			origin := store.Event{ThreadID: src.thread.ID, TurnID: turn.ID, Kind: "generation_started", MessageID: "unfinished"}
			if err := src.st.AppendEvent(&origin); err != nil {
				t.Fatal(err)
			}
			db, err := src.st.DB().DB()
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "restore.zip")
			if _, err := Create(ctx, Options{DataDir: src.dir, DB: db, Kind: kind, ProjectID: src.project.ID, Output: path}); err != nil {
				t.Fatal(err)
			}
			assertArchiveOmitsStartupIndex(t, path)
			if _, err := Restore(ctx, RestoreOptions{DataDir: src.dir, DB: db, Path: path, Confirm: true, Replace: true, ReplaceProject: true}); err != nil {
				t.Fatal(err)
			}
			if _, err := src.st.BackfillStartupRecovery(ctx); err != nil {
				t.Fatal(err)
			}
			events, err := src.st.StartupGenerationReceipts(ctx, src.thread.ID)
			if err != nil || len(events) != 1 || events[0].TurnID != origin.TurnID || events[0].Seq != origin.Seq ||
				events[0].MessageID != origin.MessageID || events[0].Kind != origin.Kind {
				t.Fatalf("restore lost the original output identity: %+v, %v", events, err)
			}
		})
	}
}

func assertArchiveOmitsStartupIndex(t *testing.T, path string) {
	t.Helper()
	arc, err := openArchive(path)
	if err != nil {
		t.Fatal(err)
	}
	defer arc.close()
	uri := url.URL{Scheme: "file", Path: arc.dbPath, RawQuery: "mode=ro"}
	db, err := sql.Open("sqlite", uri.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var objects int
	if err := db.QueryRowContext(t.Context(), `SELECT count(*) FROM sqlite_master
		WHERE name GLOB 'startup_recovery_*' OR name GLOB 'idx_startup_*'`).Scan(&objects); err != nil || objects != 0 {
		t.Fatalf("archive retained %d database-local recovery objects: %v", objects, err)
	}
}
