package memory

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/tool"
)

func TestSkillViewsAreAtomicWithoutChangingContentIdentity(t *testing.T) {
	s := newSkillStore(t)
	before := mustSkill(t, s, "read-procedure", "When a recorded process applies", "Inspect the input and verify the output.")
	const views = 32
	var wg sync.WaitGroup
	for range views {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.ViewSkill(before.Name); err != nil {
				t.Errorf("view: %v", err)
			}
		}()
	}
	wg.Wait()
	after, err := s.ReadSkill(before.Name)
	if err != nil || after.Views != views || after.LastViewedAt.IsZero() {
		t.Fatalf("usage=%+v err=%v", after.SkillInfo, err)
	}
	if after.UpdatedAt != before.UpdatedAt || after.ContentDigest != before.ContentDigest || after.Body != before.Body {
		t.Fatalf("view changed the skill content: before=%+v after=%+v", before, after)
	}
	list, err := s.ListSkills()
	if err != nil || len(list) != 1 || list[0].Views != views {
		t.Fatalf("list=%+v err=%v", list, err)
	}
	priorPrompt := PromptSections("", Snapshot{}, []SkillInfo{before.SkillInfo}, 10, true)
	if got := PromptSections("", Snapshot{}, list, 10, true); got != priorPrompt {
		t.Fatalf("usage changed the prompt index: %s", got)
	}
	raw, err := os.ReadFile(filepath.Join(s.skillsDir(), before.Name, SkillUsageFile))
	var sidecar map[string]any
	if err != nil || json.Unmarshal(raw, &sidecar) != nil || sidecar["views"] != float64(views) || len(sidecar) != 2 {
		t.Fatalf("sidecar=%s err=%v", raw, err)
	}
	wire, err := json.Marshal(after.SkillInfo)
	if err != nil || !strings.Contains(string(wire), `"views":32`) || !strings.Contains(string(wire), `"last_viewed_at":`) {
		t.Fatalf("wire=%s err=%v", wire, err)
	}
}

func TestWorkerSkillViewCountsOnlySuccessfulReads(t *testing.T) {
	s := newSkillStore(t)
	mustSkill(t, s, "worker-reference", "When worker verification is needed", "Inspect records and validate the expected result.")
	view := ViewTools(s)[0].(tool.InvokableTool)
	for _, name := range []string{"worker-reference", "worker-reference", "missing"} {
		args, _ := json.Marshal(map[string]string{"name": name})
		if _, err := view.InvokableRun(context.Background(), string(args)); err != nil {
			t.Fatal(err)
		}
	}
	sk, err := s.ReadSkill("worker-reference")
	if err != nil || sk.Views != 2 {
		t.Fatalf("worker views=%d err=%v", sk.Views, err)
	}
	if _, err := os.Stat(filepath.Join(s.skillsDir(), "missing", SkillUsageFile)); !os.IsNotExist(err) {
		t.Fatalf("a failed view wrote usage: %v", err)
	}
	if _, err := s.ViewSkill("../escape"); !errors.Is(err, ErrBadName) {
		t.Fatalf("invalid view=%v", err)
	}
}

func TestDamagedSkillUsageNeverHidesAReadableSkill(t *testing.T) {
	for _, raw := range []string{"{broken", `{"views":-2}`, `{"views":"many"}`, `{"views":2,"last_viewed_at":"invalid"}`} {
		t.Run(raw, func(t *testing.T) {
			s := newSkillStore(t)
			before := mustSkill(t, s, "usable-skill", "When the process is needed", "Read the source and verify its result.")
			path := filepath.Join(s.skillsDir(), before.Name, SkillUsageFile)
			if err := os.WriteFile(path, []byte(raw), filePerm); err != nil {
				t.Fatal(err)
			}
			read, err := s.ReadSkill(before.Name)
			if err != nil || read.Body != before.Body || read.Views != 0 || !read.LastViewedAt.IsZero() {
				t.Fatalf("read=%+v err=%v", read, err)
			}
			view, err := s.ViewSkill(before.Name)
			if err != nil || view.Views != 1 || view.LastViewedAt.IsZero() {
				t.Fatalf("view=%+v err=%v", view, err)
			}
		})
	}
}

func TestMergedSkillsKeepCombinedUsageAndLatestView(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(map[bool]string{false: "new-keeper", true: "existing-keeper"}[existing], func(t *testing.T) {
			s := newSkillStore(t)
			if existing {
				mustSkill(t, s, "combined", "When combining records", "Preserve established records.")
				seedSkillUsage(t, s, "combined", 3, time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC))
			}
			mustSkill(t, s, "first-source", "When inspecting references", "Inspect reference values.")
			mustSkill(t, s, "second-source", "When validating outcomes", "Validate resulting output.")
			latest := time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC)
			seedSkillUsage(t, s, "first-source", 4, latest)
			seedSkillUsage(t, s, "second-source", 2, latest.Add(-time.Hour))
			got, deleted, err := s.MergeSkills("combined", []string{"first-source", "second-source", "first-source"}, "When combining records", "Preserve established records, inspect reference values and validate resulting output.")
			want := int64(6)
			if existing {
				want += 3
			}
			if err != nil || got.Views != want || !got.LastViewedAt.Equal(latest) || len(deleted) != 2 {
				t.Fatalf("merged=%+v deleted=%v err=%v", got, deleted, err)
			}
			if err := s.DeleteSkill("combined"); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(s.skillsDir(), "combined")); !os.IsNotExist(err) {
				t.Fatalf("delete retained skill usage: %v", err)
			}
		})
	}
}

func TestFamilyFoldPreservesViewsForANewKeeper(t *testing.T) {
	s := newSkillStore(t)
	plantSkill(t, s.Dir(), "batch-audit-read", "When reading an audit batch", "Read each audit record.")
	plantSkill(t, s.Dir(), "batch-audit-check", "When checking an audit batch", "Check each audit record.")
	latest := time.Date(2026, 1, 9, 0, 0, 0, 0, time.UTC)
	seedSkillUsage(t, s, "batch-audit-read", 2, latest)
	seedSkillUsage(t, s, "batch-audit-check", 7, latest.Add(-time.Hour))
	report, err := s.FoldSkillFamiliesReport()
	if err != nil || len(report.Merged) != 1 {
		t.Fatalf("report=%+v err=%v", report, err)
	}
	sk, err := s.ReadSkill(report.Merged[0].Keep)
	if err != nil || sk.Views != 9 || !sk.LastViewedAt.Equal(latest) {
		t.Fatalf("folded=%+v err=%v", sk, err)
	}
}

func TestCopiedSkillStartsWithItsOwnUsage(t *testing.T) {
	src, dst := newSkillStore(t), newSkillStore(t)
	sk := mustSkill(t, src, "copied-procedure", "When migrating a process", "Inspect source data and verify migrated output.")
	seedSkillUsage(t, src, sk.Name, 13, time.Now().UTC())
	sk, err := src.ReadSkill(sk.Name)
	if err != nil {
		t.Fatal(err)
	}
	copy, err := dst.ImportSkill(sk.Name, sk.Description, sk.Body, OriginOfCopy("source-project", sk))
	if err != nil || copy.Views != 0 || !copy.LastViewedAt.IsZero() {
		t.Fatalf("copy=%+v err=%v", copy, err)
	}
	if _, err := os.Stat(filepath.Join(dst.skillsDir(), copy.Name, SkillUsageFile)); !os.IsNotExist(err) {
		t.Fatalf("copy carried usage: %v", err)
	}
	if _, err := dst.ViewSkill(copy.Name); err != nil {
		t.Fatal(err)
	}
	if _, err := dst.PullSkill(copy.Name, sk, false); err != nil {
		t.Fatal(err)
	}
	copy, _ = dst.ReadSkill(copy.Name)
	if copy.Views != 1 {
		t.Fatalf("pull reset local usage: %+v", copy)
	}
}

func TestStaleSkillsUseLastViewOrCreationWithAnInclusiveBoundary(t *testing.T) {
	now := time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC)
	cutoff := now.AddDate(0, 0, -30)
	got := StaleSkillCandidates([]SkillInfo{
		{Name: "unviewed-old", UpdatedAt: cutoff.Add(-time.Second)},
		{Name: "never-dated"},
		{Name: "view-boundary", UpdatedAt: now, LastViewedAt: cutoff},
		{Name: "recent-view", UpdatedAt: cutoff.Add(-time.Hour), LastViewedAt: cutoff.Add(time.Second)},
		{Name: "recent-write", UpdatedAt: cutoff.Add(time.Second)},
	}, now, 30)
	if len(got) != 2 || got[0].Name != "unviewed-old" || got[1].Name != "view-boundary" {
		t.Fatalf("candidates=%+v", got)
	}
	if got := StaleSkillCandidates([]SkillInfo{{Name: "old", UpdatedAt: cutoff}}, now, 0); len(got) != 0 {
		t.Fatalf("a disabled threshold produced candidates: %+v", got)
	}
}

func TestUsageWriteFailureRetainsTheSkillAndMergeSources(t *testing.T) {
	s := newSkillStore(t)
	before := mustSkill(t, s, "kept-procedure", "When retaining records", "Retain the source records.")
	mustSkill(t, s, "incoming-procedure", "When checking new values", "Check the incoming values.")
	path := filepath.Join(s.skillsDir(), before.Name, SkillUsageFile)
	if err := os.Mkdir(path, dirPerm); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ViewSkill(before.Name); err == nil {
		t.Fatal("a failed usage write was reported as counted")
	}
	read, err := s.ReadSkill(before.Name)
	if err != nil || read.Body != before.Body || read.Views != 0 {
		t.Fatalf("usage failure hid the source: %+v err=%v", read, err)
	}
	if _, _, err := s.MergeSkills(before.Name, []string{"incoming-procedure"}, "When retaining records and checking values", "Retain the source records and check the incoming values."); err == nil {
		t.Fatal("merge ignored a usage write failure")
	}
	if _, err := s.ReadSkill("incoming-procedure"); err != nil {
		t.Fatalf("failed merge discarded its source: %v", err)
	}
}

func TestUsageEncodingRefusesAnUnrepresentableTimestamp(t *testing.T) {
	s := newSkillStore(t)
	if err := s.writeSkillUsageLocked("bad-time", skillUsage{LastViewedAt: time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)}); err == nil {
		t.Fatal("invalid JSON time was stored")
	}
}

func TestFamilyFoldRetainsSourcesWhenUsageCannotBeWritten(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(map[bool]string{false: "new-keeper", true: "existing-keeper"}[existing], func(t *testing.T) {
			store := newSkillStore(t)
			if existing {
				plantSkill(t, store.Dir(), "source-audit", "When auditing inputs", "Preserve the original keeper step.")
				seedSkillUsage(t, store, "source-audit", 3, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
			}
			plantSkill(t, store.Dir(), "source-audit-read", "When reading audit input", "Read the audit input.")
			plantSkill(t, store.Dir(), "source-audit-check", "When checking audit input", "Check the audit input.")
			seedSkillUsage(t, store, "source-audit-read", 2, time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC))
			seedSkillUsage(t, store, "source-audit-check", 4, time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC))
			blocked := filepath.Join(store.skillsDir(), "source-audit", SkillUsageFile+".tmp")
			if err := os.MkdirAll(blocked, dirPerm); err != nil {
				t.Fatal(err)
			}
			report, err := store.FoldSkillFamiliesReport()
			if err == nil {
				t.Fatal("family fold ignored a usage write failure")
			}
			action := "create"
			before := 2
			unchanged := 2
			if existing {
				action = "patch"
				before = 3
			}
			if !report.Folded() || len(report.Changes) != 1 || report.Changes[0].Action != action || report.Changes[0].Name != "source-audit" || len(report.Merged) != 0 || len(report.Deleted) != 0 || report.Before != before || report.After != 3 || report.Unchanged != unchanged {
				t.Fatalf("usage failure hid the saved keeper or claimed a completed merge: %+v", report)
			}
			if existing && (len(report.Created) != 0 || strings.Join(report.Patched, ",") != "source-audit") {
				t.Fatalf("existing keeper partial write=%+v", report)
			}
			if !existing && (strings.Join(report.Created, ",") != "source-audit" || len(report.Patched) != 0) {
				t.Fatalf("new keeper partial write=%+v", report)
			}
			for name, want := range map[string]int64{"source-audit-read": 2, "source-audit-check": 4} {
				got, err := store.ReadSkill(name)
				if err != nil || got.Views != want {
					t.Fatalf("failed fold discarded source content or use: %s %+v %v", name, got, err)
				}
			}
			kept, err := store.ReadSkill("source-audit")
			if err != nil || !strings.Contains(kept.Body, "Read the audit input.") || !strings.Contains(kept.Body, "Check the audit input.") {
				t.Fatalf("saved partial keeper lost source content: %+v %v", kept, err)
			}
			if err := os.Remove(blocked); err != nil {
				t.Fatal(err)
			}
			if _, err := store.FoldSkillFamiliesReport(); err != nil {
				t.Fatal(err)
			}
			kept, err = store.ReadSkill("source-audit")
			want := int64(6)
			if existing {
				want += 3
			}
			if err != nil || kept.Views != want {
				t.Fatalf("retry lost or double-counted usage: %+v %v", kept, err)
			}
		})
	}
}

func TestMergeDeleteFailureDoesNotDoubleCountOnRetry(t *testing.T) {
	for _, fold := range []bool{false, true} {
		t.Run(map[bool]string{false: "manual", true: "family"}[fold], func(t *testing.T) {
			requireSkillRemovalPermissionFailure(t, false)
			s := newSkillStore(t)
			plantSkill(t, s.Dir(), "stable-audit", "When auditing records", "Inspect all recorded inputs.")
			plantSkill(t, s.Dir(), "stable-audit-first", "When reading audit records", "Read all recorded inputs.")
			plantSkill(t, s.Dir(), "stable-audit-second", "When checking audit records", "Check all recorded inputs.")
			latest := time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC)
			seedSkillUsage(t, s, "stable-audit", 3, latest.Add(-2*time.Hour))
			seedSkillUsage(t, s, "stable-audit-first", 2, latest.Add(-time.Hour))
			seedSkillUsage(t, s, "stable-audit-second", 7, latest)
			blocked := filepath.Join(s.skillsDir(), "stable-audit-second")
			if err := os.Chmod(blocked, 0o500); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Chmod(blocked, dirPerm) })
			var report FoldReport
			merge := func(sources []string) error {
				if fold {
					var err error
					report, err = s.FoldSkillFamiliesReport()
					return err
				}
				_, _, err := s.MergeSkills("stable-audit", sources, "", "")
				return err
			}
			if err := merge([]string{"stable-audit-first", "stable-audit-second"}); err == nil {
				t.Fatal("real source deletion failure was not reported")
			}
			if fold && (!report.Folded() || report.After != 2 || report.Unchanged != 1 || len(report.Changes) != 1 || report.Changes[0].Name != "stable-audit" || report.Changes[0].Text != "stable-audit-first" || strings.Join(report.Deleted, ",") != "stable-audit-first") {
				t.Fatalf("failed family fold hid the saved keeper or reported an undeleted source: %+v", report)
			}
			kept, err := s.ReadSkill("stable-audit")
			if err != nil || kept.Views != 5 {
				t.Fatalf("rollback lost the already transferred source: %+v err=%v", kept, err)
			}
			remaining, err := s.ReadSkill("stable-audit-second")
			if err != nil || remaining.Views != 7 {
				t.Fatalf("failed source lost its own use: %+v err=%v", remaining, err)
			}
			if _, err := s.ReadSkill("stable-audit-first"); !errors.Is(err, ErrNoMatch) {
				t.Fatalf("the first source was not transferred: %v", err)
			}
			if err := os.Chmod(blocked, dirPerm); err != nil {
				t.Fatal(err)
			}
			if err := merge([]string{"stable-audit-second"}); err != nil {
				t.Fatalf("retry: %v", err)
			}
			kept, err = s.ReadSkill("stable-audit")
			if err != nil || kept.Views != 12 || !kept.LastViewedAt.Equal(latest) {
				t.Fatalf("retry counted a source twice: %+v err=%v", kept, err)
			}
		})
	}
}

func TestPartialSourceDeletionRetainsUsageInTheKeeper(t *testing.T) {
	for _, fold := range []bool{false, true} {
		t.Run(map[bool]string{false: "manual", true: "family"}[fold], func(t *testing.T) {
			requireSkillRemovalPermissionFailure(t, true)
			store := newSkillStore(t)
			keeper := "Inspect all source records and verify the active generation before marking the recorded batch complete."
			source := "Do not inspect all source records and verify the active generation before marking the recorded batch complete."
			plantSkill(t, store.Dir(), "audit-keeper", "When inspecting records", keeper)
			plantSkill(t, store.Dir(), "audit-keeper-source", "When validating checks", source)
			latest := time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC)
			seedSkillUsage(t, store, "audit-keeper", 2, latest.Add(-time.Hour))
			seedSkillUsage(t, store, "audit-keeper-source", 7, latest)
			blocked := filepath.Join(store.skillsDir(), "audit-keeper-source", "protected")
			if err := os.Mkdir(blocked, dirPerm); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(blocked, "retained"), []byte("owned fixture"), filePerm); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(blocked, 0); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Chmod(blocked, dirPerm) })
			var report FoldReport
			var err error
			if fold {
				report, err = store.FoldSkillFamiliesReport()
			} else {
				_, _, err = store.MergeSkills("audit-keeper", []string{"audit-keeper-source"}, "", "")
			}
			if err == nil {
				t.Fatal("a partial source delete was not reported")
			}
			if _, err := store.ReadSkill("audit-keeper-source"); !errors.Is(err, ErrNoMatch) {
				t.Fatalf("fixture did not partially remove the source: %v", err)
			}
			kept, err := store.ReadSkill("audit-keeper")
			if err != nil || kept.Views != 9 || !kept.LastViewedAt.Equal(latest) {
				t.Fatalf("partial delete lost source use: %+v err=%v", kept, err)
			}
			for _, original := range []string{keeper, source} {
				if !strings.Contains(strings.Join(strings.Fields(kept.Body), " "), strings.Join(strings.Fields(original), " ")) {
					t.Fatalf("partial deletion lost the complete source body: %q", original)
				}
			}
			if fold && (!report.Folded() || report.After != 1 || report.Unchanged != 0 || len(report.Changes) != 1 || report.Changes[0].Text != "audit-keeper-source" || strings.Join(report.Deleted, ",") != "audit-keeper-source") {
				t.Fatalf("partial family receipt hid actual writes: %+v", report)
			}
		})
	}
}

func TestCopyRefusesAnOrphanDirectoryWithoutAdoptingItsUsage(t *testing.T) {
	s := newSkillStore(t)
	path := filepath.Join(s.skillsDir(), "new-copy")
	if err := os.MkdirAll(path, dirPerm); err != nil {
		t.Fatal(err)
	}
	seedSkillUsage(t, s, "new-copy", 19, time.Now().UTC())
	_, err := s.ImportSkill("new-copy", "When validating a copied process", "Inspect the copied process and validate the expected output.", SkillOrigin{ProjectID: "upstream", Name: "new-copy"})
	if !errors.Is(err, ErrSkillExists) {
		t.Fatalf("copy adopted an occupied directory: %v", err)
	}
	if _, err := s.ReadSkill("new-copy"); !errors.Is(err, ErrNoMatch) {
		t.Fatalf("refused copy wrote content: %v", err)
	}
	if usage := s.readSkillUsage("new-copy"); usage.Views != 19 {
		t.Fatalf("refusal changed the orphaned directory: %+v", usage)
	}
}

func seedSkillUsage(t *testing.T, s *Store, name string, views int64, last time.Time) {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"views": views, "last_viewed_at": last})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.skillsDir(), name, SkillUsageFile), raw, filePerm); err != nil {
		t.Fatal(err)
	}
}

// These failure tests need a real permission denial with a specific retained
// source shape. Privilege and filesystem semantics vary, so probe the same
// private-file operation rather than assuming a UID or operating system.
func requireSkillRemovalPermissionFailure(t *testing.T, partial bool) {
	t.Helper()
	source := filepath.Join(t.TempDir(), "source")
	if err := os.Mkdir(source, dirPerm); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(source, SkillFile)
	const content = "permission probe"
	if err := os.WriteFile(path, []byte(content), filePerm); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, SkillUsageFile), []byte("{}"), filePerm); err != nil {
		t.Fatal(err)
	}
	blocked, mode := source, os.FileMode(0500)
	if partial {
		blocked, mode = filepath.Join(source, "protected"), 0
		if err := os.Mkdir(blocked, dirPerm); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(blocked, "retained"), []byte(content), filePerm); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(blocked, mode); err != nil {
		t.Fatalf("set up removal permission probe: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(blocked, dirPerm); err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Errorf("restore removal permission probe: %v", err)
		}
	})
	err := os.RemoveAll(source)
	if err == nil {
		t.Skipf("filesystem permits removal despite mode %04o; cannot inject required permission failure", mode)
	}
	if !os.IsPermission(err) {
		t.Fatalf("unexpected removal probe error: %v", err)
	}
	body, readErr := os.ReadFile(path)
	if readErr == nil && string(body) != content {
		t.Fatal("removal probe content changed unexpectedly")
	}
	missing := errors.Is(readErr, os.ErrNotExist)
	if readErr != nil && !missing && !os.IsPermission(readErr) {
		t.Fatalf("inspect removal probe source: %v", readErr)
	}
	if partial && !missing {
		t.Skip("filesystem cannot reproduce partial source-file removal before the permission error")
	}
	if !partial && readErr != nil {
		t.Skip("filesystem cannot reproduce a readable retained source after the permission error")
	}
	t.Logf("real removal permission probe succeeded: partial=%v source_missing=%v", partial, missing)
}
