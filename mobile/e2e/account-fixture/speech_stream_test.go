package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"flag"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LubyRuffy/eino-swarm/internal/config"
	"github.com/LubyRuffy/eino-swarm/internal/engine"
	"github.com/LubyRuffy/eino-swarm/internal/provider"
	"github.com/LubyRuffy/eino-swarm/internal/speech"
	"github.com/LubyRuffy/eino-swarm/internal/store"
)

const speechInput = `{"model":"speech","input":"Read the completed response.","voice":"native-fixture-voice","response_format":"pcm","stream_format":"sse"}`

func realSpeechFixture(t *testing.T) *fixture {
	t.Helper()
	t.Setenv("OPENAI_BASE_URL", "")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("OPENAI_MODEL", "")
	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(cfg.DBPath())
	if err != nil {
		t.Fatal(err)
	}
	eng := engine.New(cfg, db, provider.New(cfg), slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(func() { eng.Shutdown(); db.Close() })
	return &fixture{engine: eng, counts: map[string]int{}}
}

func TestNativeSpeechSetupProjectsRealPCGatewayPCMWithoutLeakingDesktopCredential(t *testing.T) {
	f := realSpeechFixture(t)
	server := httptest.NewServer(f)
	t.Cleanup(server.Close)
	f.origin = server.URL
	for attempt := 0; attempt < 2; attempt++ {
		if err := f.setupSpeech(t.Context()); err != nil {
			t.Fatalf("opt-in setup attempt %d: %v", attempt, err)
		}
	}
	view := speech.New(f.engine.Config(), f.engine.Providers()).Snapshot()
	if view.Execution != "gateway" || view.GatewayOrigin != server.URL || !view.Output.Ready || view.Output.Model != "speech" || view.Output.Format != "pcm" || view.Output.SourceID != fixtureModelSourceID {
		t.Fatalf("real speech_config projection: %+v", view)
	}
	raw, _ := json.Marshal(view)
	if bytes.Contains(raw, []byte(desktopSpeechToken)) {
		t.Fatal("PC speech_config leaked its credential to the phone")
	}
	webRequest(t, server, "/fixture/speech/setup", "", `{}`, 200)
}

func TestNativeSpeechFixtureRefusesWrongAuthorityAndMalformedAudioRequests(t *testing.T) {
	for _, test := range []struct {
		name, path, token, body, change string
		status                          int
	}{
		{"missing-mobile", "/v1/audio/speech", "", speechInput, "", 401},
		{"desktop-token-is-not-mobile", "/v1/audio/speech", desktopSpeechToken, speechInput, "", 401},
		{"mobile-token-is-not-desktop", "/client/desktop/v1/audio/speech", "mobile", speechInput, "", 401},
		{"not-approved", "/v1/audio/speech", "mobile", speechInput, "review", 401},
		{"api-disabled", "/v1/audio/speech", "mobile", speechInput, "api", 401},
		{"signed-out", "/v1/audio/speech", "mobile", speechInput, "logout", 401},
		{"cookie", "/v1/audio/speech", "mobile", speechInput, "cookie", 401},
		{"method", "/v1/audio/speech", "mobile", "", "", 400},
		{"json", "/v1/audio/speech", "mobile", "{", "", 400},
		{"wrong-model", "/v1/audio/speech", "mobile", strings.Replace(speechInput, `"model":"speech"`, `"model":"conversation"`, 1), "", 400},
		{"not-pcm", "/v1/audio/speech", "mobile", strings.Replace(speechInput, `"pcm"`, `"mp3"`, 1), "", 400},
		{"buffered-wire", "/v1/audio/speech", "mobile", strings.Replace(speechInput, `"sse"`, `"audio"`, 1), "", 400},
		{"missing-wire", "/v1/audio/speech", "mobile", strings.Replace(speechInput, `,"stream_format":"sse"`, ``, 1), "", 400},
		{"disabled", "/v1/audio/speech", "mobile", speechInput, "disabled", 503},
	} {
		t.Run(test.name, func(t *testing.T) {
			f, server, token := webFixture(t)
			f.speech.enabled, f.speech.mode = true, "raw-finite"
			if test.token == "mobile" {
				test.token = token
			}
			switch test.change {
			case "review":
				f.review = "pending"
			case "api":
				f.api = false
			case "logout":
				f.flow.Authorized = false
			case "disabled":
				f.speech.enabled = false
			}
			method := http.MethodPost
			if test.body == "" {
				method = http.MethodGet
			}
			r := httptest.NewRequest(method, test.path, strings.NewReader(test.body))
			r.Header.Set("Authorization", "Bearer "+test.token)
			if test.change == "cookie" {
				r.Header.Set("Cookie", "fixture=not-permitted")
			}
			w := httptest.NewRecorder()
			f.ServeHTTP(w, r)
			if w.Code != test.status || len(f.speech.requests) != 0 {
				t.Fatalf("refusal status=%d requests=%d", w.Code, len(f.speech.requests))
			}
			_ = server
		})
	}
}

func TestNativeSpeechFixtureFlushesPCMAndSSEBeforeEOFAndObservesRealStop(t *testing.T) {
	for _, mode := range []string{"raw-held", "sse-held"} {
		t.Run(mode, func(t *testing.T) {
			f, server, token := webFixture(t)
			f.speech.enabled, f.speech.mode = true, mode
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			r, _ := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+"/v1/audio/speech", strings.NewReader(speechInput))
			r.Header.Set("Authorization", "Bearer "+token)
			res, err := server.Client().Do(r)
			if err != nil {
				t.Fatal(err)
			}
			defer res.Body.Close()
			if mode == "raw-held" {
				first := make([]byte, 12000)
				if _, err := io.ReadFull(res.Body, first); err != nil || !bytes.Equal(first, speechPCM()) {
					t.Fatalf("first raw frame: %v", err)
				}
			} else {
				reader := bufio.NewReader(res.Body)
				line, err := reader.ReadString('\n')
				if err != nil || line != "event: speech.audio.delta\n" {
					t.Fatalf("first SSE frame %q %v", line, err)
				}
			}
			f.speech.mu.Lock()
			premature := len(f.speech.requests) != 1 || f.speech.requests[0].EOF
			f.speech.mu.Unlock()
			if premature {
				t.Fatal("native fixture buffered synthesis through EOF")
			}
			cancel()
			res.Body.Close()
			deadline := time.Now().Add(2 * time.Second)
			for time.Now().Before(deadline) {
				f.speech.mu.Lock()
				stopped := f.speech.requests[0].Cancelled
				f.speech.mu.Unlock()
				if stopped {
					return
				}
				time.Sleep(time.Millisecond)
			}
			t.Fatal("Stop did not disconnect the real held audio source")
		})
	}
}

func TestNativeSpeechFixtureFiniteRawAndSSERetainExactPCMAndCompletion(t *testing.T) {
	for _, mode := range []string{"raw-finite", "sse-finite"} {
		f, server, token := webFixture(t)
		f.speech.enabled, f.speech.mode = true, mode
		raw := webRequest(t, server, "/v1/audio/speech", token, speechInput, 200)
		if mode == "raw-finite" {
			if !bytes.Equal([]byte(raw), bytes.Repeat(speechPCM(), 4)) {
				t.Fatal("finite PCM changed audio bytes")
			}
		} else if strings.Count(raw, base64.StdEncoding.EncodeToString(speechPCM())) != 4 || !strings.HasSuffix(raw, "event: speech.audio.done\ndata: {\"type\":\"speech.audio.done\"}\n\n") {
			t.Fatal("finite SSE lost audio or terminal frame")
		}
		var state struct {
			Data struct {
				Requests []map[string]any `json:"requests"`
			} `json:"data"`
		}
		if err := json.Unmarshal([]byte(webRequest(t, server, "/fixture/speech/state", "", "", 200)), &state); err != nil || len(state.Data.Requests) != 1 || state.Data.Requests[0]["stream_format"] != "sse" {
			t.Fatalf("native evidence must retain the actual requested stream protocol: %+v error=%v", state.Data.Requests, err)
		}
		f.speech.mu.Lock()
		if !f.speech.requests[0].EOF || f.speech.requests[0].Cancelled || f.speech.requests[0].Flushes != 4 {
			t.Fatal("finite stream did not complete")
		}
		f.speech.mu.Unlock()
	}
	f, server, _ := webFixture(t)
	f.speech.enabled, f.speech.mode = true, "raw-finite"
	webRequest(t, server, "/client/desktop/v1/audio/speech", desktopSpeechToken, speechInput, 200)
	f.speech.mu.Lock()
	defer f.speech.mu.Unlock()
	if f.speech.requests[0].Route != "/client/desktop/v1/audio/speech" {
		t.Fatal("desktop route not separately observed")
	}
}

func TestNativeSpeechFixtureObserversAndControlsStayPassiveAndBounded(t *testing.T) {
	f, server, _ := webFixture(t)
	if raw := webRequest(t, server, "/fixture/speech/observer.js", "", "", 200); strings.Contains(raw, "__ZWAI_SPEECH_FIXTURE_ORIGIN__") || !strings.Contains(raw, server.URL) {
		t.Fatal("observer origin not bound")
	}
	for _, mode := range []string{"raw-held", "sse-held", "raw-finite", "sse-finite"} {
		webRequest(t, server, "/fixture/speech/mode?value="+mode, "", `{}`, 200)
	}
	for _, path := range []string{"/fixture/speech/mode?value=other", "/fixture/speech/mode?value=raw-held"} {
		webRequest(t, server, path, "", "", 400)
	}
	webRequest(t, server, "/fixture/speech/observe", "", "{", 400)
	webRequest(t, server, "/fixture/speech/observe", "", `{"event":"invented"}`, 400)
	webRequest(t, server, "/fixture/speech/observe", "", "", 400)
	webRequest(t, server, "/fixture/speech/observe", "", `{"event":"start","node_id":7,"context_id":3,"sequence":12,"context_state":"running","sample_rate":24000,"channels":1,"length":6000,"minimum":-0.25,"maximum":0.25,"nonzero":true}`, 200)
	f.speech.mu.Lock()
	for len(f.speech.observations) < 512 {
		f.speech.observations = append(f.speech.observations, speechObservation{Event: "ended"})
	}
	f.speech.mu.Unlock()
	for _, event := range []string{"stop", "disconnect", "context_closed"} {
		webRequest(t, server, "/fixture/speech/observe", "", `{"event":"`+event+`"}`, 200)
	}
	raw := webRequest(t, server, "/fixture/speech/state", "", "", 200)
	f.speech.mu.Lock()
	if !strings.Contains(raw, `"nonzero":true`) || !strings.Contains(raw, `"node_id":7`) || !strings.Contains(raw, `"context_id":3`) || !strings.Contains(raw, `"sequence":12`) || len(f.speech.observations) != 512 || len(f.speech.requests) != 0 {
		t.Fatal("passive observations changed audio or exceeded their limit")
	}
	f.speech.mu.Unlock()
	webRequest(t, server, "/fixture/speech/unknown", "", "", 404)
}

func TestNativeSpeechSetupRefusesMissingEngineInvalidOriginAndUnwritableConfig(t *testing.T) {
	f := &fixture{counts: map[string]int{}}
	w := httptest.NewRecorder()
	f.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/fixture/speech/setup", nil))
	if w.Code != 503 {
		t.Fatal("missing engine accepted")
	}
	w = httptest.NewRecorder()
	f.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/fixture/speech/setup", nil))
	if w.Code != 503 {
		t.Fatal("wrong setup method accepted")
	}
	f = localCapFixture(t)
	if err := f.setupSpeech(t.Context()); err == nil || !strings.Contains(err.Error(), "--speech-stream") {
		t.Fatalf("mock fixture must explicitly refuse a fake account source: %v", err)
	}
	f = realSpeechFixture(t)
	f.origin = ":bad"
	if f.setupSpeech(t.Context()) == nil {
		t.Fatal("invalid source accepted")
	}
	f.origin = "http://127.0.0.1:12345"
	path := f.engine.Config().Path()
	_ = os.Remove(path)
	if err := os.MkdirAll(path, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "blocked"), []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	if f.setupSpeech(t.Context()) == nil {
		t.Fatal("failed configuration write accepted")
	}
}

type speechFailureWriter struct {
	streamChunkWriter
	writes, failAt int
}

func (w *speechFailureWriter) Write(raw []byte) (int, error) {
	w.writes++
	if w.writes == w.failAt {
		return 0, io.ErrClosedPipe
	}
	return w.streamChunkWriter.Write(raw)
}

func TestNativeSpeechFixtureRetainsWriteFlushAndFinalSSEFailures(t *testing.T) {
	for _, failure := range []string{"write", "flush", "done", "cancel"} {
		f := &fixture{}
		f.speech.enabled, f.speech.mode = true, "sse-finite"
		w := &speechFailureWriter{streamChunkWriter: streamChunkWriter{header: make(http.Header)}}
		if failure == "write" {
			w.failAt = 1
		}
		if failure == "done" {
			w.failAt = 5
		}
		if failure == "flush" {
			w.failFlush = true
		}
		ctx, cancel := context.WithCancel(t.Context())
		if failure == "cancel" {
			cancel()
		}
		r := httptest.NewRequest(http.MethodPost, "/v1/audio/speech", strings.NewReader(speechInput)).WithContext(ctx)
		f.streamSpeech(w, r)
		cancel()
		if len(f.speech.requests) != 1 || f.speech.requests[0].EOF || (failure == "cancel" && !f.speech.requests[0].Cancelled) || (failure != "cancel" && !f.speech.requests[0].Failed) {
			t.Fatalf("%s lost actual failure: %+v", failure, f.speech.requests)
		}
	}
}

func TestNativeSpeechRealAccountAnswerUsesDesktopAuthorityAndCompletedModelBoundary(t *testing.T) {
	f := realSpeechFixture(t)
	server := httptest.NewServer(f)
	t.Cleanup(server.Close)
	f.origin = server.URL
	if err := f.setupSpeech(t.Context()); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		f.mu.Lock()
		completed := f.counts["model_successes"]
		f.mu.Unlock()
		if completed > 0 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	f.mu.Lock()
	completed := f.counts["model_successes"]
	f.mu.Unlock()
	if completed != 1 {
		t.Fatalf("real account seed manager calls=%d", completed)
	}
	f.mu.Lock()
	classified := f.counts["speech_policy_calls"]
	f.mu.Unlock()
	if classified == 0 {
		t.Fatal("the real answer admission classifier never completed")
	}
	turns, err := f.engine.Store().ListTurns(f.pcThreadID)
	if err != nil || len(turns) != 1 || turns[0].Status != "done" || turns[0].Final != "Native streaming speech fixture answer." {
		t.Fatalf("missing actual completed playback answer: turns=%+v err=%v", turns, err)
	}
	webRequest(t, server, "/client/desktop/v1/chat/completions", "mobile-credential", `{}`, 401)
	webRequest(t, server, "/client/desktop/v1/chat/completions", desktopSpeechToken, `{}`, 400)
	webRequest(t, server, "/client/desktop/v1/chat/completions", desktopSpeechToken, "", 400)
}

func TestNativeSpeechSetupKeepsCreateFailureDiagnostic(t *testing.T) {
	f := realSpeechFixture(t)
	f.origin = "http://127.0.0.1:12345"
	if err := f.engine.Store().Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.setupSpeech(t.Context()); err == nil || !strings.Contains(err.Error(), "create playback thread") {
		t.Fatalf("setup lost create error: %v", err)
	}
}

func TestNativeSpeechSetupRefusesFailedOrCancelledAnswerInsteadOfShowingAnEmptyConversation(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		f := realSpeechFixture(t)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "fixture model refused", http.StatusForbidden)
		}))
		t.Cleanup(server.Close)
		f.origin = server.URL
		ctx, cancel := context.WithCancel(t.Context())
		if cancelled {
			cancel()
		}
		err := f.setupSpeech(ctx)
		cancel()
		if err == nil || f.speech.enabled {
			t.Fatalf("an incomplete answer enabled speech: %v", err)
		}
		if cancelled && !strings.Contains(err.Error(), "wait for playback answer") {
			t.Fatalf("lost cancellation: %v", err)
		}
		if !cancelled && (!strings.Contains(err.Error(), "status=error") || !strings.Contains(err.Error(), "403")) {
			t.Fatalf("lost actual terminal model error: %v", err)
		}
	}
}

func TestNativeSpeechCLIOptInUsesRealGatewayAndJoinsOwnedResources(t *testing.T) {
	t.Setenv("OPENAI_BASE_URL", "")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("OPENAI_MODEL", "")
	dir := t.TempDir()
	ready := filepath.Join(dir, "ready.json")
	previousArgs, previousFlags := os.Args, flag.CommandLine
	os.Args = []string{"account-fixture", "--speech-stream", "--data-dir", filepath.Join(dir, "data"), "--ready-file", ready, "--max-duration", "3s"}
	flag.CommandLine = flag.NewFlagSet("native-speech-fixture-test", flag.ContinueOnError)
	defer func() { os.Args = previousArgs; flag.CommandLine = previousFlags }()
	finished := make(chan struct{})
	go func() { main(); close(finished) }()
	defer func() {
		select {
		case <-finished:
		case <-time.After(8 * time.Second):
			t.Error("speech fixture did not join owned resources")
		}
	}()
	var info struct {
		Origin    string `json:"origin"`
		Anonymous string `json:"anonymous_origin"`
	}
	until := time.Now().Add(2 * time.Second)
	for time.Now().Before(until) {
		raw, err := os.ReadFile(ready)
		if err == nil && json.Unmarshal(raw, &info) == nil && info.Origin != "" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if info.Origin == "" {
		t.Fatal("speech CLI did not publish real host readiness")
	}
	client := &http.Client{Timeout: 2 * time.Second}
	response, err := client.Post(info.Origin+"/fixture/speech/setup", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || response.StatusCode != 200 || !bytes.Contains(raw, []byte(`"execution":"gateway"`)) || !bytes.Contains(raw, []byte(`"format":"pcm"`)) {
		t.Fatalf("CLI did not create a completed real-model answer: status=%d body=%s err=%v", response.StatusCode, raw, err)
	}
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("speech CLI did not finish")
	}
	for _, origin := range []string{info.Origin, info.Anonymous} {
		if response, err := client.Get(origin + "/fixture/state"); err == nil {
			response.Body.Close()
			t.Fatalf("owned fixture port survived cleanup: %s", origin)
		}
	}
}
