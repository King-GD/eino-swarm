package engine

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/LubyRuffy/eino-swarm/internal/provider"
	"github.com/LubyRuffy/eino-swarm/internal/store"
)

func TestExplicitOCRTranscribesOnlyTheSelectedStoredImage(t *testing.T) {
	pixels := generatedPNG(t)
	imageURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(pixels)
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var body struct {
			Model    string            `json:"model"`
			Messages []json.RawMessage `json:"messages"`
			Tools    []json.RawMessage `json:"tools"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if body.Model != imageOCRModel || len(body.Messages) != 2 || len(body.Tools) != 0 {
			t.Errorf("expected a single tool-free OCR request, model=%q messages=%d tools=%d", body.Model, len(body.Messages), len(body.Tools))
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var input struct {
			Role    string `json:"role"`
			Content []struct {
				Type     string `json:"type"`
				ImageURL struct {
					URL string `json:"url"`
				} `json:"image_url"`
			} `json:"content"`
		}
		if err := json.Unmarshal(body.Messages[1], &input); err != nil || input.Role != "user" ||
			len(input.Content) != 1 || input.Content[0].Type != "image_url" || input.Content[0].ImageURL.URL != imageURL {
			t.Errorf("OCR input must contain only the selected original image: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"ocr","choices":[{"index":0,"message":{"role":"assistant","content":"  first line\nsecond line"},"finish_reason":"stop"}]}`))
	}))
	t.Cleanup(srv.Close)
	e := imageInputAccount(t, srv.URL+"/v1", true)
	th, err := e.CreateThread("OCR", accountCatalogID, "")
	if err != nil {
		t.Fatal(err)
	}
	other, err := e.CreateThread("Other", accountCatalogID, "")
	if err != nil {
		t.Fatal(err)
	}
	refs, err := e.saveInputImages(th.ID, []ImageInput{
		{MIME: "image/png", Data: tinyPNG},
		{MIME: "image/png", Data: pixels},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.ExtractImageText(t.Context(), other.ID, refs[1].ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("cross-conversation OCR returned %v", err)
	}
	text, err := e.ExtractImageText(t.Context(), th.ID, refs[1].ID)
	if err != nil || text != "  first line\nsecond line" || calls.Load() != 1 {
		t.Fatalf("OCR = %q, %v, calls=%d", text, err, calls.Load())
	}
	if turns, err := e.Store().ListTurns(th.ID); err != nil || len(turns) != 0 {
		t.Fatalf("extracting text created conversation turns: %v, %v", turns, err)
	}
	if events, err := e.Store().ListEvents(th.ID, 0, 0); err != nil || len(events) != 0 {
		t.Fatalf("extracting text created conversation events: %v, %v", events, err)
	}
}

func TestExplicitOCRRequiresTheOCRGrant(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("missing OCR grant must not contact either model")
		w.WriteHeader(http.StatusForbidden)
	}))
	t.Cleanup(srv.Close)
	e := imageInputAccount(t, srv.URL+"/v1", false)
	th, err := e.CreateThread("OCR denied", accountCatalogID, "")
	if err != nil {
		t.Fatal(err)
	}
	refs, err := e.saveInputImages(th.ID, []ImageInput{{MIME: "image/png", Data: generatedPNG(t)}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.ExtractImageText(t.Context(), th.ID, refs[0].ID); !errors.Is(err, provider.ErrModelPolicy) {
		t.Fatalf("extracting without an OCR grant returned %v", err)
	}
}
