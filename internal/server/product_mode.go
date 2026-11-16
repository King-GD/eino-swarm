package server

import (
	"errors"
	"net/http"

	"github.com/LubyRuffy/eino-swarm/internal/remote"
	"github.com/gin-gonic/gin"
)

// errCustomModelsDisabled refuses edits to a gateway-managed model preset.
var errCustomModelsDisabled = errors.New("model settings cannot be changed in this mode")

// rejectSignedOut blocks work that starts a model turn while a FOFA install
// has no signed-in account. Mock skips it so the scripted provider can run.
// A server with no account component has nothing to sign in to, so it does
// not invent a refusal.
func (s *Server) rejectSignedOut(c *gin.Context) bool {
	if s == nil || s.engine == nil || s.remote == nil {
		return false
	}
	cfg := s.engine.Config()
	if cfg == nil || !cfg.Snapshot().Product.FOFA() {
		return false
	}
	if pool := s.engine.Providers(); pool != nil && pool.IsMock() {
		return false
	}
	state := s.remote.AccountModels().State
	if state == remote.AccountReady {
		return false
	}
	c.JSON(http.StatusForbidden, gin.H{
		"error": "Sign in before using this install.",
		"code":  "login_required",
	})
	return true
}

// rejectCustomModels blocks provider edits and discovery unless the current
// execution source permits them. LOCAL and editable gateway accounts do.
func (s *Server) rejectCustomModels(c *gin.Context) bool {
	if s == nil || s.engine == nil || s.engine.Config() == nil {
		return false
	}
	if s.engine.Providers().ModelPolicy().Configurable {
		return false
	}
	c.JSON(http.StatusForbidden, gin.H{
		"error": errCustomModelsDisabled.Error(),
		"code":  "custom_models_disabled",
	})
	return true
}
