package memoryexperiment

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/LubyRuffy/eino-swarm/internal/memory"
	"github.com/cloudwego/eino/components/tool"
)

func recallStore(t *testing.T) *memory.Store {
	t.Helper()
	return memory.New(t.TempDir(), 2200)
}

func recordRecallSkill(t *testing.T, store *memory.Store, name, description, body string) {
	t.Helper()
	if _, err := store.WriteSkill(name, description, body); err != nil {
		t.Fatal(err)
	}
}

func invokeRecall(t *testing.T, recall tool.InvokableTool, query string) recallResult {
	t.Helper()
	args, err := json.Marshal(map[string]string{"query": query})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := recall.InvokableRun(context.Background(), string(args))
	if err != nil {
		t.Fatal(err)
	}
	var result recallResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestRecallFindsARelevantSkillBeyondTheInjectedAlphabeticalPrefix(t *testing.T) {
	store := recallStore(t)
	for i := range 50 {
		recordRecallSkill(t, store, fmt.Sprintf("a-topic%02d", i), fmt.Sprintf("Topic%02d.", i), fmt.Sprintf("Procedure %02d.", i))
	}
	recordRecallSkill(t, store, "z-recovery", "When recovering a dropped presence stream.", "Keep the native owner on its original engine.")
	skills, err := store.ListSkills()
	if err != nil {
		t.Fatal(err)
	}
	prompt := memory.PromptSections("", memory.Snapshot{Limit: 2200}, skills, 50, true)
	if strings.Contains(prompt, "z-recovery") {
		t.Fatal("fixture no longer reproduces the current alphabetic-prefix omission")
	}
	recall, err := NewRecallTool(store, 3, 2000)
	if err != nil {
		t.Fatal(err)
	}
	result := invokeRecall(t, recall, "recovering presence stream")
	if !result.Success || len(result.Hits) != 1 || result.Hits[0].Name != "z-recovery" || result.Hits[0].ReadTool != memory.ToolSkillView {
		t.Fatalf("relevant recorded procedure is unreachable: %+v", result)
	}
	// Recall discovers applicability; opening the real skill is a separate act.
	read := memory.ViewTools(store)[0].(tool.InvokableTool)
	full, err := read.InvokableRun(context.Background(), `{"name":"z-recovery"}`)
	if err != nil || !strings.Contains(full, "Keep the native owner") {
		t.Fatalf("the actual skill_view cannot resolve the recalled name: %s %v", full, err)
	}
}

func TestRecallRanksMetadataDeterministicallyWithoutOpeningBodies(t *testing.T) {
	store := recallStore(t)
	recordRecallSkill(t, store, "b-worker", "Worker recovery.", "Never reveal this body via recall.")
	recordRecallSkill(t, store, "a-worker", "Worker recovery.", "Other private procedure body.")
	recordRecallSkill(t, store, "rare", "When investigating a lease.", "Not a metadata match for recovery.")
	recall, err := NewRecallTool(store, 1, 1000)
	if err != nil {
		t.Fatal(err)
	}
	for range 5 {
		result := invokeRecall(t, recall, "WORKER recovery")
		if len(result.Hits) != 1 || result.Hits[0].Name != "a-worker" || result.TotalMatched != 2 || !result.Truncated {
			t.Fatalf("unstable ranking or inaccurate truncation: %+v", result)
		}
	}
	skills, err := store.ListSkills()
	if err != nil {
		t.Fatal(err)
	}
	for _, skill := range skills {
		if skill.Views != 0 || !skill.LastViewedAt.IsZero() {
			t.Fatalf("metadata recall counted an unperformed skill view: %+v", skill)
		}
	}
	if got := invokeRecall(t, recall, "Never reveal"); len(got.Hits) != 0 || got.Truncated || got.TotalMatched != 0 {
		t.Fatalf("recall unexpectedly searched body or fabricated matches: %+v", got)
	}
	if got := invokeRecall(t, recall, "lease worker"); got.Hits[0].Name != "rare" {
		t.Fatalf("rarer metadata term did not outrank the common one: %+v", got)
	}
}

func TestRecallPreservesCJKApplicabilityAndCompleteBudgetedHits(t *testing.T) {
	store := recallStore(t)
	description := "仅在恢复桌面连接时使用；不要启动第二个引擎。"
	recordRecallSkill(t, store, "desktop", description, "步骤正文不应自动注入。")
	recordRecallSkill(t, store, "mobile", "处理手机绑定。", "移动端步骤。")
	recall, err := NewRecallTool(store, 5, 1000)
	if err != nil {
		t.Fatal(err)
	}
	got := invokeRecall(t, recall, "恢复桌面连接")
	if len(got.Hits) != 1 || got.Hits[0].Description != description {
		t.Fatalf("CJK applicability was lost: %+v", got)
	}
	args := `{"query":"恢复桌面连接"}`
	full, err := recall.InvokableRun(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	budget := utf8.RuneCountInString(full)
	exact, err := NewRecallTool(store, 5, budget)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := exact.InvokableRun(context.Background(), args)
	if err != nil || raw != full {
		t.Fatalf("exact budget refused a complete hit: %s %v", raw, err)
	}
	short, err := NewRecallTool(store, 5, budget-1)
	if err != nil {
		t.Fatal(err)
	}
	raw, err = short.InvokableRun(context.Background(), args)
	if err != nil || utf8.RuneCountInString(raw) > budget-1 {
		t.Fatalf("budget exceeded: %s %v", raw, err)
	}
	var truncated recallResult
	if err := json.Unmarshal([]byte(raw), &truncated); err != nil {
		t.Fatal(err)
	}
	if len(truncated.Hits) != 0 || !truncated.Truncated || truncated.TotalMatched != 1 {
		t.Fatalf("budget must omit a whole hit, never truncate its conditions: %+v", truncated)
	}
}

func TestRecallRejectsMalformedRequestsAndInvalidBudgets(t *testing.T) {
	store := recallStore(t)
	for _, tc := range []struct {
		name          string
		store         *memory.Store
		limit, budget int
	}{{"nil", nil, 1, 1000}, {"limit", store, 0, 1000}, {"budget", store, 1, 0}, {"envelope", store, 1, 10}} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewRecallTool(tc.store, tc.limit, tc.budget); err == nil {
				t.Fatal("invalid configuration accepted")
			}
		})
	}
	recall, err := NewRecallTool(store, 1, 1000)
	if err != nil {
		t.Fatal(err)
	}
	info, err := recall.Info(context.Background())
	if err != nil || info.Name != "recall_memory" || !strings.Contains(info.Desc, "untrusted") {
		t.Fatalf("missing discovery schema or source boundary: %+v %v", info, err)
	}
	for _, args := range []string{`null`, `{`, `{}`, `{"query":" "}`, `{"query":"!!!"}`, `{"query":3}`, `{"query":"a","extra":1}`, `{"query":"a"} {}`} {
		t.Run(args, func(t *testing.T) {
			if _, err := recall.InvokableRun(context.Background(), args); err == nil {
				t.Fatal("invalid recall request accepted")
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := recall.InvokableRun(ctx, `{"query":"anything"}`); err != context.Canceled {
		t.Fatalf("cancellation lost: %v", err)
	}
	if err := os.WriteFile(filepath.Join(store.Dir(), memory.SkillsDir), []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := recall.InvokableRun(context.Background(), `{"query":"anything"}`); err == nil {
		t.Fatal("unreadable catalog was represented as an empty search")
	}
}

func TestRecallTermsKeepWordsAndCJKBoundaries(t *testing.T) {
	got := recallTerms("Case_42 臺 桌面恢复 あい アイ 가나!")
	for _, want := range []string{"case", "42", "臺", "桌面", "面恢", "恢复", "あい", "アイ", "가나"} {
		if _, ok := got[want]; !ok {
			t.Errorf("missing term %q in %v", want, got)
		}
	}
	if len(got) != 9 {
		t.Fatalf("unexpected cross-boundary terms: %v", got)
	}
}
