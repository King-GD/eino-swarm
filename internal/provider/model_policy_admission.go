package provider

import (
	"context"
	"strings"
	"sync"

	"github.com/LubyRuffy/eino-swarm/internal/config"
	"github.com/cloudwego/eino/components/model"
)

// TryHoldModelPolicy freezes new model admission while the debug execution
// source is persisted. Chat requests release their read hold after binding.
func (p *Pool) TryHoldModelPolicy() (func(), bool) {
	if !p.policyUse.TryLock() {
		return nil, false
	}
	p.chatPolicyUse.Lock()
	return func() {
		p.chatPolicyUse.Unlock()
		p.policyUse.Unlock()
	}, true
}

func (p *Pool) beginChatAdmission() (func(), error) {
	if !p.chatPolicyUse.TryRLock() {
		return nil, ErrModelPolicy
	}
	return p.chatPolicyUse.RUnlock, nil
}

// boundAccountSource looks up the authorization of a client already opened
// by a turn. Match the catalog as well as the credential because the primary
// and LOCAL preset can share an id and credential but have distinct grants.
func (p *Pool) boundAccountSource(prov config.Provider, bound *AccountSource) *AccountSource {
	if bound == nil {
		return nil
	}
	if src := p.account.Load(); p.credentialMatches(src, prov, bound.owner) && src.catalogGroup == bound.catalogGroup {
		return src
	}
	for _, src := range p.localAccountSources() {
		if p.credentialMatches(src, prov, bound.owner) && src.catalogGroup == bound.catalogGroup {
			return src
		}
	}
	return nil
}

// BeginModelUse guards source and credential selection against a mode save.
func (p *Pool) BeginModelUse() (func(), error) {
	if !p.policyUse.TryRLock() {
		return nil, ErrModelPolicy
	}
	return p.policyUse.RUnlock, nil
}

func (p *Pool) trackedModel(inner model.BaseChatModel, prov config.Provider) model.BaseChatModel {
	return p.trackedModelBinding(inner, prov, false)
}

func (p *Pool) trackedModelBinding(inner model.BaseChatModel, prov config.Provider, retained bool) model.BaseChatModel {
	mode := p.cfg.ProductSnapshot().Resolved()
	bound := p.accountSourceByID(prov.ID, retained || p.localExecution())
	account := strings.HasPrefix(prov.ID, config.AccountSourcePrefix)
	currentSource := func() *AccountSource {
		if retained || mode != p.cfg.ProductSnapshot().Resolved() {
			return p.boundAccountSource(prov, bound)
		}
		src := p.accountForExecution(prov.ID)
		if bound != nil && p.credentialMatches(src, prov, bound.owner) && src.catalogGroup == bound.catalogGroup {
			return src
		}
		return nil
	}
	tracked := &recordingModel{inner: inner, policyUse: func() (func(), error) {
		release, err := p.beginChatAdmission()
		if err != nil {
			return nil, err
		}
		// A debug source switch does not retarget a client that already
		// resolved its endpoint. Later lookups read the new source.
		if account {
			source := currentSource()
			if source != nil && source.unavailable {
				release()
				return nil, ErrAccountModelsUnavailable
			}
			if source == nil || source.negotiated && (source.capabilities == nil || !source.capabilities.Authorized || !source.capabilities.Available) {
				release()
				return nil, ErrModelPolicy
			}
		}
		if !retained && mode == p.cfg.ProductSnapshot().Resolved() {
			if err := p.validateModelSelection(prov.ID, prov.Model); err != nil {
				release()
				return nil, err
			}
		}
		return release, nil
	}}
	if account {
		tracked.roleSource = currentSource
	}
	if !p.mock {
		tracked.gate = p.backoffFor(prov.BaseURL)
	}
	return tracked
}

func (r *recordingModel) checkPolicy() error {
	if r.policyUse == nil {
		return nil
	}
	release, err := r.policyUse()
	if err != nil {
		return err
	}
	release()
	return nil
}

func (r *recordingModel) admit(ctx context.Context) (context.Context, func(error), error) {
	if err := r.checkPolicy(); err != nil {
		return ctx, nil, err
	}
	ctx, finish, err := r.admitBackoff(ctx)
	if err != nil {
		return ctx, nil, err
	}
	// A cooldown can outlive a source or grant change. Recheck before using
	// the already-bound client; neither backoff nor the response owns the lock.
	if r.gate != nil {
		if err := r.checkPolicy(); err != nil {
			finish(context.Canceled)
			return ctx, nil, err
		}
	}
	var once sync.Once
	return ctx, func(err error) {
		finish(err)
		once.Do(func() {
			if r.onResult != nil {
				r.onResult(err)
			}
		})
	}, nil
}
