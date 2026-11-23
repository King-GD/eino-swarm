package memory

import (
	"os"
	"testing"
)

func TestNewNotesNeedALetterOrNumber(t *testing.T) {
	s := New(t.TempDir(), 500)
	for _, text := range []string{"字", "Ⅷ", "٣", "a"} {
		if _, changed, err := s.Add(text); err != nil || !changed {
			t.Fatalf("Add(%q): changed=%v err=%v", text, changed, err)
		}
	}
	for _, text := range []string{" \t\n", "。", "***", "\u200b\ufe0f", "⚙️", "́", "\xff"} {
		t.Run(text, func(t *testing.T) {
			s := New(t.TempDir(), 500)
			before, _, err := s.Add("字")
			if err != nil {
				t.Fatal(err)
			}
			if _, changed, err := s.Add(text); err == nil || changed {
				t.Errorf("Add(%q): changed=%v err=%v", text, changed, err)
			}
			if _, err := s.Replace("字", text); err == nil {
				t.Errorf("Replace(%q) succeeded", text)
			}
			after, err := s.Read()
			if err != nil || after.Text != before.Text || after.Rev != before.Rev {
				t.Errorf("refusals changed notes: %+v, %v", after, err)
			}
		})
	}
}

func TestHandEditCanKeepButCannotIntroduceOrDuplicateContentlessNotes(t *testing.T) {
	s := New(t.TempDir(), 500)
	original := "※\n\n※\n\n字\n"
	if err := os.WriteFile(s.memoryPath(), []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	before, err := s.Read()
	if err != nil || len(before.Entries) != 3 {
		t.Fatalf("legacy notes: %+v, %v", before, err)
	}
	for _, candidate := range []string{"※\n\n※\n\n※\n\n字", "字\n\n…"} {
		if _, err := s.OverwriteIf(before.Rev, candidate); err == nil {
			t.Errorf("invalid hand edit was accepted: %q", candidate)
		}
	}
	raw, err := os.ReadFile(s.memoryPath())
	if err != nil || string(raw) != original {
		t.Fatalf("refusals changed legacy file: %q, %v", raw, err)
	}
	edited := "字改\n\n※\n\n※"
	saved, err := s.OverwriteIf(before.Rev, edited)
	if err != nil || saved.Text != edited {
		t.Fatalf("valid hand edit failed: %+v, %v", saved, err)
	}
	if _, err := s.Overwrite("!"); err == nil {
		t.Error("unconditional save accepted a new punctuation-only note")
	}
	if cleared, err := s.OverwriteIf(saved.Rev, " \n\n "); err != nil || cleared.Text != "" {
		t.Fatalf("explicit clear failed: %+v, %v", cleared, err)
	}
}

func TestDemoteDoesNotDeleteASkillWithContentlessBody(t *testing.T) {
	s := New(t.TempDir(), 500)
	name, body := "short-reference", "…"
	if _, err := s.WriteSkill(name, "When a reference is needed", body); err != nil {
		t.Fatal(err)
	}
	if result, err := s.DemoteSkill(name, body); err == nil || result.Changed || len(result.Changes) != 0 {
		t.Errorf("contentless demotion accepted: %+v, %v", result, err)
	}
	if skill, err := s.ReadSkill(name); err != nil || skill.Body != body {
		t.Errorf("demotion removed its source: %+v, %v", skill, err)
	}
	if notes, err := s.Read(); err != nil || notes.Text != "" {
		t.Errorf("demotion wrote a note: %+v, %v", notes, err)
	}
}
