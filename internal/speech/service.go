package speech

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

// Error is a safe failure category. Upstream bodies and URLs never enter it.
type Error struct {
	Code   string
	Status int
}

func (e *Error) Error() string               { return "speech request failed (" + e.Code + ")" }
func failure(code string, status int) *Error { return &Error{Code: code, Status: status} }
func errorCode(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return "speech_unavailable"
}

// Audio owns one in-memory recording or synthesized segment.
type Audio struct {
	Filename string
	MIME     string
	Data     []byte
}

// Transcribe uploads the exact recording using the separately configured ASR.
func (s *Service) Transcribe(ctx context.Context, audio Audio) (string, error) {
	selected, err := s.selectTarget(s.cfg.Snapshot(), true)
	if err != nil {
		return "", err
	}
	if len(audio.Data) == 0 {
		return "", failure("speech_empty_recording", 400)
	}
	if int64(len(audio.Data)) > selected.settings.Limits.InputBytes {
		return "", failure("speech_input_size", 413)
	}
	mediaType, _, err := mime.ParseMediaType(audio.MIME)
	if err != nil || (!strings.HasPrefix(mediaType, "audio/") && mediaType != "video/webm" && mediaType != "video/mp4") {
		return "", failure("speech_input_format", 400)
	}
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	fields := map[string]string{"model": selected.target.Model, "response_format": "json"}
	if value := selected.settings.Input.Language; value != "" {
		fields["language"] = value
	}
	if value := selected.settings.Input.Prompt; value != "" {
		fields["prompt"] = value
	}
	for key, value := range fields {
		if err = w.WriteField(key, value); err != nil {
			return "", failure("speech_invalid_input", 400)
		}
	}
	name := filepath.Base(audio.Filename)
	if name == "." || name == "" {
		name = "recording"
	}
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", mime.FormatMediaType("form-data", map[string]string{"name": "file", "filename": name}))
	header.Set("Content-Type", mediaType)
	part, err := w.CreatePart(header)
	if err != nil {
		return "", failure("speech_invalid_input", 400)
	}
	if _, err = part.Write(audio.Data); err != nil {
		return "", failure("speech_invalid_input", 400)
	}
	if err = w.Close(); err != nil {
		return "", failure("speech_invalid_input", 400)
	}
	raw, _, err := s.perform(ctx, selected, "/audio/transcriptions", w.FormDataContentType(), body.Bytes(), 1<<20)
	if err != nil {
		return "", err
	}
	var response struct {
		Text *string `json:"text"`
	}
	if json.Unmarshal(raw, &response) != nil || response.Text == nil {
		return "", failure("speech_invalid_response", 502)
	}
	if strings.TrimSpace(*response.Text) == "" {
		return "", failure("speech_empty_transcription", 502)
	}
	return *response.Text, nil
}

// Synthesize returns a single bounded segment. Clients sequence long answers.
func (s *Service) Synthesize(ctx context.Context, text string) (Audio, error) {
	selected, err := s.selectTarget(s.cfg.Snapshot(), false)
	if err != nil {
		return Audio{}, err
	}
	if !utf8.ValidString(text) || strings.TrimSpace(text) == "" || utf8.RuneCountInString(text) > selected.settings.Limits.ChunkChars {
		return Audio{}, failure("speech_invalid_text", 400)
	}
	output := selected.settings.Output
	body := map[string]any{"model": selected.target.Model, "input": text, "response_format": output.Format, "speed": output.Speed}
	if output.Voice != "" {
		body["voice"] = output.Voice
	}
	if output.Instructions != "" {
		body["instructions"] = output.Instructions
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return Audio{}, failure("speech_invalid_input", 400)
	}
	data, mediaType, err := s.perform(ctx, selected, "/audio/speech", "application/json", raw, selected.settings.Limits.OutputBytes)
	if err != nil {
		return Audio{}, err
	}
	mediaType, _, err = mime.ParseMediaType(mediaType)
	if err != nil {
		return Audio{}, failure("speech_invalid_response", 502)
	}
	if mediaType == "application/octet-stream" {
		mediaType = "audio/mpeg"
		if output.Format == "wav" {
			mediaType = "audio/wav"
		}
	}
	if !strings.HasPrefix(mediaType, "audio/") || len(data) == 0 {
		return Audio{}, failure("speech_invalid_response", 502)
	}
	return Audio{MIME: mediaType, Data: data}, nil
}

func (s *Service) perform(ctx context.Context, selected selection, path, contentType string, body []byte, limit int64) ([]byte, string, error) {
	ctx, timeoutCancel := context.WithTimeout(ctx, time.Duration(selected.timeout)*time.Second)
	defer timeoutCancel()
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if !s.unchanged(selected) {
					cancel(failure("speech_configuration_changed", 409))
					return
				}
			}
		}
	}()
	defer func() { cancel(nil); <-watchDone }()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(selected.target.BaseURL, "/")+path, bytes.NewReader(body))
	if err != nil {
		return nil, "", failure("speech_not_configured", 400)
	}
	req.Header.Set("Content-Type", contentType)
	if selected.target.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+selected.target.APIKey)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, "", context.Cause(ctx)
		}
		return nil, "", failure("speech_unavailable", 502)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		status, code := 502, "speech_upstream_error"
		switch resp.StatusCode {
		case 401:
			status, code = 401, "speech_authentication_required"
		case 403:
			status, code = 403, "speech_access_denied"
		case 429:
			status, code = 429, "speech_rate_limited"
		case 400, 415, 422:
			status, code = 400, "speech_parameters_rejected"
		}
		return nil, "", failure(code, status)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		if ctx.Err() != nil {
			return nil, "", context.Cause(ctx)
		}
		return nil, "", failure("speech_invalid_response", 502)
	}
	if int64(len(raw)) > limit {
		return nil, "", failure("speech_output_size", 413)
	}
	if ctx.Err() != nil {
		return nil, "", context.Cause(ctx)
	}
	if !s.unchanged(selected) {
		return nil, "", failure("speech_configuration_changed", 409)
	}
	return raw, resp.Header.Get("Content-Type"), nil
}

// Failure returns an HTTP status and safe code for either transport adapter.
func Failure(err error) (int, string) {
	var value *Error
	if errors.As(err, &value) {
		return value.Status, value.Code
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return 504, "speech_timeout"
	}
	if errors.Is(err, context.Canceled) {
		return 499, "speech_canceled"
	}
	return 502, "speech_unavailable"
}

// String intentionally does not print configuration or credentials.
func (s *Service) String() string { return "speech service" }
