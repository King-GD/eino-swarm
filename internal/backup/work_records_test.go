package backup

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/LubyRuffy/eino-swarm/internal/store"
)

func seedPrivateWork(t *testing.T, s *store.Store, th *store.Thread, title string) store.NuwaNode {
	t.Helper()
	turns, err := s.ListTurns(th.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) == 0 {
		t.Fatal("fixture thread needs a turn")
	}
	node := store.NuwaNode{
		Scope: "thread", ThreadID: th.ID, TurnID: turns[0].ID,
		Kind: "fact", Title: title, AgentID: "worker", Generation: 1,
	}
	receipt := store.NuwaWriteReceipt{Key: "backup:" + title, Hash: title, CallerProjectID: &th.ProjectID}
	if err := s.UpsertNuwaNode(t.Context(), &node, 0, receipt); err != nil {
		t.Fatal(err)
	}
	return node
}

func TestBackupKeepsPrivateWorkWithItsConversation(t *testing.T) {
	src := seed(t)
	private := seedPrivateWork(t, src.st, src.thread, "member-private-record-017")
	standalone := seedPrivateWork(t, src.st, src.standalone, "unrelated-private-record-067")
	verificationTurn := store.Turn{ThreadID: src.thread.ID, ProjectID: src.project.ID, Status: store.TurnRunning}
	if err := src.st.CreateTurn(&verificationTurn); err != nil {
		t.Fatal(err)
	}
	if err := src.st.AdmitNuwaVerification(t.Context(), store.NuwaVerifyAdmission{
		ProjectID: src.project.ID, ThreadID: src.thread.ID, TurnID: verificationTurn.ID,
		AgentID: "manager", CallID: "verify-private-source", Intent: "verify", TargetID: private.ID,
	}); err != nil {
		t.Fatal(err)
	}
	srcDB, err := src.st.DB().DB()
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name                           string
		kind                           Kind
		omit                           bool
		retainMember, retainStandalone bool
	}{
		{name: "full", kind: KindFull, retainMember: true, retainStandalone: true},
		{name: "project", kind: KindProject, retainMember: true},
		{name: "full omits member", kind: KindFull, omit: true, retainStandalone: true},
		{name: "project omits member", kind: KindProject, omit: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "work-records.zwai-backup")
			options := Options{DataDir: src.dir, DB: srcDB, Kind: tc.kind, Output: out}
			if tc.kind == KindProject {
				options.ProjectID = src.project.ID
			}
			if tc.omit {
				options.OmitThreads = []OmittedThread{{ID: src.thread.ID, Title: src.thread.Title, Reason: ReasonRunning}}
			}
			if _, err := Create(t.Context(), options); err != nil {
				t.Fatal(err)
			}
			dest := seed(t)
			destDB, err := dest.st.DB().DB()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := Restore(t.Context(), RestoreOptions{
				DataDir: dest.dir, DB: destDB, Path: out, Confirm: true, Replace: tc.kind == KindFull,
			}); err != nil {
				t.Fatal(err)
			}
			assertWorkBackupNode(t, dest.st, out, private, tc.retainMember)
			assertWorkBackupNode(t, dest.st, out, standalone, tc.retainStandalone)
			var slots int64
			if err := dest.st.DB().Model(&store.NuwaVerifySlot{}).Where("target_id = ?", private.ID).Count(&slots).Error; err != nil {
				t.Fatal(err)
			}
			if (slots == 1) != tc.retainMember || slots > 1 {
				t.Fatalf("restored %d verification slots for private work; conversation present=%t", slots, tc.retainMember)
			}
			if tc.kind == KindProject && !tc.omit {
				extra := seedPrivateWork(t, dest.st, src.thread, "work-created-after-backup-419")
				beforeFileSwap = func() error { return errors.New("fixture file promotion failed") }
				_, restoreErr := Restore(t.Context(), RestoreOptions{
					DataDir: dest.dir, DB: destDB, Path: out, Confirm: true, ReplaceProject: true,
				})
				beforeFileSwap = nil
				if restoreErr == nil {
					t.Fatal("expected the injected replacement failure")
				}
				var restored store.NuwaNode
				if err := dest.st.DB().First(&restored, "id = ?", extra.ID).Error; err != nil || restored.Title != extra.Title {
					t.Fatalf("rollback lost destination private work: %+v, %v", restored, err)
				}
				if _, err := Restore(t.Context(), RestoreOptions{
					DataDir: dest.dir, DB: destDB, Path: out, Confirm: true, ReplaceProject: true,
				}); err != nil {
					t.Fatal(err)
				}
				assertWorkBackupNode(t, dest.st, out, private, true)
				assertWorkBackupNode(t, dest.st, out, extra, false)
				if err := dest.st.SetThreadProject(src.thread.ID, dest.other.ID); err != nil {
					t.Fatal(err)
				}
				preview, err := Preview(t.Context(), dest.dir, destDB, out)
				if err != nil || preview.Action != ActionBlocked {
					t.Fatalf("moved private owner accepted as a project replacement: %+v, %v", preview, err)
				}
				tables := map[string]bool{}
				for _, conflict := range preview.Conflicts {
					if conflict.ID == private.ID && conflict.Owner == dest.other.ID {
						tables[conflict.Table] = true
					}
				}
				if !tables["nuwa_nodes"] || !tables["nuwa_revisions"] {
					t.Fatalf("conflict did not identify foreign work and audit ownership: %+v", preview.Conflicts)
				}
			}
		})
	}
}

func assertWorkBackupNode(t *testing.T, s *store.Store, archive string, node store.NuwaNode, retained bool) {
	t.Helper()
	if found := archiveHas(t, archive, node.Title); found != retained {
		t.Fatalf("archive contains %q = %t, want %t", node.Title, found, retained)
	}
	for _, check := range []struct {
		model  any
		column string
	}{
		{&store.NuwaNode{}, "id"},
		{&store.NuwaRevision{}, "node_id"},
	} {
		var count int64
		if err := s.DB().Model(check.model).Where(check.column+" = ?", node.ID).Count(&count).Error; err != nil {
			t.Fatal(err)
		}
		if (count == 1) != retained || count > 1 {
			t.Fatalf("restored %T for %s has %d rows, want present=%t", check.model, node.ID, count, retained)
		}
	}
}

func TestRestoreLegacyProjectArchiveKeepsSharedWorkVisible(t *testing.T) {
	src := seed(t)
	ctx := t.Context()
	currentDB, err := src.st.DB().DB()
	if err != nil {
		t.Fatal(err)
	}
	conn, err := currentDB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "old-project.db")
	err = exportProject(ctx, conn, src.project.ID, path)
	if closeErr := conn.Close(); err != nil || closeErr != nil {
		t.Fatalf("export legacy fixture: %v, %v", err, closeErr)
	}
	legacy, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = legacy.Close() })
	for _, statement := range []string{
		"DROP INDEX idx_nuwa_scoped_revision", "DROP INDEX idx_nuwa_request_key",
		"ALTER TABLE nuwa_nodes DROP COLUMN scope", "ALTER TABLE nuwa_revisions DROP COLUMN thread_id",
		"CREATE UNIQUE INDEX idx_nuwa_revision ON nuwa_revisions(project_id, revision)",
		"CREATE UNIQUE INDEX idx_nuwa_request ON nuwa_revisions(project_id, request_key)",
		"UPDATE nuwa_revisions SET payload = json_remove(payload, '$.scope')",
	} {
		if _, err := legacy.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}
	dbBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	manifest := Manifest{Format: FormatVersion, Kind: KindProject, ProjectID: src.project.ID,
		SHA256: map[string]string{databaseName: hashOf(dbBytes)}}
	archive := manifestZip(t, mustJSON(t, manifest), map[string][]byte{databaseName: dbBytes})
	dest := seed(t)
	destDB, err := dest.st.DB().DB()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Restore(ctx, RestoreOptions{DataDir: dest.dir, DB: destDB, Path: archive, Confirm: true}); err != nil {
		t.Fatal(err)
	}
	_, nodes, err := dest.st.NuwaSnapshot(ctx, src.project.ID)
	if err != nil || len(nodes) != 1 || nodes[0].Title != "nuwa-title" || nodes[0].Scope != store.WorkScopeProject {
		t.Fatalf("legacy shared work invisible after restore: %+v, %v", nodes, err)
	}
}
