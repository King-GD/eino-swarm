package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LubyRuffy/eino-swarm/internal/config"
)

func configurePCMHTTP(t *testing.T, h *harness, endpoint string) {
	t.Helper()
	if err := h.app.Config.Update(func(c *config.Config) error {
		c.Speech.Output.SpeechEndpoint = config.SpeechEndpoint{Enabled: true, Source: "custom", BaseURL: endpoint + "/v1", APIKey: "voice-stream-key", Model: "speaker"}
		c.Speech.Output.Format = "pcm"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// The upstream gate stays closed until the client hears the first samples.
// Returning a complete recording before playback cannot satisfy this check.
func TestSpeechHTTPPlaysFirstSamplesBeforeUpstreamCompletes(t *testing.T) {
	for _, mediaType := range []string{"audio/pcm", "text/event-stream"} {
		t.Run(mediaType, func(t *testing.T) {
			gate := make(chan struct{})
			defer close(gate)
			first, last := []byte{1, 2}, []byte{3, 4}
			if mediaType == "text/event-stream" {
				first = []byte("data: {\"type\":\"speech.audio.delta\",\"audio\":\"AQI=\"}\n\n")
				last = []byte("data: {\"type\":\"speech.audio.done\"}\n\n")
			}
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				if json.NewDecoder(r.Body).Decode(&body) != nil || body["response_format"] != "pcm" || body["stream_format"] != "audio" || r.Header.Get("Authorization") != "Bearer voice-stream-key" {
					t.Error("stream did not preserve the configured PCM request and credential")
				}
				w.Header().Set("Content-Type", mediaType)
				_, _ = w.Write(first)
				w.(http.Flusher).Flush()
				select {
				case <-gate:
					_, _ = w.Write(last)
				case <-r.Context().Done():
				}
			}))
			defer upstream.Close()
			h := newHarness(t)
			configurePCMHTTP(t, h, upstream.URL)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			req, _ := http.NewRequestWithContext(ctx, "POST", h.srv.URL+"/api/audio/speech", strings.NewReader(`{"text":"answer"}`))
			req.Header.Set("Content-Type", "application/json")
			resp, err := h.srv.Client().Do(req)
			if err != nil {
				t.Fatalf("first audio waited for upstream completion: %v", err)
			}
			defer resp.Body.Close()
			got := make([]byte, len(first))
			_, err = io.ReadFull(resp.Body, got)
			if err != nil || resp.StatusCode != 200 || resp.Header.Get("Content-Type") != mediaType || !bytes.Equal(got, first) {
				t.Fatalf("first samples=%q status=%d type=%q error=%v", got, resp.StatusCode, resp.Header.Get("Content-Type"), err)
			}
		})
	}
}

func TestSpeechHTTPDoesNotReportATruncatedStreamAsComplete(t *testing.T) {
	for _, name := range []string{"complete", "truncated", "limit", "odd sample", "configuration", "caller", "timeout"} {
		t.Run(name, func(t *testing.T) {
			gate, canceled := make(chan struct{}), make(chan struct{})
			defer close(gate)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				w.Header().Set("Content-Type", "audio/pcm")
				if name == "truncated" {
					w.Header().Set("Content-Length", "8")
				}
				_, _ = w.Write([]byte{1, 2})
				w.(http.Flusher).Flush()
				select {
				case <-gate:
					if name != "truncated" {
						tail := []byte{3, 4}
						if name == "odd sample" {
							tail = []byte{3}
						}
						_, _ = w.Write(tail)
					}
				case <-r.Context().Done():
					close(canceled)
				}
			}))
			defer upstream.Close()
			h := newHarness(t)
			configurePCMHTTP(t, h, upstream.URL)
			if err := h.app.Config.Update(func(c *config.Config) error {
				if name == "limit" {
					c.Speech.Limits.OutputBytes = 2
				}
				if name == "timeout" {
					c.Speech.Output.TimeoutSeconds = 1
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			req, _ := http.NewRequestWithContext(ctx, "POST", h.srv.URL+"/api/audio/speech", strings.NewReader(`{"text":"answer"}`))
			req.Header.Set("Content-Type", "application/json")
			resp, err := h.srv.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			first := make([]byte, 2)
			if _, err := io.ReadFull(resp.Body, first); err != nil || !bytes.Equal(first, []byte{1, 2}) {
				t.Fatalf("first samples=%v error=%v", first, err)
			}
			switch name {
			case "configuration":
				if err := h.app.Config.Update(func(c *config.Config) error { c.Speech.Output.Enabled = false; return nil }); err != nil {
					t.Fatal(err)
				}
			case "caller":
				resp.Body.Close()
			case "timeout":
			default:
				gate <- struct{}{}
			}
			if name != "caller" {
				tail, err := io.ReadAll(resp.Body)
				if name == "complete" {
					if err != nil || !bytes.Equal(tail, []byte{3, 4}) {
						t.Fatalf("complete stream tail=%v error=%v", tail, err)
					}
				} else if err == nil || ctx.Err() != nil {
					t.Fatalf("failed stream returned successful EOF or only caller timeout: tail=%v error=%v", tail, err)
				}
			}
			if name == "caller" || name == "configuration" || name == "timeout" {
				select {
				case <-canceled:
				case <-time.After(time.Second):
					t.Fatal("stream cancellation did not reach upstream")
				}
			}
		})
	}
}

func TestSpeechHTTPRejectsStreamingAuthenticationAndFormatErrorsBeforeAudio(t *testing.T) {
	for _, test := range []struct {
		name, mime   string
		status, want int
	}{
		{"auth", "application/json", 401, 401},
		{"forbidden", "application/json", 403, 403},
		{"redirect", "audio/pcm", 302, 502},
		{"wrong audio", "audio/mpeg", 200, 502},
		{"empty", "audio/pcm", 200, 502},
		{"empty SSE", "text/event-stream", 200, 502},
	} {
		t.Run(test.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", test.mime)
				w.Header().Set("Location", "http://must-not-follow.invalid")
				w.WriteHeader(test.status)
			}))
			defer upstream.Close()
			h := newHarness(t)
			configurePCMHTTP(t, h, upstream.URL)
			h.json("POST", "/api/audio/speech", map[string]any{"text": "answer"}, test.want)
		})
	}
}
