package server

import (
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/LubyRuffy/eino-swarm/internal/speech"
	"github.com/gin-gonic/gin"
)

const speechHTTPConcurrency = 4

func (s *Server) speechService() *speech.Service {
	return speech.New(s.engine.Config(), s.engine.Providers())
}

func (s *Server) admitSpeech(c *gin.Context) bool {
	c.Header("Cache-Control", "no-store")
	select {
	case s.audioCalls <- struct{}{}:
		return true
	default:
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "Too many voice requests are running.", "code": "speech_busy"})
		return false
	}
}

func (s *Server) transcribeAudio(c *gin.Context) {
	if !s.admitSpeech(c) {
		return
	}
	defer func() { <-s.audioCalls }()
	service := s.speechService()
	view := service.Snapshot()
	if !view.Input.Ready {
		s.failSpeech(c, &speech.Error{Code: view.Input.Reason, Status: http.StatusForbidden})
		return
	}
	// Keep the complete multipart request bounded without disk-backed parsing.
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, view.Limits.InputBytes+(64<<10))
	defer c.Request.Body.Close()
	reader, err := c.Request.MultipartReader()
	if err != nil {
		s.failSpeech(c, &speech.Error{Code: "speech_invalid_input", Status: 400})
		return
	}
	var audio speech.Audio
	for count := 0; ; count++ {
		part, readErr := reader.NextPart()
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil || count >= 16 {
			s.failSpeech(c, &speech.Error{Code: "speech_invalid_input", Status: 400})
			return
		}
		if part.FormName() != "file" || part.FileName() == "" {
			_, readErr = io.Copy(io.Discard, io.LimitReader(part, 1025))
			part.Close()
			if readErr != nil {
				s.failSpeech(c, &speech.Error{Code: "speech_invalid_input", Status: 400})
				return
			}
			continue
		}
		if audio.Data != nil {
			part.Close()
			s.failSpeech(c, &speech.Error{Code: "speech_invalid_input", Status: 400})
			return
		}
		audio.Filename, audio.MIME = part.FileName(), part.Header.Get("Content-Type")
		audio.Data, readErr = io.ReadAll(io.LimitReader(part, view.Limits.InputBytes+1))
		part.Close()
		if readErr != nil {
			s.failSpeech(c, &speech.Error{Code: "speech_invalid_input", Status: 400})
			return
		}
		if int64(len(audio.Data)) > view.Limits.InputBytes {
			s.failSpeech(c, &speech.Error{Code: "speech_input_size", Status: 413})
			return
		}
	}
	mediaType, _, _ := mime.ParseMediaType(audio.MIME)
	if mediaType == "" || mediaType == "application/octet-stream" {
		audio.MIME = http.DetectContentType(audio.Data)
	}
	text, err := service.Transcribe(c.Request.Context(), audio)
	if err != nil {
		s.failSpeech(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"text": text})
}

func (s *Server) synthesizeAudio(c *gin.Context) {
	if !s.admitSpeech(c) {
		return
	}
	defer func() { <-s.audioCalls }()
	service := s.speechService()
	// JSON escaping can expand one four-byte rune into twelve ASCII bytes.
	limit := int64(service.Snapshot().Limits.ChunkChars)*12 + 1024
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, limit)
	defer c.Request.Body.Close()
	var input struct {
		Text string `json:"text"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		s.failSpeech(c, &speech.Error{Code: "speech_invalid_text", Status: 400})
		return
	}
	if service.Snapshot().Output.Format == "pcm" {
		s.streamSynthesizedAudio(c, service, input.Text)
		return
	}
	audio, err := service.Synthesize(c.Request.Context(), input.Text)
	if err != nil {
		s.failSpeech(c, err)
		return
	}
	c.Header("X-Content-Type-Options", "nosniff")
	c.Data(http.StatusOK, audio.MIME, audio.Data)
}

func (s *Server) discoverAudioModels(c *gin.Context) {
	if !s.admitSpeech(c) {
		return
	}
	defer func() { <-s.audioCalls }()
	var input struct {
		Kind string `json:"kind"`
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1024)
	defer c.Request.Body.Close()
	if c.ShouldBindJSON(&input) != nil || (input.Kind != "input" && input.Kind != "output") {
		s.failSpeech(c, &speech.Error{Code: "speech_invalid_input", Status: 400})
		return
	}
	models, err := s.speechService().Models(c.Request.Context(), input.Kind == "input")
	if err != nil {
		s.failSpeech(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"models": models})
}

func (s *Server) failSpeech(c *gin.Context, err error) {
	if c.Request.Context().Err() != nil {
		return
	}
	status, code := speech.Failure(err)
	if strings.TrimSpace(code) == "" {
		code = "speech_unavailable"
	}
	c.JSON(status, gin.H{"error": "Voice request could not be completed.", "code": code})
}
