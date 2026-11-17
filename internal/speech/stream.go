package speech

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/LubyRuffy/eino-swarm/internal/config"
)

// AudioStream owns its request, configuration watch and model admission hold.
// Callers must close Body, including after EOF. PCM is mono 24 kHz S16LE.
type AudioStream struct {
	MIME string
	Body io.ReadCloser
}

// SynthesizeStream exposes audio as it arrives, without buffering a segment.
// SSE is retained for compatible services that return speech.audio.* events.
func (s *Service) SynthesizeStream(ctx context.Context, text string) (*AudioStream, error) {
	release, err := s.pool.BeginModelUse()
	if err != nil {
		return nil, failure("speech_busy", 409)
	}
	owned := false
	defer func() {
		if !owned {
			release()
		}
	}()
	selected, err := s.selectTarget(s.cfg.Snapshot(), false)
	if err != nil {
		return nil, err
	}
	if !utf8.ValidString(text) || strings.TrimSpace(text) == "" || utf8.RuneCountInString(text) > selected.settings.Limits.ChunkChars {
		return nil, failure("speech_invalid_text", 400)
	}
	output := selected.settings.Output
	body := map[string]any{"model": selected.target.Model, "input": text, "response_format": output.Format, "speed": output.Speed}
	if output.Format == "pcm" {
		body["stream_format"] = "audio"
		if strings.HasPrefix(selected.target.ID, config.AccountSourcePrefix) {
			body["stream_format"] = "sse"
		}
	}
	if output.Voice != "" {
		body["voice"] = output.Voice
	}
	if output.Instructions != "" {
		body["instructions"] = output.Instructions
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, failure("speech_invalid_input", 400)
	}
	// performStream owns release on every return path from this point.
	owned = true
	stream, err := s.performStream(ctx, selected, "/audio/speech", "application/json", raw, selected.settings.Limits.OutputBytes, release)
	if err != nil {
		return nil, err
	}
	mediaType, params, err := mime.ParseMediaType(stream.MIME)
	if mediaType == "application/octet-stream" {
		mediaType = "audio/mpeg"
		switch output.Format {
		case "wav":
			mediaType = "audio/wav"
		case "pcm":
			mediaType = "audio/pcm"
		}
	}
	if err != nil || (!strings.HasPrefix(mediaType, "audio/") && mediaType != "text/event-stream") || (output.Format == "pcm" && (mediaType != "audio/pcm" && mediaType != "text/event-stream" || mediaType == "audio/pcm" && !pcmParameters(params))) {
		stream.Body.Close()
		return nil, failure("speech_invalid_response", 502)
	}
	stream.MIME = mediaType
	stream.Body.(*requestStream).pcm = mediaType == "audio/pcm"
	return stream, nil
}

func pcmParameters(params map[string]string) bool {
	for key, value := range params {
		if key != "rate" && key != "channels" || key == "rate" && value != "24000" || key == "channels" && value != "1" {
			return false
		}
	}
	return true
}

type requestStream struct {
	service       *Service
	selected      selection
	ctx           context.Context
	cancel        context.CancelCauseFunc
	timeoutCancel context.CancelFunc
	watchDone     chan struct{}
	release       func()
	body          io.ReadCloser
	limit, read   int64
	pcm           bool
	once          sync.Once
	closeErr      error
	terminal      error
}

func (s *Service) performStream(ctx context.Context, selected selection, path, contentType string, body []byte, limit int64, release func()) (*AudioStream, error) {
	ctx, timeoutCancel := context.WithTimeout(ctx, time.Duration(selected.timeout)*time.Second)
	ctx, cancel := context.WithCancelCause(ctx)
	stream := &requestStream{service: s, selected: selected, ctx: ctx, cancel: cancel, timeoutCancel: timeoutCancel, watchDone: make(chan struct{}), release: release, limit: limit}
	go func() {
		defer close(stream.watchDone)
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
	success := false
	defer func() {
		if !success {
			stream.Close()
		}
	}()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(selected.target.BaseURL, "/")+path, bytes.NewReader(body))
	if err != nil {
		return nil, failure("speech_not_configured", 400)
	}
	req.Header.Set("Content-Type", contentType)
	if selected.target.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+selected.target.APIKey)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, context.Cause(ctx)
		}
		return nil, failure("speech_unavailable", 502)
	}
	stream.body = resp.Body
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if s.pool != nil && strings.HasPrefix(selected.target.ID, config.AccountSourcePrefix) {
			s.pool.NotifyCapabilityRejection(resp.StatusCode)
		}
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
		return nil, failure(code, status)
	}
	success = true
	return &AudioStream{MIME: resp.Header.Get("Content-Type"), Body: stream}, nil
}

func (r *requestStream) Read(p []byte) (int, error) {
	if r.terminal != nil {
		return 0, r.terminal
	}
	if r.ctx.Err() != nil {
		return 0, context.Cause(r.ctx)
	}
	if !r.service.unchanged(r.selected) {
		return 0, failure("speech_configuration_changed", 409)
	}
	if int64(len(p)) > r.limit-r.read+1 {
		p = p[:r.limit-r.read+1]
	}
	n, err := r.body.Read(p)
	if r.ctx.Err() != nil {
		return 0, context.Cause(r.ctx)
	}
	if !r.service.unchanged(r.selected) {
		return 0, failure("speech_configuration_changed", 409)
	}
	r.read += int64(n)
	if r.read > r.limit {
		r.terminal = failure("speech_output_size", 413)
		return 0, r.terminal
	}
	if err != nil && !errors.Is(err, io.EOF) {
		return n, failure("speech_invalid_response", 502)
	}
	if errors.Is(err, io.EOF) && r.pcm && (r.read == 0 || r.read%2 != 0) {
		return n, failure("speech_invalid_response", 502)
	}
	return n, err
}

func (r *requestStream) Close() error {
	r.once.Do(func() {
		r.cancel(nil)
		r.timeoutCancel()
		if r.body != nil {
			r.closeErr = r.body.Close()
		}
		<-r.watchDone
		r.release()
	})
	return r.closeErr
}
