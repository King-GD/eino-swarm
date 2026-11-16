package provider

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"maps"
	"net/url"
	"strings"

	"github.com/LubyRuffy/eino-swarm/internal/config"
)

// ErrModelPolicy refuses a model outside the current execution source.
var ErrModelPolicy = errors.New("model_selection_disabled: model is outside the current preset")

// ModelPolicy is the safe, account-scoped projection consumed by PC and phone.
type ModelPolicy struct {
	Mode         string                     `json:"mode"`
	Configurable bool                       `json:"configurable"`
	Ready        bool                       `json:"ready"`
	Scope        string                     `json:"scope"`
	Defaults     map[string]config.ModelRef `json:"defaults,omitempty"`
	Bindings     map[string]config.ModelRef `json:"bindings,omitempty"`
	Roles        []AccountRole              `json:"roles,omitempty"`
}

// SetModelPolicy installs the server's edit permission and role defaults.
// Defaults name only models on this source; credentials remain private.
func (s *AccountSource) SetModelPolicy(configurable bool, defaults map[string]string) {
	s.managed = !configurable
	s.defaults = maps.Clone(defaults)
}

// SetAccountOwner separates saved overrides when the same gateway source id is
// reused for several accounts. Token renewal does not change this identity.
func (s *AccountSource) SetAccountOwner(owner string) { s.owner = owner }

func (p *Pool) activeAccount() *AccountSource {
	if p.cfg.ProductSnapshot().Resolved() == config.ProductModeLocal {
		return nil
	}
	return p.account.Load()
}

// ModelPolicy separates debug source selection from server-owned model policy.
func (p *Pool) ModelPolicy() ModelPolicy {
	if p.cfg.ProductSnapshot().Resolved() == config.ProductModeLocal {
		return ModelPolicy{Mode: "local", Configurable: true, Ready: true, Scope: config.ProductModeLocal, Bindings: p.cfg.ModelsSnapshot().Bindings[config.ProductModeLocal]}
	}
	view := ModelPolicy{Mode: "gateway"}
	src := p.account.Load()
	if src == nil {
		return view
	}
	origin, err := url.Parse(src.baseURL)
	if err != nil {
		return view
	}
	sum := sha256.Sum256([]byte(origin.Scheme + "://" + origin.Host + "\x00" + src.id + "\x00" + src.owner))
	view.Scope = "gateway:" + hex.EncodeToString(sum[:])
	view.Configurable, view.Ready = !src.managed, true
	if view.Configurable {
		view.Bindings = p.cfg.ModelsSnapshot().Bindings[view.Scope]
	}
	view.Roles = src.Roles()
	view.Defaults = make(map[string]config.ModelRef, len(src.defaults))
	for role, name := range src.defaults {
		view.Defaults[role] = config.ModelRef{ProviderID: src.id, Model: name}
	}
	return view
}

// ValidateConversationSelection checks explicit client choices. Historical
// conversation bindings are resolved separately so a managed login can retain
// them for a later LOCAL session without executing them.
func (p *Pool) ValidateConversationSelection(id, name string) error {
	if p.activeAccount() == nil {
		if err := p.validateModelSelection(id, name); err != nil {
			return err
		}
	}
	if src := p.activeAccount(); src != nil && src.managed {
		if (id != "" && id != src.id) || (name != "" && name != src.provider("").Model) {
			return ErrModelPolicy
		}
	}
	_, err := p.ResolveModel(id, name)
	return err
}

// RoleSelection resolves one functional model. An empty result is unavailable.
// Managed selections never merge personal providers or legacy local pins.
func (p *Pool) RoleSelection(role, fallbackProvider, fallbackModel string) (config.ModelRef, bool) {
	policy := p.ModelPolicy()
	src := p.activeAccount()
	if src != nil && src.managed {
		if on, configured := src.Role(role); configured {
			if on {
				if name := src.defaults[role]; name != "" {
					return config.ModelRef{ProviderID: src.id, Model: name}, true
				}
				return config.ModelRef{ProviderID: src.id, Model: role}, true
			}
			if role == "title" || role == "compact" {
				return config.ModelRef{ProviderID: src.id, Model: src.provider("").Model}, true
			}
			return config.ModelRef{}, true
		}
		if name := src.defaults[role]; name != "" {
			return config.ModelRef{ProviderID: src.id, Model: name}, true
		}
		if role == "conversation" || role == "title" || role == "compact" {
			return config.ModelRef{ProviderID: src.id, Model: src.provider("").Model}, true
		}
		return config.ModelRef{}, true
	}
	if ref, ok := p.cfg.ModelsSnapshot().Bindings[policy.Scope][role]; ok {
		return ref, true
	}
	if src != nil {
		if name := src.defaults[role]; name != "" {
			return config.ModelRef{ProviderID: src.id, Model: name}, true
		}
	}
	return config.ModelRef{ProviderID: fallbackProvider, Model: fallbackModel}, false
}

func (s *AccountSource) permits(name string) bool {
	for _, n := range s.names {
		if name == n {
			return true
		}
	}
	for _, n := range s.defaults {
		if name == n {
			return true
		}
	}
	for _, r := range s.roles {
		if r.ID == name && r.Enabled {
			return true
		}
	}
	return false
}

// RoleProvider resolves non-conversational requests without rewriting them to
// the conversational alias. Empty bindings remain explicitly unavailable.
func (p *Pool) RoleProvider(role string) (config.Provider, bool, error) {
	ref, configured := p.RoleSelection(role, "", "")
	if !configured {
		return config.Provider{}, false, nil
	}
	if ref.ProviderID == "" || ref.Model == "" {
		return config.Provider{}, true, ErrModelPolicy
	}
	if err := p.validateModelSelection(ref.ProviderID, ref.Model); err != nil {
		return config.Provider{}, true, err
	}
	prov, err := p.Resolve(ref.ProviderID)
	prov.Model = ref.Model
	if src := p.activeAccount(); src != nil && src.id == ref.ProviderID {
		for _, r := range src.roles {
			if r.ID == role {
				prov.Label = r.Label
			}
		}
	}
	return prov, true, err
}

func (p *Pool) validateModelSelection(id, name string) error {
	if p.mock {
		return nil
	}
	src := p.activeAccount()
	if src == nil && p.cfg.ProductSnapshot().Resolved() != config.ProductModeLocal && p.cfg.Snapshot().Remote.AuthMode == config.RemoteAuthAccount {
		return ErrModelPolicy
	}
	if src != nil && src.unavailable && id == src.id {
		return ErrAccountModelsUnavailable
	}
	if strings.HasPrefix(id, config.AccountSourcePrefix) && (src == nil || src.id != id) {
		return ErrAccountLoginRequired
	}
	if src != nil && src.managed && (id != src.id || (name != "" && !src.permits(name))) {
		return ErrModelPolicy
	}
	return nil
}
