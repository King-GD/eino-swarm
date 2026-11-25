package remote

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"sync"

	"github.com/LubyRuffy/eino-swarm/internal/engine"
	"github.com/LubyRuffy/eino-swarm/internal/knowledge"
)

const knowledgeListSize = 32
const maxKnowledgeTransferBytes = 16 << 20

type KnowledgeListView struct {
	ProjectID  string              `json:"project_id"`
	Enabled    bool                `json:"enabled"`
	Pages      []knowledge.PageRef `json:"pages"`
	NextOffset int64               `json:"next_offset,omitempty"`
}

type KnowledgeChunkView struct {
	ID         string `json:"id"`
	ProjectID  string `json:"project_id"`
	PageID     string `json:"page_id"`
	SHA256     string `json:"sha256"`
	Size       int64  `json:"size"`
	Offset     int64  `json:"offset"`
	NextOffset int64  `json:"next_offset"`
	Data       string `json:"data"`
	More       bool   `json:"more"`
}

type knowledgeTransfer struct {
	id, projectID, pageID, hash string
	body                        []byte
}

type knowledgeReadState struct {
	mu       sync.Mutex
	closed   bool
	transfer *knowledgeTransfer
}

func knowledgeRequestValid(req Request) bool {
	return len(req.ID) <= 128 && len(req.ProjectID) > 0 && len(req.ProjectID) <= 128
}

func knowledgeError(req Request, path, sessionID string, err error) Response {
	switch {
	case errors.Is(err, knowledge.ErrDisabled):
		return fail(req.ID, path, sessionID, "knowledge_disabled", "the PC knowledge base is disabled")
	case errors.Is(err, knowledge.ErrNotFound):
		return fail(req.ID, path, sessionID, "knowledge_page_not_found", "knowledge page not found")
	default:
		return mapErr(req.ID, path, sessionID, err)
	}
}

func handleKnowledgeList(eng *engine.Engine, req Request, path, sessionID string) Response {
	offset := int(req.Offset)
	if !knowledgeRequestValid(req) || offset < 0 || int64(offset) != req.Offset || len(req.Text) > 512 {
		return fail(truncate(req.ID, 128), path, sessionID, "bad_request", "invalid knowledge list request")
	}
	if !eng.Config().SwarmSnapshot().Knowledge {
		return knowledgeError(req, path, sessionID, knowledge.ErrDisabled)
	}
	kb, err := eng.ProjectKnowledge(req.ProjectID)
	if err != nil {
		return knowledgeError(req, path, sessionID, err)
	}
	snap, err := kb.Snapshot()
	if err != nil {
		return knowledgeError(req, path, sessionID, err)
	}
	pages := snap.SearchPage(req.Text, offset, knowledgeListSize+1)
	result := &KnowledgeListView{ProjectID: req.ProjectID, Enabled: snap.Enabled(), Pages: pages}
	if len(pages) > knowledgeListSize {
		result.Pages = pages[:knowledgeListSize]
		result.NextOffset = req.Offset + knowledgeListSize
	}
	for i := range result.Pages {
		result.Pages[i].Title = truncate(result.Pages[i].Title, 240)
	}
	resp := okBase(req.ID, path, sessionID)
	resp.Knowledge = result
	if raw, err := json.Marshal(resp); err != nil || len(raw) > plaintextBudget() {
		return fail(req.ID, path, sessionID, "too_large", "knowledge list exceeds the phone frame limit")
	}
	return resp
}

func (p *LinkPump) dispatchKnowledgeRead(req Request) {
	if req.V != 0 && req.V != ProtocolV {
		p.reply(fail(truncate(req.ID, 128), p.path, p.sessionID, "bad_version", "unsupported protocol version"))
		return
	}
	if !knowledgeRequestValid(req) || !knowledge.ValidSlug(req.KnowledgePageID) ||
		req.Offset < 0 || len(req.KnowledgeTransferID) > 32 || p.eng == nil {
		p.reply(fail(truncate(req.ID, 128), p.path, p.sessionID, "bad_request", "invalid knowledge page request"))
		return
	}
	state := &p.knowledgeRead
	state.mu.Lock()
	resp := p.knowledgeReadLocked(req, state)
	state.mu.Unlock()
	p.reply(resp)
}

func (p *LinkPump) knowledgeReadLocked(req Request, state *knowledgeReadState) Response {
	refuse := func(code, msg string) Response { return fail(req.ID, p.path, p.sessionID, code, msg) }
	if state.closed {
		return refuse("transfer_expired", "knowledge link closed")
	}
	transfer := state.transfer
	if req.KnowledgeTransferID != "" {
		if transfer == nil || req.KnowledgeTransferID != transfer.id {
			return refuse("transfer_expired", "knowledge page transfer expired")
		}
		if transfer.projectID != req.ProjectID || transfer.pageID != req.KnowledgePageID {
			return refuse("bad_request", "knowledge page transfer belongs to another scope")
		}
		if strings.TrimSpace(req.Op) == OpKnowledgeCancel {
			state.transfer = nil
			return okBase(req.ID, p.path, p.sessionID)
		}
		// Project removal and global disable revoke an in-progress read.
		if !p.eng.Config().SwarmSnapshot().Knowledge {
			state.transfer = nil
			return knowledgeError(req, p.path, p.sessionID, knowledge.ErrDisabled)
		}
		if _, err := p.eng.GetProject(req.ProjectID); err != nil {
			state.transfer = nil
			return knowledgeError(req, p.path, p.sessionID, err)
		}
	} else {
		if req.Offset != 0 || strings.TrimSpace(req.Op) == OpKnowledgeCancel {
			return refuse("bad_request", "first knowledge chunk must start at zero")
		}
		page, err := p.eng.ProjectKnowledgePage(context.Background(), req.ProjectID, req.KnowledgePageID)
		if err != nil {
			return knowledgeError(req, p.path, p.sessionID, err)
		}
		body, err := json.Marshal(page)
		if err != nil {
			return refuse("read_failed", "knowledge page cannot be encoded")
		}
		if len(body) > maxKnowledgeTransferBytes {
			return refuse("too_large", "knowledge page exceeds the transfer byte limit")
		}
		var token [16]byte
		if _, err := rand.Read(token[:]); err != nil {
			return refuse("read_failed", "knowledge page transfer identity unavailable")
		}
		sum := sha256.Sum256(body)
		transfer = &knowledgeTransfer{id: hex.EncodeToString(token[:]), projectID: req.ProjectID, pageID: req.KnowledgePageID,
			hash: hex.EncodeToString(sum[:]), body: body}
	}
	size := int64(len(transfer.body))
	if req.Offset >= size {
		return refuse("bad_request", "knowledge page offset is beyond the transfer")
	}
	end := min(req.Offset+FileChunkRaw, size)
	resp := okBase(req.ID, p.path, p.sessionID)
	resp.KnowledgeChunk = &KnowledgeChunkView{
		ID: transfer.id, ProjectID: transfer.projectID, PageID: transfer.pageID, SHA256: transfer.hash, Size: size,
		Offset: req.Offset, NextOffset: end, Data: base64.StdEncoding.EncodeToString(transfer.body[req.Offset:end]), More: end < size,
	}
	if raw, err := json.Marshal(resp); err != nil || len(raw) > plaintextBudget() {
		return refuse("too_large", "knowledge chunk exceeds the phone frame limit")
	}
	if end < size {
		state.transfer = transfer
	} else {
		state.transfer = nil
	}
	return resp
}

func (p *LinkPump) stopKnowledgeRead() {
	state := &p.knowledgeRead
	state.mu.Lock()
	defer state.mu.Unlock()
	state.closed = true
	state.transfer = nil
}
