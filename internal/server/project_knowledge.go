package server

import (
	"errors"
	"net/http"
	"time"

	"github.com/LubyRuffy/eino-swarm/internal/knowledge"
	"github.com/gin-gonic/gin"
)

func (s *Server) registerProjectKnowledge(api *gin.RouterGroup) {
	api.GET("/projects/:id/knowledge", s.getProjectKnowledge)
	api.POST("/projects/:id/knowledge", s.enableProjectKnowledge)
	api.GET("/projects/:id/knowledge/tree", s.getProjectKnowledgeTree)
	api.GET("/projects/:id/knowledge/graph", s.getProjectKnowledgeGraph)
	api.GET("/projects/:id/knowledge/pages/:page", s.getProjectKnowledgePage)
	api.POST("/projects/:id/knowledge/reveal", s.revealProjectKnowledge)
}

func (s *Server) projectKnowledgeSnapshot(c *gin.Context) (*knowledge.Snapshot, error) {
	kb, err := s.engine.ProjectKnowledge(c.Param("id"))
	if err != nil {
		return nil, err
	}
	return kb.Snapshot()
}

func (s *Server) getProjectKnowledge(c *gin.Context) {
	snapshot, err := s.projectKnowledgeSnapshot(c)
	if err != nil {
		s.failProjectKnowledge(c, err)
		return
	}
	c.JSON(http.StatusOK, snapshot.Status(time.Now()))
}

func (s *Server) enableProjectKnowledge(c *gin.Context) {
	snapshot, err := s.engine.EnableProjectKnowledge(c.Param("id"))
	if err != nil {
		s.failProjectKnowledge(c, err)
		return
	}
	c.JSON(http.StatusOK, snapshot.Status(time.Now()))
}

func (s *Server) getProjectKnowledgeTree(c *gin.Context) {
	snapshot, err := s.projectKnowledgeSnapshot(c)
	if err != nil {
		s.failProjectKnowledge(c, err)
		return
	}
	tree, err := snapshot.Tree(c.Query("view"), time.Now())
	if errors.Is(err, knowledge.ErrNotFound) {
		badRequest(c, "%v", err)
		return
	}
	if err != nil {
		s.failProjectKnowledge(c, err)
		return
	}
	c.JSON(http.StatusOK, tree)
}

func (s *Server) getProjectKnowledgeGraph(c *gin.Context) {
	snapshot, err := s.projectKnowledgeSnapshot(c)
	if err != nil {
		s.failProjectKnowledge(c, err)
		return
	}
	graph, err := snapshot.Graph()
	if err != nil {
		s.failProjectKnowledge(c, err)
		return
	}
	c.JSON(http.StatusOK, graph)
}

func (s *Server) getProjectKnowledgePage(c *gin.Context) {
	id := c.Param("page")
	if !knowledge.ValidSlug(id) {
		badRequest(c, "invalid knowledge page id")
		return
	}
	page, err := s.engine.ProjectKnowledgePage(c.Request.Context(), c.Param("id"), id)
	if err != nil {
		s.failProjectKnowledge(c, err)
		return
	}
	c.JSON(http.StatusOK, page)
}

func (s *Server) revealProjectKnowledge(c *gin.Context) {
	if s.opts.Reveal == nil {
		c.JSON(http.StatusNotImplemented, gin.H{"error": "showing files in the file manager is only available in the desktop app"})
		return
	}
	snapshot, err := s.projectKnowledgeSnapshot(c)
	if err != nil {
		s.failProjectKnowledge(c, err)
		return
	}
	if !snapshot.Enabled() {
		s.failProjectKnowledge(c, knowledge.ErrDisabled)
		return
	}
	dir := s.engine.KnowledgeProjectDir(c.Param("id"))
	if err := s.opts.Reveal(dir); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"revealed": dir})
}

func (s *Server) failProjectKnowledge(c *gin.Context, err error) {
	switch {
	case errors.Is(err, knowledge.ErrDisabled):
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error(), "code": "knowledge_disabled"})
	case errors.Is(err, knowledge.ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error(), "code": "knowledge_page_not_found"})
	default:
		s.fail(c, err)
	}
}
