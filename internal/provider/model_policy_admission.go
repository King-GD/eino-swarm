package provider

import (
	"context"
	"strings"
	"sync"

	"github.com/LubyRuffy/eino-swarm/internal/config"
	"github.com/cloudwego/eino/components/model"
)

// TryHoldModelPolicy freezes model admission while the debug execution source
// is persisted. A stream retains its read hold until completion or cancellation.
func (p *Pool) TryHoldModelPolicy() (func(), bool) {
	if !p.policyUse.TryLock() {
		return nil, false
	}
	return p.policyUse.Unlock, true
}

func (p *Pool) sameAccountCredential(prov config.Provider, owner string) bool {
	if p.credentialMatches(p.account.Load(), prov, owner) {
		return true
	}
	for _, src := range p.localAccountSources() {
		if p.credentialMatches(src, prov, owner) {
			return true
		}
	}
	return false
}

// BeginModelUse covers typed calls (audio and embeddings) that bypass chat.
func (p *Pool) BeginModelUse() (func(), error) {
	if !p.policyUse.TryRLock() {
		return nil, ErrModelPolicy
	}
	return p.policyUse.RUnlock, nil
}

func (p *Pool) trackedModel(inner model.BaseChatModel, prov config.Provider) model.BaseChatModel {
	mode := p.cfg.ProductSnapshot().Resolved()
	owner := ""
	if src := p.accountForExecution(prov.ID); src != nil {
		owner = src.owner
	}
	tracked := &recordingModel{inner: inner, policyUse: func() (func(), error) {
		release, err := p.BeginModelUse()
		if err != nil {
			return nil, err
		}
		// A debug source switch does not retarget a client that already
		// resolved its endpoint. Later lookups read the new source.
		if mode != p.cfg.ProductSnapshot().Resolved() {
			if strings.HasPrefix(prov.ID, config.AccountSourcePrefix) && !p.sameAccountCredential(prov, owner) {
				release()
				return nil, ErrModelPolicy
			}
			return release, nil
		}
		if strings.HasPrefix(prov.ID, config.AccountSourcePrefix) {
			src := p.accountForExecution(prov.ID)
			if !p.credentialMatches(src, prov, owner) {
				release()
				return nil, ErrModelPolicy
			}
		}
		if err := p.validateModelSelection(prov.ID, prov.Model); err != nil {
			release()
			return nil, err
		}
		return release, nil
	}}
	if !p.mock {
		tracked.gate = p.backoffFor(prov.BaseURL)
	}
	return tracked
}

func (r *recordingModel) admit(ctx context.Context) (context.Context, func(error), error) {
	release := func() {}
	if r.policyUse != nil {
		var err error
		release, err = r.policyUse()
		if err != nil {
			return ctx, nil, err
		}
	}
	ctx, finish, err := r.admitBackoff(ctx)
	if err != nil {
		release()
		return ctx, nil, err
	}
	var once sync.Once
	return ctx, func(err error) { finish(err); once.Do(release) }, nil
}
