package engine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	swarm "github.com/LubyRuffy/eino-swarm"
	"github.com/LubyRuffy/eino-swarm/internal/provider"
	"github.com/LubyRuffy/eino-swarm/internal/store"
)

func TestRecoveredImageToolReceiptUsesOnlyItsOwnAttempt(t *testing.T) {
	const callID = "image-call"
	const turnID = "turn-fixture"
	call := orphanedToolCall{AgentID: swarm.DefaultManagerID, ToolCallID: callID,
		Text: `generate_image({"prompt":"fixture"})`}
	ref := store.ImageRef{ID: "img_1", MIME: "image/png"}
	for _, tc := range []struct {
		name   string
		stage  store.Event
		result string
		code   string
	}{
		{"missing", store.Event{}, swarm.HandoffUnknownToolResult, errImageUnknown.Error()},
		{"another call", store.Event{ToolCallID: "wrong", Text: "completed", Images: []store.ImageRef{ref}}, swarm.HandoffUnknownToolResult, errImageUnknown.Error()},
		{"running", store.Event{ToolCallID: callID, Text: "running"}, swarm.HandoffUnknownToolResult, errImageUnknown.Error()},
		{"completed", store.Event{ToolCallID: callID, Text: "completed", Images: []store.ImageRef{ref}}, imageToolResult(nil), ""},
		{"denied", store.Event{ToolCallID: callID, Text: "failed", Err: "image_generation_access_denied"}, imageToolFailure("image_generation_access_denied"), "image_generation_access_denied"},
		{"incomplete failure", store.Event{ToolCallID: callID, Text: "failed"}, imageToolFailure(errImageUnknown.Error()), errImageUnknown.Error()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var events []store.Event
			if tc.stage.ToolCallID != "" {
				tc.stage.Kind, tc.stage.TurnID = KindImageGeneration, turnID
				events = append(events, tc.stage)
			}
			result, code, handled := orphanedImageToolResult(turnID, call, events)
			if !handled || result != tc.result || code != tc.code {
				t.Fatalf("tool recovery: %v %q %q", handled, result, code)
			}
		})
	}
	call.AgentID = "unrelated-worker"
	if _, _, handled := orphanedImageToolResult(turnID, call, nil); handled {
		t.Fatal("claimed a worker's similarly named tool")
	}
}

func TestImageAdmissionStopsBeforeWritingForCancelledOrUnownedTurn(t *testing.T) {
	e := newTestEngineAt(t, t.TempDir())
	thread, err := e.CreateThread("Image admission", "", "")
	if err != nil {
		t.Fatal(err)
	}
	turn := &store.Turn{ThreadID: thread.ID}
	if err := e.store.CreateTurn(turn); err != nil {
		t.Fatal(err)
	}
	rt := e.runtimeFor(thread.ID)
	if err := rt.claimImageGeneration(t.Context(), turn, "unowned"); !errors.Is(err, context.Canceled) {
		t.Fatalf("inactive image turn claimed a gateway request: %v", err)
	}
	ctx, cancel := context.WithCancelCause(t.Context())
	cancel(context.DeadlineExceeded)
	if err := rt.claimImageGeneration(ctx, turn, "cancelled"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancelled image request lost its cause: %v", err)
	}
	if event, err := e.store.LatestImageGeneration(t.Context(), thread.ID, turn.ID); event != nil || err != nil {
		t.Fatalf("refused image request wrote an attempt: %+v %v", event, err)
	}
}

func TestImageTimeoutAndPromptErrorsUseStableCodes(t *testing.T) {
	for _, tc := range []struct {
		err  error
		code string
	}{
		{context.DeadlineExceeded, "image_generation_timeout"},
		{ErrImagePrompt, "image_generation_invalid_prompt"},
		{ErrImageReadOnly, "image_generation_read_only"},
		{ErrImageUnavailable, "image_generation_unavailable"},
		{errImageUnknown, "image_generation_unknown"},
	} {
		if got := imageErrorCode(tc.err); got != tc.code {
			t.Errorf("image error %v: %q, want %q", tc.err, got, tc.code)
		}
	}
}

func TestImageRestoreRetainsStopWithoutReplayingTheAttempt(t *testing.T) {
	e := newTestEngineAt(t, t.TempDir())
	thread, err := e.CreateThread("Stopped drawing", "", "")
	if err != nil {
		t.Fatal(err)
	}
	turn := &store.Turn{ThreadID: thread.ID, ImageCommand: true}
	if err := e.store.CreateTurn(turn); err != nil {
		t.Fatal(err)
	}
	if err := e.recordImageEvent(t.Context(), imageStage(turn, turn.ID, "running", "")); err != nil {
		t.Fatal(err)
	}
	settled, err := e.restoreImageGeneration(t.Context(), turn, true, nil)
	if err != nil || !settled {
		t.Fatalf("restore did not settle the stopped turn: %v %v", settled, err)
	}
	row, err := e.store.GetTurn(turn.ID)
	if err != nil || row.Status != store.TurnCancelled {
		t.Fatalf("stopped turn outcome: %+v %v", row, err)
	}
	stage, err := e.store.LatestImageGeneration(t.Context(), thread.ID, turn.ID)
	if err != nil || stage == nil || stage.Text != "cancelled" || stage.ToolCallID != turn.ID {
		t.Fatalf("stopped image receipt: %+v %v", stage, err)
	}
}

func TestImageWriteRefusesDeletedConversationAndDoesNotLeaveTemporaryFiles(t *testing.T) {
	e := newTestEngineAt(t, t.TempDir())
	thread, err := e.CreateThread("Deleted while generating", "", "")
	if err != nil {
		t.Fatal(err)
	}
	turn := &store.Turn{ThreadID: thread.ID}
	if err := e.store.CreateTurn(turn); err != nil {
		t.Fatal(err)
	}
	stage := imageStage(turn, "call", "running", "")
	pixels := provider.GeneratedImage{Data: generatedPNG(t), MIME: "image/png", Width: 3, Height: 2}
	unsupported := pixels
	unsupported.MIME = "image/svg+xml"
	if _, err := e.commitGeneratedImage(t.Context(), stage, unsupported); err == nil {
		t.Fatal("accepted an unsupported media type for inline display")
	}
	if err := e.DeleteThread(thread.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.commitGeneratedImage(t.Context(), stage, pixels); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("late completion did not refuse deleted owner: %v", err)
	}
	if _, err := os.Stat(e.generatedImagesDir(thread.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("late completion recreated the media directory: %v", err)
	}

	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "img_1"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := writeGeneratedImage(dir, "img_1", pixels.Data); err == nil {
		t.Fatal("renamed a staged image over an existing directory")
	}
	list, err := os.ReadDir(dir)
	if err != nil || len(list) != 1 || !list[0].IsDir() {
		t.Fatalf("failed write left temporary image data: %+v %v", list, err)
	}
}
