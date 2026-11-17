package server

import (
	"reflect"

	"github.com/LubyRuffy/eino-swarm/internal/config"
	"github.com/LubyRuffy/eino-swarm/internal/provider"
)

func (s *Server) currentSettingsView() settingsView {
	cfg := s.engine.Config().Snapshot()
	v := toSettingsView(cfg)
	policy := s.engine.Providers().ModelPolicy()
	v.ModelPolicy = policy
	if policy.Configurable {
		v.Models.Bindings = cfg.Models.Bindings[policy.Scope]
		return v
	}
	v.Models.Providers = []providerView{}
	v.Models.Default, v.Models.AccountModel, v.Models.AccountPresentation = "", "", ""
	v.Models.DefaultSource = config.DefaultSourceAccount
	v.Swarm.TitleProvider, v.Swarm.TitleModel = "", ""
	v.Swarm.CompactProvider, v.Swarm.CompactModel = "", ""
	v.Search.EmbeddingProvider, v.Search.EmbeddingModel = "", ""
	v.Speech.Input.SpeechEndpoint = config.SpeechEndpoint{Source: config.SpeechSourceAccount}
	v.Speech.Output.SpeechEndpoint = config.SpeechEndpoint{Source: config.SpeechSourceAccount}
	v.Speech.Input.HasAPIKey, v.Speech.Output.HasAPIKey = false, false
	return v
}

// A sanitized hidden row can round-trip during an unrelated autosave. Preserve
// its private selection; a new concrete model must never change a managed preset.
func validatePresetSettings(old, next *config.Config, policy provider.ModelPolicy) error {
	if next.Product.Resolved() == config.ProductModeLocal || policy.Configurable {
		return nil
	}
	pins := [][2]*string{{&next.Swarm.TitleProvider, &old.Swarm.TitleProvider}, {&next.Swarm.TitleModel, &old.Swarm.TitleModel}, {&next.Swarm.CompactProvider, &old.Swarm.CompactProvider}, {&next.Swarm.CompactModel, &old.Swarm.CompactModel}, {&next.Search.EmbeddingProvider, &old.Search.EmbeddingProvider}, {&next.Search.EmbeddingModel, &old.Search.EmbeddingModel}}
	for _, pin := range pins {
		if *pin[0] != "" && *pin[0] != *pin[1] {
			return errCustomModelsDisabled
		}
		*pin[0] = *pin[1]
	}
	for _, pair := range [][2]*config.SpeechEndpoint{{&next.Speech.Input.SpeechEndpoint, &old.Speech.Input.SpeechEndpoint}, {&next.Speech.Output.SpeechEndpoint, &old.Speech.Output.SpeechEndpoint}} {
		empty := *pair[0] == (config.SpeechEndpoint{Source: config.SpeechSourceAccount})
		if !empty && *pair[0] != *pair[1] {
			return errCustomModelsDisabled
		}
		*pair[0] = *pair[1]
	}
	if !reflect.DeepEqual(old.Models.Bindings, next.Models.Bindings) {
		return errCustomModelsDisabled
	}
	return nil
}
