package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LubyRuffy/eino-swarm/internal/speech"
	"github.com/gin-gonic/gin"
)

func TestSpeechFailureNeverPublishesToACanceledHTTPCaller(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/audio/speech", nil).WithContext(ctx)
	(&Server{}).failSpeech(c, errors.New("private provider body"))
	if w.Body.Len() != 0 {
		t.Fatal("canceled voice request wrote a late response")
	}
	w = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/audio/speech", nil)
	(&Server{}).failSpeech(c, &speech.Error{Status: http.StatusBadRequest})
	if w.Code != 400 || !strings.Contains(w.Body.String(), "speech_unavailable") {
		t.Fatalf("malformed internal failure=%d %s", w.Code, w.Body.String())
	}
}
