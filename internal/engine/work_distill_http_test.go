package engine

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/LubyRuffy/eino-swarm/internal/config"
	"github.com/LubyRuffy/eino-swarm/internal/provider"
	"github.com/LubyRuffy/eino-swarm/internal/store"
)

func TestWorkDistillationUsesPrivateSavedEvidenceAndHonorsGlobalSwitch(t *testing.T) {
	ctx := t.Context()
	e := newTestEngine(t)
	var calls atomic.Int32
	var switchOffDuringModel atomic.Bool
	setEnabled := func(enabled bool) error {
		return e.UpdateConfig(func(cfg *config.Config) error {
			cfg.Swarm.WorkLedger = enabled
			return nil
		})
	}
	// Exercise the real streaming provider adapter, with no callable tools.
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var req scheduleCompletionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode extraction: %v", err)
			http.Error(w, "invalid request", 400)
			return
		}
		source := joinedContent(req.Messages)
		if !strings.Contains(source, "A durable observation") || strings.Contains(source, "Another conversation") ||
			strings.Contains(source, "Derived tool payload") || len(req.Tools) != 0 {
			t.Errorf("wrong extraction inputs: %s, tools=%v", source, req.Tools)
		}
		if switchOffDuringModel.Load() {
			if err := setEnabled(false); err != nil {
				t.Error(err)
			}
		}
		writeContextFlowReply(w, req.Stream, map[string]any{"content": `{"records":[
			{"kind":"fact","title":"Observed behavior","summary":"The source reports a repeatable result.","status":"reported","evidence":[1]},
			{"kind":"task","title":"Repeat the measurement","summary":"The source asks for another measurement.","status":"pending","evidence":[1]},
			{"kind":"artifact","title":"Saved report","summary":"This output still exists.","status":"reported","artifact_path":"results/report.md","evidence":[1]},
			{"kind":"artifact","title":"Missing report","summary":"This output was removed.","status":"reported","artifact_path":"results/missing.md","evidence":[1]}
		]}`}, "stop")
	}))
	defer endpoint.Close()
	e.cfg.Models.Providers[0].BaseURL = endpoint.URL
	e.cfg.Models.Providers[0].Model = "offline-work-distiller"
	e.pool = provider.New(e.cfg)
	makeHistory := func(text string) (*store.Thread, store.Event) {
		t.Helper()
		thread, err := e.CreateThread(text, "", "")
		if err != nil {
			t.Fatal(err)
		}
		turn := store.Turn{ThreadID: thread.ID}
		if err := e.store.CreateTurn(&turn); err != nil {
			t.Fatal(err)
		}
		event := store.Event{ThreadID: thread.ID, TurnID: turn.ID, Kind: KindUser, Role: "user", Text: text}
		if err := e.store.AppendEvent(&event); err != nil {
			t.Fatal(err)
		}
		derived := store.Event{ThreadID: thread.ID, TurnID: turn.ID, Kind: KindUser, Role: "user", Text: "Derived tool payload", WorkRecordPayload: true}
		if err := e.store.AppendEvent(&derived); err != nil {
			t.Fatal(err)
		}
		if err := e.store.FinishTurn(turn.ID, store.TurnDone, "", ""); err != nil {
			t.Fatal(err)
		}
		return thread, event
	}
	thread, evidence := makeHistory("A durable observation in results/report.md")
	if err := os.WriteFile(filepath.Join(e.TaskFileDir(thread.ID), "results", "report.md"), []byte("An existing output"), 0o600); err != nil {
		t.Fatal(err)
	}
	makeHistory("Another conversation")
	version, err := e.ThreadWorkRevision(ctx, thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	generation, err := e.DataGeneration()
	if err != nil {
		t.Fatal(err)
	}
	if err := setEnabled(false); err != nil {
		t.Fatal(err)
	}
	if _, err := e.DistillThreadWork(ctx, thread.ID, version, generation); !errors.Is(err, ErrWorkDisabled) || calls.Load() != 0 {
		t.Fatalf("disabled extraction reached the model: %v; calls=%d", err, calls.Load())
	}
	if err := setEnabled(true); err != nil {
		t.Fatal(err)
	}
	if count, err := e.DistillThreadWork(ctx, thread.ID, version, generation); err != nil || count != 3 {
		t.Fatalf("extract historical work: count=%d, err=%v", count, err)
	}
	view, err := e.ThreadWorkSnapshot(ctx, thread.ID)
	if err != nil || len(view.Nodes) != 3 || !view.HasOwnWork || view.ThreadRevision != version.ThreadRevision+3 {
		t.Fatalf("missing committed extraction: %+v, err=%v", view, err)
	}
	for _, node := range view.Nodes {
		if node.Kind == "artifact" && node.ArtifactPath != "results/report.md" {
			t.Fatalf("extraction kept an unavailable artifact: %+v", node)
		}
		if node.Scope != store.WorkScopeThread || node.AgentID != workDistiller || node.ProjectID != "" ||
			len(node.Evidence) != 1 || node.Evidence[0].Seq != evidence.Seq || node.Evidence[0].TurnID != evidence.TurnID {
			t.Fatalf("wrong extraction provenance: %+v", node)
		}
	}
	if _, err := e.DistillThreadWork(ctx, thread.ID, view.WorkRevision, generation); !errors.Is(err, store.ErrNuwaConflict) || calls.Load() != 1 {
		t.Fatalf("repeat extraction was not refused: %v; calls=%d", err, calls.Load())
	}

	// Turning the setting off while the model is still responding must
	// discard its output, even though it was admitted when the switch was on.
	late, _ := makeHistory("A durable observation whose extraction was interrupted")
	lateVersion, err := e.ThreadWorkRevision(ctx, late.ID)
	if err != nil {
		t.Fatal(err)
	}
	switchOffDuringModel.Store(true)
	if _, err := e.DistillThreadWork(ctx, late.ID, lateVersion, generation); !errors.Is(err, ErrWorkDisabled) {
		t.Fatalf("late extraction was not refused: %v", err)
	}
	lateView, err := e.ThreadWorkSnapshot(ctx, late.ID)
	if err != nil || len(lateView.Nodes) != 0 || lateView.ThreadRevision != lateVersion.ThreadRevision {
		t.Fatalf("switching off allowed an in-flight write: %+v, err=%v", lateView, err)
	}
}
