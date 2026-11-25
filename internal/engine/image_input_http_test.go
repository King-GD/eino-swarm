package engine

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/LubyRuffy/eino-swarm/internal/provider"
	"github.com/LubyRuffy/eino-swarm/internal/store"
)

const imageConversationModel = "conversation-fixture"
const imageOCRModel = "ocr-fixture"

func imageInputAccount(t *testing.T, baseURL string, ocrAllowed bool) *Engine {
	t.Helper()
	e := newAccountEngine(t, baseURL, imageConversationModel, imageOCRModel)
	src := accountCatalog(t, baseURL, imageConversationModel, imageOCRModel)
	src.SetModelPolicy(false, map[string]string{"conversation": imageConversationModel, "ocr": imageOCRModel})
	src.SetCapabilities(&provider.SourceCapabilities{
		Authorized: true,
		Available:  true,
		Capabilities: map[string]provider.RoleCapability{
			"conversation": {Supported: true, Available: true, Endpoints: []string{"chat_completions"}},
			"ocr":          {Supported: true, Available: ocrAllowed, Endpoints: []string{"chat_completions"}},
		},
	})
	e.pool.SetAccountSource(src)
	return e
}

func TestQueuedImageReachesTheConversationModelWithOCRConfigured(t *testing.T) {
	var mu sync.Mutex
	var bodies [][]byte
	firstRequest := make(chan struct{})
	releaseFirst := make(chan struct{})
	release := sync.OnceFunc(func() { close(releaseFirst) })
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		mu.Lock()
		bodies = append(bodies, raw)
		first := len(bodies) == 1
		mu.Unlock()
		if first {
			close(firstRequest)
			select {
			case <-releaseFirst:
			case <-r.Context().Done():
				return
			}
		}
		writeCheckedCompletion(w, raw)
	}))
	t.Cleanup(srv.Close)
	defer release()
	e := imageInputAccount(t, srv.URL+"/v1", true)
	th, err := e.CreateThread("Vision input", accountCatalogID, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.StartTurn(th.ID, "Start with the text."); err != nil {
		t.Fatal(err)
	}
	select {
	case <-firstRequest:
	case <-time.After(5 * time.Second):
		t.Fatal("the preceding turn made no model request")
	}

	const caption = "Describe the attached scene."
	pixels := generatedPNG(t)
	receipt, err := e.DeliverComposerInput(th.ID, DeliveryQueue, "", "", UserInput{
		Text: caption, Images: []ImageInput{{Name: "scene.png", MIME: "image/png", Data: pixels}},
	})
	if err != nil || receipt.Followup == nil || len(receipt.Followup.Images) != 1 {
		t.Fatalf("image was not queued with the follow-up: %+v, %v", receipt, err)
	}
	release()
	turns := waitForTurnCount(t, e, th.ID, 2)
	waitTurn(t, e, turns[1].ID)
	queuedEnd := len(snapshotBodies(&mu, &bodies))

	// A later turn must see the original image through SQLite and input-file
	// replay, even when the user does not attach it again.
	next, err := e.StartTurn(th.ID, "Consider the same image again.")
	if err != nil {
		t.Fatal(err)
	}
	waitTurn(t, e, next.ID)
	requests := snapshotBodies(&mu, &bodies)
	type message struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}
	var queuedImage, replayedImage bool
	wantURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(pixels)
	for i, raw := range requests {
		var request struct {
			Model    string    `json:"model"`
			Messages []message `json:"messages"`
		}
		if err := json.Unmarshal(raw, &request); err != nil {
			t.Fatal(err)
		}
		if request.Model != imageConversationModel {
			t.Errorf("request %d used %q, want %q", i+1, request.Model, imageConversationModel)
		}
		for _, msg := range request.Messages {
			if msg.Role != "user" || len(msg.Content) == 0 || msg.Content[0] != '[' {
				continue
			}
			var parts []struct {
				Type     string `json:"type"`
				Text     string `json:"text"`
				ImageURL struct {
					URL string `json:"url"`
				} `json:"image_url"`
			}
			if err := json.Unmarshal(msg.Content, &parts); err != nil {
				t.Fatal(err)
			}
			var textFound, imageFound bool
			for _, part := range parts {
				textFound = textFound || (part.Type == "text" && part.Text == caption)
				imageFound = imageFound || (part.Type == "image_url" && part.ImageURL.URL == wantURL)
			}
			if textFound && imageFound {
				queuedImage = queuedImage || i < queuedEnd
				replayedImage = replayedImage || i >= queuedEnd
			}
		}
	}
	if !queuedImage || !replayedImage {
		t.Fatalf("original image and caption must reach both turns: queued=%t replayed=%t", queuedImage, replayedImage)
	}

	messages, err := e.store.ListMessages(th.ID)
	if err != nil {
		t.Fatal(err)
	}
	var imageRefs []store.ImageRef
	for _, msg := range messages {
		if msg.TurnID == turns[1].ID && msg.Role == "user" && msg.Content == caption {
			imageRefs = msg.Images
		}
	}
	if len(imageRefs) != 1 || imageRefs[0] != receipt.Followup.Images[0] || imageRefs[0].MIME != "image/png" {
		t.Fatalf("the original attachment must stay on the sent message: %+v", imageRefs)
	}
	stored, mime, err := e.ReadInputImage(th.ID, imageRefs[0].ID)
	if err != nil || mime != "image/png" || !bytes.Equal(stored, pixels) {
		t.Fatalf("stored image MIME=%q, unchanged=%t, error=%v", mime, bytes.Equal(stored, pixels), err)
	}
	decoded, err := png.Decode(bytes.NewReader(stored))
	if err != nil {
		t.Fatalf("decode stored image: %v", err)
	}
	if size := decoded.Bounds().Size(); size.X != 3 || size.Y != 2 {
		t.Fatalf("stored image dimensions=%v, want 3x2", size)
	}
}

func TestGatewayConversationAcceptsImagesWithoutOCRPermission(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		writeCheckedCompletion(w, raw)
	}))
	t.Cleanup(srv.Close)
	e := imageInputAccount(t, srv.URL+"/v1", false)
	th, err := e.CreateThread("Image permission", accountCatalogID, "")
	if err != nil {
		t.Fatal(err)
	}
	turn, err := e.StartTurnInput(th.ID, UserInput{
		Images: []ImageInput{{MIME: "image/png", Data: generatedPNG(t)}},
	})
	if err != nil {
		t.Fatalf("conversation image was refused without OCR permission: %v", err)
	}
	waitTurn(t, e, turn.ID)
}
