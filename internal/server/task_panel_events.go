package server

import "github.com/gin-gonic/gin"

// The browser multiplexes panels onto its existing conversation connection.
// Two windows must leave HTTP/1.1 connection capacity for drafts and API reads.
// Panel cursors stay in payloads: they must never become chat Last-Event-ID.
type taskPanelDelivery struct {
	threadID string
	cursor   int64
	ready    bool
	failed   bool
}

func (s *Server) startTaskPanelDelivery(c *gin.Context, threadID string) *taskPanelDelivery {
	if c.Query("panels") != "1" {
		return nil
	}
	d := &taskPanelDelivery{threadID: threadID}
	s.pollTaskPanelDelivery(c, d)
	return d
}

func (s *Server) pollTaskPanelDelivery(c *gin.Context, d *taskPanelDelivery) bool {
	if d == nil {
		return false
	}
	if !d.ready || d.failed {
		panels, cursor, err := s.engine.Store().SnapshotTaskPanels(c.Request.Context(), d.threadID)
		if err != nil {
			return d.unavailable(c)
		}
		writeSSE(c, "panel_ready", gin.H{"panels": panels, "cursor": cursor}, 0)
		d.cursor, d.ready, d.failed = cursor, true, false
		return true
	}
	changes, err := s.engine.Store().TaskPanelChanges(c.Request.Context(), d.threadID, d.cursor)
	if err != nil {
		return d.unavailable(c)
	}
	for _, change := range changes {
		writeSSE(c, "panel_update", change, 0)
		d.cursor = change.Cursor
	}
	return len(changes) > 0
}

func (d *taskPanelDelivery) unavailable(c *gin.Context) bool {
	if d.failed {
		return false
	}
	d.failed = true
	writeSSE(c, "panel_error", gin.H{"error": "panel state unavailable"}, 0)
	return true
}
