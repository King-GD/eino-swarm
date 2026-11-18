package engine

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	swarm "github.com/LubyRuffy/eino-swarm"
	"github.com/LubyRuffy/eino-swarm/internal/config"
	"github.com/LubyRuffy/eino-swarm/internal/provider"
	"github.com/LubyRuffy/eino-swarm/internal/store"
	"gorm.io/gorm"
)

const imageHTTPTestSource = "gateway:image-fixture"

func imageHTTPEngine(t *testing.T, handler http.HandlerFunc) (*Engine, *store.Thread) {
	t.Helper()
	endpoint := httptest.NewServer(handler)
	t.Cleanup(endpoint.Close)
	e := newTestEngineAt(t, t.TempDir())
	e.cfg.Product.Mode = config.ProductModeFOFA
	e.cfg.Swarm.ManagerMaxIterations = 8
	e.cfg.Swarm.AutoTitle = false
	e.cfg.Swarm.ComposerPredictions = false
	e.cfg.Memory.Enabled = false
	e.cfg.Memory.AutoReview = false
	e.cfg.Memory.AutoDream = false
	e.pool = provider.New(e.cfg)
	source, err := provider.NewAccountSource(imageHTTPTestSource, "Image fixture", endpoint.URL+"/client/desktop/v1", "fixture-key", provider.Catalog{
		Names: []string{"chat-fixture", "image-fixture"},
		Types: map[string]string{"chat-fixture": "chat", "image-fixture": "image"},
	})
	if err != nil {
		t.Fatal(err)
	}
	source.SetModelPolicy(true, map[string]string{"conversation": "chat-fixture", "image": "image-fixture"})
	source.SetCapabilities(&provider.SourceCapabilities{Authorized: true, Available: true, Configurable: true,
		Capabilities: map[string]provider.RoleCapability{
			"conversation": {Supported: true, Available: true, Configurable: true, Endpoints: []string{"chat_completions"}},
			"image":        {Supported: true, Available: true, Configurable: true, Endpoints: []string{"images"}},
		}})
	e.pool.SetAccountSource(source)
	th, err := e.CreateThread("Image fixture", imageHTTPTestSource, "")
	if err != nil {
		t.Fatal(err)
	}
	return e, th
}

func generatedPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 3, 2))
	img.Set(1, 1, color.RGBA{R: 40, G: 150, B: 210, A: 255})
	var pngBytes bytes.Buffer
	if err := png.Encode(&pngBytes, img); err != nil {
		t.Fatal(err)
	}
	return pngBytes.Bytes()
}

func serveImageFixture(t *testing.T, w http.ResponseWriter, r *http.Request, prompt string, pixels []byte) {
	t.Helper()
	if r.Method != http.MethodPost || r.URL.Path != "/client/desktop/v1/images/generations" || r.Header.Get("Authorization") != "Bearer fixture-key" {
		t.Errorf("unexpected generation request: %s %s", r.Method, r.URL.Path)
		http.NotFound(w, r)
		return
	}
	var body struct {
		Model  string `json:"model"`
		Prompt string `json:"prompt"`
		N      int    `json:"n"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Model != "image-fixture" || body.Prompt != prompt || body.N != 1 {
		t.Errorf("bad generation body: %+v, %v", body, err)
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = fmt.Fprintf(w, `{"data":[{"b64_json":%q}]}`, base64.StdEncoding.EncodeToString(pixels))
}

func TestImageCommandGeneratesWithoutChatAndReplaysOwnedBytes(t *testing.T) {
	pixels := generatedPNG(t)
	prompt := "夜色中的路灯 /goal a literal suffix"
	started := make(chan struct{})
	release := make(chan struct{})
	var requests atomic.Int32
	e, thread := imageHTTPEngine(t, func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		close(started)
		select {
		case <-release:
			serveImageFixture(t, w, r, prompt, pixels)
		case <-r.Context().Done():
		}
	})
	defer close(release)
	turn, err := e.StartTurn(thread.ID, "／IMAGE"+prompt)
	if err != nil || !turn.ImageCommand {
		t.Fatalf("image command: %+v, %v", turn, err)
	}
	waitImageFixture(t, started)
	stage, err := e.store.LatestImageGeneration(t.Context(), thread.ID, turn.ID)
	if err != nil || stage == nil || stage.Text != "running" || stage.ToolCallID != turn.ID {
		t.Fatalf("pending receipt: %+v, %v", stage, err)
	}
	if _, err := e.StartTurn(thread.ID, "/image second"); !errors.Is(err, ErrBusy) {
		t.Fatalf("second image: %v", err)
	}
	if err := e.Steer(thread.ID, "/image no"); !errors.Is(err, ErrBusy) {
		t.Fatalf("steer image: %v", err)
	}
	if err := e.Steer(thread.ID, "ordinary steer"); !errors.Is(err, ErrBusy) {
		t.Fatalf("steer into dedicated generation: %v", err)
	}
	if _, err := e.EnqueueFollowup(thread.ID, "/image no queue"); !errors.Is(err, ErrBusy) {
		t.Fatalf("queue image: %v", err)
	}
	if _, err := e.RequeueFollowup(thread.ID, "unused", "/image no"); !errors.Is(err, ErrBusy) {
		t.Fatalf("requeue image: %v", err)
	}
	release <- struct{}{}
	if got := waitForTurn(t, e, turn.ID); got.Status != store.TurnDone {
		t.Fatalf("finished turn: %+v", got)
	}
	assertGeneratedImage(t, e, thread.ID, turn.ID, pixels)
	if requests.Load() != 1 {
		t.Fatalf("sent %d inference requests", requests.Load())
	}
	if err := e.DeleteThread(thread.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(e.generatedImagesDir(thread.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("deleted conversation left its media behind: %v", err)
	}
}

func assertGeneratedImage(t *testing.T, e *Engine, threadID, turnID string, pixels []byte) {
	t.Helper()
	replayed, err := e.Replay(threadID, 0)
	if err != nil {
		t.Fatal(err)
	}
	var stages []string
	var ref store.ImageRef
	var callID string
	for _, event := range replayed {
		if event.TurnID != turnID || event.Kind != KindImageGeneration {
			continue
		}
		stages = append(stages, event.Text)
		if callID == "" {
			callID = event.ToolCallID
		}
		if callID == "" || event.ToolCallID != callID || event.Seq <= 0 || strings.Contains(event.Text, base64.StdEncoding.EncodeToString(pixels)) {
			t.Fatalf("unbound or invalid image event: %+v", event)
		}
		if event.Text == "completed" && len(event.Images) == 1 {
			ref = event.Images[0]
		}
	}
	if strings.Join(stages, ",") != "running,completed" || ref.Width != 3 || ref.Height != 2 || ref.MIME != "image/png" {
		t.Fatalf("replay: %v, %+v", stages, ref)
	}
	file, mime, err := e.OpenGeneratedImage(threadID, ref.ID)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(file)
	if closeErr := file.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if err != nil || mime != ref.MIME || !bytes.Equal(got, pixels) {
		t.Fatalf("stored pixels: %q, matches? %v, %v", mime, bytes.Equal(got, pixels), err)
	}
	if _, _, err := e.OpenGeneratedImage("unrelated", ref.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("cross-conversation image: %v", err)
	}
	if _, _, err := e.OpenGeneratedImage(threadID, "../"+ref.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("path traversal: %v", err)
	}
}

func TestManagerGeneratesOneImageEvenWithTwoToolCalls(t *testing.T) {
	pixels := generatedPNG(t)
	var images, models atomic.Int32
	e, thread := imageHTTPEngine(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/images/generations") {
			images.Add(1)
			serveImageFixture(t, w, r, "a lighthouse in the fog", pixels)
			return
		}
		var request scheduleCompletionRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("read model request: %v", err)
			http.Error(w, "bad fixture input", 400)
			return
		}
		if len(request.Messages) > 0 && replyNoScheduleClaimsFixture(t, w, request.Messages[0].Content, request.Stream, len(request.Tools)) {
			return
		}
		switch models.Add(1) {
		case 1:
			if !requestHasTool(request, generateImageToolName) {
				t.Error("the available generation tool was omitted from the real model request")
			}
			var calls []any
			for i := range 2 {
				calls = append(calls, map[string]any{"index": i, "id": fmt.Sprintf("image-call-%d", i), "type": "function",
					"function": map[string]any{"name": generateImageToolName, "arguments": `{"prompt":"a lighthouse in the fog"}`}})
			}
			writeContextFlowReply(w, request.Stream, map[string]any{"tool_calls": calls}, "tool_calls")
		case 2:
			var saved, limited bool
			for _, msg := range request.Messages {
				saved = saved || msg.Content == imageToolResult(nil)
				limited = limited || strings.Contains(msg.Content, "image_generation_already_attempted")
				if strings.Contains(msg.Content, base64.StdEncoding.EncodeToString(pixels)) {
					t.Error("pixels entered the model context")
				}
			}
			if !saved || !limited {
				t.Errorf("tool receipts missing: saved=%v limited=%v", saved, limited)
			}
			writeContextFlowReply(w, request.Stream, map[string]any{"content": "Here is the image."}, "stop")
		default:
			t.Error("unexpected extra chat request")
			writeContextFlowReply(w, request.Stream, map[string]any{"content": "The image already exists."}, "stop")
		}
	})
	turn, err := e.StartTurn(thread.ID, "Please generate a picture of a lighthouse.")
	if err != nil {
		t.Fatal(err)
	}
	if got := waitForTurn(t, e, turn.ID); got.Status != store.TurnDone {
		t.Fatalf("finished turn: %+v", got)
	}
	assertGeneratedImage(t, e, thread.ID, turn.ID, pixels)
	if images.Load() != 1 || models.Load() != 2 {
		t.Fatalf("generation count=%d model count=%d", images.Load(), models.Load())
	}
}

func TestImageGenerationNeverRepostsDuringRestore(t *testing.T) {
	for _, stop := range []bool{false, true} {
		t.Run(fmt.Sprintf("stop=%v", stop), func(t *testing.T) {
			started := make(chan struct{})
			cancelled := make(chan struct{})
			var requests atomic.Int32
			e, thread := imageHTTPEngine(t, func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if strings.HasSuffix(r.URL.Path, "/images/generations") {
					// Consume the request so the real HTTP server observes client cancellation.
					var request map[string]any
					if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
						t.Error(err)
					}
					close(started)
					select {
					case <-r.Context().Done():
						close(cancelled)
					case <-time.After(5 * time.Second):
						t.Error("image request stayed live after its turn was interrupted")
					}
				}
			})
			turn, err := e.StartTurn(thread.ID, "/image a lighthouse")
			if err != nil {
				t.Fatal(err)
			}
			waitImageFixture(t, started)
			if stop {
				if err := e.Interrupt(thread.ID); err != nil {
					t.Fatal(err)
				}
				if got := waitForTurn(t, e, turn.ID); got.Status != store.TurnCancelled {
					t.Errorf("Stop left incorrect turn state: %+v", got)
				}
			}
			e.Shutdown()
			waitImageFixture(t, cancelled)
			successor := New(e.cfg, e.store, e.pool, e.log)
			t.Cleanup(successor.Shutdown)
			if n, err := successor.ResumeOrphanedTurns(); n != 0 || err != nil {
				t.Fatalf("restoring image should settle without starting work: count=%d err=%v", n, err)
			}
			got, err := e.store.LatestImageGeneration(t.Context(), thread.ID, turn.ID)
			want := "unknown"
			if stop {
				want = "cancelled"
			}
			if err != nil || got == nil || got.Text != want || len(got.Images) != 0 || requests.Load() != 1 {
				t.Fatalf("restore receipt: %+v, %v, requests=%d", got, err, requests.Load())
			}
			if _, err := e.RetryFailedTurn(thread.ID, turn.ID); !errors.Is(err, ErrNoFailedTurn) {
				t.Fatalf("retry could replay a potentially billed command: %v", err)
			}
		})
	}
}

func TestImageCommandRejectsInvalidDraftAndReportsImageStorageFailure(t *testing.T) {
	pixels := generatedPNG(t)
	var requests atomic.Int32
	e, thread := imageHTTPEngine(t, func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		serveImageFixture(t, w, r, "a cloud", pixels)
	})
	for _, tc := range []struct {
		in   UserInput
		want error
	}{
		{UserInput{Text: "/image"}, ErrImagePrompt},
		{UserInput{Text: "/image a cloud", Images: []ImageInput{{}}}, ErrImageTextOnly},
		{UserInput{Text: "/image a cloud", Files: []string{"x.txt"}}, ErrImageTextOnly},
		{UserInput{Text: "/image a cloud", HistoryReport: true}, ErrImageReadOnly},
	} {
		if _, err := e.StartTurnInput(thread.ID, tc.in); !errors.Is(err, tc.want) {
			t.Errorf("invalid draft returned %v, want %v", err, tc.want)
		}
	}
	if err := e.SetPlanMode(thread.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := e.StartTurn(thread.ID, "/image a cloud"); !errors.Is(err, ErrImageReadOnly) {
		t.Errorf("read-only plan admitted generation: %v", err)
	}
	if err := e.SetPlanMode(thread.ID, false); err != nil {
		t.Fatal(err)
	}

	if err := os.MkdirAll(e.cfg.ThreadInputsDir(thread.ID), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(e.generatedImagesDir(thread.ID), []byte("storage fault"), 0o600); err != nil {
		t.Fatal(err)
	}
	turn, err := e.StartTurn(thread.ID, "/image a cloud")
	if err != nil {
		t.Fatal(err)
	}
	if got := waitForTurn(t, e, turn.ID); got.Status != store.TurnError || got.Error != errImageSave.Error() {
		t.Fatalf("storage failure claimed a valid response: %+v", got)
	}
	stage, err := e.store.LatestImageGeneration(t.Context(), thread.ID, turn.ID)
	if err != nil || stage == nil || stage.Text != "failed" || stage.Err != errImageSave.Error() || len(stage.Images) != 0 {
		t.Fatalf("storage failure receipt: %+v, %v", stage, err)
	}
	e.pool.SetAccountSource(nil)
	if _, err := e.StartTurn(thread.ID, "/image a cloud"); !errors.Is(err, ErrImageUnavailable) || requests.Load() != 1 {
		t.Fatalf("revoked generation: %v, requests=%d", err, requests.Load())
	}
}

func waitImageFixture(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal("image request did not reach the expected boundary")
	}
}

func TestImageCommandHandsOffAnUncertainGatewayRequest(t *testing.T) {
	started := make(chan struct{})
	var calls atomic.Int32
	e, thread := imageHTTPEngine(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		close(started)
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
			t.Error("handoff did not cancel the owned gateway call")
		}
	})
	turn, err := e.StartTurn(thread.ID, "/image a bridge")
	if err != nil {
		t.Fatal(err)
	}
	waitImageFixture(t, started)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	status, err := e.PrepareRestart(ctx)
	if err != nil || !status.Ready {
		t.Fatalf("the image operation did not checkpoint: %+v, %v", status, err)
	}
	e.Shutdown()
	successor := New(e.cfg, e.store, e.pool, e.log)
	t.Cleanup(successor.Shutdown)
	if n, err := successor.ResumeOrphanedTurns(); n != 0 || err != nil {
		t.Fatalf("handoff resumed an external image effect: %d, %v", n, err)
	}
	stage, err := e.store.LatestImageGeneration(ctx, thread.ID, turn.ID)
	if err != nil || stage == nil || stage.Text != "unknown" || calls.Load() != 1 {
		t.Fatalf("handoff receipt: %+v, %v, gateway calls=%d", stage, err, calls.Load())
	}
}

func TestImageRestoreRecognizesACommittedImageBeforeTurnFinalization(t *testing.T) {
	pixels := generatedPNG(t)
	e, thread := imageHTTPEngine(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("restore called the gateway after a committed image")
		http.Error(w, "unexpected request", 500)
	})
	turn := &store.Turn{ThreadID: thread.ID, UserText: "/image a bridge", ImageCommand: true}
	if err := e.store.CreateTurn(turn); err != nil {
		t.Fatal(err)
	}
	if err := e.recordImageEvent(t.Context(), imageStage(turn, turn.ID, "running", "")); err != nil {
		t.Fatal(err)
	}
	_, err := e.commitGeneratedImage(t.Context(), imageStage(turn, turn.ID, "running", ""),
		provider.GeneratedImage{Data: pixels, MIME: "image/png", Width: 3, Height: 2})
	if err != nil {
		t.Fatal(err)
	}
	if n, err := e.ResumeOrphanedTurns(); n != 0 || err != nil {
		t.Fatalf("committed image reconciliation: %d, %v", n, err)
	}
	if row, err := e.store.GetTurn(turn.ID); err != nil || row.Status != store.TurnDone {
		t.Fatalf("committed image was not recovered as successful: %+v, %v", row, err)
	}
	assertGeneratedImage(t, e, thread.ID, turn.ID, pixels)
}

func TestImagePublishFailureRemovesTheUnreferencedBytes(t *testing.T) {
	pixels := generatedPNG(t)
	e, thread := imageHTTPEngine(t, func(w http.ResponseWriter, r *http.Request) {
		serveImageFixture(t, w, r, "a bridge", pixels)
	})
	db := e.store.DB()
	if err := db.Callback().Create().Before("gorm:create").Register("refuse_image_publish", func(tx *gorm.DB) {
		if event, ok := tx.Statement.Dest.(*store.Event); ok && event.Kind == KindImageGeneration && event.Text == "completed" {
			_ = tx.AddError(errors.New("fixture completion write failure"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	turn, err := e.StartTurn(thread.ID, "/image a bridge")
	if err != nil {
		t.Fatal(err)
	}
	if got := waitForTurn(t, e, turn.ID); got.Status != store.TurnError || got.Error != errImageSave.Error() {
		t.Fatalf("write failure claimed success: %+v", got)
	}
	entries, err := os.ReadDir(e.generatedImagesDir(thread.ID))
	if err != nil || len(entries) != 0 {
		t.Fatalf("unreferenced image bytes survived: %v, %v", entries, err)
	}
}

func TestResumedManagerReceivesUnknownImageReceiptWithoutAnotherPost(t *testing.T) {
	started := make(chan struct{})
	var requests atomic.Int32
	e, th := imageHTTPEngine(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/images/generations") {
			if requests.Add(1) != 1 {
				t.Error("restoration reissued the generation request")
				http.Error(w, "unexpected generation request", 500)
				return
			}
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body["prompt"] != "A spruce forest" {
				t.Errorf("read image request %v: %v", body, err)
			}
			close(started)
			select {
			case <-r.Context().Done():
			case <-time.After(7 * time.Second):
				t.Error("handoff did not cancel the image request")
			}
			return
		}
		var req scheduleCompletionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			http.Error(w, "invalid fixture request", 400)
			return
		}
		if len(req.Messages) > 0 && replyNoScheduleClaimsFixture(t, w, req.Messages[0].Content, req.Stream, len(req.Tools)) {
			return
		}
		if requests.Load() > 0 {
			var unknown bool
			for _, msg := range req.Messages {
				unknown = unknown || msg.Content == swarm.HandoffUnknownToolResult
			}
			if !unknown || requestHasTool(req, generateImageToolName) {
				t.Errorf("resumed agent must receive its unknown receipt with the attempted operation unavailable: unknown=%v tools=%d", unknown, len(req.Tools))
			}
			writeContextFlowReply(w, req.Stream, map[string]any{"content": "I could not confirm the image."}, "stop")
			return
		}
		calls := []any{map[string]any{"index": 0, "id": "image-handoff-call", "type": "function",
			"function": map[string]any{"name": generateImageToolName, "arguments": `{"prompt":"A spruce forest"}`}}}
		writeContextFlowReply(w, req.Stream, map[string]any{"tool_calls": calls}, "tool_calls")
	})
	turn, err := e.StartTurn(th.ID, "Generate a picture.")
	if err != nil {
		t.Fatal(err)
	}
	waitImageFixture(t, started)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if status, err := e.PrepareRestart(ctx); err != nil || !status.Ready {
		t.Fatalf("could not checkpoint image tool: %+v %v", status, err)
	}
	e.Shutdown()
	successor := New(e.cfg, e.store, e.pool, e.log)
	t.Cleanup(successor.Shutdown)
	if n, err := successor.ResumeOrphanedTurns(); err != nil || n != 1 {
		t.Fatalf("resume manager: count=%d err=%v", n, err)
	}
	if row := waitForTurn(t, successor, turn.ID); row.Status != store.TurnDone || requests.Load() != 1 {
		t.Fatalf("resumed conversation status=%s requests=%d", row.Status, requests.Load())
	}
	stage, err := successor.Store().LatestImageGeneration(t.Context(), th.ID, turn.ID)
	if err != nil || stage == nil || stage.Text != "unknown" || stage.ToolCallID != "image-handoff-call" || len(stage.Images) != 0 {
		t.Fatalf("image receipt after handoff: %+v %v", stage, err)
	}
}

func TestGeneratedImageReadRefusesMissingAndReplacedFiles(t *testing.T) {
	e := newTestEngineAt(t, t.TempDir())
	th, err := e.CreateThread("Media read", "", "")
	if err != nil {
		t.Fatal(err)
	}
	turn := &store.Turn{ThreadID: th.ID}
	if err := e.store.CreateTurn(turn); err != nil {
		t.Fatal(err)
	}
	pixels := generatedPNG(t)
	ref, err := e.commitGeneratedImage(t.Context(), imageStage(turn, "read", "running", ""),
		provider.GeneratedImage{Data: pixels, MIME: "image/png", Width: 3, Height: 2})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(e.generatedImagesDir(th.ID), ref.ID)
	refused := func() {
		t.Helper()
		if file, _, err := e.OpenGeneratedImage(th.ID, ref.ID); !errors.Is(err, store.ErrNotFound) {
			if file != nil {
				_ = file.Close()
			}
			t.Fatalf("stale media reference was not refused: %v", err)
		}
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	refused()
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	refused()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("<html>"), 0o600); err != nil {
		t.Fatal(err)
	}
	refused()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(e.generatedImagesDir(th.ID)); err != nil {
		t.Fatal(err)
	}
	refused()
}

func TestImageFailureStagesPreserveGatewayReason(t *testing.T) {
	for _, tc := range []struct {
		status int
		body   string
		code   string
	}{
		{http.StatusForbidden, "private cause", "image_generation_access_denied"},
		{http.StatusTooManyRequests, "private cause", "image_generation_failed"},
		{http.StatusOK, `{"data":[{"b64_json":"not a picture"}]}`, "image_generation_invalid_result"},
	} {
		t.Run(tc.code, func(t *testing.T) {
			var calls atomic.Int32
			e, thread := imageHTTPEngine(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			})
			turn, err := e.StartTurn(thread.ID, "/image Sample composition")
			if err != nil {
				t.Fatal(err)
			}
			if row := waitForTurn(t, e, turn.ID); row.Status != store.TurnError || row.Error != tc.code {
				t.Fatalf("image error: %+v", row)
			}
			stage, err := e.store.LatestImageGeneration(t.Context(), thread.ID, turn.ID)
			if err != nil || stage == nil || stage.Text != "failed" || stage.Err != tc.code || len(stage.Images) != 0 || calls.Load() != 1 {
				t.Fatalf("image gateway failure: %+v %v, calls=%d", stage, err, calls.Load())
			}
		})
	}
}

func TestImageCommandRequiresADurableAttemptBeforeContactingGateway(t *testing.T) {
	e, thread := imageHTTPEngine(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("made an untracked paid request")
		http.Error(w, "unexpected generation request", 500)
	})
	if err := e.store.DB().Callback().Create().Before("gorm:create").Register("refuse_image_admission", func(tx *gorm.DB) {
		if event, ok := tx.Statement.Dest.(*store.Event); ok && event.Kind == KindImageGeneration && event.Text == "running" {
			_ = tx.AddError(errors.New("fixture admission write failure"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	turn, err := e.StartTurn(thread.ID, "/image Sample composition")
	if err != nil {
		t.Fatal(err)
	}
	if row := waitForTurn(t, e, turn.ID); row.Status != store.TurnError || row.Error != errImageSave.Error() {
		t.Fatalf("image admission failure: %+v", row)
	}
	if stage, err := e.store.LatestImageGeneration(t.Context(), thread.ID, turn.ID); err != nil || stage != nil {
		t.Fatalf("unexpected image stage: %+v %v", stage, err)
	}
}
