package server_test

import (
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LubyRuffy/eino-swarm/internal/config"
)

func voiceRequestBody(t *testing.T, files int, extra int, mime string, payload []byte) ([]byte, string) {
	t.Helper()
	var data bytes.Buffer
	writer := multipart.NewWriter(&data)
	for i := 0; i < extra; i++ {
		if err := writer.WriteField("unused", "value"); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < files; i++ {
		header := make(textproto.MIMEHeader)
		header.Set("Content-Disposition", `form-data; name="file"; filename="recording.wav"`)
		header.Set("Content-Type", mime)
		part, err := writer.CreatePart(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = part.Write(payload); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return data.Bytes(), writer.FormDataContentType()
}

func TestSpeechHTTPRefusesInvalidBodiesBeforeDispatch(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("invalid voice request reached upstream")
		w.WriteHeader(500)
	}))
	defer upstream.Close()
	for _, test := range []struct {
		name, path, body, mediaType string
		files, extras               int
		data                        []byte
		disabled                    bool
		limit                       int64
		want                        int
	}{
		{name: "disabled", path: "transcriptions", disabled: true, want: 403},
		{name: "not multipart", path: "transcriptions", body: "{}", mediaType: "application/json", want: 400},
		{name: "broken multipart", path: "transcriptions", body: "broken", mediaType: "multipart/form-data; boundary=x", want: 400},
		{name: "two files", path: "transcriptions", files: 2, data: []byte("data"), mediaType: "audio/wav", want: 400},
		{name: "too many parts", path: "transcriptions", files: 1, extras: 16, data: []byte("data"), mediaType: "audio/wav", want: 400},
		{name: "no recording", path: "transcriptions", extras: 1, want: 400},
		{name: "empty recording", path: "transcriptions", files: 1, mediaType: "audio/wav", want: 400},
		{name: "too large", path: "transcriptions", files: 1, data: []byte("12345"), mediaType: "audio/wav", limit: 4, want: 413},
		{name: "wrong file type", path: "transcriptions", files: 1, data: []byte("{}"), mediaType: "application/json", want: 400},
		{name: "bad speech JSON", path: "speech", body: "{", mediaType: "application/json", want: 400},
		{name: "empty speech", path: "speech", body: `{"text":""}`, mediaType: "application/json", want: 400},
		{name: "bad catalog JSON", path: "models", body: "{", mediaType: "application/json", want: 400},
		{name: "unknown catalog kind", path: "models", body: `{"kind":"chat"}`, mediaType: "application/json", want: 400},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newHarness(t)
			if err := h.app.Config.Update(func(c *config.Config) error {
				c.Speech.Input.SpeechEndpoint = config.SpeechEndpoint{Enabled: !test.disabled, Source: "custom", BaseURL: upstream.URL + "/v1", Model: "asr"}
				c.Speech.Output.SpeechEndpoint = config.SpeechEndpoint{Enabled: true, Source: "custom", BaseURL: upstream.URL + "/v1", Model: "tts"}
				if test.limit > 0 {
					c.Speech.Limits.InputBytes = test.limit
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			body, mediaType := []byte(test.body), test.mediaType
			if test.files > 0 || test.extras > 0 {
				body, mediaType = voiceRequestBody(t, test.files, test.extras, test.mediaType, test.data)
			}
			req, err := http.NewRequest("POST", h.srv.URL+"/api/audio/"+test.path, bytes.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Content-Type", mediaType)
			resp, err := h.srv.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode != test.want {
				t.Fatalf("status=%d want=%d body=%s", resp.StatusCode, test.want, raw)
			}
		})
	}
}

func TestVoiceHTTPBoundsConcurrencyAndCancelsOwnedRequests(t *testing.T) {
	entered, left := make(chan struct{}, 4), make(chan struct{}, 4)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		entered <- struct{}{}
		<-r.Context().Done()
		left <- struct{}{}
	}))
	defer upstream.Close()
	h := newHarness(t)
	if err := h.app.Config.Update(func(c *config.Config) error {
		c.Speech.Output.SpeechEndpoint = config.SpeechEndpoint{Enabled: true, Source: "custom", BaseURL: upstream.URL + "/v1", Model: "tts"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	cancelers := []context.CancelFunc{}
	errs := make(chan error, 4)
	for i := 0; i < 4; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		cancelers = append(cancelers, cancel)
		wg.Add(1)
		go func() {
			defer wg.Done()
			req, err := http.NewRequestWithContext(ctx, "POST", h.srv.URL+"/api/audio/speech", strings.NewReader(`{"text":"answer"}`))
			if err != nil {
				errs <- err
				return
			}
			req.Header.Set("Content-Type", "application/json")
			resp, err := h.srv.Client().Do(req)
			if resp != nil {
				resp.Body.Close()
			}
			errs <- err
		}()
	}
	defer func() {
		for _, cancel := range cancelers {
			cancel()
		}
		wg.Wait()
	}()
	for i := 0; i < 4; i++ {
		select {
		case <-entered:
		case <-time.After(3 * time.Second):
			t.Fatal("voice concurrency fixture never entered upstream")
		}
	}
	h.json("POST", "/api/audio/speech", map[string]any{"text": "answer"}, 429)
	h.json("POST", "/api/audio/models", map[string]any{"kind": "input"}, 429)
	h.json("POST", "/api/audio/transcriptions", map[string]any{}, 429)
	for _, cancel := range cancelers {
		cancel()
	}
	wg.Wait()
	for i := 0; i < 4; i++ {
		if err := <-errs; err == nil {
			t.Fatal("canceled HTTP request returned success")
		}
		select {
		case <-left:
		case <-time.After(3 * time.Second):
			t.Fatal("HTTP cancellation did not reach voice upstream")
		}
	}
}
