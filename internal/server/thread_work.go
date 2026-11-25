package server

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/LubyRuffy/eino-swarm/internal/engine"
	"github.com/LubyRuffy/eino-swarm/internal/store"
	"github.com/gin-gonic/gin"
)

type workVersion struct {
	store.WorkRevision
	DataGeneration int64 `json:"data_generation"`
}

func (v workVersion) tag() string {
	raw, _ := json.Marshal(v)
	return strconv.Quote(base64.RawURLEncoding.EncodeToString(raw))
}

func parseWorkTag(tag string) (workVersion, error) {
	var version workVersion
	invalid := fmt.Errorf("%w: use the thread work snapshot ETag in If-Match", store.ErrInvalidNuwaNode)
	quoted, err := strconv.Unquote(tag)
	if err != nil || len(quoted) > 1024 {
		return version, invalid
	}
	raw, err := base64.RawURLEncoding.DecodeString(quoted)
	if err != nil || json.Unmarshal(raw, &version) != nil || version.ThreadID == "" ||
		version.ThreadRevision < 0 || version.ProjectRevision < 0 || version.DataGeneration < 0 ||
		version.tag() != tag {
		return workVersion{}, invalid
	}
	return version, nil
}

func (s *Server) readWorkGeneration() (int64, error) {
	if err := s.engine.RejectIfBackup(); err != nil {
		return 0, err
	}
	return s.engine.DataGeneration()
}

func (s *Server) checkWorkGeneration(expected int64) error {
	generation, err := s.readWorkGeneration()
	if err != nil {
		return err
	}
	if generation != expected {
		return store.ErrNuwaConflict
	}
	return nil
}

func (s *Server) getThreadWork(c *gin.Context) {
	generation, err := s.readWorkGeneration()
	if err != nil {
		s.failWork(c, err)
		return
	}
	view, err := s.engine.ThreadWorkSnapshot(c.Request.Context(), c.Param("id"))
	if err == nil {
		err = s.checkWorkGeneration(generation)
	}
	if err != nil {
		s.failWork(c, err)
		return
	}
	version := workVersion{WorkRevision: view.WorkRevision, DataGeneration: generation}
	c.Header("Cache-Control", "no-store")
	c.Header("ETag", version.tag())
	c.JSON(http.StatusOK, struct {
		engine.ThreadWorkView
		DataGeneration int64 `json:"data_generation"`
	}{view, generation})
}

func (s *Server) promoteThreadWork(c *gin.Context) {
	expected, err := parseWorkTag(c.GetHeader("If-Match"))
	if err != nil {
		s.failWork(c, err)
		return
	}
	var req struct {
		IDs []string `json:"ids"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		s.failWork(c, fmt.Errorf("%w: expected an array of work record IDs", store.ErrInvalidNuwaNode))
		return
	}
	promotion, err := s.engine.PromoteThreadWork(c.Request.Context(), c.Param("id"), req.IDs, expected.WorkRevision, expected.DataGeneration)
	if err != nil {
		s.failWork(c, err)
		return
	}
	version := workVersion{WorkRevision: promotion.WorkRevision, DataGeneration: expected.DataGeneration}
	c.Header("Cache-Control", "no-store")
	c.Header("ETag", version.tag())
	c.JSON(http.StatusOK, struct {
		store.NuwaPromotion
		DataGeneration int64 `json:"data_generation"`
	}{promotion, version.DataGeneration})
}

func (s *Server) distillThreadWork(c *gin.Context) {
	if s.rejectSignedOut(c) {
		return
	}
	expected, err := parseWorkTag(c.GetHeader("If-Match"))
	if err != nil {
		s.failWork(c, err)
		return
	}
	count, err := s.engine.DistillThreadWork(c.Request.Context(), c.Param("id"), expected.WorkRevision, expected.DataGeneration)
	if err != nil {
		s.failWork(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{"created": count})
}

func (s *Server) failWork(c *gin.Context, err error) {
	switch {
	case errors.Is(err, engine.ErrWorkDisabled):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error(), "code": "work_disabled"})
	case errors.Is(err, store.ErrNotFound):
		s.missingRecord(c, "thread_not_found", "This conversation no longer exists. Refresh the conversation list.",
			gin.H{"thread_id": c.Param("id")})
	case errors.Is(err, store.ErrInvalidNuwaNode), errors.Is(err, store.ErrNuwaConflict),
		errors.Is(err, engine.ErrRestoring), errors.Is(err, engine.ErrRestarting), errors.Is(err, engine.ErrBackupBusy):
		s.fail(c, err)
	default:
		s.log.Warn("could not access work records", "thread", c.Param("id"), "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Work records are unavailable.", "code": "work_unavailable"})
	}
}

func (s *Server) threadWorkVersion(c *gin.Context, threadID string) (workVersion, error) {
	generation, err := s.readWorkGeneration()
	if err != nil {
		return workVersion{}, err
	}
	revision, err := s.engine.ThreadWorkRevision(c.Request.Context(), threadID)
	if err == nil {
		err = s.checkWorkGeneration(generation)
	}
	return workVersion{WorkRevision: revision, DataGeneration: generation}, err
}

type workDelivery struct {
	threadID string
	previous workVersion
}

func (s *Server) pollWorkDelivery(c *gin.Context, delivery *workDelivery) (bool, error) {
	if delivery == nil {
		return false, nil
	}
	current, err := s.threadWorkVersion(c, delivery.threadID)
	if err != nil || current == delivery.previous {
		return false, err
	}
	delivery.previous = current
	writeSSE(c, "nuwa_updated", current, 0)
	return true, nil
}

func (s *Server) streamThreadWork(c *gin.Context) {
	threadID := c.Param("id")
	version, err := s.threadWorkVersion(c, threadID)
	if err != nil {
		s.failWork(c, err)
		return
	}
	w := c.Writer
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	writeSSE(c, "nuwa_updated", version, 0)
	w.Flush()
	delivery := &workDelivery{threadID: threadID, previous: version}
	ticker := time.NewTicker(lagCheckInterval)
	defer ticker.Stop()
	heartbeat := time.NewTicker(heartbeatInterval)
	defer heartbeat.Stop()
	for {
		select {
		case <-c.Request.Context().Done():
			return
		case <-ticker.C:
			changed, err := s.pollWorkDelivery(c, delivery)
			if errors.Is(err, store.ErrNotFound) {
				return
			}
			if changed {
				w.Flush()
			}
		case <-heartbeat.C:
			writeSSE(c, "heartbeat", gin.H{"thread_id": threadID}, 0)
			w.Flush()
		}
	}
}
