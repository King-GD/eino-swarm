package provider

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LubyRuffy/eino-swarm/internal/config"
	"github.com/cloudwego/eino/schema"
)

func TestImageOCRModelUsesTheOCRBindingAndItsOwnEndpoint(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Model != "reader-fixture" || r.URL.Path != "/v1/responses" {
			t.Errorf("OCR used model %q on %s, decode error %v", body.Model, r.URL.Path, err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"recognized"}]}]}`))
	}))
	t.Cleanup(upstream.Close)
	pool := New(config.Default())
	source := accountSourceFor(t, upstream.URL+"/v1", "fixture-token")
	source.SetModelPolicy(false, map[string]string{"conversation": "chat-fixture", "ocr": "reader-fixture"})
	source.SetCapabilities(&SourceCapabilities{
		Authorized: true,
		Available:  true,
		Capabilities: map[string]RoleCapability{
			"conversation": {Supported: true, Available: true, Endpoints: []string{"chat_completions"}},
			"ocr":          {Supported: true, Available: true, Endpoints: []string{"responses"}},
		},
	})
	pool.SetAccountSource(source)
	ocr, err := pool.ImageOCRModel(t.Context(), source.ID())
	if err != nil {
		t.Fatal(err)
	}
	out, err := ocr.Generate(t.Context(), []*schema.Message{schema.UserMessage("fixture")})
	if err != nil || out == nil || out.Content != "recognized" {
		t.Fatalf("OCR response = %+v, %v", out, err)
	}
}
