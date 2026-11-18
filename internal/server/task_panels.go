package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/LubyRuffy/eino-swarm/internal/config"
	"github.com/LubyRuffy/eino-swarm/internal/store"
	"github.com/gin-gonic/gin"
)

func (s *Server) registerTaskPanels(api *gin.RouterGroup) {
	api.GET("/task-panels/catalog", s.getTaskPanelCatalog)
	api.GET("/task-panels/settings", s.getTaskPanelSettings)
	api.PUT("/task-panels/settings", s.putTaskPanelSettings)
	api.GET("/threads/:id/panels", s.getTaskPanels)
	api.GET("/threads/:id/panels/events", s.streamTaskPanels)
	api.GET("/threads/:id/panels/:panel", s.getTaskPanel)
}

func (s *Server) getTaskPanelCatalog(c *gin.Context) {
	definitions, err := s.engine.TaskPanelCatalog()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "panel catalog unavailable"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"plugins": definitions})
}

func (s *Server) getTaskPanelSettings(c *gin.Context) {
	c.JSON(http.StatusOK, s.engine.Config().Snapshot().TaskPanels.WithDefaults())
}

func (s *Server) putTaskPanelSettings(c *gin.Context) {
	// Independent PATCH-like fields avoid an older settings page overwriting
	// another client's current source limits or the rest of the configuration.
	var fields map[string]json.RawMessage
	decoder := json.NewDecoder(http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&fields); err != nil || fields == nil {
		badRequest(c, "invalid task panel settings")
		return
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		badRequest(c, "settings must contain one JSON object")
		return
	}
	allowed := map[string]bool{"enabled": true, "fofa_client": true, "max_assets": true, "batch_size": true, "concurrency": true, "timeout_seconds": true, "flush_interval_ms": true, "max_state_bytes": true, "max_update_bytes": true}
	for key, raw := range fields {
		if !allowed[key] || string(raw) == "null" {
			badRequest(c, "unknown or null task panel setting")
			return
		}
	}
	if len(fields) == 0 {
		badRequest(c, "empty task panel settings")
		return
	}
	err := s.engine.Config().Update(func(cfg *config.Config) error {
		current := cfg.TaskPanels.WithDefaults()
		raw, err := json.Marshal(current)
		if err != nil {
			return err
		}
		var merged map[string]json.RawMessage
		if err = json.Unmarshal(raw, &merged); err != nil {
			return err
		}
		for key, value := range fields {
			merged[key] = value
		}
		raw, err = json.Marshal(merged)
		if err != nil {
			return err
		}
		if err = json.Unmarshal(raw, &current); err != nil {
			return err
		}
		if err = current.Validate(); err != nil {
			return err
		}
		cfg.TaskPanels = current
		return nil
	})
	if err != nil {
		badRequest(c, "could not save task panel settings: %v", err)
		return
	}
	s.getTaskPanelSettings(c)
}

func (s *Server) getTaskPanels(c *gin.Context) {
	panels, cursor, err := s.engine.Store().SnapshotTaskPanels(c.Request.Context(), c.Param("id"))
	if err != nil {
		s.taskPanelReadError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"panels": panels, "cursor": cursor})
}

func (s *Server) getTaskPanel(c *gin.Context) {
	panel, err := s.engine.Store().TaskPanel(c.Request.Context(), c.Param("id"), c.Param("panel"))
	if err != nil {
		s.taskPanelReadError(c, err)
		return
	}
	c.JSON(http.StatusOK, panel)
}

func (s *Server) taskPanelReadError(c *gin.Context, err error) {
	if errors.Is(err, store.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "task panel or conversation not found"})
		return
	}
	c.JSON(http.StatusInternalServerError, gin.H{"error": "task panel state unavailable"})
}

func (s *Server) streamTaskPanels(c *gin.Context) {
	ctx := c.Request.Context()
	threadID := c.Param("id")
	panels, cursor, err := s.engine.Store().SnapshotTaskPanels(ctx, threadID)
	if err != nil {
		s.taskPanelReadError(c, err)
		return
	}
	w := c.Writer
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	// Every connect receives one atomic fresh snapshot and its exact cursor.
	// This repairs arbitrary missed updates without replaying model/tool work.
	writeSSE(c, "ready", gin.H{"panels": panels, "cursor": cursor}, cursor)
	w.Flush()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	heartbeat := time.NewTicker(heartbeatInterval)
	defer heartbeat.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-heartbeat.C:
			writeSSE(c, "heartbeat", gin.H{"cursor": cursor}, 0)
			w.Flush()
		case <-tick.C:
			changes, err := s.engine.Store().TaskPanelChanges(ctx, threadID, cursor)
			if err != nil {
				return
			}
			for _, change := range changes {
				writeSSE(c, "panel_update", change, change.Cursor)
				cursor = change.Cursor
			}
			if len(changes) > 0 {
				w.Flush()
			}
		}
	}
}
