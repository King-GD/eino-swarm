package server

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/LubyRuffy/eino-swarm/internal/clients"
	"github.com/LubyRuffy/eino-swarm/internal/store"
	"github.com/gin-gonic/gin"
)

func (s *Server) getClients(c *gin.Context) {
	if raw := c.Query("before"); raw != "" {
		if _, err := strconv.ParseInt(raw, 10, 64); err != nil {
			badRequest(c, "before must be a unix millisecond")
			return
		}
	}
	c.JSON(http.StatusOK, clients.Catalog{Tools: []clients.Group{}})
}

func (s *Server) getClientTask(c *gin.Context) {
	id := strings.TrimSpace(c.Query("id"))
	if id == "" {
		badRequest(c, "id is required")
		return
	}
	if raw := c.Query("before"); raw != "" {
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || n < 0 {
			badRequest(c, "before must be a byte offset")
			return
		}
	}
	s.fail(c, store.ErrNotFound)
}
