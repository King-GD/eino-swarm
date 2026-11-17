package speech

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/textproto"
	"path/filepath"
	"strings"
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
	release, err := s.pool.BeginModelUse()
	if err != nil {
		return "", failure("speech_busy", 409)
	}
	defer release()
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
	if selected.parameters != nil {
		fields = map[string]string{"model": selected.target.Model}
		for key, value := range selected.parameters {
			fields[key] = value.(string)
		}
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
	release, err := s.pool.BeginModelUse()
	if err != nil {
		return Audio{}, failure("speech_busy", 409)
	}
	defer release()
	selected, err := s.selectTarget(s.cfg.Snapshot(), false)
	if err != nil {
		return Audio{}, err
	}
	if !utf8.ValidString(text) || strings.TrimSpace(text) == "" || utf8.RuneCountInString(text) > selected.settings.Limits.ChunkChars {
		return Audio{}, failure("speech_invalid_text", 400)
	}
	output := selected.settings.Output
	body := speechBody(selected, text, false)
	raw, err := json.Marshal(body)
	if err != nil {
		return Audio{}, failure("speech_invalid_input", 400)
	}
	data, mediaType, err := s.perform(ctx, selected, "/audio/speech", "application/json", raw, selected.settings.Limits.OutputBytes)
	if err != nil {
		return Audio{}, err
	}
	mediaType, params, err := mime.ParseMediaType(mediaType)
	if err != nil {
		return Audio{}, failure("speech_invalid_response", 502)
	}
	if mediaType == "application/octet-stream" {
		mediaType = "audio/mpeg"
		if output.Format == "wav" {
			mediaType = "audio/wav"
		} else if output.Format == "pcm" {
			mediaType = "audio/pcm"
		}
	}
	if !strings.HasPrefix(mediaType, "audio/") || len(data) == 0 || (output.Format == "pcm" && (mediaType != "audio/pcm" || !pcmParameters(params) || len(data)%2 != 0)) {
		return Audio{}, failure("speech_invalid_response", 502)
	}
	return Audio{MIME: mediaType, Data: data}, nil
}

func (s *Service) perform(ctx context.Context, selected selection, path, contentType string, body []byte, limit int64) ([]byte, string, error) {
	stream, err := s.performStream(ctx, selected, path, contentType, body, limit, func() {})
	if err != nil {
		return nil, "", err
	}
	defer stream.Body.Close()
	raw, err := io.ReadAll(stream.Body)
	if err != nil {
		return nil, "", err
	}
	return raw, stream.MIME, nil
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
