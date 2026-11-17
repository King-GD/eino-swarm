package server_test

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/LubyRuffy/eino-swarm/internal/config"
)

func TestSpeechSettingsArePCOwnedAndPreserveWriteOnlyCredentials(t *testing.T) {
	h := newHarness(t)
	initial := h.json("GET", "/api/settings", nil, 200)["settings"].(map[string]any)
	voice, ok := initial["speech"].(map[string]any)
	if !ok {
		t.Fatal("settings omitted voice configuration")
	}
	if voice["input"].(map[string]any)["enabled"] != false || voice["output"].(map[string]any)["enabled"] != false {
		t.Fatal("voice should default off")
	}
	input := map[string]any{"enabled": true, "source": "custom", "base_url": "http://voice.invalid/v1", "model": "recognizer", "api_key": "private-voice-secret"}
	h.json("PUT", "/api/settings", map[string]any{"speech": map[string]any{"input": input}}, 200)
	delete(input, "api_key")
	input["language"] = "zh"
	h.json("PUT", "/api/settings", map[string]any{"speech": map[string]any{"input": input}}, 200)
	if got := h.app.Config.Snapshot().Speech.Input; got.APIKey != "private-voice-secret" || got.Language != "zh" || !got.Enabled {
		t.Fatalf("saved=%+v", got)
	}
	for _, path := range []string{"/api/settings", "/api/meta"} {
		resp := h.do("GET", path, nil)
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if bytes.Contains(raw, []byte("private-voice-secret")) {
			t.Fatalf("credential leaked via %s", path)
		}
	}
	meta := h.json("GET", "/api/meta", nil, 200)
	state := meta["speech"].(map[string]any)
	if state["execution"] != "pc_local" || state["input"].(map[string]any)["ready"] != true || state["output"].(map[string]any)["ready"] != false {
		t.Fatalf("state=%v", state)
	}
	input["api_key"] = ""
	h.json("PUT", "/api/settings", map[string]any{"speech": map[string]any{"input": input}}, 200)
	if h.app.Config.Snapshot().Speech.Input.APIKey != "" {
		t.Fatal("explicit clear did not remove voice key")
	}
	h.json("PUT", "/api/settings", map[string]any{"speech": map[string]any{"output": map[string]any{"source": "custom", "speed": 9}}}, 400)
	output := map[string]any{"enabled": true, "source": "custom", "base_url": "http://voice.invalid/v1", "model": "speaker", "api_key": "private-output-secret"}
	h.json("PUT", "/api/settings", map[string]any{"speech": map[string]any{"output": output, "limits": map[string]any{"input_bytes": 1024, "output_bytes": 2048, "chunk_chars": 100, "staging_ttl_seconds": 30}}}, 200)
	delete(output, "api_key")
	h.json("PUT", "/api/settings", map[string]any{"speech": map[string]any{"output": output}}, 200)
	if h.app.Config.Snapshot().Speech.Output.APIKey != "private-output-secret" {
		t.Fatal("omitted output key was not retained")
	}
	output["api_key"] = ""
	h.json("PUT", "/api/settings", map[string]any{"speech": map[string]any{"output": output}}, 200)
	if h.app.Config.Snapshot().Speech.Output.APIKey != "" || h.app.Config.Snapshot().Speech.Limits.ChunkChars != 100 {
		t.Fatal("output key clear or independent limits patch failed")
	}
}

func TestSpeechHTTPUploadsRecordingAndReturnsAudioWithoutCreatingATurn(t *testing.T) {
	var calls atomic.Int32
	audio := []byte("RIFF\x24\x00\x00\x00WAVEfmt \x10\x00\x00\x00\x01\x00\x01\x00\x80\x3e\x00\x00\x00\x7d\x00\x00\x02\x00\x10\x00data\x00\x00\x00\x00")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer voice-key" {
			t.Error("missing server-owned credential")
		}
		switch r.URL.Path {
		case "/v1/audio/transcriptions":
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Error(err)
				return
			}
			defer func() {
				if err := r.MultipartForm.RemoveAll(); err != nil {
					t.Error(err)
				}
			}()
			f, _, err := r.FormFile("file")
			if err != nil {
				t.Error(err)
				return
			}
			defer f.Close()
			raw, _ := io.ReadAll(f)
			if !bytes.Equal(raw, audio) || r.FormValue("model") != "recognizer" {
				t.Error("ASR upload changed")
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"text":"dictated words"}`)
		case "/v1/audio/speech":
			w.Header().Set("Content-Type", "audio/wav")
			_, _ = w.Write(audio)
		case "/v1/models":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"data":[{"id":"recognizer","model_type":"transcription"},{"id":"speaker","model_type":"speech"},{"id":"chat","model_type":"chat"}]}`)
		default:
			w.WriteHeader(404)
		}
	}))
	defer upstream.Close()
	h := newHarness(t)
	if err := h.app.Config.Update(func(c *config.Config) error {
		c.Speech.Input.SpeechEndpoint = config.SpeechEndpoint{Enabled: true, Source: "custom", BaseURL: upstream.URL + "/v1", APIKey: "voice-key", Model: "recognizer"}
		c.Speech.Output.SpeechEndpoint = config.SpeechEndpoint{Enabled: true, Source: "custom", BaseURL: upstream.URL + "/v1", APIKey: "voice-key", Model: "speaker"}
		c.Speech.Output.Format = "wav"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	part, err := w.CreateFormFile("file", "recording.wav")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write(audio)
	_ = w.Close()
	req, _ := http.NewRequest("POST", h.srv.URL+"/api/audio/transcriptions", &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	resp, err := h.srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !bytes.Contains(raw, []byte("dictated words")) {
		t.Fatalf("ASR status=%d body=%s", resp.StatusCode, raw)
	}
	resp = h.do("POST", "/api/audio/speech", map[string]any{"text": "answer"})
	raw, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "audio/wav" || !bytes.Equal(raw, audio) {
		t.Fatalf("TTS status=%d type=%s body=%s", resp.StatusCode, resp.Header.Get("Content-Type"), raw)
	}
	models := h.json("POST", "/api/audio/models", map[string]any{"kind": "input"}, 200)
	names, _ := json.Marshal(models["models"])
	if string(names) != `["recognizer"]` {
		t.Fatalf("audio models=%s", names)
	}
	before := calls.Load()
	if err := h.app.Config.Update(func(c *config.Config) error { c.Speech.Output.Enabled = false; return nil }); err != nil {
		t.Fatal(err)
	}
	h.json("POST", "/api/audio/speech", map[string]any{"text": "answer"}, 403)
	if calls.Load() != before {
		t.Fatal("disabled voice reached upstream")
	}
	if err := h.app.Config.Update(func(c *config.Config) error { c.Speech.Output.Source = "account"; return nil }); err != nil {
		t.Fatal(err)
	}
	denied := h.json("POST", "/api/audio/models", map[string]any{"kind": "output"}, 401)
	if denied["code"] != "speech_login_required" {
		t.Fatalf("signed-out account discovery=%v", denied["code"])
	}
	listed := h.json("GET", "/api/threads", nil, 200)
	if strings.Contains(string(mustJSON(t, listed)), "dictated words") {
		t.Fatal("dictation created durable chat content")
	}
}
