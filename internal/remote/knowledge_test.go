package remote

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/LubyRuffy/eino-swarm/internal/config"
	"github.com/LubyRuffy/eino-swarm/internal/engine"
	"github.com/LubyRuffy/eino-swarm/internal/knowledge"
)

func phoneKnowledgeProject(t *testing.T, e *engine.Engine, name string, enable bool) (string, *knowledge.Store) {
	t.Helper()
	project, err := e.CreateProject(name, "", "", true)
	if err != nil {
		t.Fatal(err)
	}
	if enable {
		if _, err := e.EnableProjectKnowledge(project.ID); err != nil {
			t.Fatal(err)
		}
	}
	kb, err := e.ProjectKnowledge(project.ID)
	if err != nil {
		t.Fatal(err)
	}
	return project.ID, kb
}

func phoneKnowledgeWrite(t *testing.T, kb *knowledge.Store, page knowledge.Page) *knowledge.Page {
	t.Helper()
	page.Type = "entry"
	saved, err := kb.WritePage(knowledge.PageWrite{Page: page}, knowledge.Stamp{UpdatedAt: time.Now(), UpdatedTurn: "fixture-turn"})
	if err != nil {
		t.Fatal(err)
	}
	return saved
}

func TestPhoneKnowledgeListsTheSelectedProjectInBoundedPages(t *testing.T) {
	e := testEngine(t)
	project, kb := phoneKnowledgeProject(t, e, "Local records", true)
	other, _ := phoneKnowledgeProject(t, e, "Inactive", false)
	for i := range knowledgeListSize + 2 {
		phoneKnowledgeWrite(t, kb, knowledge.Page{ID: fmt.Sprintf("page-%02d", i), Title: fmt.Sprintf("Record %02d", i)})
	}
	phoneKnowledgeWrite(t, kb, knowledge.Page{ID: "alias-page", Title: "Named entry", Aliases: []string{"exact phrase"}})
	thread, err := e.CreateThread("Owned conversation", "", project)
	if err != nil {
		t.Fatal(err)
	}
	relay := newFollowupRelay(t, e)
	r := relay.rpc(t, Request{ID: "open-owner", Op: OpOpen, ThreadID: thread.ID})
	if !r.OK || r.Detail == nil || r.Detail.ProjectID != project {
		t.Fatalf("phone conversation lost project identity: %+v", r)
	}
	first := relay.rpc(t, Request{ID: "first", Op: OpProjectKnowledge, ProjectID: project})
	if !first.OK || first.Knowledge == nil || first.Knowledge.ProjectID != project ||
		!first.Knowledge.Enabled || len(first.Knowledge.Pages) != knowledgeListSize || first.Knowledge.NextOffset != knowledgeListSize {
		t.Fatalf("first page: %+v", first.Knowledge)
	}
	more := relay.rpc(t, Request{ID: "more", Op: OpProjectKnowledge, ProjectID: project, Offset: first.Knowledge.NextOffset})
	if !more.OK || len(more.Knowledge.Pages) != 3 || more.Knowledge.NextOffset != 0 {
		t.Fatalf("next page: %+v", more.Knowledge)
	}
	seen := map[string]bool{}
	for _, ref := range append(first.Knowledge.Pages, more.Knowledge.Pages...) {
		if seen[ref.ID] {
			t.Fatal("duplicate page", ref)
		}
		seen[ref.ID] = true
	}
	search := relay.rpc(t, Request{ID: "alias", Op: OpProjectKnowledge, ProjectID: project, Text: "exact phrase"})
	if !search.OK || len(search.Knowledge.Pages) != 1 || search.Knowledge.Pages[0].ID != "alias-page" {
		t.Fatalf("alias search: %+v", search.Knowledge)
	}
	inactive := relay.rpc(t, Request{ID: "inactive", Op: OpProjectKnowledge, ProjectID: other})
	if !inactive.OK || inactive.Knowledge.Enabled || len(inactive.Knowledge.Pages) != 0 {
		t.Fatalf("inactive project: %+v", inactive)
	}
	for _, req := range []Request{
		{ID: "negative", ProjectID: project, Offset: -1},
		{ID: "missing-id"},
		{ID: "query-budget", ProjectID: project, Text: strings.Repeat("x", 513)},
	} {
		req.Op = OpProjectKnowledge
		if r := relay.rpc(t, req); r.OK || r.Code != "bad_request" {
			t.Fatalf("invalid listing: %+v", r)
		}
	}
	if r := relay.rpc(t, Request{ID: "unknown-project", Op: OpProjectKnowledge, ProjectID: "../other"}); r.OK || r.Code != "not_found" {
		t.Fatalf("invalid project selector: %+v", r)
	}
}

func TestPhoneKnowledgeReadsACompletePageAndRefusesForeignTransferTokens(t *testing.T) {
	e := testEngine(t)
	project, kb := phoneKnowledgeProject(t, e, "Own library", true)
	other, _ := phoneKnowledgeProject(t, e, "Peer library", true)
	phoneKnowledgeWrite(t, kb, knowledge.Page{ID: "related-page", Title: "Related entry"})
	body := strings.Repeat("段落 with [[related-page|evidence]].\n", 2400)
	page := phoneKnowledgeWrite(t, kb, knowledge.Page{
		ID: "page", Title: "Long entry", Body: body,
		Links:   []knowledge.Link{{Rel: "related", To: "related-page"}},
		Sources: []knowledge.Source{{URI: "https://example.invalid/source"}},
	})
	relay := newFollowupRelay(t, e)
	read := func(id, projectID, pageID, token string, offset int64) Response {
		t.Helper()
		return relay.rpc(t, Request{ID: id, Op: OpKnowledgePage, ProjectID: projectID,
			KnowledgePageID: pageID, KnowledgeTransferID: token, Offset: offset})
	}
	first := read("first", project, "page", "", 0)
	if !first.OK || first.KnowledgeChunk == nil || !first.KnowledgeChunk.More {
		t.Fatalf("page did not stream: %+v", first)
	}
	frozen := *first.KnowledgeChunk
	for _, tc := range []struct {
		name, id, page, token, code string
		offset                      int64
	}{
		{"foreign", other, "page", frozen.ID, "bad_request", frozen.NextOffset},
		{"another-page", project, "related-page", frozen.ID, "bad_request", frozen.NextOffset},
		{"unknown-token", project, "page", strings.Repeat("a", 32), "transfer_expired", frozen.NextOffset},
		{"traversal", project, "../page", "", "bad_request", 0},
		{"unknown-page", other, "page", "", "knowledge_page_not_found", 0},
		{"outside", project, "page", frozen.ID, "bad_request", frozen.Size + 1},
		{"first-offset", project, "page", "", "bad_request", 1},
	} {
		if r := read(tc.name, tc.id, tc.page, tc.token, tc.offset); r.OK || r.Code != tc.code {
			t.Fatalf("%s: %+v", tc.name, r)
		}
	}
	// A later edit should appear on refresh, never halfway through this body.
	page.Body = "Newer"
	phoneKnowledgeWrite(t, kb, *page)
	var full []byte
	chunk := frozen
	for {
		wire, err := json.Marshal(chunk)
		if err != nil || len(wire) > plaintextBudget() || chunk.ID != frozen.ID || chunk.SHA256 != frozen.SHA256 {
			t.Fatal("changed or oversized frame", len(wire), err)
		}
		part, err := base64.StdEncoding.DecodeString(chunk.Data)
		if err != nil || len(part) == 0 || len(part) > FileChunkRaw || chunk.Offset != int64(len(full)) {
			t.Fatal("wrong chunk", err)
		}
		full = append(full, part...)
		if !chunk.More {
			break
		}
		next := read(fmt.Sprintf("part-%d", chunk.NextOffset), project, "page", frozen.ID, chunk.NextOffset)
		if !next.OK || next.KnowledgeChunk == nil {
			t.Fatalf("follow-up: %+v", next)
		}
		chunk = *next.KnowledgeChunk
	}
	sum := sha256.Sum256(full)
	var decoded knowledge.PageDetail
	if err := json.Unmarshal(full, &decoded); err != nil || int64(len(full)) != frozen.Size ||
		hex.EncodeToString(sum[:]) != frozen.SHA256 || decoded.Body != body ||
		len(decoded.Sources) != 1 || decoded.Sources[0].URI != "https://example.invalid/source" ||
		len(decoded.Outgoing) != 1 || decoded.Outgoing[0].Page.ID != "related-page" {
		t.Fatal("read lost original bytes, relationships or sources", err)
	}
	if r := read("released", project, "page", frozen.ID, 0); r.OK || r.Code != "transfer_expired" {
		t.Fatalf("finished transfer retained: %+v", r)
	}
	page.Body = body
	// Use the current revision to reopen a second transfer.
	snap, _ := kb.Snapshot()
	current, _ := snap.Page("page")
	page.Revision = current.Revision
	phoneKnowledgeWrite(t, kb, *page)
	reopened := read("reopen", project, "page", "", 0)
	if !reopened.OK || reopened.KnowledgeChunk == nil || !reopened.KnowledgeChunk.More {
		t.Fatal("could not open a new transfer")
	}
	cancel := Request{ID: "cancel", Op: OpKnowledgeCancel, ProjectID: project, KnowledgePageID: "page", KnowledgeTransferID: reopened.KnowledgeChunk.ID}
	if r := relay.rpc(t, cancel); !r.OK {
		t.Fatalf("cancel: %+v", r)
	}
	if r := read("cancelled", project, "page", cancel.KnowledgeTransferID, 0); r.OK || r.Code != "transfer_expired" {
		t.Fatalf("cancelled transfer still readable: %+v", r)
	}
	again := read("disable-open", project, "page", "", 0)
	if err := e.UpdateConfig(func(c *config.Config) error { c.Swarm.Knowledge = false; return nil }); err != nil {
		t.Fatal(err)
	}
	if r := read("disabled-chunk", project, "page", again.KnowledgeChunk.ID, again.KnowledgeChunk.NextOffset); r.OK || r.Code != "knowledge_disabled" {
		t.Fatalf("global disable was not enforced: %+v", r)
	}
	if r := relay.rpc(t, Request{ID: "disabled-list", Op: OpProjectKnowledge, ProjectID: project}); r.OK || r.Code != "knowledge_disabled" {
		t.Fatalf("global disable allowed listing: %+v", r)
	}
}

func TestPhoneKnowledgeTransferIsRevokedWhenItsProjectIsDeleted(t *testing.T) {
	e := testEngine(t)
	project, kb := phoneKnowledgeProject(t, e, "Removed library", true)
	phoneKnowledgeWrite(t, kb, knowledge.Page{ID: "page", Title: "Long entry", Body: strings.Repeat("line\n", FileChunkRaw/4)})
	relay := newFollowupRelay(t, e)
	req := Request{ID: "first", Op: OpKnowledgePage, ProjectID: project, KnowledgePageID: "page"}
	first := relay.rpc(t, req)
	if !first.OK || first.KnowledgeChunk == nil || !first.KnowledgeChunk.More {
		t.Fatal("expected an open transfer", first.Code)
	}
	if err := e.DeleteProject(project); err != nil {
		t.Fatal(err)
	}
	req.ID = "deleted"
	req.KnowledgeTransferID = first.KnowledgeChunk.ID
	req.Offset = first.KnowledgeChunk.NextOffset
	if response := relay.rpc(t, req); response.OK || response.Code != "not_found" {
		t.Fatalf("removed project still accessible: %+v", response)
	}
	req.ID = "released"
	if response := relay.rpc(t, req); response.OK || response.Code != "transfer_expired" {
		t.Fatalf("revoked transfer still retained: %+v", response)
	}
}

func TestPhoneKnowledgeRefusesUnsupportedVersionsAndClosedLinks(t *testing.T) {
	pump, replies := testPump(t, testEngine(t))
	req := Request{ID: "version", V: ProtocolV + 1, Op: OpKnowledgePage, ProjectID: "project", KnowledgePageID: "page"}
	if response := panelPumpRequest(t, pump, replies, req); response.OK || response.Code != "bad_version" {
		t.Fatalf("unknown version accepted: %+v", response)
	}
	pump.Close()
	req.ID, req.V = "closed", ProtocolV
	if response := panelPumpRequest(t, pump, replies, req); response.OK || response.Code != "transfer_expired" {
		t.Fatalf("closed link accepted a read: %+v", response)
	}
}
