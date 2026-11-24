package remote

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/LubyRuffy/eino-swarm/internal/config"
	"github.com/LubyRuffy/eino-swarm/internal/store"
)

func panelPumpRequest(t *testing.T, p *LinkPump, log *pushLog, req Request) Response {
	t.Helper()
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	p.Dispatch(raw)
	rows := log.snapshot()
	if len(rows) == 0 {
		t.Fatal("no panel reply")
	}
	return rows[len(rows)-1]
}
func TestPhonePanelBadRequestsPreserveAnExistingTransfer(t *testing.T) {
	e, panel, source := phonePanelFixture(t)
	panel = phonePanelPublish(t, e, panel, source, "initial-observation", map[string]any{"count": 1})
	p, log := testPump(t, e)
	first := panelPumpRequest(t, p, log, Request{ID: "first", Op: OpTaskPanelRead, ThreadID: panel.ThreadID, PanelID: panel.ID})
	if !first.OK || first.PanelTransfer == nil {
		t.Fatal(first)
	}
	token := first.PanelTransfer.ID
	negative := int64(-1)
	base := int64(1)
	cases := []Request{
		{ID: "version", V: 999, Op: OpTaskPanelRead, ThreadID: panel.ThreadID, PanelID: panel.ID},
		{ID: strings.Repeat("x", 129), Op: OpTaskPanels, ThreadID: panel.ThreadID},
		{ID: "thread", Op: OpTaskPanels},
		{ID: "panel", Op: OpTaskPanelRead, ThreadID: panel.ThreadID},
		{ID: "negative", Op: OpTaskPanelRead, ThreadID: panel.ThreadID, PanelID: panel.ID, Offset: -1},
		{ID: "first-offset", Op: OpTaskPanelRead, ThreadID: panel.ThreadID, PanelID: panel.ID, Offset: 1},
		{ID: "negative-base", Op: OpTaskPanelRead, ThreadID: panel.ThreadID, PanelID: panel.ID, PanelBaseRevision: &negative},
		{ID: "wrong-thread", Op: OpTaskPanelRead, ThreadID: "other", PanelID: panel.ID, PanelTransferID: token},
		{ID: "wrong-panel", Op: OpTaskPanelRead, ThreadID: panel.ThreadID, PanelID: "other", PanelTransferID: token},
		{ID: "chunk-base", Op: OpTaskPanelRead, ThreadID: panel.ThreadID, PanelID: panel.ID, PanelTransferID: token, PanelBaseRevision: &base},
		{ID: "past-end", Op: OpTaskPanelRead, ThreadID: panel.ThreadID, PanelID: panel.ID, PanelTransferID: token, Offset: first.PanelTransfer.Size + 1},
	}
	for _, req := range cases {
		got := panelPumpRequest(t, p, log, req)
		if got.OK || got.PanelTransfer != nil {
			t.Fatalf("invalid request accepted: %s %+v", req.ID, got)
		}
		if p.panelTransfer == nil || p.panelTransfer.id != token {
			t.Fatal("bad request destroyed transfer", req.ID)
		}
	}
	got := panelPumpRequest(t, p, log, Request{ID: "spaces", Op: " task_panels ", ThreadID: panel.ThreadID})
	if !got.OK {
		t.Fatal(got)
	}
	if p.panelTransfer == nil || p.panelTransfer.id != token {
		t.Fatal("manifest polling cancelled active read")
	}
	p.Close()
	got = panelPumpRequest(t, p, log, Request{ID: "closed", Op: OpTaskPanelRead, ThreadID: panel.ThreadID, PanelID: panel.ID})
	if got.OK || got.Code != "transfer_expired" {
		t.Fatal(got)
	}
}
func TestPhonePanelDeletedScopeReleasesOnlyItsTransfer(t *testing.T) {
	e, panel, source := phonePanelFixture(t)
	panel = phonePanelPublish(t, e, panel, source, "initial-observation", map[string]any{"count": 1})
	p, log := testPump(t, e)
	first := panelPumpRequest(t, p, log, Request{ID: "first", Op: OpTaskPanelRead, ThreadID: panel.ThreadID, PanelID: panel.ID})
	if !first.OK || first.PanelTransfer == nil {
		t.Fatal(first)
	}
	if err := e.Store().DeleteThread(panel.ThreadID); err != nil {
		t.Fatal(err)
	}
	gone := panelPumpRequest(t, p, log, Request{ID: "gone", Op: OpTaskPanelRead, ThreadID: panel.ThreadID, PanelID: panel.ID, PanelTransferID: first.PanelTransfer.ID})
	if gone.OK || gone.Code != "not_found" || p.panelTransfer != nil {
		t.Fatal(gone)
	}
}
func TestPhonePanelEncodingFailureDoesNotReplaceTransfer(t *testing.T) {
	e, panel, source := phonePanelFixture(t)
	panel = phonePanelPublish(t, e, panel, source, "initial-observation", map[string]any{"count": 1})
	p, log := testPump(t, e)
	first := panelPumpRequest(t, p, log, Request{ID: "first", Op: OpTaskPanelRead, ThreadID: panel.ThreadID, PanelID: panel.ID})
	if !first.OK || first.PanelTransfer == nil {
		t.Fatal(first)
	}
	if err := e.Store().DB().Model(&store.TaskPanelRow{}).Where("id = ?", panel.ID).UpdateColumn("state", []byte(`{`)).Error; err != nil {
		t.Fatal(err)
	}
	broken := panelPumpRequest(t, p, log, Request{ID: "broken", Op: OpTaskPanelRead, ThreadID: panel.ThreadID, PanelID: panel.ID})
	if broken.OK || broken.Code != "read_failed" || p.panelTransfer == nil || p.panelTransfer.id != first.PanelTransfer.ID {
		t.Fatal(broken)
	}
}
func TestPhonePanelStatelessReadRequiresLinkAndManifestReportsFailures(t *testing.T) {
	e, panel, source := phonePanelFixture(t)
	panel = phonePanelPublish(t, e, panel, source, "initial-observation", map[string]any{"count": 1})
	for _, op := range []string{OpTaskPanelRead, OpTaskPanelCancel} {
		if got := Handle(e, config.RemoteConfig{}, Request{ID: op, Op: op, ThreadID: panel.ThreadID, PanelID: panel.ID}, "relay", "session"); got.OK || got.Code != "link_required" {
			t.Fatal(got)
		}
	}
	for _, threadID := range []string{"", "absent"} {
		if got := Handle(e, config.RemoteConfig{}, Request{ID: "manifest", Op: OpTaskPanels, ThreadID: threadID}, "relay", "session"); got.OK {
			t.Fatal(got)
		}
	}
	if got := Handle(e, config.RemoteConfig{}, Request{ID: "manifest", Op: OpTaskPanels, ThreadID: panel.ThreadID}, "relay", "session"); !got.OK || got.TaskPanels == nil {
		t.Fatal(got)
	}
	p, log := testPump(t, e)
	first := panelPumpRequest(t, p, log, Request{ID: "first", Op: OpTaskPanelRead, ThreadID: panel.ThreadID, PanelID: panel.ID})
	if !first.OK {
		t.Fatal(first)
	}
	if err := e.Store().Close(); err != nil {
		t.Fatal(err)
	}
	got := panelPumpRequest(t, p, log, Request{ID: "db", Op: OpTaskPanelRead, ThreadID: panel.ThreadID, PanelID: panel.ID, PanelTransferID: first.PanelTransfer.ID})
	if got.OK || p.panelTransfer != nil {
		t.Fatal("unavailable lifetime retained transfer", got)
	}
}
