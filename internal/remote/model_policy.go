package remote

import (
	"encoding/json"
	"strings"

	"github.com/LubyRuffy/eino-swarm/internal/config"
	"github.com/LubyRuffy/eino-swarm/internal/provider"
)

func parseModelDefaults(raw json.RawMessage) (map[string]string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var defaults map[string]string
	if json.Unmarshal(raw, &defaults) != nil || len(defaults) > len(config.ModelRoles()) {
		return nil, errAccountResponse
	}
	for role, name := range defaults {
		if !config.KnownModelRole(role) || strings.TrimSpace(name) == "" || len(name) > accountMaxNameLen || strings.IndexFunc(name, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 {
			return nil, errAccountResponse
		}
	}
	return defaults, nil
}

func (b accountBootstrap) configurable() bool {
	if b.editable != nil {
		return *b.editable
	}
	return b.profile == nil && len(b.roles) == 0
}

// A managed preset is built exclusively from server-published role bindings.
// It must never discover a broader gateway catalog.
func fetchPreset(boot accountBootstrap, token string, who AccountModelsState) accountOutcome {
	if boot.defaults["conversation"] == "" && (boot.sourceCapabilities("preset") == nil || !boot.sourceCapabilities("preset").Available) {
		who.State, who.Reason = AccountUnavailable, AccountReasonUnsafe
		return accountOutcome{state: who}
	}
	cat := provider.Catalog{Types: map[string]string{}}
	seen := map[string]bool{}
	for _, role := range config.ModelRoles() {
		name := boot.defaults[role]
		if name == "" {
			continue
		}
		if !seen[name] {
			cat.Names = append(cat.Names, name)
			seen[name] = true
		}
		kind := role
		if role == "conversation" || role == "title" || role == "compact" || role == "translation" {
			kind = "chat"
		}
		cat.Types[name] = kind
	}
	src, err := provider.NewAccountSource(boot.sourceID, boot.label, boot.apiBaseURL, token, cat)
	if err != nil {
		who.State, who.Reason = AccountUnavailable, AccountReasonUnsafe
		return accountOutcome{state: who}
	}
	src.SetModelPolicy(false, boot.defaults)
	who.State, who.Label, who.SourceID, who.Models = AccountReady, boot.label, src.ID(), 1
	kinds := src.Kinds()
	who.Kinds = &kinds
	return accountOutcome{state: who, source: src}
}

// A private authorized account whose policy is still unknown stays closed. The
// projection is not an executable catalog and never invents a source identity.
func unavailableAccountPolicy(id accountIdentity, sourceID, label string, configurable bool) *provider.AccountSource {
	source := provider.NewUnavailableAccountSource(sourceID, label, id.origin+strings.TrimSuffix(desktopModelsPrefix, "/"), id.token, configurable)
	source.SetCapabilities(nil)
	source.SetAccountOwner(id.userID)
	return source
}
