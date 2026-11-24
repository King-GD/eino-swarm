package remote

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/LubyRuffy/eino-swarm/internal/engine"
	"github.com/LubyRuffy/eino-swarm/internal/store"
	"github.com/LubyRuffy/eino-swarm/internal/taskpanel"
)

func phonePanelFixture(t *testing.T) (*engine.Engine, taskpanel.Panel, taskpanel.Source) {
	t.Helper()
	e := testEngine(t)
	thread, err := e.CreateThread("Phone panel fixture", "", "")
	if err != nil {
		t.Fatal(err)
	}
	turn := store.Turn{ThreadID: thread.ID}
	if err = e.Store().CreateTurn(&turn); err != nil {
		t.Fatal(err)
	}
	source := taskpanel.Source{ThreadID: thread.ID, TurnID: turn.ID, AgentID: "manager", ToolCallID: "panel-fixture"}
	definition := taskpanel.Definition{ID: "transfer-fixture", Version: "1", Name: "Transfer fixture", Entry: "task-panels/transfer-fixture.html", Schema: json.RawMessage(`{"type":"object"}`), InitialState: json.RawMessage(`{"count":0}`)}
	panel, err := e.Store().OpenTaskPanel(t.Context(), definition, source)
	if err != nil {
		t.Fatal(err)
	}
	return e, panel, source
}

// Exercise sealed pairlink frames and the production dispatcher, rather than
// calling an HTTP handler that the phone cannot reach.
func TestPhoneTaskPanelManifestUsesExistingSealedLink(t *testing.T) {
	e, panel, source := phonePanelFixture(t)
	relay := newFollowupRelay(t, e)
	list := func(id string, want int) {
		t.Helper()
		got := relay.rpc(t, Request{ID: id, Op: OpTaskPanels, ThreadID: panel.ThreadID})
		if !got.OK || got.TaskPanels == nil || len(*got.TaskPanels) != want {
			t.Fatalf("manifest %s: %+v", id, got)
		}
	}
	list("initial-state-only", 0)
	panel = phonePanelPublish(t, e, panel, source, "observed-zero", map[string]any{"count": 0})
	list("zero-is-an-observation", 1)
	first := relay.rpc(t, Request{ID: "read-zero", Op: OpTaskPanelRead, ThreadID: panel.ThreadID, PanelID: panel.ID})
	panelChunkBytes(t, first)
	if got := relay.rpc(t, Request{ID: "foreign-close", Op: OpTaskPanelClose, ThreadID: "other", PanelID: panel.ID}); got.OK || got.Code != "not_found" {
		t.Fatalf("foreign close succeeded: %+v", got)
	}
	if got := relay.rpc(t, Request{ID: "transfer-is-not-an-instance", Op: OpTaskPanelClose, ThreadID: panel.ThreadID, PanelID: panel.ID, PanelTransferID: first.PanelTransfer.ID}); got.OK || got.Code != "bad_request" {
		t.Fatalf("close accepted a transfer token: %+v", got)
	}
	e.BeginRestore()
	if got := relay.rpc(t, Request{ID: "close-during-restore", Op: OpTaskPanelClose, ThreadID: panel.ThreadID, PanelID: panel.ID}); got.OK || got.Code != "restoring" {
		t.Fatalf("close admitted during restore: %+v", got)
	}
	e.FinishRestore()
	if got := relay.rpc(t, Request{ID: "close", Op: OpTaskPanelClose, ThreadID: panel.ThreadID, PanelID: panel.ID}); !got.OK {
		t.Fatalf("close failed: %+v", got)
	}
	list("closed", 0)
	got := relay.rpc(t, Request{ID: "frozen-transfer", Op: OpTaskPanelRead, ThreadID: panel.ThreadID, PanelID: panel.ID, PanelTransferID: first.PanelTransfer.ID})
	if got.OK || got.Code != "transfer_expired" {
		t.Fatalf("closed transfer was revived: %+v", got)
	}
}

func phonePanelPublish(t *testing.T, e *engine.Engine, p taskpanel.Panel, src taskpanel.Source, id string, state any) taskpanel.Panel {
	t.Helper()
	body, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	next, err := e.Store().PublishTaskPanel(t.Context(), src, taskpanel.Update{ProtocolVersion: 1, PanelID: p.ID, OperationID: id, BaseRevision: p.Revision, Kind: "snapshot", State: body})
	if err != nil {
		t.Fatal(err)
	}
	return next
}
func panelChunkBytes(t *testing.T, response Response) []byte {
	t.Helper()
	if !response.OK || response.PanelTransfer == nil {
		t.Fatalf("read failed: %+v", response)
	}
	raw, err := json.Marshal(response)
	if err != nil || len(raw)+24 > MaxPushPayload {
		t.Fatal("sealed frame budget", len(raw), err)
	}
	chunk, err := base64.StdEncoding.DecodeString(response.PanelTransfer.Data)
	if err != nil || len(chunk) > TaskPanelChunkRaw {
		t.Fatal(len(chunk), err)
	}
	return chunk
}
func TestPhoneTaskPanelTransferIsImmutableAcrossLiveUpdatesAndScopedTokens(t *testing.T) {
	e, panel, source := phonePanelFixture(t)
	panel = phonePanelPublish(t, e, panel, source, "large-observation", map[string]any{"count": 1, "blob": strings.Repeat("recorded-世界", 10000)})
	relay := newFollowupRelay(t, e)
	first := relay.rpc(t, Request{ID: "first", Op: OpTaskPanelRead, ThreadID: panel.ThreadID, PanelID: panel.ID})
	assembled := panelChunkBytes(t, first)
	fixed := *first.PanelTransfer
	if !fixed.More || fixed.Revision != panel.Revision {
		t.Fatal(fixed)
	}
	// A later durable revision must not change the bytes of this transfer.
	newer, err := e.Store().PublishTaskPanel(t.Context(), source, taskpanel.Update{ProtocolVersion: 1, PanelID: panel.ID, OperationID: "later-observation", BaseRevision: panel.Revision, Kind: "patch", Operations: []byte(`[{"op":"replace","path":"/count","value":2}]`)})
	if err != nil {
		t.Fatal(err)
	}
	offset := fixed.NextOffset
	for offset < fixed.Size {
		response := relay.rpc(t, Request{ID: fmt.Sprintf("part-%d", offset), Op: OpTaskPanelRead, ThreadID: panel.ThreadID, PanelID: panel.ID, PanelTransferID: fixed.ID, Offset: offset})
		chunk := panelChunkBytes(t, response)
		meta := response.PanelTransfer
		if meta.ID != fixed.ID || meta.ThreadID != fixed.ThreadID || meta.PanelID != fixed.PanelID || meta.Revision != fixed.Revision || meta.Size != fixed.Size || meta.SHA256 != fixed.SHA256 || meta.Offset != offset || meta.NextOffset != offset+int64(len(chunk)) {
			t.Fatal("mixed transfer metadata", meta)
		}
		assembled = append(assembled, chunk...)
		offset = meta.NextOffset
	}
	sum := sha256.Sum256(assembled)
	if hex.EncodeToString(sum[:]) != fixed.SHA256 || int64(len(assembled)) != fixed.Size {
		t.Fatal("immutable hash mismatch")
	}
	var body struct {
		Panel taskpanel.Panel `json:"panel"`
	}
	if err = json.Unmarshal(assembled, &body); err != nil || body.Panel.Revision != panel.Revision || !bytes.Contains(body.Panel.State, []byte(`"count":1`)) {
		t.Fatal("live update changed frozen body", err)
	}
	base := panel.Revision
	patchReply := relay.rpc(t, Request{ID: "patch", Op: OpTaskPanelRead, ThreadID: panel.ThreadID, PanelID: panel.ID, PanelBaseRevision: &base})
	deltaBytes := panelChunkBytes(t, patchReply)
	delta := patchReply.PanelTransfer
	var envelope struct {
		Change *taskpanel.Change `json:"change"`
		Panel  *taskpanel.Panel  `json:"panel"`
	}
	if err = json.Unmarshal(deltaBytes, &envelope); err != nil || envelope.Change == nil || envelope.Panel != nil || envelope.Change.BaseRevision != base || delta.Revision != newer.Revision || delta.ID == fixed.ID {
		t.Fatal("exact patch unavailable", envelope, err)
	}
	for _, op := range []string{OpTaskPanelRead, OpTaskPanelCancel} {
		response := relay.rpc(t, Request{ID: op + "-old", Op: op, ThreadID: panel.ThreadID, PanelID: panel.ID, PanelTransferID: fixed.ID})
		if response.OK || response.Code != "transfer_expired" {
			t.Fatal("old token accepted", response)
		}
	}
	wrong := relay.rpc(t, Request{ID: "wrong-thread", Op: OpTaskPanelRead, ThreadID: "another-thread", PanelID: panel.ID, PanelTransferID: delta.ID})
	if wrong.OK || wrong.PanelTransfer != nil || wrong.Code != "bad_request" {
		t.Fatal("cross-thread transfer leaked", wrong)
	}
	wrong = relay.rpc(t, Request{ID: "wrong-panel", Op: OpTaskPanelCancel, ThreadID: panel.ThreadID, PanelID: "another-panel", PanelTransferID: delta.ID})
	if wrong.OK || wrong.Code != "bad_request" {
		t.Fatal("cross-panel cancel accepted", wrong)
	}
	// A failed replacement cannot destroy the current valid token.
	failed := relay.rpc(t, Request{ID: "missing-panel", Op: OpTaskPanelRead, ThreadID: panel.ThreadID, PanelID: "missing"})
	if failed.OK || failed.Code != "not_found" {
		t.Fatal(failed)
	}
	preserved := relay.rpc(t, Request{ID: "preserved", Op: OpTaskPanelRead, ThreadID: panel.ThreadID, PanelID: panel.ID, PanelTransferID: delta.ID})
	if got := panelChunkBytes(t, preserved); !bytes.Equal(got, deltaBytes) {
		t.Fatal("stale request damaged successor")
	}
	cancel := relay.rpc(t, Request{ID: "cancel", Op: OpTaskPanelCancel, ThreadID: panel.ThreadID, PanelID: panel.ID, PanelTransferID: delta.ID})
	if !cancel.OK {
		t.Fatal(cancel)
	}
	if response := relay.rpc(t, Request{ID: "cancelled-read", Op: OpTaskPanelRead, ThreadID: panel.ThreadID, PanelID: panel.ID, PanelTransferID: delta.ID}); response.OK || response.Code != "transfer_expired" {
		t.Fatal(response)
	}
	_ = relay.rpc(t, Request{ID: "before-close", Op: OpTaskPanelRead, ThreadID: panel.ThreadID, PanelID: panel.ID})
	handler := <-relay.handlerReady
	relay.close()
	handler.pump.panelMu.Lock()
	defer handler.pump.panelMu.Unlock()
	if !handler.pump.panelClosed || handler.pump.panelTransfer != nil {
		t.Fatal("link close retained transfer bytes")
	}
}

func TestPhoneTaskPanelEmptyAndOversizedManifestsAreExplicit(t *testing.T) {
	e, panel, _ := phonePanelFixture(t)
	relay := newFollowupRelay(t, e)
	empty, err := e.CreateThread("No panels", "", "")
	if err != nil {
		t.Fatal(err)
	}
	response := relay.rpc(t, Request{ID: "empty", Op: OpTaskPanels, ThreadID: empty.ID})
	if !response.OK || response.TaskPanels == nil || len(*response.TaskPanels) != 0 {
		t.Fatal("empty manifest was omitted", response)
	}
	raw, _ := json.Marshal(response)
	if !bytes.Contains(raw, []byte(`"task_panels":[]`)) {
		t.Fatal(string(raw))
	}
	var rows []store.TaskPanelRow
	for i := range 160 {
		definition, _ := json.Marshal(taskpanel.Definition{Version: strings.Repeat("v", 512)})
		rows = append(rows, store.TaskPanelRow{ID: fmt.Sprintf("manifest-%d", i), ThreadID: panel.ThreadID, PluginID: fmt.Sprintf("manifest-plugin-%d", i), DefinitionJSON: definition, Revision: 1, SourceStatus: "running"})
	}
	if err = e.Store().DB().CreateInBatches(rows, 32).Error; err != nil {
		t.Fatal(err)
	}
	response = relay.rpc(t, Request{ID: "too-large", Op: OpTaskPanels, ThreadID: panel.ThreadID})
	if response.OK || response.Code != "too_large" || response.TaskPanels != nil {
		t.Fatal("manifest silently truncated", response)
	}
}
