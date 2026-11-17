package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestSelectionTranslationRejectsWhenBusy(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := &Server{translations: make(chan struct{}, 1)}
	s.translations <- struct{}{}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/threads/th/translation", strings.NewReader(`{"text":"Hello"}`))
	c.Request.Header.Set("Content-Type", "application/json")
	s.translateSelection(c)
	if w.Code != http.StatusTooManyRequests || !strings.Contains(w.Body.String(), "selection_translation_busy") {
		t.Fatalf("busy response = %d %s", w.Code, w.Body.String())
	}
}

func TestSelectionTranslationTimeoutDoesNotExposeTheCause(t *testing.T) {
	status, body := selectionTranslationFailure(context.DeadlineExceeded)
	if status != http.StatusGatewayTimeout || body["code"] != "selection_translation_timeout" {
		t.Fatalf("timeout = %d %+v", status, body)
	}
	if strings.Contains(body["error"].(string), "deadline") {
		t.Fatalf("timeout exposed the cause: %+v", body)
	}
}
