package server_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"log/slog"
	"math/rand/v2"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/LubyRuffy/eino-swarm/frontend"
	"github.com/LubyRuffy/eino-swarm/internal/config"
	"github.com/LubyRuffy/eino-swarm/internal/engine"
	"github.com/LubyRuffy/eino-swarm/internal/provider"
	"github.com/LubyRuffy/eino-swarm/internal/remote"
	"github.com/LubyRuffy/eino-swarm/internal/server"
	"github.com/LubyRuffy/eino-swarm/internal/store"
)

const imageFixtureSource = "gateway:fixture-images"

func imageFixturePNG(t *testing.T) []byte {
	t.Helper()
	picture := image.NewNRGBA(image.Rect(0, 0, 168, 117))
	rng := rand.New(rand.NewPCG(4, 77))
	for y := 0; y < picture.Bounds().Dy(); y++ {
		for x := 0; x < picture.Bounds().Dx(); x++ {
			picture.SetNRGBA(x, y, color.NRGBA{R: uint8(rng.Uint32()), G: uint8(rng.Uint32()), B: uint8(rng.Uint32()), A: 255})
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, picture); err != nil {
		t.Fatal(err)
	}
	return encoded.Bytes()
}

func newImageFixture(t *testing.T, dataDir string, pixels []byte, delay time.Duration) (*engine.Engine, *server.Server) {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/client/desktop/v1/images/generations" ||
			r.Header.Get("Authorization") != "Bearer fixture-image-token" {
			t.Errorf("unexpected image gateway request %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		var input struct{ Model, Prompt string }
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil || input.Model != "drawing-fixture" || input.Prompt == "" {
			t.Errorf("image gateway request: model %q, empty prompt %v, error %v", input.Model, input.Prompt == "", err)
			http.Error(w, "invalid image request", http.StatusBadRequest)
			return
		}
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-r.Context().Done():
			return
		case <-timer.C:
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]string{"b64_json": base64.StdEncoding.EncodeToString(pixels)}}})
	}))
	t.Cleanup(upstream.Close)
	cfg, err := config.Load(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Update(func(c *config.Config) error {
		c.Product.Mode = config.ProductModeLocal
		c.UI.WelcomeDismissed = true
		c.Models.Bindings = map[string]map[string]config.ModelRef{
			config.ProductModeLocal: {"conversation": {ProviderID: imageFixtureSource, Model: "dialog-fixture"}},
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(cfg.DBPath())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	source, err := provider.NewAccountSource(imageFixtureSource, "Fixture gateway", upstream.URL+"/client/desktop/v1", "fixture-image-token", provider.Catalog{
		Names: []string{"dialog-fixture", "drawing-fixture"}, Types: map[string]string{"dialog-fixture": "chat", "drawing-fixture": "image"},
	})
	if err != nil {
		t.Fatal(err)
	}
	source.SetModelPolicy(true, map[string]string{"conversation": "dialog-fixture", "image": "drawing-fixture"})
	source.SetCapabilities(&provider.SourceCapabilities{Authorized: true, Available: true, Configurable: true,
		Capabilities: map[string]provider.RoleCapability{
			"conversation": {Available: true, Supported: true, Configurable: true, Endpoints: []string{"chat_completions"}},
			"image":        {Available: true, Supported: true, Configurable: true, Endpoints: []string{"images"}},
		}})
	pool := provider.New(cfg)
	pool.SetLocalAccountSources([]*provider.AccountSource{source})
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	eng := engine.New(cfg, st, pool, log)
	t.Cleanup(eng.Shutdown)
	srv, err := server.New(server.Options{Engine: eng, Logger: log, Assets: frontend.Assets(), Mode: server.ModeWeb, Version: "image-fixture"})
	if err != nil {
		t.Fatal(err)
	}
	return eng, srv
}

func TestGeneratedImageServesOriginalBytesOverHTTPAndPairedChunks(t *testing.T) {
	pixels := imageFixturePNG(t)
	eng, srv := newImageFixture(t, t.TempDir(), pixels, 0)
	httpServer := httptest.NewServer(srv.Handler())
	defer httpServer.Close()
	thread, err := eng.CreateThread("drawing", "", "")
	if err != nil {
		t.Fatal(err)
	}
	other, err := eng.CreateThread("other thread", "", "")
	if err != nil {
		t.Fatal(err)
	}
	post := func(text string, expected int) {
		t.Helper()
		req, err := json.Marshal(map[string]string{"text": text})
		if err != nil {
			t.Fatal(err)
		}
		resp, err := http.Post(httpServer.URL+"/api/threads/"+thread.ID+"/steer", "application/json", bytes.NewReader(req))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != expected {
			got, _ := io.ReadAll(resp.Body)
			t.Fatalf("start request status %d: %s", resp.StatusCode, got)
		}
	}
	post("/image", http.StatusBadRequest)
	post("/image A geometric picture", http.StatusAccepted)
	var completed store.Event
	deadline := time.Now().Add(5 * time.Second)
	for completed.Seq == 0 && time.Now().Before(deadline) {
		events, err := eng.Replay(thread.ID, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, event := range events {
			if event.Kind == "image_generation" && event.Text == "completed" {
				completed = event
			}
		}
		if completed.Seq == 0 {
			time.Sleep(10 * time.Millisecond)
		}
	}
	if len(completed.Images) != 1 {
		t.Fatalf("no durable result image: %+v", completed)
	}
	media := completed.Images[0]
	base := httpServer.URL + "/api/threads/"
	for _, id := range []string{thread.ID, other.ID, "thread-that-does-not-exist"} {
		response, err := http.Get(base + id + "/generated-images/" + media.ID)
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if id == thread.ID {
			if response.StatusCode != 200 || response.Header.Get("Content-Type") != "image/png" ||
				response.Header.Get("X-Content-Type-Options") != "nosniff" || !bytes.Equal(data, pixels) {
				t.Fatalf("image response: %d %v equal=%v", response.StatusCode, response.Header, bytes.Equal(data, pixels))
			}
		} else if response.StatusCode != 404 {
			t.Fatalf("cross-thread image returned %d", response.StatusCode)
		}
	}

	cfg := config.RemoteConfig{}
	history := remote.Handle(eng, cfg, remote.Request{Op: remote.OpLog, ThreadID: thread.ID, Before: completed.Seq + 1}, "test", "fixture")
	var found bool
	for _, event := range history.Events {
		if event.Seq == completed.Seq {
			found = event.GeneratedImage != nil && *event.GeneratedImage == media && event.ToolCallID == completed.ToolCallID
		}
	}
	if !found {
		t.Fatalf("paired history did not expose the same media reference: %+v", history)
	}
	var collected []byte
	var frames int
	for {
		result := remote.Handle(eng, cfg, remote.Request{Op: remote.OpGeneratedImageChunk, ThreadID: thread.ID, ImageID: media.ID, Before: int64(len(collected))}, "test", "fixture")
		chunk := result.FileChunk
		if !result.OK || chunk == nil || chunk.MIME != media.MIME || chunk.Offset != int64(len(collected)) {
			t.Fatalf("paired image chunk: %+v", result)
		}
		wire, err := json.Marshal(result)
		if err != nil || len(wire) >= remote.MaxPushPayload {
			t.Fatalf("image chunk does not fit a sealed frame: %d %v", len(wire), err)
		}
		part, err := base64.StdEncoding.DecodeString(chunk.Data)
		if err != nil || len(part) == 0 {
			t.Fatalf("invalid image chunk: %v", err)
		}
		collected = append(collected, part...)
		frames++
		if chunk.NextOffset != int64(len(collected)) {
			t.Fatalf("image chunk offset: %+v", chunk)
		}
		if !chunk.More {
			break
		}
	}
	if frames < 2 || !bytes.Equal(collected, pixels) {
		t.Fatalf("paired chunks changed the image: frames=%d equal=%v", frames, bytes.Equal(collected, pixels))
	}
	if size, err := png.DecodeConfig(bytes.NewReader(collected)); err != nil || size.Width != media.Width || size.Height != media.Height {
		t.Fatalf("paired dimensions: %v %v", size, err)
	}
	for _, check := range []struct {
		threadID, imageID, code string
		offset                  int64
	}{
		{other.ID, media.ID, "not_found", 0},
		{thread.ID, "../outside", "not_found", 0},
		{thread.ID, media.ID, "bad_request", -1},
		{thread.ID, media.ID, "bad_request", int64(len(pixels)) + 1},
	} {
		reply := remote.Handle(eng, cfg, remote.Request{Op: remote.OpGeneratedImageChunk, ThreadID: check.threadID, ImageID: check.imageID, Before: check.offset}, "test", "fixture")
		if reply.OK || reply.Code != check.code {
			t.Fatalf("wrong refusal for %+v: %+v", check, reply)
		}
	}
}

// Only the opt-in Playwright worker opens this loopback server. Its store and
// gateway fixture both belong to the run, and the app makes real HTTP requests.
func TestImageGenerationBrowserFixture(t *testing.T) {
	address := os.Getenv("ZWAI_IMAGE_BROWSER_FIXTURE")
	if address == "" {
		t.Skip("browser image fixture is not requested")
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil || host != "127.0.0.1" {
		t.Fatal("browser image fixture requires a loopback address")
	}
	if frontend.Assets() == nil {
		t.Fatal("build the frontend before running the image browser fixture")
	}
	_, srv := newImageFixture(t, t.TempDir(), imageFixturePNG(t), 1200*time.Millisecond)
	listener, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	httpServer := &http.Server{Handler: srv.Handler(), ReadHeaderTimeout: 5 * time.Second}
	t.Cleanup(func() { _ = httpServer.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	go func() {
		<-ctx.Done()
		_ = httpServer.Close()
	}()
	t.Logf("image browser fixture at %s", strings.TrimSpace(address))
	if err := httpServer.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		t.Fatal(err)
	}
}
