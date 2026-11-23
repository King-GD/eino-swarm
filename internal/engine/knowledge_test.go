package engine

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/LubyRuffy/eino-swarm/internal/knowledge"
	"github.com/LubyRuffy/eino-swarm/internal/store"
)

func TestKnowledgeCitationDoesNotRebindAfterAnUnreadSteerIsEdited(t *testing.T) {
	e := newTestEngine(t)
	project, err := e.CreateProject("Source revision fixture", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	thread := knowledgeFixtureThread(t, e, project.ID, "Source conversation")
	release := holdTurn(t, e, thread.ID)
	defer release()

	const original = "Keep the original condition"
	const revised = "Apply the revised condition"
	if err := e.Steer(thread.ID, original); err != nil {
		t.Fatal(err)
	}
	eventSeq := steerSeq(t, e, thread.ID, original)
	messages, err := e.store.ListMessages(thread.ID)
	if err != nil || len(messages) != 1 || messages[0].EventSeq != eventSeq {
		t.Fatalf("steer did not reach its replay row: %+v, %v", messages, err)
	}
	message := messages[0]
	refs := []knowledgeSourceRef{
		{Kind: "message", ThreadID: thread.ID, Seq: message.Seq},
		{Kind: "event", ThreadID: thread.ID, Seq: eventSeq},
	}
	sources, err := e.resolveKnowledgeSources(t.Context(), project.ID, refs)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.EnableProjectKnowledge(project.ID); err != nil {
		t.Fatal(err)
	}
	kb, err := e.ProjectKnowledge(project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := kb.WritePage(knowledge.PageWrite{Page: knowledge.Page{
		ID: "entry-a", Type: "entry", Title: "Entry A", Body: "Original condition", Sources: sources,
	}}, knowledge.Stamp{UpdatedAt: time.Now(), UpdatedTurn: message.TurnID}); err != nil {
		t.Fatal(err)
	}

	assertSources := func(messageAvailable bool) {
		t.Helper()
		page, err := e.ProjectKnowledgePage(t.Context(), project.ID, "entry-a")
		if err != nil || len(page.Sources) != 2 {
			t.Fatalf("page lost its two citations: %+v, %v", page, err)
		}
		for i, available := range []bool{messageAvailable, true} {
			got := page.Sources[i]
			if got.Available == nil || *got.Available != available || got.RecordID != sources[i].RecordID ||
				!got.Timestamp.Equal(sources[i].Timestamp) || got.Classification != "primary" {
				t.Errorf("source %d: got %+v, want original citation with available=%t", i, got, available)
			}
		}
	}
	assertSources(true)
	if err := e.ReviseUnreadSteer(thread.ID, eventSeq, revised); err != nil {
		t.Fatal(err)
	}
	messages, err = e.store.ListMessages(thread.ID)
	if err != nil || len(messages) != 1 || messages[0].Seq != message.Seq ||
		messages[0].TurnID != message.TurnID || !messages[0].CreatedAt.Equal(message.CreatedAt) ||
		messages[0].Content != steerModelCaption(revised) {
		t.Fatalf("edit must reuse the original message row and its metadata: %+v, %v", messages, err)
	}
	assertSources(false)
	current, err := e.resolveKnowledgeSources(t.Context(), project.ID, refs)
	if err != nil || current[0].RecordID == sources[0].RecordID || current[1].RecordID != sources[1].RecordID {
		t.Fatalf("edited message did not get a distinct version, or original event changed: %+v, %v", current, err)
	}
}

func TestKnowledgeSummaryIsNeutralAndBoundedAndPageReadsArePaged(t *testing.T) {
	e := newTestEngine(t)
	project, err := e.CreateProject("Knowledge budget fixture", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	thread := knowledgeFixtureThread(t, e, project.ID, "Current task")
	kb, err := e.ProjectKnowledge(project.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"", "../no", ".", "pj_missing"} {
		if _, err := e.ProjectKnowledge(id); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("invalid or unknown project %q: %v", id, err)
		}
	}
	if _, err := e.readKnowledge(t.Context(), project.ID, kb, `{"action":"schema"}`, 100); !errors.Is(err, knowledge.ErrDisabled) {
		t.Fatalf("disabled store read: %v", err)
	}
	snap, err := e.EnableProjectKnowledge(project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := kb.AppendSchema(knowledge.SchemaAppend{Types: []knowledge.PageType{{
		ID: "variant", Dir: "variants", Description: strings.Repeat("界", knowledgeSchemaPromptBudget),
	}}}, snap.Status(time.Now()).Revision); err != nil {
		t.Fatal(err)
	}
	body := "正文还原"
	page, err := kb.WritePage(knowledge.PageWrite{Page: knowledge.Page{ID: "entry-a", Type: "entry", Title: "Entry A",
		Aliases: []string{"Previous title"}, Body: body, Links: []knowledge.Link{{Rel: "related", To: "missing"}}}},
		knowledge.Stamp{UpdatedAt: time.Now(), UpdatedTurn: "no-longer-available"})
	if err != nil {
		t.Fatal(err)
	}
	pc, err := e.projectContextFor(thread)
	if err != nil {
		t.Fatal(err)
	}
	if len(pc.knowledgeSummary) > knowledgeSchemaPromptBudget || !utf8.ValidString(pc.knowledgeSummary) ||
		!strings.Contains(pc.knowledgeSummary, "Type entry") || !strings.Contains(pc.knowledgeSummary, "1 pages.") {
		t.Fatalf("knowledge summary misses its budget or neutral schema: %q", pc.knowledgeSummary)
	}
	prompt := strings.ToLower(pc.knowledgePrompt(false))
	for _, forbidden := range []string{body, "competitor", "vendor", "vulnerability", "product roadmap"} {
		if strings.Contains(prompt, forbidden) {
			t.Errorf("schema prompt contained body text or a built-in domain term %q", forbidden)
		}
	}
	read := func(raw string) knowledgeReadReply {
		t.Helper()
		value, err := e.readKnowledge(t.Context(), project.ID, kb, raw, 40_000)
		var reply knowledgeReadReply
		if err != nil {
			t.Fatalf("read %s: %v", raw, err)
		}
		if err := json.Unmarshal([]byte(value), &reply); err != nil || !reply.OK {
			t.Fatalf("read result: %s (%v)", value, err)
		}
		return reply
	}
	matches := read(`{"action":"search","query":"previous title","limit":2}`).Matches
	if matches == nil || len(*matches) != 1 || (*matches)[0].ID != page.ID {
		t.Fatalf("alias search: %+v", matches)
	}
	issues := read(`{"action":"lint","limit":1}`).Issues
	if issues == nil || len(*issues) != 1 || (*issues)[0].Target != "missing" {
		t.Fatalf("missing red-link lint: %+v", issues)
	}
	first, rest := read(`{"action":"page","id":"entry-a","limit":2}`), read(`{"action":"page","id":"entry-a","offset":2}`)
	if first.NextOffset != 2 || rest.NextOffset != 0 || first.Page.Body+rest.Page.Body != body ||
		first.Page.UpdatedThread != "" || first.Page.UpdatedTurn != "no-longer-available" {
		t.Fatalf("page identity or Unicode pagination changed: %+v / %+v", first.Page, rest.Page)
	}
	for _, tc := range []struct{ args, want string }{
		{`{"action":"lint","offset":-1}`, "nonnegative"}, {`{"action":1}`, "invalid tool arguments"},
		{`{"action":"other"}`, "action must be"}, {`{"action":"page","id":"missing"}`, "not found"},
	} {
		if _, err := e.readKnowledge(t.Context(), project.ID, kb, tc.args, 1000); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("read %s: got %v, want %q", tc.args, err, tc.want)
		}
	}
	if _, err := e.readKnowledge(t.Context(), project.ID, kb, `{"action":"schema"}`, 100); err == nil || !strings.Contains(err.Error(), "result budget") {
		t.Fatalf("oversized schema lost its explicit refusal: %v", err)
	}
}

func TestKnowledgeToolOwnerRefusesAMovedConversationOrUntrustedCaller(t *testing.T) {
	e := newTestEngine(t)
	project, err := e.CreateProject("Owner fixture", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	thread := knowledgeFixtureThread(t, e, project.ID, "Original task")
	turn := &store.Turn{ThreadID: thread.ID, ProjectID: project.ID}
	if err := e.store.CreateTurn(turn); err != nil {
		t.Fatal(err)
	}
	ctx := withHistoryReadOwner(t.Context(), thread.ID, turn.ID)
	if err := e.knowledgeToolOwner(ctx, turn, project.ID, false); err != nil {
		t.Fatal(err)
	}
	if err := e.knowledgeToolOwner(t.Context(), turn, project.ID, false); err == nil {
		t.Fatal("a caller without a trusted owner was admitted")
	}
	if err := e.knowledgeToolOwner(ctx, turn, project.ID, true); err == nil {
		t.Fatal("a caller without a real agent identity was admitted to write")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := e.knowledgeToolOwner(cancelled, turn, project.ID, false); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled call: %v", err)
	}
	if err := e.MoveThread(thread.ID, ""); err != nil {
		t.Fatal(err)
	}
	if err := e.knowledgeToolOwner(ctx, turn, project.ID, false); !errors.Is(err, errTurnProjectOwnerChanged) {
		t.Fatalf("moved conversation's old Registry retained project access: %v", err)
	}
	if err := e.store.FinishTurn(turn.ID, store.TurnDone, "", ""); err != nil {
		t.Fatal(err)
	}
}
