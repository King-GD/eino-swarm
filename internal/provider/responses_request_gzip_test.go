package provider

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LubyRuffy/eino-swarm/internal/config"
	"github.com/cloudwego/eino/schema"
)

// A live Gateway grant can change while a conversation keeps its model copy.
// The upload encoding follows that exact source without changing its request.
func TestResponsesUploadUsesCurrentGatewayGzipCapability(t *testing.T) {
	type request struct {
		encoding string
		size     int
		raw      []byte
	}
	requests := make(chan request, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" || r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("request path or media type changed")
		}
		encoding := r.Header.Get("Content-Encoding")
		wire, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		raw := wire
		if encoding == "gzip" {
			z, err := gzip.NewReader(bytes.NewReader(wire))
			if err != nil {
				t.Error(err)
				return
			}
			defer z.Close()
			raw, err = io.ReadAll(z)
			if err != nil {
				t.Error(err)
				return
			}
		}
		requests <- request{encoding: encoding, size: len(wire), raw: raw}
		var body struct{ Stream bool }
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Error(err)
			return
		}
		const completed = "{\"id\":\"resp_probe\",\"status\":\"completed\",\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"answer\"}]}]}"
		if body.Stream {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"answer\"}\n\n")
			w.(http.Flusher).Flush()
			fmt.Fprintf(w, "data: {\"type\":\"response.completed\",\"response\":%s}\n\n", completed)
			return
		}
		fmt.Fprint(w, completed)
	}))
	defer server.Close()
	pool := New(config.Default())
	source := accountSourceFor(t, server.URL+"/v1", "fixture-token")
	publish := func(enabled bool) {
		t.Helper()
		const manifest = `
		{"data":{"version":1,"model_roles_version":3,"sources":{
			"preset":{"authorized":true,"available":true,%s"capabilities":{
				"conversation":{"supported":true,"available":true,"configurable":true,"endpoints":["responses"],
					"responses_modes":["native"],"responses_developer_history":1}
			}},
			"gateway":{}
		}}}
		`
		field := ""
		if enabled {
			field = `"responses_request_gzip":true,`
		}
		capabilities, err := ParseCapabilities([]byte(fmt.Sprintf(manifest, field)))
		if err != nil {
			t.Fatal(err)
		}
		copy := *source
		copy.SetCapabilities(capabilities.Sources["preset"])
		pool.SetAccountSource(&copy)
	}
	publish(false)
	ctx := t.Context()
	shared, err := pool.Get(ctx, source.id, "server-model")
	if err != nil {
		t.Fatal(err)
	}
	// The same wrapper chain is used by a Gateway conversation planner.
	manager := WithoutBuiltinTools(WithResponsesContinuation(shared, &ResponsesContinuation{}))
	large := strings.Repeat("Repeated conversation context. ", 512)
	input := []*schema.Message{schema.UserMessage(large)}
	generate := func() request {
		t.Helper()
		out, err := manager.Generate(ctx, input)
		if err != nil || out == nil || out.Content != "answer" {
			t.Fatalf("generate = %v, %v", out, err)
		}
		return <-requests
	}

	legacy := generate()
	if legacy.encoding != "" || legacy.size != len(legacy.raw) {
		t.Fatalf("legacy request used %q", legacy.encoding)
	}
	publish(true)
	compressed := generate()
	if compressed.encoding != "gzip" || compressed.size >= legacy.size || !bytes.Equal(compressed.raw, legacy.raw) {
		t.Fatalf("gzip request encoding=%q, bytes=%d, plain=%d, decoded exact=%v",
			compressed.encoding, compressed.size, legacy.size, bytes.Equal(compressed.raw, legacy.raw))
	}
	t.Logf("Responses fixture request bytes: %d JSON, %d gzip", legacy.size, compressed.size)

	stream, err := manager.Stream(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	parts, err := responsesReadAll(t, stream)
	if err != nil || len(parts) == 0 || parts[0].Content != "answer" || (<-requests).encoding != "gzip" {
		t.Fatalf("streaming gzip request failed: %v", err)
	}

	out, err := manager.Generate(ctx, []*schema.Message{schema.UserMessage("short")})
	if err != nil || out == nil || (<-requests).encoding != "" {
		t.Fatalf("small request unexpectedly compressed: %v", err)
	}

	localConfig := config.Default()
	localConfig.Product.Mode = config.ProductModeLocal
	localConfig.Models.Providers = []config.Provider{
		{ID: "personal", BaseURL: server.URL + "/v1", Model: "server-model", APIMode: config.APIModeResponses},
	}
	personal, err := New(localConfig).Get(ctx, "personal", "server-model")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := personal.Generate(ctx, input); err != nil {
		t.Fatal(err)
	}
	if got := <-requests; got.encoding != "" {
		t.Fatalf("personal endpoint borrowed the Gateway capability: %q", got.encoding)
	}

	publish(false)
	reverted := generate()
	if reverted.encoding != "" || !bytes.Equal(reverted.raw, legacy.raw) {
		t.Fatalf("retained model kept its old encoding %q", reverted.encoding)
	}
}
