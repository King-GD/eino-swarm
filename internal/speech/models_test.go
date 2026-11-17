package speech

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LubyRuffy/eino-swarm/internal/config"
)

func TestVoiceDiscoveryFindsTypedModelsBeforeEnablingOrChoosingOne(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/v1/models" || r.Header.Get("Authorization") != "Bearer private-key" {
			t.Error("discovery did not use selected private connection")
		}
		_, _ = io.WriteString(w, `{"data":[{"id":"asr","model_type":"transcription"},{"id":"tts","model_type":"speech"},{"id":"chat","model_type":"chat"},{"id":"unknown"}]}`)
	}))
	defer upstream.Close()
	cfg, svc := configuredSpeech(t, upstream.URL+"/v1")
	if err := cfg.Update(func(c *config.Config) error { c.Speech.Input.Enabled = false; c.Speech.Input.Model = ""; return nil }); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		input bool
		want  string
	}{{true, "asr"}, {false, "tts"}} {
		names, err := svc.Models(context.Background(), test.input)
		if err != nil || len(names) != 1 || names[0] != test.want {
			t.Fatalf("catalog=%v %v", names, err)
		}
	}
	if err := cfg.Update(func(c *config.Config) error { c.Speech.Input.Source = "account"; return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Models(context.Background(), true); errorCode(err) != "speech_login_required" {
		t.Fatalf("signed-out account discovery=%v", err)
	}
}

func TestVoiceDiscoveryRefusesMalformedLargeFailedAndRedirectedCatalogs(t *testing.T) {
	for _, test := range []struct {
		name, body string
		status     int
	}{
		{"malformed", "{", 200},
		{"large", strings.Repeat("x", (1<<20)+1), 200},
		{"failure", "private-upstream-body", 503},
		{"redirect", "private-upstream-body", 302},
	} {
		t.Run(test.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Location", "http://redirect.invalid")
				w.WriteHeader(test.status)
				_, _ = io.WriteString(w, test.body)
			}))
			defer upstream.Close()
			_, svc := configuredSpeech(t, upstream.URL+"/v1")
			if _, err := svc.Models(context.Background(), false); err == nil || strings.Contains(err.Error(), "private") {
				t.Fatalf("bad discovery response accepted or leaked: %v", err)
			}
		})
	}
	closed := httptest.NewServer(http.NotFoundHandler())
	closed.Close()
	_, svc := configuredSpeech(t, closed.URL+"/v1")
	if _, err := svc.Models(context.Background(), true); errorCode(err) != "speech_catalog_unavailable" {
		t.Fatalf("network failure=%v", err)
	}
	blocked := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer blocked.Close()
	_, svc = configuredSpeech(t, blocked.URL+"/v1")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := svc.Models(ctx, true); err != context.DeadlineExceeded {
		t.Fatalf("discovery cancellation=%v", err)
	}
}
