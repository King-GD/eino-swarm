package store

import (
	"testing"
)

func TestWikiSourceTextKeepsObservedOutputAndDropsCommandArguments(t *testing.T) {
	st := openTestStore(t)
	ctx := t.Context()
	p := Project{Name: "Original result"}
	if err := st.CreateProject(&p); err != nil {
		t.Fatal(err)
	}
	thread := Thread{ProjectID: p.ID}
	if err := st.CreateThread(&thread); err != nil {
		t.Fatal(err)
	}
	turn := Turn{ThreadID: thread.ID, Status: TurnDone}
	if err := st.CreateTurn(&turn); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		body, want string
		id         uint
	}{
		{body: `{"full_command":"argument that is not evidence","exit_code":0,"stdout":"original\nmaterial","stderr":"warning"}`, want: "original\nmaterial\nwarning"},
		{body: `{"full_command":"another command","exit_code":1,"stdout":"partial error result","stderr":"error"}`},
		{body: `{"kind":"report","observed":"independent structured data"}`, want: `{"kind":"report","observed":"independent structured data"}`},
	}
	for i := range cases {
		event := Event{ThreadID: thread.ID, TurnID: turn.ID, Kind: "tool_result", Text: cases[i].body}
		if err := st.AppendEvent(&event); err != nil {
			t.Fatal(err)
		}
		cases[i].id = event.ID
	}
	job := ProjectDistillation{ProjectID: p.ID, ID: "wiki-projection", State: "running", WikiGraph: true}
	if _, err := st.StartDistillationQueue(ctx, job, thread.ID, func(int) ([]byte, error) { return nil, nil }); err != nil {
		t.Fatal(err)
	}
	for i, tc := range cases {
		text, err := st.ReadWikiSourceText(ctx, job.ID, tc.id, 0, 1024)
		if err != nil || text.Text != tc.want || text.TotalBytes != len(tc.want) {
			t.Fatalf("case %d supplied an incorrect observed payload: %+v %v", i, text, err)
		}
	}
}
