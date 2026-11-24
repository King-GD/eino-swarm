package provider

import (
	"context"

	"github.com/cloudwego/eino/components/model"
)

const imageOCRRole = "ocr"

// ImageOCRModel binds the configured OCR provider and model for a single
// extraction. Conversation defaults and aliases do not participate.
func (p *Pool) ImageOCRModel(ctx context.Context, conversationProviderID string) (model.BaseChatModel, error) {
	release, err := p.BeginModelUse()
	if err != nil {
		return nil, err
	}
	defer release()
	ref, configured := p.RoleSelection(imageOCRRole, conversationProviderID, "")
	if !configured || ref.ProviderID == "" || ref.Model == "" || !p.FeatureAvailable(ref.ProviderID, imageOCRRole) {
		return nil, ErrModelPolicy
	}
	if err := p.validateModelSelection(ref.ProviderID, ref.Model); err != nil {
		return nil, err
	}
	prov, err := p.Resolve(ref.ProviderID)
	if err != nil {
		return nil, err
	}
	prov.Model = ref.Model
	if source := p.accountForExecution(ref.ProviderID); source != nil && source.negotiated {
		prov.APIMode = source.chatAPI(imageOCRRole)
	}
	p.mu.Lock()
	inner, err := p.build(ctx, prov)
	p.mu.Unlock()
	if err != nil {
		return nil, err
	}
	guarded := p.guardCapability(p.trackedModel(inner, prov), prov.ID, imageOCRRole)
	return WithoutBuiltinTools(wrap(guarded, imageOCRRole, prov, "", p.cfg.SwarmSnapshot().CompletionTokenLimit(), nil)), nil
}
