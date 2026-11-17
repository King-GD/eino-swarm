package speech

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LubyRuffy/eino-swarm/internal/config"
)

func setPCM(t *testing.T, cfg *config.Config) {
	t.Helper()
	if err := cfg.Update(func(c *config.Config) error { c.Speech.Output.Format = "pcm"; return nil }); err != nil {
		t.Fatal(err)
	}
}

func TestPCMStreamHoldsModelOwnershipUntilIdempotentClose(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if json.NewDecoder(r.Body).Decode(&body) != nil || body["stream_format"] != "audio" || body["voice"] != "selected" || body["instructions"] != "calm" {
			t.Errorf("PCM body=%v", body)
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write([]byte{0, 1, 2, 3})
	}))
	defer upstream.Close()
	cfg, svc := configuredSpeech(t, upstream.URL+"/v1")
	setPCM(t, cfg)
	if err := cfg.Update(func(c *config.Config) error {
		c.Speech.Output.Voice = "selected"
		c.Speech.Output.Instructions = "calm"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	stream, err := svc.SynthesizeStream(context.Background(), "answer")
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Body.Close()
	if release, ok := svc.pool.TryHoldModelPolicy(); ok {
		release()
		t.Fatal("stream released model admission before playback completed")
	}
	data, err := io.ReadAll(stream.Body)
	if err != nil || stream.MIME != "audio/pcm" || !bytes.Equal(data, []byte{0, 1, 2, 3}) {
		t.Fatalf("PCM=%v type=%q error=%v", data, stream.MIME, err)
	}
	if release, ok := svc.pool.TryHoldModelPolicy(); ok {
		release()
		t.Fatal("EOF released ownership without Close")
	}
	if err := stream.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if err := stream.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if release, ok := svc.pool.TryHoldModelPolicy(); !ok {
		t.Fatal("Close did not release model admission")
	} else {
		release()
	}
	if _, err := stream.Body.Read(make([]byte, 2)); !errors.Is(err, context.Canceled) {
		t.Fatalf("closed stream read=%v", err)
	}
	buffered, err := svc.Synthesize(context.Background(), "answer")
	if err != nil || buffered.MIME != "audio/pcm" || !bytes.Equal(buffered.Data, data) {
		t.Fatalf("buffered PCM=%v error=%v", buffered, err)
	}
}

func TestSpeechStreamRefusesInvalidPCMAndPreservesCompatibleFormats(t *testing.T) {
	for _, test := range []struct{ format, mime, want string }{
		{"pcm", "audio/pcm", "audio/pcm"},
		{"pcm", "audio/pcm;rate=24000;channels=1", "audio/pcm"},
		{"pcm", "text/event-stream", "text/event-stream"},
		{"wav", "application/octet-stream", "audio/wav"},
		{"mp3", "application/octet-stream", "audio/mpeg"},
		{"pcm", "audio/mpeg", ""},
		{"pcm", "audio/L16", ""},
		{"pcm", "audio/pcm;rate=48000", ""},
		{"pcm", "audio/pcm;channels=2", ""},
		{"pcm", "audio/pcm;bits=8", ""},
		{"pcm", "application/json", ""},
		{"pcm", "invalid;", ""},
	} {
		t.Run(test.format+test.mime, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", test.mime)
				_, _ = w.Write([]byte{0, 1})
			}))
			defer upstream.Close()
			cfg, svc := configuredSpeech(t, upstream.URL+"/v1")
			if err := cfg.Update(func(c *config.Config) error { c.Speech.Output.Format = test.format; return nil }); err != nil {
				t.Fatal(err)
			}
			stream, err := svc.SynthesizeStream(context.Background(), "answer")
			if test.want == "" {
				if err == nil || errorCode(err) != "speech_invalid_response" {
					t.Fatalf("invalid format accepted: %v %v", stream, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer stream.Body.Close()
			if stream.MIME != test.want {
				t.Fatalf("type=%q want=%q", stream.MIME, test.want)
			}
			if _, err := io.ReadAll(stream.Body); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSpeechStreamRefusalsReleaseAdmissionBeforeAnyModelCall(t *testing.T) {
	cfg, svc := configuredSpeech(t, "http://unused.invalid/v1")
	for _, text := range []string{"", "\xff", strings.Repeat("x", 1001)} {
		if _, err := svc.SynthesizeStream(context.Background(), text); errorCode(err) != "speech_invalid_text" {
			t.Fatalf("invalid text=%v", err)
		}
	}
	release, ok := svc.pool.TryHoldModelPolicy()
	if !ok {
		t.Fatal("invalid request retained model admission")
	}
	if _, err := svc.SynthesizeStream(context.Background(), "answer"); errorCode(err) != "speech_busy" {
		t.Fatalf("busy=%v", err)
	}
	release()
	if err := cfg.Update(func(c *config.Config) error { c.Speech.Output.Enabled = false; return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SynthesizeStream(context.Background(), "answer"); errorCode(err) != "speech_disabled" {
		t.Fatalf("disabled=%v", err)
	}
}

func TestSpeechStreamReadChecksFreshConfigurationAndCancellation(t *testing.T) {
	for _, change := range []bool{false, true} {
		t.Run(map[bool]string{false: "caller", true: "configuration"}[change], func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "audio/pcm")
				_, _ = w.Write([]byte{0, 1})
			}))
			defer upstream.Close()
			cfg, svc := configuredSpeech(t, upstream.URL+"/v1")
			setPCM(t, cfg)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			stream, err := svc.SynthesizeStream(ctx, "answer")
			if err != nil {
				t.Fatal(err)
			}
			defer stream.Body.Close()
			if change {
				if err := cfg.Update(func(c *config.Config) error { c.Speech.Output.Enabled = false; return nil }); err != nil {
					t.Fatal(err)
				}
			} else {
				cancel()
			}
			_, err = stream.Body.Read(make([]byte, 2))
			if err == nil || change && errorCode(err) != "speech_configuration_changed" || !change && !errors.Is(err, context.Canceled) {
				t.Fatalf("read=%v", err)
			}
		})
	}
}

func TestPCMStreamNeverAcceptsEmptyOddOversizedOrTruncatedSamples(t *testing.T) {
	for _, name := range []string{"empty", "odd", "limit", "truncated"} {
		t.Run(name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "audio/pcm")
				if name == "truncated" {
					w.Header().Set("Content-Length", "8")
				}
				if name != "empty" {
					_, _ = w.Write([]byte{1, 2, 3})
				}
			}))
			defer upstream.Close()
			cfg, svc := configuredSpeech(t, upstream.URL+"/v1")
			setPCM(t, cfg)
			if name == "limit" {
				if err := cfg.Update(func(c *config.Config) error { c.Speech.Limits.OutputBytes = 2; return nil }); err != nil {
					t.Fatal(err)
				}
			}
			stream, err := svc.SynthesizeStream(context.Background(), "answer")
			if err != nil {
				t.Fatal(err)
			}
			defer stream.Body.Close()
			_, err = io.ReadAll(stream.Body)
			want := "speech_invalid_response"
			if name == "limit" {
				want = "speech_output_size"
			}
			if errorCode(err) != want {
				t.Fatalf("read=%v want=%s", err, want)
			}
			if name == "limit" {
				if _, again := stream.Body.Read(make([]byte, 2)); errorCode(again) != want {
					t.Fatalf("repeat read=%v", again)
				}
			}
			if name != "limit" && name != "truncated" {
				if _, err := svc.Synthesize(context.Background(), "answer"); errorCode(err) != "speech_invalid_response" {
					t.Fatalf("buffered PCM=%v", err)
				}
			}
		})
	}
}

func TestPCMStreamCancellationInterruptsAnAlreadyOpenRead(t *testing.T) {
	for _, change := range []bool{false, true} {
		t.Run(map[bool]string{false: "caller", true: "configuration"}[change], func(t *testing.T) {
			left := make(chan struct{})
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				w.Header().Set("Content-Type", "audio/pcm")
				_, _ = w.Write([]byte{1, 2})
				w.(http.Flusher).Flush()
				<-r.Context().Done()
				close(left)
			}))
			defer upstream.Close()
			cfg, svc := configuredSpeech(t, upstream.URL+"/v1")
			setPCM(t, cfg)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			stream, err := svc.SynthesizeStream(ctx, "answer")
			if err != nil {
				t.Fatal(err)
			}
			defer stream.Body.Close()
			if _, err := io.ReadFull(stream.Body, make([]byte, 2)); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { _, err := stream.Body.Read(make([]byte, 2)); done <- err }()
			if change {
				if err := cfg.Update(func(c *config.Config) error { c.Speech.Output.Enabled = false; return nil }); err != nil {
					t.Fatal(err)
				}
			} else {
				cancel()
			}
			select {
			case err := <-done:
				if err == nil || change && errorCode(err) != "speech_configuration_changed" || !change && !errors.Is(err, context.Canceled) {
					t.Fatalf("blocked read=%v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("read did not cancel")
			}
			select {
			case <-left:
			case <-time.After(time.Second):
				t.Fatal("upstream did not cancel")
			}
		})
	}
}

func TestSpeechStreamPreparationErrorsReleaseTheirResources(t *testing.T) {
	cfg, svc := configuredSpeech(t, "http://unused.invalid/v1")
	// A malformed internal selection must still stop its watcher and release.
	selected, _ := svc.selectTarget(cfg.Snapshot(), false)
	selected.target.BaseURL = ":"
	released := false
	if _, err := svc.performStream(context.Background(), selected, "/audio/speech", "application/json", nil, 10, func() { released = true }); errorCode(err) != "speech_not_configured" || !released {
		t.Fatalf("invalid request=%v released=%v", err, released)
	}
	// Config validation normally excludes non-finite values. Exercise the last
	// serialization boundary independently of persistence and network dispatch.
	cfg.Speech.Output.Speed = math.NaN()
	if _, err := svc.SynthesizeStream(context.Background(), "answer"); errorCode(err) != "speech_invalid_input" {
		t.Fatalf("nonfinite request=%v", err)
	}
}
