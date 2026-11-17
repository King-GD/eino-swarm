package provider

import (
	"strings"

	"github.com/LubyRuffy/eino-swarm/internal/config"
)

// localAccountList is the signed-in catalogs LOCAL can switch among. It is
// not the managed role projection: gateway mode keeps that projection, and
// functional roles do not read this list.
type localAccountList struct {
	sources []*AccountSource
}

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

// SetLocalAccountSources publishes the server catalogs a signed-in LOCAL
// session can switch among. Nil clears them. Sign-out also clears them
// through SetAccountSource(nil). A scripted pool ignores them.
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

// accountForExecution resolves a stored gateway id while the account is still
// signed in. LOCAL prefers the switchable server catalogs, then the retained
// projection, and does not treat that id as signed out. Gateway mode uses
// only the active projection, so a managed preset still hides other models.
func (p *Pool) accountForExecution(id string) *AccountSource {
	if p == nil {
		return nil
	}
	id = strings.TrimSpace(id)
	if !strings.HasPrefix(id, config.AccountSourcePrefix) {
		return nil
	}
	if p.localExecution() {
		for _, src := range p.localAccountSources() {
			if src != nil && src.ID() == id {
				return src
			}
		}
		if src := p.account.Load(); src != nil && src.ID() == id {
			return src
		}
		return nil
	}
	if src := p.activeAccount(); src != nil && src.ID() == id {
		return src
	}
	return nil
}

// switchableAccountSources is what the LOCAL composer can pick. A catalog
// that has no conversation row is skipped. With none published, the retained
// signed-in projection stays selectable so the login is not presented as gone.
func (p *Pool) switchableAccountSources() []*AccountSource {
	var listed []*AccountSource
	for _, src := range p.localAccountSources() {
		if src != nil && len(src.rows(false, "")) > 0 {
			listed = append(listed, src)
		}
	}
	if len(listed) > 0 {
		return listed
	}
	if src := p.account.Load(); src != nil && len(src.rows(false, "")) > 0 {
		return []*AccountSource{src}
	}
	return nil
}

func (p *Pool) listLocalModels() []Info {
	personal := listModels(p.cfg.Snapshot(), p.mock)
	var accountRows []Info
	for _, src := range p.switchableAccountSources() {
		accountRows = append(accountRows, src.rows(false, "")...)
	}
	return append(accountRows, personal...)
}

func (p *Pool) credentialMatches(src *AccountSource, prov config.Provider, owner string) bool {
	return src != nil && src.id == prov.ID && src.owner == owner && src.token == prov.APIKey && src.baseURL == prov.BaseURL
}
