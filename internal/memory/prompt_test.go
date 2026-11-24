package memory

import (
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/tool"
)

// Pressure is advisory. A misleading prompt must not make an agent discard
// useful memory while the real write tool still has enough room to retain it.
func TestMemoryToolStillAcceptsFittingGrowthUnderPressure(t *testing.T) {
	store := NewLimited(t.TempDir(), 100, 90)
	before, err := store.Overwrite(strings.Repeat("a", 80))
	if err != nil || before.Percent() != 80 {
		t.Fatalf("pressure fixture: %+v %v", before, err)
	}
	mem := Tools(store, nil)[0].(tool.InvokableTool)
	// Eighteen new characters plus the two-character note separator fill the
	// remaining twenty characters exactly. No note is shortened or removed.
	result := run(t, mem, map[string]any{"action": "add", "content": strings.Repeat("b", 18)})
	if result["success"] != true || result["usage"] != "100/100" {
		t.Fatalf("fitting growth must remain writable at 80%%: %v", result)
	}
	after, err := store.Read()
	if err != nil || len(after.Entries) != 2 || after.Entries[0] != before.Entries[0] || after.Chars != 100 {
		t.Fatalf("the write lost existing memory or ignored separator costs: %+v %v", after, err)
	}
	if refused := run(t, mem, map[string]any{"action": "add", "content": "c"}); refused["success"] != false || refused["available_chars"] != float64(0) {
		t.Fatalf("a real total overflow must still be refused: %v", refused)
	}
}

// A replacement spends only its net increase from the remaining total budget,
// while its entire new content must still fit the independent per-note cap.
func TestMemoryToolCanGrowAReplacementWhileItsEntryStillFits(t *testing.T) {
	store := NewLimited(t.TempDir(), 100, 90)
	if _, err := store.Overwrite(strings.Repeat("a", 80)); err != nil {
		t.Fatal(err)
	}
	mem := Tools(store, nil)[0].(tool.InvokableTool)
	grown := strings.Repeat("b", 90)
	result := run(t, mem, map[string]any{"action": "replace", "old_text": "a", "content": grown})
	if result["success"] != true || result["usage"] != "90/100" {
		t.Fatalf("a replacement may spend its fitting net growth: %v", result)
	}
	tooLong := run(t, mem, map[string]any{"action": "replace", "old_text": "b", "content": strings.Repeat("c", 91)})
	if tooLong["success"] != false || tooLong["entry_max"] != float64(90) {
		t.Fatalf("remaining total capacity cannot waive the per-note cap: %v", tooLong)
	}
	after, err := store.Read()
	if err != nil || len(after.Entries) != 1 || after.Entries[0] != grown {
		t.Fatalf("the refused replacement must preserve the successful one: %+v %v", after, err)
	}
}

func TestPromptCarriesTheProjectInstructionAndTheNotes(t *testing.T) {
	snap := Snapshot{
		Entries: []string{"one stored note", "another one\nwith a second line"},
		Chars:   55,
		Limit:   100,
	}
	out := PromptSections("the project's own instruction", snap, nil, 50, true)

	if !strings.Contains(out, "the project's own instruction") {
		t.Fatalf("the project instruction must reach the manager verbatim:\n%s", out)
	}
	for _, note := range snap.Entries[:1] {
		if !strings.Contains(out, note) {
			t.Fatalf("note %q missing:\n%s", note, out)
		}
	}
	// A multi-line note must stay one list item, or the prompt reads as two
	// unrelated notes.
	if !strings.Contains(out, "- another one\n  with a second line") {
		t.Fatalf("multi-line note not indented:\n%s", out)
	}
	// The agent has to see how full the store is, or it has no reason to
	// consolidate before it is refused.
	if !strings.Contains(out, "55%") || !strings.Contains(out, "55/100") {
		t.Fatalf("usage header missing:\n%s", out)
	}
	if strings.Contains(out, "will be refused") {
		t.Fatalf("a store at 55%% must not use the pressure wording:\n%s", out)
	}
	if !strings.Contains(out, ToolMemory) {
		t.Fatalf("the prompt must name the tool that maintains the notes:\n%s", out)
	}
	for _, required := range []string{"status that will change", "remaining total budget", "per-note cap", "separator", "does not necessarily free space"} {
		if !strings.Contains(out, required) {
			t.Fatalf("the prompt must describe the actual write budget (%q missing):\n%s", required, out)
		}
	}
	if strings.Contains(out, "the only write that lands") {
		t.Fatalf("the prompt must not invent a reduction-only write policy:\n%s", out)
	}
	if !strings.Contains(out, "short, declarative notes") || !strings.Contains(out, "collides") {
		t.Fatalf("the manager must see the same quality bar the tools enforce:\n%s", out)
	}
	if !strings.Contains(out, "share a stem") {
		t.Fatalf("the manager must be told a shared stem is the same subject:\n%s", out)
	}
}

// Only names and summaries. Inlining every procedure would make the prompt
// grow with the store, which is the cost skill_view exists to avoid.
func TestPromptListsSkillNamesAndSummariesOnly(t *testing.T) {
	skills := []SkillInfo{
		{Name: "first-procedure", Description: "when a certain kind of work comes up", UpdatedAt: time.Now()},
		{Name: "second-procedure", Description: "when another does"},
	}
	out := PromptSections("", Snapshot{Limit: 100}, skills, 50, true)
	for _, s := range skills {
		if !strings.Contains(out, s.Name) || !strings.Contains(out, s.Description) {
			t.Fatalf("skill %q missing from the index:\n%s", s.Name, out)
		}
	}
	if !strings.Contains(out, ToolSkillView) || !strings.Contains(out, ToolSkillManage) {
		t.Fatalf("the prompt must name the skill tools:\n%s", out)
	}

	// The cap keeps a large store from taking over the prompt, and the
	// remainder has to be admitted or the agent thinks it has seen them all.
	capped := PromptSections("", Snapshot{Limit: 100}, skills, 1, true)
	if !strings.Contains(capped, "first-procedure") || strings.Contains(capped, "second-procedure") {
		t.Fatalf("index not capped:\n%s", capped)
	}
	if !strings.Contains(capped, "1 more") {
		t.Fatalf("the capped index must admit what it left out:\n%s", capped)
	}

	empty := PromptSections("", Snapshot{Limit: 100}, nil, 50, true)
	if !strings.Contains(empty, "No skills recorded yet") || !strings.Contains(empty, "nothing yet") {
		t.Fatalf("a fresh project should say so plainly:\n%s", empty)
	}
	// skill_view and the workspace are two stores that look the same on disk.
	// The prompt has to say so even when the index is empty, or a listing of
	// the workspace becomes a missing-name call.
	for _, p := range []string{out, empty} {
		if !strings.Contains(p, "does not open workspace files") {
			t.Fatalf("the prompt must keep skill_view off workspace files:\n%s", p)
		}
	}
	if strings.Contains(empty, "Call "+ToolSkillView) {
		t.Fatalf("an empty index must not invite a view:\n%s", empty)
	}
}

func TestWorkerPromptMirrorsTheIndexWithoutWriteTools(t *testing.T) {
	snap := Snapshot{
		Entries: []string{"one stored note", "another one\nwith a second line"},
		Chars:   55,
		Limit:   100,
	}
	skills := []SkillInfo{
		{Name: "first-procedure", Description: "when a certain kind of work comes up"},
		{Name: "second-procedure", Description: "when another does"},
	}
	out := WorkerPromptSections(snap, skills, 1)
	if !strings.Contains(out, "one stored note") || !strings.Contains(out, "- another one\n  with a second line") {
		t.Fatalf("notes missing:\n%s", out)
	}
	if !strings.Contains(out, "55%") || !strings.Contains(out, "55/100") {
		t.Fatalf("usage header missing:\n%s", out)
	}
	if !strings.Contains(out, "first-procedure") || strings.Contains(out, "second-procedure") {
		t.Fatalf("index not capped:\n%s", out)
	}
	if !strings.Contains(out, "1 more") {
		t.Fatalf("the capped index must admit what it left out:\n%s", out)
	}
	if !strings.Contains(out, ToolSkillView) || !strings.Contains(out, "does not open workspace files") {
		t.Fatalf("workers must still be able to open a skill:\n%s", out)
	}
	for _, write := range []string{ToolMemory, ToolSkillManage} {
		if strings.Contains(out, write) {
			t.Fatalf("worker prompt named %s:\n%s", write, out)
		}
	}
	empty := WorkerPromptSections(Snapshot{Limit: 100}, nil, 50)
	if !strings.Contains(empty, "No skills recorded yet") || !strings.Contains(empty, "nothing yet") {
		t.Fatalf("a fresh project should say so plainly:\n%s", empty)
	}
	if strings.Contains(empty, "Call "+ToolSkillView) {
		t.Fatalf("an empty index must not invite a view:\n%s", empty)
	}
}

// A project with memory switched off must not be told about tools it does not
// have: a prompt that advertises one produces failed tool calls.
func TestMemoryOffLeavesOnlyTheProjectInstruction(t *testing.T) {
	out := PromptSections("still the project's instruction", Snapshot{Entries: []string{"a note"}, Limit: 10},
		[]SkillInfo{{Name: "n", Description: "d"}}, 50, false)
	if !strings.Contains(out, "still the project's instruction") {
		t.Fatalf("the instruction is not part of memory and must survive:\n%s", out)
	}
	for _, unwanted := range []string{ToolMemory, ToolSkillView, ToolSkillManage, "a note", "## Memory", "## Skills"} {
		if strings.Contains(out, unwanted) {
			t.Fatalf("memory is off but the prompt still mentions %q:\n%s", unwanted, out)
		}
	}
	if PromptSections("", Snapshot{}, nil, 50, false) != "" {
		t.Fatal("a project with neither an instruction nor memory adds nothing to the prompt")
	}
}

// These prompts describe where memory lives and when to write to it. Nothing
// about any particular request, domain or file belongs in them: whatever is
// here is paid for by every conversation, and an example baked in becomes a
// rule the model follows on unrelated work.
func TestMemoryPromptsStayGenericAndGrounded(t *testing.T) {
	prompts := map[string]string{
		"sections": PromptSections("", Snapshot{Limit: 100},
			[]SkillInfo{{Name: "n", Description: "d"}}, 50, true),
		"review":  ReviewPrompt(),
		"tidy":    CatalogTidyPrompt(),
		"catalog": ReviewCatalog([]SkillInfo{{Name: "n", Description: "d"}}, [][]string{{"n", "n-extra"}}),
		"worker": WorkerPromptSections(Snapshot{Limit: 100},
			[]SkillInfo{{Name: "n", Description: "d"}}, 50),
	}
	// Words that only appear if someone illustrated the feature with an
	// example and left it in.
	leaks := []string{
		"notes.md", "README", "example", "e.g.", "for instance", "such as",
		"python", "javascript", "docker", "kubernetes", "postgres",
		"table", "spreadsheet", "blog", "weather", "stock price",
	}
	for name, p := range prompts {
		lower := strings.ToLower(p)
		for _, leak := range leaks {
			if strings.Contains(lower, strings.ToLower(leak)) {
				t.Fatalf("the %s prompt mentions %q; prompts describe capabilities, the request supplies the task", name, leak)
			}
		}
		if strings.TrimSpace(p) == "" {
			t.Fatalf("the %s prompt is empty", name)
		}
	}
	worker := prompts["worker"]
	for _, write := range []string{ToolMemory, ToolSkillManage} {
		if strings.Contains(worker, write) {
			t.Fatalf("the worker prompt named the write tool %s:\n%s", write, worker)
		}
	}
	if !strings.Contains(worker, ToolSkillView) {
		t.Fatal("the worker prompt must name skill_view")
	}
	tidy := prompts["tidy"]
	if !strings.Contains(tidy, ToolMemoryReclassify) || strings.Contains(LibraryTidyPrompt(), ToolMemoryReclassify) {
		t.Fatalf("only project tidy may move existing notes:\n%s", tidy)
	}
	if !strings.Contains(worker, "Most tasks have nothing to add") {
		t.Fatal("the worker prompt must allow returning nothing extra")
	}
}

// The reviewer is not continuing the conversation and nobody is waiting for
// it. Saying so is what stops it from answering the user again, and the high
// bar is what keeps the store worth reading.
func TestReviewPromptSetsTheBarAndNamesItsTools(t *testing.T) {
	p := ReviewPrompt()
	for _, want := range []string{ToolMemory, ToolSkillManage, ToolSkillView, ToolApplyReview} {
		if !strings.Contains(p, want) {
			t.Fatalf("the reviewer must be told about %q:\n%s", want, p)
		}
	}
	for _, want := range []string{"not continuing", "no update", "current project notes", "exact quote", "unstated date", "fallible", "refusal", "empty array", "merge", "one by one"} {
		if !strings.Contains(p, want) {
			t.Fatalf("the reviewer must be allowed to store nothing (%q missing):\n%s", want, p)
		}
	}
	if strings.Contains(p, "every index entry") {
		t.Fatal("opening every skill is what spends the tool-round cap")
	}
}

// A review saved each finished task as a new skill named after that task,
// which no later task opened. Both review prompts must hold a new skill to a
// reuse bar on a different task, while still keeping observed recovery.
func TestReviewPromptsDoNotFileTheFinishedRequestAsASkill(t *testing.T) {
	for name, prompt := range map[string]string{"review": ReviewPrompt(), "library review": LibraryReviewPrompt()} {
		flat := strings.Join(strings.Fields(prompt), " ")
		for _, want := range []string{
			"worth reusing on a different later task",
			"Do not create one for this conversation's own request",
			"steps a capable agent already takes without guidance",
			"recovery from a failure actually observed here",
		} {
			if !strings.Contains(flat, want) {
				t.Errorf("%s lacks the reuse bar %q", name, want)
			}
		}
	}
	for name, prompt := range map[string]string{"worker": WorkerPromptSections(Snapshot{Limit: 100}, nil, 50), "tidy": CatalogTidyPrompt()} {
		if strings.Contains(prompt, "worth reusing on a different later task") {
			t.Errorf("%s is not a post-turn review and should not carry its new-skill bar", name)
		}
	}
}

func TestReviewCatalogListsSkillsAndFamiliesWithoutInventingAConversation(t *testing.T) {
	if ReviewCatalog(nil, nil) != "" {
		t.Fatal("an empty catalog must not become a review transcript")
	}
	bare := ReviewCatalog(nil, [][]string{{"weekly-rollup-notes", "weekly-rollup-send"}})
	if !strings.Contains(bare, "(none)") || !strings.Contains(bare, "weekly-rollup-notes, weekly-rollup-send") {
		t.Fatalf("a family with no index rows:\n%s", bare)
	}
	out := ReviewCatalog(
		[]SkillInfo{{Name: "weekly-rollup-notes", Description: "when filing notes"}},
		[][]string{{"weekly-rollup-notes", "weekly-rollup-send"}},
	)
	if !strings.Contains(out, "weekly-rollup-notes — when filing notes") {
		t.Fatalf("index missing:\n%s", out)
	}
	if !strings.Contains(out, "weekly-rollup-notes, weekly-rollup-send") {
		t.Fatalf("family missing:\n%s", out)
	}
	if !strings.Contains(out, "become one skill after this review") || !strings.Contains(out, "one by one") {
		t.Fatalf("the catalog must say the family is folded after the review, without a walk:\n%s", out)
	}
	joined := AttachReviewCatalog("Conversation to review:\n\nhuman: hi\n", out)
	if !strings.HasPrefix(joined, "Conversation to review:") || !strings.Contains(joined, "Skills already recorded") {
		t.Fatalf("attached catalog:\n%s", joined)
	}
	if AttachReviewCatalog("Conversation to review:\n", "") != "Conversation to review:\n" {
		t.Fatal("an empty catalog must not rewrite the transcript")
	}
	if got := AttachReviewCatalog("  ", "Skills already recorded"); got != "Skills already recorded" {
		t.Fatalf("an empty transcript must still carry a catalog that exists: %q", got)
	}
}

func TestCatalogTidyPromptSetsTheBarAndNamesItsTools(t *testing.T) {
	p := CatalogTidyPrompt()
	for _, want := range []string{ToolSkillManage, ToolSkillView, "conversation attached", "write nothing", "merge"} {
		if !strings.Contains(p, want) {
			t.Fatalf("the catalog tidy must be told about %q:\n%s", want, p)
		}
	}
	if !strings.Contains(p, ToolMemoryReclassify) {
		t.Fatal("project tidy must name its source-preserving move tool")
	}
}

func TestCatalogTidyMessageWrapsTheIndexWithoutInventingAConversation(t *testing.T) {
	if CatalogTidyMessage("", 90, nil, nil) != "Catalog to curate:\n\n(none)" {
		t.Fatal("an empty catalog still needs a user turn or the reviewer has nothing to answer")
	}
	out := CatalogTidyMessage(ReviewCatalog(
		[]SkillInfo{{Name: "weekly-rollup", Description: "when filing the week"}},
		nil,
	), 90, nil, nil)
	if !strings.HasPrefix(out, "Catalog to curate:") {
		t.Fatalf("catalog tidy must mark itself, or the mock reviewer stores a conversation clip:\n%s", out)
	}
	if !strings.Contains(out, "weekly-rollup — when filing the week") {
		t.Fatalf("index missing:\n%s", out)
	}
	if strings.Contains(out, "Conversation to review") {
		t.Fatal("a catalog tidy is not a conversation review")
	}
}

// The old assertion required refusing all growth at 80%, contradicting the
// actual memory tool. Pressure should warn about the real remaining capacity
// without making agents remove unrelated notes or invent another write gate.
func TestPromptWarnsWhenMemoryIsUnderPressure(t *testing.T) {
	for _, tc := range []struct {
		name       string
		chars      int
		freePhrase string
	}{
		{name: "threshold", chars: 80, freePhrase: "Free characters: 20"},
		{name: "near full", chars: 99, freePhrase: "Free characters: 1"},
		{name: "full", chars: 100, freePhrase: "Free characters: 0"},
		{name: "legacy over budget", chars: 105, freePhrase: "Free characters: 0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := PromptSections("", Snapshot{
				Entries: []string{"a stored note"}, Chars: tc.chars, Limit: 100,
			}, nil, 50, true)
			for _, required := range []string{tc.freePhrase, "capacity warning", "separator costs", "only related notes", "preserve unrelated notes"} {
				if !strings.Contains(out, required) {
					t.Fatalf("pressure advice must retain the actual budget and scope (%q missing):\n%s", required, out)
				}
			}
			for _, falseClaim := range []string{"A write that increases the character count will be refused", "the only write that lands", "Shorten or drop notes before storing anything new", "Free characters: -5"} {
				if strings.Contains(out, falseClaim) {
					t.Fatalf("pressure must not invent write restrictions or negative capacity (%q):\n%s", falseClaim, out)
				}
			}
		})
	}
}

func TestReviewPromptBoundsTheDeltaAndMaintainsProcedureExecutionOrder(t *testing.T) {
	prompt := ReviewPrompt()
	for _, required := range []string{"First identify durable changes supported by this turn", "Compare only related", "unrelated old memory", "immediately submit an empty array", "execution order", "preconditions", "recovery steps", "not append a timeline"} {
		if !strings.Contains(prompt, required) {
			t.Fatalf("review instruction lacks the generic delta/order boundary %q", required)
		}
	}
	if strings.Index(prompt, "First identify") > strings.Index(prompt, "Compare only related") {
		t.Fatal("review must identify supported differences before comparing related old entries")
	}
	for _, preserved := range []string{"scope, conditions, version and negation", "exact quote", "unstated date", "revision-checked commit", "quoted text", "successful paired tool_result"} {
		if !strings.Contains(prompt, preserved) {
			t.Fatalf("latency instructions weakened %q", preserved)
		}
	}
}

func TestReviewPromptSeparatesNormalWorkFromConditionalRecovery(t *testing.T) {
	prompt := ReviewPrompt()
	for _, required := range []string{
		"normal path separately from recovery branches for failures that have already occurred",
		"Make each recovery branch self-contained",
		"entry condition for each branch",
		"success was observed after the relevant failure",
		"observed return-to-normal or completion condition",
		"do not require executing the normal path first",
		"failed probes are diagnostic context, not actions to repeat automatically",
		"without turning observed chronology into a required replay",
	} {
		if !strings.Contains(prompt, required) {
			t.Errorf("review instruction does not constrain procedure branches: missing %q", required)
		}
	}
	for _, leak := range []string{"fixture", "heldout", "baseline", "dataset", "python", "docker", "example", "e.g."} {
		if strings.Contains(strings.ToLower(prompt), leak) {
			t.Errorf("review instruction contains task-specific illustration %q", leak)
		}
	}
}

func TestReviewPromptRepairsOnlyRelatedNotesWithinTheExistingCapacity(t *testing.T) {
	prompt := ReviewPrompt()
	for _, required := range []string{
		"existing iteration budget",
		"usage, over_by, current_entries and matched",
		"shorten or replace only related notes",
		"preserve unrelated memory",
		"Each draft operation must fit the same limits",
		"release related space before adding a new note",
		"not a way to hide blocked writes",
		"stop without submitting a successful empty proposal",
	} {
		if !strings.Contains(prompt, required) {
			t.Errorf("review capacity repair rule missing %q", required)
		}
	}
	if strings.Contains(prompt, "If no safe supported change fits, submit an empty") {
		t.Fatal("a capacity-blocked proposal must not become a successful empty review")
	}
}

func TestMemoryBoundaryIsConsistentAcrossGenericPrompts(t *testing.T) {
	prompts := map[string]string{
		"manager":        PromptSections("", Snapshot{Limit: 100}, nil, 50, true),
		"worker":         WorkerPromptSections(Snapshot{Limit: 100}, nil, 50),
		"review":         ReviewPrompt(),
		"tidy":           CatalogTidyPrompt(),
		"library review": LibraryReviewPrompt(),
		"library tidy":   LibraryTidyPrompt(),
		"library live":   LibraryPromptSections(nil, 50),
		"library worker": LibraryWorkerPromptSections(nil, 50),
	}
	for name, prompt := range prompts {
		for _, want := range []string{"go off track", "know how to complete", "preconditions", "steps", "verification", "failure", "when to use", "only within a procedure"} {
			if !strings.Contains(prompt, want) {
				t.Errorf("%s lacks boundary %q", name, want)
			}
		}
		for _, leak := range []string{"one or two sentences", "multi-step", "make e2e", "usage.json", "EntryMax", "SKILL.md", "git worktree"} {
			if strings.Contains(prompt, leak) {
				t.Errorf("%s contains old length heuristic or task-specific wording %q", name, leak)
			}
		}
	}
}
