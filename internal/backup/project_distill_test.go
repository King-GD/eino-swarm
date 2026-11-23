package backup

import (
	"path/filepath"
	"testing"

	"github.com/LubyRuffy/eino-swarm/internal/store"
)

func TestDistillationReceiptsFollowBackupPrivacyAndProjectScope(t *testing.T) {
	src := seed(t)
	ctx := t.Context()
	for _, p := range []*store.Project{src.project, src.other} {
		if err := src.st.StartProjectDistillation(ctx, store.ProjectDistillation{
			ProjectID: p.ID, ID: "review-" + p.ID, State: "running", Receipt: []byte(`{"state":"running"}`),
		}); err != nil {
			t.Fatal(err)
		}
	}
	sqlDB, err := src.st.DB().DB()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name       string
		options    Options
		wantSource bool
		wantOther  bool
	}{
		{name: "full", options: Options{Kind: KindFull}, wantSource: true, wantOther: true},
		{name: "full-with-omission", options: Options{Kind: KindFull, OmitThreads: []OmittedThread{{ID: src.thread.ID}}}, wantOther: true},
		{name: "project", options: Options{Kind: KindProject, ProjectID: src.project.ID}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opt := tc.options
			opt.DataDir, opt.DB, opt.Output = src.dir, sqlDB, filepath.Join(t.TempDir(), "source.zip")
			if _, err := Create(ctx, opt); err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			if _, err := Restore(ctx, RestoreOptions{DataDir: dir, Path: opt.Output, Confirm: true, Replace: true}); err != nil {
				t.Fatal(err)
			}
			dst, err := store.Open(filepath.Join(dir, filepath.Base(src.cfg.DBPath())))
			if err != nil {
				t.Fatal(err)
			}
			defer dst.Close()
			for projectID, want := range map[string]bool{src.project.ID: tc.wantSource, src.other.ID: tc.wantOther} {
				row, err := dst.ProjectDistillationReceipt(ctx, projectID)
				if err != nil || (row != nil) != want {
					t.Fatalf("receipt for %s present=%v want %v, %v", projectID, row != nil, want, err)
				}
			}
		})
	}
}
