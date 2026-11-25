package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"

	"github.com/LubyRuffy/eino-swarm/internal/provider"
	"github.com/LubyRuffy/eino-swarm/internal/store"
	"github.com/gin-gonic/gin"
)

const imageOCRConcurrency = 2

// getInputImage serves one pasted image so the transcript can render a
// thumbnail. Inline, not attachment: the <img> tag has to display it. The
// bytes were sniffed as a real image on the way in, so this is not the
// workspace-download path that forces download to keep agent HTML from
// executing as same-origin script.
func (s *Server) getInputImage(c *gin.Context) {
	th, ok := s.thread(c)
	if !ok {
		return
	}
	data, mime, err := s.engine.ReadInputImage(th.ID, c.Param("image_id"))
	if err != nil {
		s.fail(c, err)
		return
	}
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Cache-Control", "private, max-age=31536000, immutable")
	c.Header("Content-Disposition",
		fmt.Sprintf("inline; filename*=UTF-8''%s", escapePath(filepath.Base(c.Param("image_id")))))
	c.Data(http.StatusOK, mime, data)
}

func (s *Server) extractInputImageText(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if s.rejectSignedOut(c) {
		return
	}
	select {
	case s.imageOCR <- struct{}{}:
		defer func() { <-s.imageOCR }()
	default:
		c.JSON(http.StatusTooManyRequests, gin.H{"code": "image_ocr_busy"})
		return
	}
	ctx := c.Request.Context()
	text, err := s.engine.ExtractImageText(ctx, c.Param("id"), c.Param("image_id"))
	switch {
	case ctx.Err() != nil:
		return
	case err == nil:
		c.JSON(http.StatusOK, gin.H{"text": text})
	case errors.Is(err, store.ErrNotFound):
		s.fail(c, err)
	case errors.Is(err, provider.ErrModelPolicy), errors.Is(err, provider.ErrAccountLoginRequired), errors.Is(err, provider.ErrAccountModelsUnavailable):
		c.JSON(http.StatusForbidden, gin.H{"code": "image_ocr_unavailable"})
	case errors.Is(err, context.DeadlineExceeded):
		c.JSON(http.StatusGatewayTimeout, gin.H{"code": "image_ocr_timeout"})
	default:
		c.JSON(http.StatusBadGateway, gin.H{"code": "image_ocr_failed"})
	}
}
