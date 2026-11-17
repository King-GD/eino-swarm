package server

import (
	"errors"
	"io"
	"net/http"

	"github.com/LubyRuffy/eino-swarm/internal/speech"
	"github.com/gin-gonic/gin"
)

// Gin 1.12 consumes ErrAbortHandler as a broken connection before invoking a
// custom recovery callback. A private non-error marker reaches that callback;
// only then is it converted to net/http's abort signal.
type abortSpeechResponse struct{}

// Gin retains its normal panic log and 500 handling. A failed streaming body
// instead reaches net/http, which aborts it without a successful EOF.
func recoverServerPanic(c *gin.Context, value any) {
	if _, ok := value.(abortSpeechResponse); ok {
		panic(http.ErrAbortHandler)
	}
	c.AbortWithStatus(http.StatusInternalServerError)
}

func (s *Server) streamSynthesizedAudio(c *gin.Context, service *speech.Service, text string) {
	stream, err := service.SynthesizeStream(c.Request.Context(), text)
	if err != nil {
		s.failSpeech(c, err)
		return
	}
	defer stream.Body.Close()
	buffer := make([]byte, 32<<10)
	written := false
	for {
		n, err := stream.Body.Read(buffer)
		if err != nil && !errors.Is(err, io.EOF) {
			if !written {
				s.failSpeech(c, err)
				return
			}
			status, code := speech.Failure(err)
			s.log.Warn("speech stream interrupted", "code", code, "status", status)
			panic(abortSpeechResponse{})
		}
		if n > 0 {
			if !written {
				c.Header("X-Content-Type-Options", "nosniff")
				c.Header("Content-Type", stream.MIME)
				c.Header("X-Accel-Buffering", "no")
				c.Status(http.StatusOK)
				written = true
			}
			if _, writeErr := c.Writer.Write(buffer[:n]); writeErr != nil {
				return
			}
			c.Writer.Flush()
		}
		if errors.Is(err, io.EOF) {
			if !written {
				s.failSpeech(c, &speech.Error{Code: "speech_invalid_response", Status: http.StatusBadGateway})
			}
			return
		}
	}
}
