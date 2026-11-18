package provider

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LubyRuffy/eino-swarm/internal/config"
)

func imageAccountSource(t *testing.T, id, base, token string, configurable bool, defaults map[string]string) *AccountSource {
	t.Helper()
	src, err := NewAccountSource(id, "Gateway", base, token, Catalog{
		Names: []string{"chat", "z-image", "a-image", "default-image"},
		Types: map[string]string{"chat": "chat", "z-image": "image", "a-image": "image", "default-image": "image"},
	})
	if err != nil {
		t.Fatal(err)
	}
	src.SetCapabilities(roleManifest("image", "images", configurable))
	src.SetModelPolicy(configurable, defaults)
	return src
}

func encodedImage(t *testing.T, width, height int, encoder func(io.Writer, image.Image) error) []byte {
	t.Helper()
	var imageBytes bytes.Buffer
	if err := encoder(&imageBytes, image.NewGray(image.Rect(0, 0, width, height))); err != nil {
		t.Fatal(err)
	}
	return imageBytes.Bytes()
}

func imageResponse(data []byte) string {
	return `{"data":[{"b64_json":"` + base64.StdEncoding.EncodeToString(data) + `"}]}`
}

func TestImageGenerationBindsOnlyTheApprovedSourceAndKeepsExactPixels(t *testing.T) {
	const basePath = "/client/desktop/v1"
	pixels := encodedImage(t, 3, 2, png.Encode)
	var hits atomic.Int32
	var wantModel atomic.Value
	var wantToken atomic.Value
	wantToken.Store("fixture-private")
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.Method != http.MethodPost || r.URL.Path != basePath+imageGenerationPath ||
			r.Header.Get("Authorization") != "Bearer "+wantToken.Load().(string) || r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("unexpected image request %s %s", r.Method, r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body) != 3 || body["model"] != wantModel.Load() || body["prompt"] != "用户描述" || body["n"] != float64(1) {
			t.Errorf("request %+v: %v", body, err)
		}
		_, _ = io.WriteString(w, imageResponse(pixels))
	}))
	defer gateway.Close()
	cfg := config.Default()
	p := New(cfg)
	makeSource := func(editable bool, defaults map[string]string) *AccountSource {
		return imageAccountSource(t, accountTestID, gateway.URL+basePath, "fixture-private", editable, defaults)
	}
	for _, tc := range []struct {
		name, conversation, want string
		source                   *AccountSource
		bind                     config.ModelRef
	}{
		{name: "server default precedes catalog", source: makeSource(true, map[string]string{"image": "default-image"}), conversation: accountTestID, want: "default-image"},
		{name: "empty provider uses conversation default", source: makeSource(true, nil), want: "a-image"},
		{name: "stable catalog fallback", source: makeSource(true, map[string]string{"image": "wrong"}), conversation: accountTestID, want: "a-image"},
		{name: "explicit binding works with personal conversation", source: makeSource(true, nil), conversation: "personal", want: "z-image",
			bind: config.ModelRef{ProviderID: accountTestID, Model: "z-image"}},
		{name: "stale typed binding fails closed", source: makeSource(true, nil), conversation: accountTestID,
			bind: config.ModelRef{ProviderID: accountTestID, Model: "deleted"}},
		{name: "chat binding fails closed", source: makeSource(true, nil), conversation: accountTestID,
			bind: config.ModelRef{ProviderID: accountTestID, Model: "chat"}},
		{name: "a personal selection does not inherit the gateway", source: makeSource(true, nil), conversation: "personal"},
		{name: "a managed default uses the server choice", source: makeSource(false, map[string]string{"image": "default-image"}), conversation: "personal",
			want: "default-image", bind: config.ModelRef{ProviderID: accountTestID, Model: "z-image"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p.SetAccountSource(tc.source)
			cfg.Models.Bindings = nil
			if tc.bind != (config.ModelRef{}) {
				cfg.Models.Bindings = map[string]map[string]config.ModelRef{p.ModelPolicy().Scope: {"image": tc.bind}}
			}
			wantModel.Store(tc.want)
			if p.ImageAvailable(tc.conversation) != (tc.want != "") {
				t.Fatal("image tool exposure did not match the source selection")
			}
			before := hits.Load()
			got, err := p.GenerateImage(t.Context(), tc.conversation, " 用户描述 ")
			if tc.want == "" {
				if ImageErrorCode(err) != "image_unavailable" || hits.Load() != before {
					t.Fatalf("unavailable image made a request: %v", err)
				}
				return
			}
			if err != nil || hits.Load() != before+1 || !bytes.Equal(pixels, got.Data) || got.MIME != "image/png" || got.Width != 3 || got.Height != 2 {
				t.Fatalf("wrong image/media metadata %+v %v", got, err)
			}
		})
	}

	managed := makeSource(false, nil)
	managed.SetRoles([]AccountRole{{ID: "image", Enabled: true}})
	p.SetAccountSource(managed)
	wantModel.Store("image")
	if _, err := p.GenerateImage(t.Context(), "personal", "用户描述"); err != nil {
		t.Fatal(err)
	}
	disabled := makeSource(false, map[string]string{"image": "default-image"})
	disabled.SetRoles([]AccountRole{{ID: "image", Enabled: false}})
	p.SetAccountSource(disabled)
	if p.ImageAvailable(accountTestID) {
		t.Fatal("disabled fixed role fell back to its saved default")
	}
}

func TestImageAuthorizationIsIndependentOfSiblingAndLegacyCapabilities(t *testing.T) {
	const siblingID = "gateway:0123456789abcdef0123456789abcde1"
	cfg := config.Default()
	cfg.Product.Mode = config.ProductModeLocal
	p := New(cfg)
	preset := imageAccountSource(t, accountTestID, "https://gateway.invalid/client/desktop/v1", "private", true, nil)
	original := imageAccountSource(t, siblingID, "https://gateway.invalid/client/models/v1/gateway", "private", true, nil)
	preset.SetCapabilities(nil)
	p.SetAccountSource(preset)
	p.SetLocalAccountSources([]*AccountSource{original})
	if p.ImageAvailable(accountTestID) || p.ImageAvailable("personal") || !p.ImageAvailable(siblingID) {
		t.Fatal("image capabilities leaked across LOCAL sources")
	}
	cfg.Models.Bindings = map[string]map[string]config.ModelRef{config.ProductModeLocal: {"image": {ProviderID: siblingID, Model: "a-image"}}}
	if !p.ImageAvailable("personal") {
		t.Fatal("authorized explicit binding was unavailable")
	}
	catalog := p.ModelPolicy().FunctionalCatalogs[0]
	if catalog.ProviderID != siblingID || strings.Join(catalog.ImageModels, ",") != "z-image,a-image,default-image" || original.Kinds().Image != 3 {
		t.Fatalf("wrong image catalog %+v", catalog)
	}
	for _, row := range p.List() {
		if strings.Contains(row.Model, "-image") {
			t.Fatalf("image model became a conversational choice: %+v", row)
		}
	}
	p.SetAccountSource(nil)
	if p.ImageAvailable(siblingID) || p.ImageAvailable("personal") {
		t.Fatal("logout did not clear the grants")
	}
	legacy := accountSourceFor(t, "https://gateway.invalid/v1", "private")
	p.SetAccountSource(legacy)
	if p.ImageAvailable(legacy.id) {
		t.Fatal("legacy nonnegotiated catalog granted image use")
	}
	if NewMock(cfg).ImageAvailable(legacy.id) {
		t.Fatal("offline fixtures offered a real account tool")
	}
}

func TestGeneratedImageRejectsInvalidMediaBeforeReportingSuccess(t *testing.T) {
	pngData := encodedImage(t, 3, 2, png.Encode)
	wideData := encodedImage(t, 8193, 1, png.Encode)
	wrongArea := encodedImage(t, 8192, 2049, png.Encode)
	jpegData := encodedImage(t, 4, 2, func(w io.Writer, m image.Image) error { return jpeg.Encode(w, m, nil) })
	for _, tc := range []struct {
		name, body, mime string
		width, height    int
	}{
		{name: "PNG", body: imageResponse(pngData), mime: "image/png", width: 3, height: 2},
		{name: "JPEG", body: imageResponse(jpegData), mime: "image/jpeg", width: 4, height: 2},
		{name: "WebP", body: `{"data":[{"b64_json":"UklGRiQAAABXRUJQVlA4IBgAAAAwAQCdASoDAAIAAgA0JaQAA3AA/vv9UAA="}]}`, mime: "image/webp", width: 3, height: 2},
		{name: "malformed JSON", body: `{"data":[`},
		{name: "missing result", body: `{"data":[]}`},
		{name: "unexpected multiple results", body: `{"data":[{},{}]}`},
		{name: "URL only", body: `{"data":[{"url":"https://untrusted.invalid/image"}]}`},
		{name: "invalid base64", body: `{"data":[{"b64_json":"%notbase64"}]}`},
		{name: "unrecognized format", body: imageResponse([]byte("<html>x</html>"))},
		{name: "truncated after valid header", body: imageResponse(pngData[:len(pngData)-20])},
		{name: "oversized dimensions", body: imageResponse(wideData)},
		{name: "oversized pixel count", body: imageResponse(wrongArea)},
		{name: "oversized decoded bytes", body: imageResponse(make([]byte, maxGeneratedImageBytes+1))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := decodeGeneratedImage([]byte(tc.body))
			if tc.mime == "" {
				if ImageErrorCode(err) != "image_response_invalid" || got.Data != nil {
					t.Fatalf("accepted invalid media: got %+v, err %v", got, err)
				}
				return
			}
			if err != nil || got.MIME != tc.mime || got.Width != tc.width || got.Height != tc.height {
				t.Fatalf("invalid pixel metadata: got %+v, err %v", got, err)
			}
		})
	}
}

func TestImageGenerationFailsWithoutRetryOrLeakingUpstreamBody(t *testing.T) {
	for _, tc := range []struct {
		name           string
		status         int
		body, wantCode string
	}{
		{name: "authentication", status: 401, body: "private upstream", wantCode: "image_forbidden"},
		{name: "authorization", status: 403, body: "private upstream", wantCode: "image_forbidden"},
		{name: "upstream failure", status: 503, body: "private upstream", wantCode: "image_request_failed"},
		{name: "redirect", status: 307, body: "private upstream", wantCode: "image_request_failed"},
		{name: "oversized body", status: 200, body: strings.Repeat("x", maxImageResponseBytes+1), wantCode: "image_response_invalid"},
		{name: "invalid body", status: 200, body: "private upstream", wantCode: "image_response_invalid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var hits atomic.Int32
			other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("followed provider redirect") }))
			defer other.Close()
			gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				w.Header().Set("Location", other.URL)
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer gateway.Close()
			p := New(config.Default())
			var refreshes atomic.Int32
			p.SetCapabilitiesTrigger(func() { refreshes.Add(1) })
			src := imageAccountSource(t, accountTestID, gateway.URL, "fixture-private", true, nil)
			p.SetAccountSource(src)
			_, err := p.GenerateImage(t.Context(), accountTestID, "fixture")
			if ImageErrorCode(err) != tc.wantCode || hits.Load() != 1 || strings.Contains(err.Error(), "private") {
				t.Fatalf("got %v after %d requests", err, hits.Load())
			}
			rejection := tc.wantCode == "image_forbidden"
			if (refreshes.Load() == 1) != rejection || p.ImageAvailable(accountTestID) == rejection {
				t.Fatalf("rejection discovery state: %d %v", refreshes.Load(), p.ImageAvailable(accountTestID))
			}
		})
	}
}

func TestImageGenerationRechecksLiveGrantAcrossCredentialChange(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusUnauthorized} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			entered, unblock := make(chan struct{}), make(chan struct{})
			pixels := encodedImage(t, 3, 2, png.Encode)
			gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				close(entered)
				<-unblock
				w.WriteHeader(status)
				_, _ = io.WriteString(w, imageResponse(pixels))
			}))
			defer gateway.Close()
			p := New(config.Default())
			p.SetAccountSource(imageAccountSource(t, accountTestID, gateway.URL, "old", true, nil))
			result := make(chan error, 1)
			go func() {
				_, err := p.GenerateImage(t.Context(), accountTestID, "fixture")
				result <- err
			}()
			<-entered
			p.SetAccountSource(imageAccountSource(t, accountTestID, gateway.URL, "new", true, nil))
			close(unblock)
			want := "image_unavailable"
			if status == http.StatusUnauthorized {
				want = "image_forbidden"
			}
			if err := <-result; ImageErrorCode(err) != want {
				t.Fatalf("late result kept old grant: %v", err)
			}
			if !p.ImageAvailable(accountTestID) {
				t.Fatal("old refusal cleared replacement account")
			}
		})
	}
}

func TestImageGenerationCancellationAndTimeoutPropagateToRequest(t *testing.T) {
	for _, tc := range []struct {
		wait  bool
		code  string
		cause error
	}{
		{wait: false, code: "image_cancelled", cause: context.Canceled},
		{wait: true, code: "image_timeout", cause: context.DeadlineExceeded},
	} {
		t.Run(tc.code, func(t *testing.T) {
			entered := make(chan struct{})
			cancelled := make(chan struct{})
			gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				close(entered)
				<-r.Context().Done()
				close(cancelled)
			}))
			defer gateway.Close()
			p := New(config.Default())
			p.SetAccountSource(imageAccountSource(t, accountTestID, gateway.URL, "private", true, nil))
			ctx, stop := context.WithCancel(t.Context())
			defer stop()
			if tc.wait {
				deadline, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
				defer cancel()
				ctx = deadline
			}
			result := make(chan error, 1)
			go func() {
				_, err := p.GenerateImage(ctx, accountTestID, "fixture")
				result <- err
			}()
			<-entered
			if !tc.wait {
				stop()
			}
			if err := <-result; ImageErrorCode(err) != tc.code || !errors.Is(err, tc.cause) {
				t.Fatalf("wrong cause: %v", err)
			}
			select {
			case <-cancelled:
			case <-time.After(2 * time.Second):
				t.Fatal("request stayed open after cancellation")
			}
		})
	}
}

func TestImagePromptRefusesEmptyOversizedAndInvalidUTF8(t *testing.T) {
	for _, prompt := range []string{" ", strings.Repeat("画", 1366), "\xff", "embedded\x00null"} {
		if _, err := ValidateImagePrompt(prompt); ImageErrorCode(err) != "image_invalid_prompt" {
			t.Fatalf("accepted invalid prompt bytes: len=%d %v", len(prompt), err)
		}
	}
	if prompt, err := ValidateImagePrompt("画"); err != nil || prompt != "画" {
		t.Fatalf("rejected valid UTF-8: %v", err)
	}
}

func TestImageGenerationRefusesBeforeHTTPWhenAdmissionCannotStart(t *testing.T) {
	var hits atomic.Int32
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer gateway.Close()
	p := New(config.Default())
	p.SetAccountSource(imageAccountSource(t, accountTestID, gateway.URL, "private", true, nil))

	ctx, stop := context.WithCancel(t.Context())
	stop()
	if _, err := p.GenerateImage(ctx, accountTestID, "fixture"); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-cancelled request: %v", err)
	}
	if _, err := p.GenerateImage(t.Context(), accountTestID, " "); ImageErrorCode(err) != "image_invalid_prompt" {
		t.Fatalf("invalid prompt passed admission: %v", err)
	}
	var missing *Pool
	if _, err := missing.GenerateImage(t.Context(), accountTestID, "fixture"); ImageErrorCode(err) != "image_unavailable" {
		t.Fatalf("missing pool passed admission: %v", err)
	}
	release, ok := p.TryHoldModelPolicy()
	if !ok {
		t.Fatal("could not hold model policy")
	}
	if _, err := p.GenerateImage(t.Context(), accountTestID, "fixture"); ImageErrorCode(err) != "image_unavailable" {
		t.Errorf("held policy admitted image request: %v", err)
	}
	release()

	for _, base := range []string{"https://gateway.invalid/v1?key=private", "mailto:fixture", gateway.URL + "/fixture#fragment"} {
		p.SetAccountSource(imageAccountSource(t, accountTestID, base, "private", true, nil))
		if _, err := p.GenerateImage(t.Context(), accountTestID, "fixture"); ImageErrorCode(err) != "image_unavailable" {
			t.Errorf("unsafe image base passed admission: %v", err)
		}
	}
	if hits.Load() != 0 {
		t.Fatalf("pre-admission refusal made %d HTTP requests", hits.Load())
	}
}

func TestImageGenerationInterruptedHTTPBodyAndTransportHaveSafeFailures(t *testing.T) {
	for _, tc := range []struct {
		name        string
		closeServer bool
		want        string
	}{
		{name: "truncated body", want: "image_response_invalid"},
		{name: "unreachable transport", closeServer: true, want: "image_request_failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Length", "1000")
				_, _ = io.WriteString(w, `{"data":[`)
			}))
			defer gateway.Close()
			p := New(config.Default())
			p.SetAccountSource(imageAccountSource(t, accountTestID, gateway.URL, "private", true, nil))
			if tc.closeServer {
				gateway.Close()
			}
			if _, err := p.GenerateImage(t.Context(), accountTestID, "fixture"); ImageErrorCode(err) != tc.want {
				t.Fatalf("request error: %v", err)
			}
		})
	}
}

func TestLocalImageRefusalInvalidatesOnlyItsSource(t *testing.T) {
	const siblingID = "gateway:0123456789abcdef0123456789abcde1"
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusForbidden) }))
	defer gateway.Close()
	cfg := config.Default()
	cfg.Product.Mode = config.ProductModeLocal
	p := New(cfg)
	preset := imageAccountSource(t, accountTestID, gateway.URL+"/client/desktop/v1", "private", true, nil)
	original := imageAccountSource(t, siblingID, gateway.URL+"/client/models/v1/gateway", "private", true, nil)
	p.SetAccountSource(preset)
	p.SetLocalAccountSources([]*AccountSource{original})
	if _, err := p.GenerateImage(t.Context(), siblingID, "fixture"); ImageErrorCode(err) != "image_forbidden" {
		t.Fatalf("refusal: %v", err)
	}
	if p.ImageAvailable(siblingID) || !p.ImageAvailable(accountTestID) {
		t.Fatal("refusal removed the wrong grant")
	}
	if ImageErrorCode(nil) != "" {
		t.Fatal("success unexpectedly has an error code")
	}
}
