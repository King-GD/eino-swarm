package provider

import (
	"context"
	"strings"

	"github.com/LubyRuffy/eino-swarm/internal/config"
	"github.com/cloudwego/eino/components/model"
)

// ResumeModelBuilder rebuilds a durable turn's model without applying the new
// conversation preset. Its account source must still be independently granted.
// Callers supply only a stored turn binding, never an explicit client choice.
func (p *Pool) ResumeModelBuilder(ctx context.Context, id, name, effort string, rec Recorder) (func(string, string) model.BaseChatModel, error) {
	return p.boundModelBuilder(ctx, id, name, p.effectiveReasoningEffort(effort), rec)
}

func (p *Pool) boundModelBuilder(ctx context.Context, id, name, effort string, rec Recorder) (func(string, string) model.BaseChatModel, error) {
	if p.mock || !strings.HasPrefix(id, config.AccountSourcePrefix) {
		return p.modelBuilder(ctx, id, name, effort, rec)
	}
	source := p.accountSourceByID(id, true)
	if primary := p.account.Load(); primary != nil && primary.unavailable && (source == nil || source.unavailable) {
		// Startup serves a restoring listener before the account discovery
		// goroutine finishes. Join that owned loop instead of failing the
		// durable turn against its temporary unavailable projection.
		p.mu.Lock()
		refresh := p.capabilitiesRefresh
		p.mu.Unlock()
		if refresh != nil {
			if err := refresh(ctx); err != nil {
				return nil, err
			}
			source = p.accountSourceByID(id, true)
		}
	}
	if source == nil {
		return nil, ErrAccountLoginRequired
	}
	if source.unavailable || !source.allowsRole("conversation") {
		return nil, ErrAccountModelsUnavailable
	}
	prov := source.provider(name)
	if name != "" {
		if !source.permits(name) {
			return nil, ErrModelPolicy
		}
		prov.Model = name
	}
	// Retained clients stay outside the new-selection cache. Sharing that cache
	// would give a subsequent ordinary lookup the recovery admission policy.
	p.mu.Lock()
	shared, err := p.build(ctx, prov)
	p.mu.Unlock()
	if err != nil {
		return nil, err
	}
	shared = p.trackedModelBinding(shared, prov, true)
	limit := p.cfg.SwarmSnapshot().CompletionTokenLimit()
	return func(role, agentID string) model.BaseChatModel {
		return wrap(p.guardCapability(shared, prov.ID, role), agentID, prov, effort, limit, rec)
	}, nil
}
