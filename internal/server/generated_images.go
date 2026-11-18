package server

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

func (s *Server) getGeneratedImage(c *gin.Context) {
	thread, ok := s.thread(c)
	if !ok {
		return
	}
	file, mime, err := s.engine.OpenGeneratedImage(thread.ID, c.Param("image_id"))
	if err != nil {
		s.fail(c, err)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		s.fail(c, err)
		return
	}
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Cache-Control", "private, no-cache")
	c.Header("Content-Disposition", "inline")
	c.Header("Content-Type", mime)
	http.ServeContent(c.Writer, c.Request, "", info.ModTime(), file)
}
