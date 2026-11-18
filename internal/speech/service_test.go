package speech

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LubyRuffy/eino-swarm/internal/config"
	"github.com/LubyRuffy/eino-swarm/internal/provider"
)

// wavFixture is a complete 16-bit mono PCM recording, not just magic bytes.
func wavFixture() []byte {
	var data bytes.Buffer
	data.WriteString("RIFF")
	_ = binary.Write(&data, binary.LittleEndian, uint32(36+320))
	data.WriteString("WAVEfmt ")
	_ = binary.Write(&data, binary.LittleEndian, uint32(16))
	for _, value := range []uint16{1, 1} {
		_ = binary.Write(&data, binary.LittleEndian, value)
	}
	for _, value := range []uint32{16000, 32000} {
		_ = binary.Write(&data, binary.LittleEndian, value)
	}
	for _, value := range []uint16{2, 16} {
		_ = binary.Write(&data, binary.LittleEndian, value)
	}
	data.WriteString("data")
	_ = binary.Write(&data, binary.LittleEndian, uint32(320))
	data.Write(make([]byte, 320))
	return data.Bytes()
}

func configuredSpeech(t *testing.T, endpoint string) (*config.Config, *Service) {
	t.Helper()
	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Update(func(next *config.Config) error {
		next.Product.Mode = config.ProductModeLocal
		next.Speech.Input.Enabled, next.Speech.Output.Enabled = true, true
		next.Speech.Input.Source, next.Speech.Output.Source = config.SpeechSourceCustom, config.SpeechSourceCustom
		next.Speech.Input.BaseURL, next.Speech.Output.BaseURL = endpoint, endpoint
		next.Speech.Input.Model, next.Speech.Output.Model = "recognizer", "speaker"
		next.Speech.Input.APIKey, next.Speech.Output.APIKey = "private-key", "private-key"
		next.Speech.Output.Format = "wav"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return cfg, New(cfg, provider.New(cfg))
}

func TestVoiceSnapshotKeepsTheSelectedInputDevice(t *testing.T) {
	cfg, svc := configuredSpeech(t, "http://127.0.0.1:9/v1")
	if err := cfg.Update(func(next *config.Config) error {
		next.Speech.Input.DeviceID = "camera-mic"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if svc.Snapshot().Input.DeviceID != "camera-mic" {
		t.Fatalf("snapshot dropped the selected microphone: %+v", svc.Snapshot().Input)
	}
}

func TestRecordedBytesReachASRAndSynthesizedBytesStayUnchanged(t *testing.T) {
	wav := wavFixture()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer private-key" {
			t.Error("missing credential")
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
			file, header, err := r.FormFile("file")
			if err != nil {
				t.Error(err)
				return
			}
			defer file.Close()
			got, _ := io.ReadAll(file)
			if !bytes.Equal(got, wav) || header.Header.Get("Content-Type") != "audio/wav" || r.FormValue("model") != "recognizer" || r.FormValue("response_format") != "json" {
				t.Error("recording/model changed on upload")
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"text":"recognized words"}`)
		case "/v1/audio/speech":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["input"] != "answer text" || body["model"] != "speaker" || body["response_format"] != "wav" {
				t.Errorf("speech request=%v", body)
			}
			w.Header().Set("Content-Type", "audio/wav")
			_, _ = w.Write(wav)
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer upstream.Close()
	_, svc := configuredSpeech(t, upstream.URL+"/v1")
	text, err := svc.Transcribe(context.Background(), Audio{Filename: "capture.wav", MIME: "audio/wav", Data: wav})
	if err != nil || text != "recognized words" {
		t.Fatalf("transcription=%q err=%v", text, err)
	}
	audio, err := svc.Synthesize(context.Background(), "answer text")
	if err != nil || audio.MIME != "audio/wav" || !bytes.Equal(audio.Data, wav) {
		t.Fatalf("audio bytes changed: %+v %v", audio, err)
	}
}

func TestSpeechSnapshotHasNoCredentialsOrLocalEndpoint(t *testing.T) {
	cfg, svc := configuredSpeech(t, "http://private-endpoint.invalid/v1")
	view := svc.Snapshot()
	if view.Execution != "pc_local" || !view.Input.Ready || !view.Output.Ready {
		t.Fatalf("view=%+v", view)
	}
	raw, _ := json.Marshal(view)
	for _, secret := range []string{"private-key", "private-endpoint"} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("snapshot leaked %q", secret)
		}
	}
	if err := cfg.Update(func(next *config.Config) error { next.Speech.Input.Enabled = false; return nil }); err != nil {
		t.Fatal(err)
	}
	if view = svc.Snapshot(); view.Input.Ready || !view.Output.Ready {
		t.Fatal("independent switch was ignored")
	}
}

func TestVoiceRequestCancellationAndConfigurationChangeReachUpstream(t *testing.T) {
	for _, change := range []bool{false, true} {
		t.Run(map[bool]string{false: "caller", true: "configuration"}[change], func(t *testing.T) {
			entered, left := make(chan struct{}), make(chan struct{})
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				close(entered)
				<-r.Context().Done()
				close(left)
			}))
			defer upstream.Close()
			cfg, svc := configuredSpeech(t, upstream.URL+"/v1")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() { _, err := svc.Synthesize(ctx, "answer"); done <- err }()
			select {
			case <-entered:
			case <-time.After(2 * time.Second):
				t.Fatal("upstream not called")
			}
			if change {
				if err := cfg.Update(func(next *config.Config) error { next.Speech.Output.Enabled = false; return nil }); err != nil {
					t.Fatal(err)
				}
			} else {
				cancel()
			}
			select {
			case err := <-done:
				if err == nil || (!change && !errors.Is(err, context.Canceled)) {
					t.Fatalf("cancellation=%v", err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("request did not cancel")
			}
			select {
			case <-left:
			case <-time.After(2 * time.Second):
				t.Fatal("upstream did not observe cancellation")
			}
		})
	}
}
