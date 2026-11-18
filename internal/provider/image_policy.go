package provider

import (
	"slices"
	"strings"

	"github.com/LubyRuffy/eino-swarm/internal/config"
)

func (s *AccountSource) imageAuthorized() bool {
	return s != nil && !s.unavailable && s.token != "" && s.capabilities.Allows(imageRole)
}

func (s *AccountSource) imageConfigurable() bool {
	return s.imageAuthorized() && !s.managed && s.capabilities.Configurable && s.capabilities.Capabilities[imageRole].Configurable
}

func (s *AccountSource) imageModelAllowed(model string) bool {
	if !s.imageAuthorized() || model == "" {
		return false
	}
	if !s.imageConfigurable() {
		return s.fixedImageModel() == model
	}
	return s.types[model] == imageRole
}

func (s *AccountSource) fixedImageModel() string {
	if enabled, declared := s.Role(imageRole); declared {
		if enabled {
			return imageRole
		}
		return ""
	}
	return s.defaults[imageRole]
}

func (s *AccountSource) defaultImageModel() string {
	if !s.imageAuthorized() {
		return ""
	}
	if !s.imageConfigurable() {
		return s.fixedImageModel()
	}
	if model := s.defaults[imageRole]; s.types[model] == imageRole {
		return model
	}
	model := ""
	for name, kind := range s.types {
		if kind == imageRole && (model == "" || name < model) {
			model = name
		}
	}
	return model
}

func (p *Pool) imageTarget(conversationProviderID string) (*AccountSource, string) {
	if p == nil || p.cfg == nil || p.mock {
		return nil, ""
	}
	scope := config.ProductModeLocal
	if active := p.activeAccount(); active != nil {
		if active.managed {
			// The managed account's preset owns inference regardless of the
			// conversation's historical provider selection.
			return readyImageTarget(active, active.defaultImageModel())
		}
		scope = active.bindingScope()
	} else if !p.localExecution() {
		return nil, ""
	}

	if ref, ok := p.cfg.ModelsSnapshot().Bindings[scope][imageRole]; ok {
		target := p.accountForExecution(ref.ProviderID)
		if target.imageConfigurable() {
			return readyImageTarget(target, ref.Model)
		}
		return nil, ""
	}
	if strings.TrimSpace(conversationProviderID) == "" {
		conversationProviderID = p.DefaultProviderID()
	}
	source := p.accountForExecution(conversationProviderID)
	return readyImageTarget(source, source.defaultImageModel())
}

func readyImageTarget(source *AccountSource, name string) (*AccountSource, string) {
	if !source.imageModelAllowed(name) {
		return nil, ""
	}
	return source, name
}

func (p *Pool) currentImageGrant(source *AccountSource, model string) bool {
	current := p.accountForExecution(source.id)
	return current.imageModelAllowed(model) && current.owner == source.owner &&
		current.catalogGroup == source.catalogGroup && current.baseURL == source.baseURL && current.token == source.token
}

// Only the still-published image grant can be invalidated by this request's
// late refusal. The existing discovery owner independently refreshes the view.
func (p *Pool) refreshRejectedImageCapability(source *AccountSource) {
	copy := *source
	copy.capabilities = cloneCapabilities(source.capabilities)
	delete(copy.capabilities.Capabilities, imageRole)

	activeChanged := p.account.CompareAndSwap(source, &copy)
	localChanged := false
	if local := p.localAccounts.Load(); local != nil {
		if idx := slices.Index(local.sources, source); idx >= 0 {
			next := &localAccountList{sources: slices.Clone(local.sources)}
			next.sources[idx] = &copy
			localChanged = p.localAccounts.CompareAndSwap(local, next)
		}
	}
	if activeChanged || localChanged {
		p.Invalidate()
		p.triggerCapabilityRefresh()
	}
}
