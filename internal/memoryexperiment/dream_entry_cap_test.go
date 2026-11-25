package memoryexperiment

import (
	"context"
	"encoding/json"
	"testing"
)

// The optional per-note limit must reach the writer, while older callers that
// do not supply it retain exactly the previous omitted-field behavior.
func TestDreamInputCarriesOptionalPerNoteCapAndRejectsNegativeCapsBeforeModel(t *testing.T) {
	_, notes, sources, options, _ := dreamFixture(t)
	for _, cap := range []int{0, 1, 360, -1} {
		raw, err := json.Marshal(options)
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]any
		if err := json.Unmarshal(raw, &fields); err != nil {
			t.Fatal(err)
		}
		fields["entry_max"] = cap
		raw, err = json.Marshal(fields)
		if err != nil {
			t.Fatal(err)
		}
		var withCap DreamOptions
		if err := json.Unmarshal(raw, &withCap); err != nil {
			t.Fatal(err)
		}
		messages, err := BuildDreamMessages(notes, sources, withCap)
		if cap < 0 {
			if err == nil || len(messages) != 0 {
				t.Fatal("negative per-note cap admitted")
			}
			fake := &dreamTestModel{}
			if _, err := GenerateDream(context.Background(), fake, notes, sources, withCap); err == nil || fake.calls != 0 {
				t.Fatal("negative cap reached model")
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		var input map[string]any
		if err := json.Unmarshal([]byte(messages[1].Content), &input); err != nil {
			t.Fatal(err)
		}
		actual, present := input["entry_max"]
		if cap == 0 && present {
			t.Fatal("legacy zero cap changed model input")
		}
		if cap > 0 && (!present || actual != float64(cap)) {
			t.Fatalf("missing per-note cap: got %v, want %d", actual, cap)
		}
	}
}
