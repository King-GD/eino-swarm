package provider

import (
	"strings"

	"github.com/LubyRuffy/eino-swarm/internal/config"
)

// localAccountList is the extra server catalogs a signed-in LOCAL session can
// switch among. Gateway mode keeps the managed projection and does not list them.
type localAccountList struct {
	sources []*AccountSource
}

// LocalAccountSources returns the extra catalogs published for LOCAL.
func (p *Pool) LocalAccountSources() []*AccountSource { return p.localAccountSources() }

func (p *Pool) localAccountSources() []*AccountSource {
	if p == nil {
		return nil
	}
	list := p.localAccounts.Load()
	if list == nil {
		return nil
	}
	return list.sources
}

// SetLocalAccountSources publishes the extra catalogs LOCAL can switch among.
// Nil clears them. Sign-out also clears them through SetAccountSource(nil).
func (p *Pool) SetLocalAccountSources(sources []*AccountSource) {
	if p == nil || p.mock {
		return
	}
	if p.sameLocalAccounts(sources) {
		return
	}
	if len(sources) == 0 {
		p.localAccounts.Store(nil)
	} else {
		p.localAccounts.Store(&localAccountList{sources: append([]*AccountSource(nil), sources...)})
	}
	p.Invalidate()
}

func (p *Pool) sameLocalAccounts(sources []*AccountSource) bool {
	current := p.localAccountSources()
	if len(current) != len(sources) {
		return false
	}
	for i, src := range current {
		if !src.equal(sources[i]) {
			return false
		}
	}
	return true
}

// accountForExecution resolves a stored gateway id while the account is signed
// in. LOCAL also searches the extra server catalogs. A missing source stays
// account_login_required and is not replaced by a personal provider.
func (p *Pool) accountForExecution(id string) *AccountSource {
	return p.accountSourceByID(id, p.localExecution())
}

// accountSourceByID may include hidden, independently authorized catalogs only
// for a model already bound to a durable turn or worker.
func (p *Pool) accountSourceByID(id string, includeHidden bool) *AccountSource {
	if p == nil {
		return nil
	}
	id = strings.TrimSpace(id)
	if !strings.HasPrefix(id, config.AccountSourcePrefix) {
		return nil
	}
	if includeHidden {
		for _, src := range p.localAccountSources() {
			if src != nil && src.ID() == id {
				return src
			}
		}
	}
	if src := p.account.Load(); src != nil && src.ID() == id {
		return src
	}
	return nil
}

func (p *Pool) effectiveLocalAccountSources() []*AccountSource {
	local := p.localAccountSources()
	sources := make([]*AccountSource, 0, len(local)+1)
	seen := make(map[string]bool, len(local)+1)
	add := func(src *AccountSource) {
		if src == nil || src.id == "" || seen[src.id] {
			return
		}
		seen[src.id] = true
		sources = append(sources, src)
	}
	for _, src := range local {
		add(src)
	}
	add(p.account.Load())
	return sources
}

func (p *Pool) listLocalModels() []Info {
	personal := listModels(p.cfg.Snapshot(), p.mock)
	defaultID := p.DefaultProviderID()
	var accountRows []Info
	for _, src := range p.effectiveLocalAccountSources() {
		if !src.unavailable {
			isDefault := src.ID() == defaultID
			preferred := ""
			if name, follows := src.localConversationDefault(); isDefault && follows {
				preferred = name
			}
			accountRows = append(accountRows, src.rows(isDefault, preferred)...)
		}
	}
	return p.markConversationBinding(append(accountRows, personal...))
}

// LOCAL uses the original account catalog when available. An independently
// authorized preset is the fallback if the original has no chat capability.
func (p *Pool) localConversationSource() *AccountSource {
	var fallback *AccountSource
	for _, src := range p.effectiveLocalAccountSources() {
		if !src.negotiated || !src.canStartConversation() {
			continue
		}
		if src.catalogGroup == "original" {
			return src
		}
		if fallback == nil {
			fallback = src
		}
	}
	return fallback
}

func (s *AccountSource) localConversationDefault() (name string, follows bool) {
	if s.profiled {
		return config.OfficialModelAlias, true
	}
	name = chooseName(s.conversationalNames(), s.defaults["conversation"])
	return name, name != "" && name == s.defaults["conversation"]
}

func (p *Pool) credentialMatches(src *AccountSource, prov config.Provider, owner string) bool {
	return src != nil && src.id == prov.ID && src.owner == owner && src.token == prov.APIKey && src.baseURL == prov.BaseURL
}
