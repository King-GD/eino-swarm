package server

import (
	"errors"
	"net/http"

	"github.com/LubyRuffy/eino-swarm/internal/engine"
	"github.com/LubyRuffy/eino-swarm/internal/store"
	"github.com/gin-gonic/gin"
)

func (s *Server) deliverComposer(c *gin.Context) {
	th, ok := s.thread(c)
	if !ok || s.rejectSignedOut(c) {
		return
	}
	var req struct {
		turnRequest
		Mode         engine.DeliveryMode `json:"mode"`
		ExpectedTurn string              `json:"expected_turn_id"`
		FollowupID   string              `json:"followup_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "could not read the request body: %v", err)
		return
	}
	in, err := req.turnRequest.input()
	if err != nil {
		s.fail(c, err)
		return
	}
	receipt, err := s.engine.DeliverComposerInput(th.ID, req.Mode, req.ExpectedTurn, req.FollowupID, in)
	if err != nil {
		if req.FollowupID != "" && errors.Is(err, store.ErrNotFound) {
			s.missingRecord(c, "followup_unavailable", "This message is no longer in the waiting queue.",
				gin.H{"thread_id": th.ID, "followup_id": req.FollowupID})
			return
		}
		s.fail(c, err)
		return
	}
	c.JSON(http.StatusAccepted, receipt)
}

type followupRequest struct {
	Text string `json:"text"`
}

func (s *Server) listFollowups(c *gin.Context) {
	th, ok := s.thread(c)
	if !ok {
		return
	}
	list, err := s.engine.ListFollowups(th.ID)
	if err != nil {
		s.fail(c, err)
		return
	}
	if list == nil {
		list = []store.Followup{}
	}
	c.JSON(http.StatusOK, gin.H{"followups": list})
}

func (s *Server) enqueueFollowup(c *gin.Context) {
	th, ok := s.thread(c)
	if !ok {
		return
	}
	var req followupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "could not read the request body: %v", err)
		return
	}
	if s.rejectSignedOut(c) {
		return
	}
	f, err := s.engine.EnqueueFollowup(th.ID, req.Text)
	if err != nil {
		s.fail(c, err)
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"followup": f})
}

func (s *Server) deleteFollowup(c *gin.Context) {
	th, ok := s.thread(c)
	if !ok {
		return
	}
	if err := s.engine.DeleteFollowup(th.ID, c.Param("fid")); err != nil {
		s.fail(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) requeueFollowup(c *gin.Context) {
	th, ok := s.thread(c)
	if !ok {
		return
	}
	var req followupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "could not read the request body: %v", err)
		return
	}
	f, err := s.engine.RequeueFollowup(th.ID, c.Param("fid"), req.Text)
	if err != nil {
		s.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"followup": f})
}

func (s *Server) steerFollowup(c *gin.Context) {
	th, ok := s.thread(c)
	if !ok {
		return
	}
	if s.rejectSignedOut(c) {
		return
	}
	if c.Request.ContentLength != 0 {
		var req struct {
			Mode         engine.DeliveryMode `json:"mode"`
			ExpectedTurn string              `json:"expected_turn_id"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			badRequest(c, "could not read the request body: %v", err)
			return
		}
		_, err := s.engine.DeliverComposerInput(th.ID, req.Mode, req.ExpectedTurn, c.Param("fid"), engine.UserInput{})
		s.completeFollowupSteer(c, th.ID, nil, err)
		return
	}
	turn, err := s.engine.SteerFollowup(th.ID, c.Param("fid"))
	s.completeFollowupSteer(c, th.ID, turn, err)
}

func (s *Server) completeFollowupSteer(c *gin.Context, threadID string, turn *store.Turn, err error) {
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.missingRecord(c, "followup_unavailable",
				"This message is no longer in the waiting queue. It may already have been sent or removed. Refresh the queue before trying again.",
				gin.H{"thread_id": threadID, "followup_id": c.Param("fid")})
			return
		}
		s.fail(c, err)
		return
	}
	if turn == nil {
		c.JSON(http.StatusAccepted, gin.H{"steered": true})
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"steered": false, "turn": turn})
}
