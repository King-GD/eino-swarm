package remote

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strings"

	"github.com/LubyRuffy/eino-swarm/internal/engine"
	"github.com/LubyRuffy/eino-swarm/internal/panelplugins/easm"
	"github.com/LubyRuffy/eino-swarm/internal/store"
	"github.com/LubyRuffy/eino-swarm/internal/taskpanel"
)

const (
	// TaskPanelChunkRaw fits its base64 representation and sealed reply metadata.
	TaskPanelChunkRaw = 36 << 10
	// MaxTaskPanelTransferBytes bounds the complete encoded panel envelope.
	MaxTaskPanelTransferBytes = 64 << 20
	// Even minimum-size generated identities cannot fit this many in one frame.
	maxTaskPanelManifestEntries = 1024
)

// PanelTransferView binds every byte slice to one immutable per-link snapshot.
type PanelTransferView struct {
	ID         string `json:"id"`
	ThreadID   string `json:"thread_id"`
	PanelID    string `json:"panel_id"`
	Revision   int64  `json:"revision"`
	Size       int64  `json:"size"`
	SHA256     string `json:"sha256"`
	Offset     int64  `json:"offset"`
	NextOffset int64  `json:"next_offset"`
	Data       string `json:"data"`
	More       bool   `json:"more"`
}

type panelTransfer struct {
	id, threadID, panelID, hash string
	revision                    int64
	body                        []byte
}

func panelRequestValid(req Request) bool {
	return len(req.ID) <= 128 && len(req.ThreadID) > 0 && len(req.ThreadID) <= 128 && len(req.PanelID) <= 128 && len(req.PanelTransferID) <= 128 &&
		(req.EASMPanelVersion == "" || req.EASMPanelVersion == easm.LegacyPanelVersion ||
			req.EASMPanelVersion == easm.PreviousPanelVersion || req.EASMPanelVersion == easm.PanelVersion)
}

func handleTaskPanels(eng *engine.Engine, req Request, path, sessionID string) Response {
	if !panelRequestValid(req) {
		return fail(req.ID, path, sessionID, "bad_request", "invalid panel request identity")
	}
	rows, more, err := eng.Store().TaskPanelManifests(context.Background(), req.ThreadID, maxTaskPanelManifestEntries)
	if err != nil {
		return mapErr(req.ID, path, sessionID, err)
	}
	if more {
		return fail(req.ID, path, sessionID, "too_large", "panel manifest exceeds the phone frame limit")
	}
	for i := range rows {
		if rows[i].PluginID != easm.PanelID {
			continue
		}
		if version := easm.ClientVersion(rows[i].PluginVersion, req.EASMPanelVersion); version != "" {
			rows[i].PluginVersion = version
		}
	}
	resp := okBase(req.ID, path, sessionID)
	resp.TaskPanels = &rows
	if raw, err := json.Marshal(resp); err != nil || len(raw) > plaintextBudget() {
		return fail(req.ID, path, sessionID, "too_large", "panel manifest exceeds the phone frame limit")
	}
	return resp
}

func (p *LinkPump) dispatchTaskPanels(req Request) {
	req.Op = strings.TrimSpace(req.Op)
	if len(req.ID) > 128 {
		req.ID = truncate(req.ID, 128)
		p.reply(fail(req.ID, p.path, p.sessionID, "bad_request", "invalid panel request identity"))
		return
	}
	if req.V != 0 && req.V != ProtocolV {
		p.reply(fail(req.ID, p.path, p.sessionID, "bad_version", "unsupported protocol version"))
		return
	}
	if !panelRequestValid(req) || p.eng == nil {
		req.ID = truncate(req.ID, 128)
		p.reply(fail(req.ID, p.path, p.sessionID, "bad_request", "invalid panel request identity"))
		return
	}
	p.panelMu.Lock()
	if p.panelClosed {
		p.panelMu.Unlock()
		p.reply(fail(req.ID, p.path, p.sessionID, "transfer_expired", "panel link is closed"))
		return
	}
	var resp Response
	switch req.Op {
	case OpTaskPanels:
		resp = handleTaskPanels(p.eng, req, p.path, p.sessionID)
	case OpTaskPanelCancel:
		if p.panelTransfer == nil || p.panelTransfer.id != req.PanelTransferID {
			resp = fail(req.ID, p.path, p.sessionID, "transfer_expired", "panel transfer is unavailable")
		} else if req.ThreadID != p.panelTransfer.threadID || req.PanelID != p.panelTransfer.panelID {
			resp = fail(req.ID, p.path, p.sessionID, "bad_request", "panel transfer belongs to another scope")
		} else {
			p.panelTransfer = nil
			resp = okBase(req.ID, p.path, p.sessionID)
		}
	case OpTaskPanelRead:
		resp = p.readTaskPanelLocked(req)
	case OpTaskPanelClose:
		resp = p.closeTaskPanelLocked(req)
	}
	p.panelMu.Unlock()
	resp = withHost(resp, p.cfg)
	if raw, err := json.Marshal(resp); err != nil || len(raw) > plaintextBudget() {
		resp = fail(req.ID, p.path, p.sessionID, "too_large", "panel reply exceeds the phone frame limit")
	}
	p.reply(resp)
}

func (p *LinkPump) closeTaskPanelLocked(req Request) Response {
	if req.PanelID == "" || req.PanelTransferID != "" || req.PanelBaseRevision != nil || req.Offset != 0 {
		return fail(req.ID, p.path, p.sessionID, "bad_request", "invalid panel close request")
	}
	if err := requireStartupReady(p.eng, req.Op); err != nil {
		return mapErr(req.ID, p.path, p.sessionID, err)
	}
	if err := p.eng.CloseTaskPanel(context.Background(), req.ThreadID, req.PanelID); err != nil {
		return mapErr(req.ID, p.path, p.sessionID, err)
	}
	if transfer := p.panelTransfer; transfer != nil && transfer.threadID == req.ThreadID && transfer.panelID == req.PanelID {
		p.panelTransfer = nil
	}
	return okBase(req.ID, p.path, p.sessionID)
}

func (p *LinkPump) readTaskPanelLocked(req Request) Response {
	failRead := func(code, message string) Response { return fail(req.ID, p.path, p.sessionID, code, message) }
	if req.PanelID == "" || req.Offset < 0 || (req.PanelBaseRevision != nil && *req.PanelBaseRevision < 0) {
		return failRead("bad_request", "invalid panel transfer request")
	}
	transfer := p.panelTransfer
	if req.PanelTransferID == "" {
		if req.Offset != 0 {
			return failRead("bad_request", "first panel chunk must start at zero")
		}
		panel, change, err := p.eng.Store().TaskPanelReadForTransfer(context.Background(), req.ThreadID, req.PanelID, req.PanelBaseRevision)
		if err != nil {
			return mapErr(req.ID, p.path, p.sessionID, err)
		}
		if panel.PluginID == easm.PanelID {
			panel, change, err = projectEASMPanel(panel, change, req.EASMPanelVersion)
			if err != nil {
				return failRead("read_failed", "EASM panel cannot be represented in the requested format")
			}
		}
		var body []byte
		if change != nil {
			body, err = json.Marshal(struct {
				Change *taskpanel.Change `json:"change"`
			}{change})
		} else {
			body, err = json.Marshal(struct {
				Panel taskpanel.Panel `json:"panel"`
			}{panel})
		}
		if err != nil {
			return failRead("read_failed", "panel cannot be encoded")
		}
		if len(body) > MaxTaskPanelTransferBytes {
			return failRead("too_large", "panel exceeds the transfer byte limit")
		}
		var token [16]byte
		if _, err = rand.Read(token[:]); err != nil {
			return failRead("read_failed", "panel transfer identity unavailable")
		}
		sum := sha256.Sum256(body)
		transfer = &panelTransfer{id: hex.EncodeToString(token[:]), threadID: req.ThreadID, panelID: req.PanelID, revision: panel.Revision, hash: hex.EncodeToString(sum[:]), body: body}
	} else {
		if transfer == nil || transfer.id != req.PanelTransferID {
			return failRead("transfer_expired", "panel transfer is unavailable")
		}
		if req.ThreadID != transfer.threadID || req.PanelID != transfer.panelID || req.PanelBaseRevision != nil || req.EASMPanelVersion != "" {
			return failRead("bad_request", "panel transfer belongs to another scope")
		}
		visible, err := p.eng.Store().TaskPanelVisible(context.Background(), transfer.threadID, transfer.panelID)
		if err != nil || !visible {
			p.panelTransfer = nil
			if err == nil {
				err = store.ErrNotFound
			}
			return mapErr(req.ID, p.path, p.sessionID, err)
		}
	}
	if req.Offset > int64(len(transfer.body)) {
		return failRead("bad_request", "panel offset is beyond the transfer")
	}
	end := min(req.Offset+TaskPanelChunkRaw, int64(len(transfer.body)))
	resp := okBase(req.ID, p.path, p.sessionID)
	resp.PanelTransfer = &PanelTransferView{ID: transfer.id, ThreadID: transfer.threadID, PanelID: transfer.panelID, Revision: transfer.revision, Size: int64(len(transfer.body)), SHA256: transfer.hash, Offset: req.Offset, NextOffset: end, Data: base64.StdEncoding.EncodeToString(transfer.body[req.Offset:end]), More: end < int64(len(transfer.body))}
	// A failed first reply must not retire an older valid transfer.
	raw, err := json.Marshal(withHost(resp, p.cfg))
	if err != nil || len(raw) > plaintextBudget() {
		return failRead("too_large", "panel reply exceeds the phone frame limit")
	}
	p.panelTransfer = transfer
	return resp
}

func (p *LinkPump) stopTaskPanels() {
	p.panelMu.Lock()
	defer p.panelMu.Unlock()
	p.panelClosed = true
	p.panelTransfer = nil
}
