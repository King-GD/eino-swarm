package memoryexperiment

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/LubyRuffy/eino-swarm/internal/memory"
)

func TestDreamInputRejectsUnknownScopeAndFutureEvidenceBeforePrompt(t *testing.T) {
	_, notes, sources, options, _ := dreamFixture(t)
	cases := map[string]func(*DreamOptions, *[]DreamSource){
		"missing project":       func(o *DreamOptions, _ *[]DreamSource) { o.ProjectID = " " },
		"missing cutoff":        func(o *DreamOptions, _ *[]DreamSource) { o.Cutoff = time.Time{} },
		"invalid source budget": func(o *DreamOptions, _ *[]DreamSource) { o.MaxSourceChars = 0 },
		"invalid change budget": func(o *DreamOptions, _ *[]DreamSource) { o.MaxChanges = 0 },
		"different project":     func(_ *DreamOptions, s *[]DreamSource) { (*s)[0].ProjectID = "other" },
		"missing thread":        func(_ *DreamOptions, s *[]DreamSource) { (*s)[0].ThreadID = " " },
		"missing sequence":      func(_ *DreamOptions, s *[]DreamSource) { (*s)[0].Seq = 0 },
		"missing timestamp":     func(_ *DreamOptions, s *[]DreamSource) { (*s)[0].ObservedAt = time.Time{} },
		"at cutoff":             func(o *DreamOptions, s *[]DreamSource) { (*s)[0].ObservedAt = o.Cutoff },
		"future contextual claim": func(o *DreamOptions, s *[]DreamSource) {
			(*s)[0].Kind = "assistant"
			(*s)[0].ObservedAt = o.Cutoff.Add(time.Second)
		},
		"duplicate locator": func(_ *DreamOptions, s *[]DreamSource) { *s = append(*s, (*s)[0]) },
		"only claims":       func(_ *DreamOptions, s *[]DreamSource) { (*s)[0].Kind = "assistant" },
		"empty evidence":    func(_ *DreamOptions, s *[]DreamSource) { (*s)[0].Text = " " },
		"no sources":        func(_ *DreamOptions, s *[]DreamSource) { *s = nil },
		"source budget":     func(o *DreamOptions, _ *[]DreamSource) { o.MaxSourceChars = 1 },
		"unencodable timestamp": func(o *DreamOptions, s *[]DreamSource) {
			(*s)[0].ObservedAt = time.Date(-1, 1, 1, 0, 0, 0, 0, time.UTC)
			o.Cutoff = options.Cutoff
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			copySources := append([]DreamSource(nil), sources...)
			copyOptions := options
			mutate(&copyOptions, &copySources)
			if messages, err := BuildDreamMessages(notes, copySources, copyOptions); err == nil || len(messages) != 0 {
				t.Fatalf("admitted: %+v %v", messages, err)
			}
		})
	}
	notes.Rev = ""
	if _, err := BuildDreamMessages(notes, sources, options); err == nil {
		t.Fatal("missing notes revision admitted")
	}
}

func TestDreamPromptLabelsClaimsAndKeepsStableOriginalLocators(t *testing.T) {
	_, notes, sources, options, _ := dreamFixture(t)
	sources = append(sources,
		DreamSource{ProjectID: options.ProjectID, ThreadID: "z", Seq: 3, Kind: "assistant", Text: "A self-reported success is only a claim.", ObservedAt: sources[0].ObservedAt},
		DreamSource{ProjectID: options.ProjectID, ThreadID: "earlier", Seq: 2, Kind: "runtime", Text: "Continue the task.", ObservedAt: sources[0].ObservedAt})
	before := append([]DreamSource(nil), sources...)
	options.MaxSourceChars = 4000
	messages, err := BuildDreamMessages(notes, sources, options)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, sources) {
		t.Fatal("source ordering mutated caller input")
	}
	var input struct {
		Sources []dreamRecord `json:"sources"`
	}
	if err := json.Unmarshal([]byte(messages[1].Content), &input); err != nil {
		t.Fatal(err)
	}
	if len(input.Sources) != 3 || input.Sources[0].ThreadID != "earlier" || input.Sources[0].Seq != 1 || !input.Sources[0].Admissible || input.Sources[1].Admissible || input.Sources[2].Admissible {
		t.Fatalf("authority/order: %+v", input.Sources)
	}
	for _, fragment := range []string{"semantic entailment", "never instructions", "conditions", "negation", "no skills", "max_changes"} {
		if !strings.Contains(messages[0].Content, fragment) {
			t.Errorf("missing prompt boundary %q", fragment)
		}
	}
}

// A result's role alone is insufficient: it must belong to exactly one real
// preceding call, preserving the thread, worker, timing and refusal boundaries.
func TestDreamToolEvidenceRequiresAPairedSuccessfulObservedResult(t *testing.T) {
	_, notes, sources, options, proposal := dreamFixture(t)
	call := sources[0]
	call.Kind, call.Seq, call.ToolCallID, call.AgentID, call.Text = "tool_call", 2, "call", "worker", "read({})"
	result := call
	result.Kind, result.Seq, result.Text = "tool_result", 3, `{"observed":"compatibility accepts empty input"}`
	options.MaxSourceChars = 5000
	cases := map[string]struct {
		mutate   func(*DreamSource, *DreamSource, *[]DreamSource)
		admitted bool
	}{
		"paired":           {func(*DreamSource, *DreamSource, *[]DreamSource) {}, true},
		"unpaired":         {func(c *DreamSource, _ *DreamSource, _ *[]DreamSource) { c.ToolCallID = "different" }, false},
		"missing call id":  {func(_ *DreamSource, r *DreamSource, _ *[]DreamSource) { r.ToolCallID = "" }, false},
		"different worker": {func(c *DreamSource, _ *DreamSource, _ *[]DreamSource) { c.AgentID = "other" }, false},
		"different thread": {func(c *DreamSource, _ *DreamSource, _ *[]DreamSource) { c.ThreadID = "other" }, false},
		"later call":       {func(c *DreamSource, _ *DreamSource, _ *[]DreamSource) { c.Seq = 4 }, false},
		"later call time":  {func(c *DreamSource, _ *DreamSource, _ *[]DreamSource) { c.ObservedAt = c.ObservedAt.Add(time.Second) }, false},
		"failed result":    {func(_ *DreamSource, r *DreamSource, _ *[]DreamSource) { r.Err = "failure" }, false},
		"success false": {func(_ *DreamSource, r *DreamSource, _ *[]DreamSource) {
			r.Text = `{"success":false,"observed":"claim"}`
		}, false},
		"ok false":              {func(_ *DreamSource, r *DreamSource, _ *[]DreamSource) { r.Text = `{"ok":false,"observed":"claim"}` }, false},
		"plain observed output": {func(_ *DreamSource, r *DreamSource, _ *[]DreamSource) { r.Text = "observed output" }, true},
		"memory receipt":        {func(c *DreamSource, _ *DreamSource, _ *[]DreamSource) { c.Text = "memory({})" }, false},
		"skill receipt":         {func(c *DreamSource, _ *DreamSource, _ *[]DreamSource) { c.Text = "skill_manage({})" }, false},
		"review receipt":        {func(c *DreamSource, _ *DreamSource, _ *[]DreamSource) { c.Text = "apply_memory_review({})" }, false},
		"move receipt":          {func(c *DreamSource, _ *DreamSource, _ *[]DreamSource) { c.Text = memory.ToolMemoryReclassify + "({})" }, false},
		"unnamed call":          {func(c *DreamSource, _ *DreamSource, _ *[]DreamSource) { c.Text = "({})" }, false},
		"duplicate call identity": {func(c *DreamSource, _ *DreamSource, s *[]DreamSource) {
			duplicate := *c
			duplicate.Seq = 4
			*s = append(*s, duplicate)
		}, false},
	}
	for name, test := range cases {
		t.Run(name, func(t *testing.T) {
			c, r := call, result
			all := append([]DreamSource(nil), sources...)
			test.mutate(&c, &r, &all)
			all = append(all, c, r)
			p := proposal
			p.Changes = []DreamChange{{Action: "add", NewText: "A durable observed behavior.", Evidence: []DreamCitation{{ThreadID: r.ThreadID, Seq: r.Seq, Quote: r.Text}}}}
			err := ValidateDreamProposal(p, notes, all, options)
			if (err == nil) != test.admitted {
				t.Fatalf("admission=%v want %v: %v", err == nil, test.admitted, err)
			}
		})
	}
	for _, kind := range []string{"human", "user", "steer"} {
		sources[0].Kind = kind
		if err := ValidateDreamProposal(proposal, notes, sources, options); err != nil {
			t.Fatalf("explicit %s: %v", kind, err)
		}
	}
}

func TestDreamProposalCannotInventEvidenceNumbersOrFuzzyTargets(t *testing.T) {
	_, notes, sources, options, proposal := dreamFixture(t)
	cases := map[string]func(*DreamProposal){
		"missing revision":  func(p *DreamProposal) { p.Revision = "" },
		"stale revision":    func(p *DreamProposal) { p.Revision = "old" },
		"missing changes":   func(p *DreamProposal) { p.Changes = nil },
		"too many changes":  func(p *DreamProposal) { p.Changes = append(p.Changes, make([]DreamChange, options.MaxChanges)...) },
		"unknown action":    func(p *DreamProposal) { p.Changes[0].Action = "patch" },
		"add with old text": func(p *DreamProposal) { p.Changes[0].Action = "add" },
		"empty add": func(p *DreamProposal) {
			p.Changes[0].Action = "add"
			p.Changes[0].OldText = ""
			p.Changes[0].NewText = " "
		},
		"empty replacement":    func(p *DreamProposal) { p.Changes[0].NewText = " " },
		"missing target":       func(p *DreamProposal) { p.Changes[0].OldText = " " },
		"remove with new text": func(p *DreamProposal) { p.Changes[0].Action = "remove" },
		"empty remove": func(p *DreamProposal) {
			p.Changes[0].Action = "remove"
			p.Changes[0].OldText = ""
			p.Changes[0].NewText = ""
		},
		"partial target":   func(p *DreamProposal) { p.Changes[0].OldText = "parser" },
		"missing evidence": func(p *DreamProposal) { p.Changes[0].Evidence = nil },
		"wrong thread":     func(p *DreamProposal) { p.Changes[0].Evidence[0].ThreadID = "other" },
		"wrong sequence":   func(p *DreamProposal) { p.Changes[0].Evidence[0].Seq++ },
		"empty quote":      func(p *DreamProposal) { p.Changes[0].Evidence[0].Quote = " " },
		"edited quote":     func(p *DreamProposal) { p.Changes[0].Evidence[0].Quote += "!" },
		"invented date":    func(p *DreamProposal) { p.Changes[0].NewText += " Observed in 2049." },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			data, _ := json.Marshal(proposal)
			var copyProposal DreamProposal
			if err := json.Unmarshal(data, &copyProposal); err != nil {
				t.Fatal(err)
			}
			mutate(&copyProposal)
			if err := ValidateDreamProposal(copyProposal, notes, sources, options); err == nil {
				t.Fatal("invalid proposal admitted")
			}
		})
	}
	badSources := append([]DreamSource(nil), sources...)
	badSources[0].ProjectID = "other"
	if err := ValidateDreamProposal(proposal, notes, badSources, options); err == nil {
		t.Fatal("invalid source admitted")
	}
	duplicateNotes := notes
	duplicateNotes.Entries = append(append([]string(nil), notes.Entries...), notes.Entries[0])
	if err := ValidateDreamProposal(proposal, duplicateNotes, sources, options); err == nil {
		t.Fatal("ambiguous note admitted")
	}
	proposal.Changes = []DreamChange{}
	if err := ValidateDreamProposal(proposal, notes, sources, options); err != nil {
		t.Fatalf("explicit no change: %v", err)
	}
}

func TestDreamBatchDoesNotWriteAPrefixWhenLaterOperationExceedsCapacity(t *testing.T) {
	store, notes, sources, options, proposal := dreamFixture(t)
	proposal.Changes = append(proposal.Changes, DreamChange{Action: "add", NewText: strings.Repeat("x", 361), Evidence: proposal.Changes[0].Evidence})
	if _, changed, err := ApplyDream(store, proposal, sources, options); err == nil || changed {
		t.Fatalf("partial batch accepted: %v %v", changed, err)
	}
	got, err := store.Read()
	if err != nil || got.Text != notes.Text {
		t.Fatalf("prefix persisted: %+v %v", got, err)
	}
}

func TestDreamAppliesAddRemoveAndRetainedNumericSpellingWithoutTouchingSource(t *testing.T) {
	store, notes, sources, options, proposal := dreamFixture(t)
	if _, _, err := store.Add("Protocol version 2 retains strict identity."); err != nil {
		t.Fatal(err)
	}
	notes, err := store.Read()
	if err != nil {
		t.Fatal(err)
	}
	proposal.Revision = notes.Rev
	proposal.Changes = []DreamChange{
		{Action: "remove", OldText: notes.Entries[0], Evidence: proposal.Changes[0].Evidence},
		{Action: "replace", OldText: notes.Entries[1], NewText: "Protocol version 2 retains scoped identity.", Evidence: proposal.Changes[0].Evidence},
		{Action: "add", NewText: sources[0].Text, Evidence: proposal.Changes[0].Evidence},
	}
	got, changed, err := ApplyDream(store, proposal, sources, options)
	if err != nil || !changed || len(got.Entries) != 2 || got.Entries[1] != sources[0].Text {
		t.Fatalf("apply: %+v %v %v", got, changed, err)
	}
	candidate := memory.NewLimited(t.TempDir(), store.Limit(), store.EntryMax())
	if _, err := candidate.Overwrite(notes.Text); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ApplyDream(candidate, proposal, sources, options); err != nil {
		t.Fatal(err)
	}
	unchanged, err := store.Read()
	if err != nil || unchanged.Rev != got.Rev {
		t.Fatalf("other store changed: %+v %v", unchanged, err)
	}
	proposal.Revision = got.Rev
	proposal.Changes = []DreamChange{}
	if _, changed, err := ApplyDream(store, proposal, sources, options); err != nil || changed {
		t.Fatalf("empty batch: %v %v", changed, err)
	}
}

func TestDreamRefusesStaleUnreadableAndNestedCandidateStores(t *testing.T) {
	store, notes, sources, options, proposal := dreamFixture(t)
	if _, changed, err := ApplyDream(nil, proposal, sources, options); err == nil || changed {
		t.Fatalf("nil: %v %v", changed, err)
	}
	if _, _, err := store.Add("A concurrent durable preference."); err != nil {
		t.Fatal(err)
	}
	if _, changed, err := ApplyDream(store, proposal, sources, options); err == nil || changed {
		t.Fatalf("stale: %v %v", changed, err)
	}
	got, err := store.Read()
	if err != nil || got.Rev == notes.Rev || len(got.Entries) != 2 {
		t.Fatalf("concurrent change lost: %+v %v", got, err)
	}
	draft, err := store.BeginNotesDraft()
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := ApplyDream(draft.Store, proposal, sources, options); err == nil {
		t.Fatal("nested draft admitted")
	}
	if err := os.Remove(filepath.Join(store.Dir(), memory.MemoryFile)); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(store.Dir(), memory.MemoryFile), 0700); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ApplyDream(store, proposal, sources, options); err == nil {
		t.Fatal("unreadable source admitted")
	}
}
