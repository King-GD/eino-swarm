package provider

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/LubyRuffy/eino-swarm/internal/config"
	_ "golang.org/x/image/webp"
)

const (
	imageRole              = "image"
	imageGenerationPath    = "/images/generations"
	maxImagePromptBytes    = 4096
	maxGeneratedImageBytes = 8 << 20
	maxGeneratedImageSide  = 8192
	maxGeneratedPixels     = 16 << 20
	// Reserve space for the bounded JSON envelope as well as the encoded pixels.
	maxImageResponseBytes = (maxGeneratedImageBytes+2)/3*4 + (64 << 10)
)

var (
	errImageUnavailable = errors.New("image_unavailable")
	errImagePrompt      = errors.New("image_invalid_prompt")
	errImageResponse    = errors.New("image_response_invalid")
	errImageForbidden   = errors.New("image_forbidden")
	errImageRequest     = errors.New("image_request_failed")
)

// GeneratedImage keeps the gateway's original bytes and measured raster metadata.
type GeneratedImage struct {
	Data          []byte
	MIME          string
	Width, Height int
}

// ValidateImagePrompt returns the trimmed prompt the gateway accepts.
func ValidateImagePrompt(prompt string) (string, error) {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" || len(prompt) > maxImagePromptBytes || strings.ContainsRune(prompt, 0) || !utf8.ValidString(prompt) {
		return "", errImagePrompt
	}
	return prompt, nil
}

// ImageErrorCode reports only a stable reason safe for the transcript.
func ImageErrorCode(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, context.Canceled):
		return "image_cancelled"
	case errors.Is(err, context.DeadlineExceeded):
		return "image_timeout"
	case errors.Is(err, errImageUnavailable):
		return errImageUnavailable.Error()
	case errors.Is(err, errImagePrompt):
		return errImagePrompt.Error()
	case errors.Is(err, errImageResponse):
		return errImageResponse.Error()
	case errors.Is(err, errImageForbidden):
		return errImageForbidden.Error()
	default:
		return errImageRequest.Error()
	}
}

// ImageAvailable reports whether a new call can be offered for this conversation.
func (p *Pool) ImageAvailable(conversationProviderID string) bool {
	source, _ := p.imageTarget(conversationProviderID)
	return source != nil
}

// GenerateImage makes one cancellable, non-streaming gateway call. Credentials
// are sampled from the live source and the client never follows a redirect.
func (p *Pool) GenerateImage(ctx context.Context, conversationProviderID, prompt string) (GeneratedImage, error) {
	prompt, err := ValidateImagePrompt(prompt)
	if err != nil {
		return GeneratedImage{}, err
	}
	if err = ctx.Err(); err != nil {
		return GeneratedImage{}, err
	}
	if p == nil || p.cfg == nil {
		return GeneratedImage{}, errImageUnavailable
	}
	release, err := p.BeginModelUse()
	if err != nil {
		return GeneratedImage{}, errImageUnavailable
	}
	defer release()

	source, model := p.imageTarget(conversationProviderID)
	if source == nil {
		return GeneratedImage{}, errImageUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, config.DefaultRequestTimeout)
	defer cancel()

	req, err := imageRequest(ctx, source, model, prompt)
	if err != nil {
		return GeneratedImage{}, err
	}
	if !p.currentImageGrant(source, model) {
		return GeneratedImage{}, errImageUnavailable
	}
	client := http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return GeneratedImage{}, ctx.Err()
		}
		return GeneratedImage{}, errImageRequest
	}
	defer resp.Body.Close()
	if ctx.Err() != nil {
		return GeneratedImage{}, ctx.Err()
	}
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		p.refreshRejectedImageCapability(source)
		return GeneratedImage{}, fmt.Errorf("%w (HTTP %d)", errImageForbidden, resp.StatusCode)
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		return GeneratedImage{}, fmt.Errorf("%w (HTTP %d)", errImageRequest, resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxImageResponseBytes+1))
	if ctx.Err() != nil {
		return GeneratedImage{}, ctx.Err()
	}
	if err != nil || len(raw) > maxImageResponseBytes {
		return GeneratedImage{}, errImageResponse
	}
	out, err := decodeGeneratedImage(raw)
	if ctx.Err() != nil {
		return GeneratedImage{}, ctx.Err()
	}
	if !p.currentImageGrant(source, model) {
		return GeneratedImage{}, errImageUnavailable
	}
	return out, err
}

func imageRequest(ctx context.Context, source *AccountSource, model, prompt string) (*http.Request, error) {
	u, err := url.Parse(source.baseURL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil ||
		u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" {
		return nil, errImageUnavailable
	}
	body, err := json.Marshal(struct {
		Model  string `json:"model"`
		Prompt string `json:"prompt"`
		N      int    `json:"n"`
	}{Model: model, Prompt: prompt, N: 1})
	if err != nil {
		return nil, errImageRequest
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.JoinPath(imageGenerationPath).String(), bytes.NewReader(body))
	if err != nil {
		return nil, errImageUnavailable
	}
	// A submitted generation can have billing effects. Disallow automatic
	// body replay even when a pooled connection fails.
	req.GetBody = nil
	req.Header.Set("Authorization", "Bearer "+source.token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}

func decodeGeneratedImage(raw []byte) (GeneratedImage, error) {
	var envelope struct {
		Data []struct {
			Base64 string `json:"b64_json"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &envelope) != nil || len(envelope.Data) != 1 {
		return GeneratedImage{}, errImageResponse
	}
	encoded := envelope.Data[0].Base64
	if len(encoded) == 0 || len(encoded) > base64.StdEncoding.EncodedLen(maxGeneratedImageBytes) {
		return GeneratedImage{}, errImageResponse
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(data) > maxGeneratedImageBytes {
		return GeneratedImage{}, errImageResponse
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width < 1 || cfg.Height < 1 || cfg.Width > maxGeneratedImageSide ||
		cfg.Height > maxGeneratedImageSide || cfg.Width*cfg.Height > maxGeneratedPixels {
		return GeneratedImage{}, errImageResponse
	}
	switch format {
	case "png", "jpeg", "webp":
	default:
		return GeneratedImage{}, errImageResponse
	}
	decoded, _, err := image.Decode(bytes.NewReader(data))
	if err != nil || decoded.Bounds().Dx() != cfg.Width || decoded.Bounds().Dy() != cfg.Height {
		return GeneratedImage{}, errImageResponse
	}
	return GeneratedImage{Data: data, MIME: "image/" + format, Width: cfg.Width, Height: cfg.Height}, nil
}
