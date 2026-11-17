package config

import (
	"errors"
	"maps"
	"strings"
)

// ModelRef is a credential-free functional model selection.
type ModelRef struct {
	ProviderID string `yaml:"provider_id" json:"provider_id"`
	Model      string `yaml:"model" json:"model"`
}

// ModelRoles is the existing functional model surface, in settings order.
func ModelRoles() []string {
	return []string{"conversation", "title", "compact", "translation", "embedding", "ocr", "decisions", "transcription", "speech", "image"}
}

// KnownModelRole validates a functional binding without accepting arbitrary keys.
func KnownModelRole(role string) bool {
	for _, known := range ModelRoles() {
		if known == role {
			return true
		}
	}
	return false
}

func cloneBindings(bindings map[string]map[string]ModelRef) map[string]map[string]ModelRef {
	if bindings == nil {
		return nil
	}
	out := make(map[string]map[string]ModelRef, len(bindings))
	for scope, refs := range bindings {
		out[scope] = maps.Clone(refs)
	}
	return out
}

func (c *Config) validateModelBindings() error {
	for scope, refs := range c.Models.Bindings {
		if scope != ProductModeLocal && !strings.HasPrefix(scope, "gateway:") {
			return errors.New("config: invalid model binding scope")
		}
		for role, ref := range refs {
			if !KnownModelRole(role) || strings.TrimSpace(ref.ProviderID) == "" || strings.TrimSpace(ref.Model) == "" {
				return errors.New("config: a model binding needs a known role, provider_id and model")
			}
			if scope == ProductModeLocal && strings.HasPrefix(ref.ProviderID, AccountSourcePrefix) {
				return errors.New("config: LOCAL cannot bind a gateway model")
			}
		}
	}
	return nil
}
