package server

import (
	"context"
	"errors"
	"io"
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

func TestSpeechRecoveryKeepsNormalPanic500AndAbortsFailedHTTPBodies(t *testing.T) {
	router := gin.New()
	router.Use(gin.CustomRecoveryWithWriter(io.Discard, recoverServerPanic))
	router.GET("/normal", func(c *gin.Context) { panic("ordinary failure") })
	router.GET("/stream", func(c *gin.Context) {
		_, _ = c.Writer.Write([]byte{1, 2})
		c.Writer.Flush()
		panic(abortSpeechResponse{})
	})
	server := httptest.NewServer(router)
	defer server.Close()
	response, err := server.Client().Get(server.URL + "/normal")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 500 {
		t.Fatalf("normal panic status=%d", response.StatusCode)
	}
	response, err = server.Client().Get(server.URL + "/stream")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if raw, err := io.ReadAll(response.Body); err == nil || len(raw) != 2 {
		t.Fatalf("abort returned successful EOF: bytes=%v error=%v", raw, err)
	}
}
