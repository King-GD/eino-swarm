package speech

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LubyRuffy/eino-swarm/internal/config"
	"github.com/LubyRuffy/eino-swarm/internal/provider"
)

func TestVoiceReportsSafeFailureCategoriesWithoutProviderBodies(t *testing.T) {
	for _, status := range []int{302, 400, 401, 403, 415, 422, 429, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Location", "http://redirect.invalid/private-key")
				w.WriteHeader(status)
				_, _ = io.WriteString(w, "private-upstream-message")
			}))
			defer upstream.Close()
			_, svc := configuredSpeech(t, upstream.URL+"/v1")
			_, err := svc.Synthesize(context.Background(), "answer")
			if err == nil || strings.Contains(err.Error(), "private") || strings.Contains(fmt.Sprint(svc), "private") {
				t.Fatalf("unsafe failure: %v", err)
			}
			got, code := Failure(err)
			want := 502
			switch status {
			case 400, 415, 422:
				want = 400
			case 401:
				want = 401
			case 403:
				want = 403
			case 429:
				want = 429
			}
			if got != want || !strings.HasPrefix(code, "speech_") {
				t.Fatalf("status=%d code=%s want=%d", got, code, want)
			}
		})
	}
	for _, err := range []error{context.Canceled, context.DeadlineExceeded, errors.New("private cause")} {
		status, code := Failure(err)
		if code == "" || status < 400 {
			t.Fatalf("failure=%d %s", status, code)
		}
		if errorCode(err) != "speech_unavailable" {
			t.Fatal("unknown error should stay generic")
		}
	}
}

func TestASRRejectsInvalidUploadsAndMalformedSuccessfulResponses(t *testing.T) {
	for _, body := range []string{`not-json`, `{"text":null}`, `{"text":3}`, `{"text":"  "}`} {
		t.Run(body, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, body)
			}))
			defer upstream.Close()
			_, svc := configuredSpeech(t, upstream.URL+"/v1")
			_, err := svc.Transcribe(context.Background(), Audio{Filename: "capture.wav", MIME: "audio/wav", Data: wavFixture()})
			if status, _ := Failure(err); err == nil || status != 502 {
				t.Fatalf("malformed successful ASR was accepted: %v", err)
			}
		})
	}
	cfg, svc := configuredSpeech(t, "http://unused.invalid/v1")
	for _, audio := range []Audio{{}, {MIME: "application/json", Data: []byte(`{}`)}, {MIME: "invalid;", Data: []byte("data")}} {
		if _, err := svc.Transcribe(context.Background(), audio); err == nil {
			t.Fatal("invalid recording was accepted")
		}
	}
	if err := cfg.Update(func(c *config.Config) error { c.Speech.Limits.InputBytes = 1; return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Transcribe(context.Background(), Audio{MIME: "audio/wav", Data: wavFixture()}); err == nil {
		t.Fatal("oversized recording was accepted")
	}
	for _, text := range []string{"", "\xff", strings.Repeat("x", 1001)} {
		if _, err := svc.Synthesize(context.Background(), text); err == nil {
			t.Fatal("invalid text was accepted")
		}
	}
}

func TestTTSValidatesAudioTypeAndBoundsTheResponse(t *testing.T) {
	for _, test := range []struct {
		name, mime, body string
		limit            int64
		want             int
	}{
		{"not audio", "application/json", `{"error":"private"}`, 0, 502},
		{"invalid MIME", "invalid;", "data", 0, 502},
		{"empty", "audio/wav", "", 0, 502},
		{"too large", "audio/wav", "12345", 4, 413},
		{"binary WAV", "application/octet-stream", string(wavFixture()), 0, 200},
	} {
		t.Run(test.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", test.mime)
				_, _ = io.WriteString(w, test.body)
			}))
			defer upstream.Close()
			cfg, svc := configuredSpeech(t, upstream.URL+"/v1")
			if test.limit > 0 {
				if err := cfg.Update(func(c *config.Config) error { c.Speech.Limits.OutputBytes = test.limit; return nil }); err != nil {
					t.Fatal(err)
				}
			}
			audio, err := svc.Synthesize(context.Background(), "answer")
			if test.want == 200 {
				if err != nil || audio.MIME != "audio/wav" {
					t.Fatalf("binary WAV: %+v %v", audio, err)
				}
			} else if status, _ := Failure(err); err == nil || status != test.want {
				t.Fatalf("failure=%v status=%d", err, status)
			}
		})
	}
}

func TestVoiceConnectionFailureTruncatedBodyAndDeadlineAreDistinct(t *testing.T) {
	closed := httptest.NewServer(http.NotFoundHandler())
	closed.Close()
	_, svc := configuredSpeech(t, closed.URL+"/v1")
	if _, err := svc.Synthesize(context.Background(), "answer"); errorCode(err) != "speech_unavailable" {
		t.Fatalf("network failure=%v", err)
	}
	truncated := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/wav")
		w.Header().Set("Content-Length", "100")
		_, _ = io.WriteString(w, "short")
	}))
	defer truncated.Close()
	_, svc = configuredSpeech(t, truncated.URL+"/v1")
	if _, err := svc.Synthesize(context.Background(), "answer"); errorCode(err) != "speech_invalid_response" {
		t.Fatalf("truncation=%v", err)
	}
	blocked := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.Copy(io.Discard, r.Body); <-r.Context().Done() }))
	defer blocked.Close()
	_, svc = configuredSpeech(t, blocked.URL+"/v1")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := svc.Synthesize(ctx, "answer"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline=%v", err)
	}
}

func TestLocalSpeechRequiresAnExplicitLocalModelBinding(t *testing.T) {
	cfg, svc := configuredSpeech(t, "http://local.invalid/v1")
	for _, change := range []func(*config.Config){
		func(c *config.Config) { c.Speech.Input.Source = "account" },
		func(c *config.Config) { c.Speech.Input.Model = "" },
		func(c *config.Config) { c.Speech.Input.BaseURL = "" },
		func(c *config.Config) { c.Speech.Input.Source = "provider"; c.Speech.Input.ProviderID = "missing" },
	} {
		previous := cfg.Snapshot().Speech
		if err := cfg.Update(func(c *config.Config) error { change(c); return nil }); err != nil {
			t.Fatal(err)
		}
		if svc.Snapshot().Input.Ready {
			t.Fatal("invalid local binding became ready")
		}
		if _, err := svc.Transcribe(context.Background(), Audio{}); err == nil {
			t.Fatal("unconfigured ASR accepted input")
		}
		if err := cfg.Update(func(c *config.Config) error { c.Speech = previous; return nil }); err != nil {
			t.Fatal(err)
		}
	}
	if err := cfg.Update(func(c *config.Config) error {
		c.Speech.Input.Source = "provider"
		c.Speech.Input.ProviderID = "default"
		c.Speech.Input.Model = "selected-asr"
		c.Models.Providers[0].BaseURL = "http://provider.invalid/v1"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if view := svc.Snapshot(); !view.Input.Ready || view.Input.Model != "selected-asr" {
		t.Fatalf("provider selection=%+v", view)
	}
}

func TestGatewaySpeechUsesFixedRolesEvenWithAnOfficialChatProfile(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/client/desktop/v1/audio/speech" || r.Header.Get("Authorization") != "Bearer private-host-token" {
			t.Error("Gateway role request was misrouted")
		}
		data, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(data), `"model":"speech"`) || strings.Contains(string(data), "chat-alias") {
			t.Errorf("wrong role request: %s", data)
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(wavFixture())
	}))
	defer upstream.Close()
	cfg, svc := configuredSpeech(t, "http://must-not-use.invalid/v1")
	if err := cfg.Update(func(c *config.Config) error {
		c.Product.Mode = "fofa"
		c.Speech.Output.Voice = "selected-voice"
		c.Speech.Output.Instructions = "speak calmly"
		c.Speech.Output.Format = "mp3"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if view := svc.Snapshot(); view.Output.Ready || view.Output.Reason != "speech_login_required" {
		t.Fatalf("signed-out view=%+v", view)
	}
	if New(cfg, nil).Snapshot().Output.Ready {
		t.Fatal("nil account pool became ready")
	}
	src, err := provider.NewAccountProfile("gateway:test", upstream.URL+"/client/desktop/v1", "private-host-token", provider.AccountProfile{Label: "chat-alias"})
	if err != nil {
		t.Fatal(err)
	}
	svc.pool.SetAccountSource(src)
	if svc.Snapshot().Output.Ready {
		t.Fatal("unannounced audio role became ready")
	}
	src, err = provider.NewAccountProfile("gateway:test", upstream.URL+"/client/desktop/v1", "private-host-token", provider.AccountProfile{Label: "chat-alias"})
	if err != nil {
		t.Fatal(err)
	}
	src.SetRoles([]provider.AccountRole{{ID: "speech", Enabled: true, Label: "Voice"}, {ID: "transcription", Enabled: true, Label: "Dictation"}})
	svc.pool.SetAccountSource(src)
	view := svc.Snapshot()
	if view.Execution != "gateway" || view.GatewayOrigin != upstream.URL || !view.Input.Ready || view.Input.Model != "transcription" || view.Output.Model != "speech" || view.Output.Label != "Voice" {
		t.Fatalf("gateway view=%+v", view)
	}
	audio, err := svc.Synthesize(context.Background(), "answer")
	if err != nil || audio.MIME != "audio/mpeg" {
		t.Fatalf("gateway audio=%+v %v", audio, err)
	}
	models, err := svc.Models(context.Background(), true)
	if err != nil || len(models) != 1 || models[0] != "transcription" {
		t.Fatalf("role models=%v %v", models, err)
	}
}
