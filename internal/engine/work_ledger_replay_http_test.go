package engine

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/LubyRuffy/eino-swarm/internal/store"
)

// A lost acknowledgement must return the original successful private update
// even after that node has been published. A fresh demotion stays refused.
func TestWorkUpdateReceiptReplaysAfterItsNodeIsPromotedAtModelHTTP(t *testing.T) {
	const updateCall = "explicit-private-update"
	var e *Engine
	var fixtureMu sync.Mutex
	var updateArgs, committed string
	var published store.NuwaNode
	var expected store.WorkRevision
	var expectedAudit int64
	type receipt struct {
		OK   bool           `json:"ok"`
		Node store.NuwaNode `json:"node"`
	}
	var model *scheduleCompletionHTTP
	e, model = newScheduleCompletionHTTPEngine(t, func(w http.ResponseWriter, _ *http.Request, step int, request scheduleCompletionRequest) {
		fixtureMu.Lock()
		defer fixtureMu.Unlock()
		finish := func() {
			writeContextFlowReply(w, request.Stream, map[string]any{"content": "The work record is current."}, "stop")
		}
		switch step {
		case 1:
			nuwaValidationTool(w, request, "create", "record_work", `{"kind":"fact","title":"Checked source"}`)
		case 2:
			var created receipt
			if raw := nuwaValidationReceipt(request, "create"); json.Unmarshal([]byte(raw), &created) != nil || !created.OK || created.Node.Scope != store.WorkScopeThread {
				t.Errorf("fixture did not create a private fact: %s", raw)
				finish()
				return
			}
			updateArgs = fmt.Sprintf(`{"id":%q,"version":%d,"scope":"thread","summary":"The source was checked."}`, created.Node.ID, created.Node.Version)
			nuwaValidationTool(w, request, updateCall, "record_work", updateArgs)
		case 3:
			committed = nuwaValidationReceipt(request, updateCall)
			var updated receipt
			if json.Unmarshal([]byte(committed), &updated) != nil || !updated.OK || updated.Node.Scope != store.WorkScopeThread || updated.Node.Version != 2 {
				t.Errorf("fixture did not commit the explicit private update: %s", committed)
				finish()
				return
			}
			nuwaValidationTool(w, request, "promote", "record_work", fmt.Sprintf(`{"id":%q,"version":%d,"scope":"project"}`, updated.Node.ID, updated.Node.Version))
		case 4:
			var shared receipt
			if raw := nuwaValidationReceipt(request, "promote"); json.Unmarshal([]byte(raw), &shared) != nil || !shared.OK || shared.Node.Scope != store.WorkScopeProject || shared.Node.Version != 3 {
				t.Errorf("fixture did not promote the fact: %s", raw)
				finish()
				return
			}
			published = shared.Node
			var err error
			expected, err = e.ThreadWorkRevision(t.Context(), published.ThreadID)
			if err != nil {
				t.Errorf("read the promoted revision pair: %v", err)
				finish()
				return
			}
			if err := e.store.DB().WithContext(t.Context()).Model(&store.NuwaRevision{}).Where("node_id = ?", published.ID).Count(&expectedAudit).Error; err != nil {
				t.Errorf("read published audit count: %v", err)
				finish()
				return
			}
			nuwaValidationTool(w, request, updateCall, "record_work", updateArgs)
		case 5:
			if replayed := nuwaValidationReceipt(request, updateCall); replayed != committed {
				t.Errorf("replayed receipt differs from the original private commit: got %s, want %s", replayed, committed)
			}
			nuwaValidationTool(w, request, "fresh-demotion", "record_work",
				fmt.Sprintf(`{"id":%q,"version":%d,"scope":"thread","summary":"Do not save this change."}`, published.ID, published.Version))
		case 6:
			refused := nuwaValidationReceipt(request, "fresh-demotion")
			if !strings.Contains(refused, `"ok":false`) || !strings.Contains(refused, "scope") {
				t.Errorf("a fresh demotion was not explicitly refused: %s", refused)
			}
			finish()
		default:
			t.Errorf("unexpected model step %d", step)
			finish()
		}
	})
	e.cfg.Memory.AutoReview = false
	project, err := e.CreateProject("Receipt after promotion", "", "", false, false)
	if err != nil {
		t.Fatal(err)
	}
	thread, err := e.CreateThread("Receipt owner", "", project.ID)
	if err != nil {
		t.Fatal(err)
	}
	turn, err := e.StartTurn(thread.ID, "Check the source and publish the resulting record.")
	if err != nil {
		t.Fatal(err)
	}
	finished := waitForTurn(t, e, turn.ID)
	waitSettled(t, e, thread.ID)
	view, err := e.ThreadWorkSnapshot(t.Context(), thread.ID)
	fixtureMu.Lock()
	defer fixtureMu.Unlock()
	var audit int64
	if auditErr := e.store.DB().WithContext(t.Context()).Model(&store.NuwaRevision{}).Where("node_id = ?", published.ID).Count(&audit).Error; auditErr != nil || audit != expectedAudit {
		t.Fatalf("replay or refused demotion changed the audit count: got %d, want %d (%v)", audit, expectedAudit, auditErr)
	}
	if err != nil || finished.Status != store.TurnDone || len(model.snapshot()) != 6 ||
		view.WorkRevision != expected || len(view.Nodes) != 1 || view.Nodes[0].ID != published.ID ||
		view.Nodes[0].Version != published.Version || view.Nodes[0].Scope != store.WorkScopeProject || view.Nodes[0].Summary != published.Summary {
		t.Fatalf("replay or refused demotion changed the promoted record: %+v err=%v", view, err)
	}
}
