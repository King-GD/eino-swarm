package engine

import (
	"context"
	"strings"
	"time"

	"github.com/LubyRuffy/eino-swarm/internal/provider"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// roleModel picks the fixed role for a FOFA account provider. ok is false when
// this install or provider is not in role mode, and the caller keeps its own
// model. An enabled role returns its id. Title and compact stay on when their
// switch is off, using conversation. Other roles return an empty model so the
// caller does not make the call.
func (e *Engine) roleModel(providerID, role string) (modelName string, ok bool) {
	if e == nil || e.cfg == nil || e.pool == nil {
		return "", false
	}
	ref, configured := e.pool.RoleSelection(role, providerID, "")
	return ref.Model, configured
}

func (e *Engine) applyRoleModel(providerID, role, fallbackProvider, fallbackModel string) (string, string) {
	ref, _ := e.pool.RoleSelection(role, fallbackProvider, fallbackModel)
	return ref.ProviderID, ref.Model
}

// enabledAccountRole returns a FOFA role only while that role is switched on.
// A missing or disabled role leaves the caller on its existing model.
func (e *Engine) enabledAccountRole(role string) (providerID, modelName string, ok bool) {
	if e == nil || e.cfg == nil || e.pool == nil || !e.cfg.Product.FOFA() {
		return "", "", false
	}
	id := e.pool.AccountSourceID()
	enabled, configured := e.pool.AccountRole(id, role)
	if !configured || !enabled {
		return "", "", false
	}
	return id, role, true
}

// readRoleImages sends user images through the ocr role and returns text for
// the conversation. used is false when the role is off, so the images stay on
// the original message.
func (e *Engine) readRoleImages(providerID, text string, images []ImageInput) (string, []ImageInput, bool, error) {
	if len(images) > 0 && !e.pool.FeatureAvailable(providerID, "ocr") {
		return "", nil, true, provider.ErrModelPolicy
	}
	ref, ok := e.pool.RoleSelection("ocr", providerID, "")
	modelName := ref.Model
	if !ok || modelName == "" || len(images) == 0 {
		return text, images, false, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	builder, err := e.pool.RoleModelBuilder(ctx, ref.ProviderID, modelName, "", nil, "ocr")
	if err != nil {
		return "", nil, true, err
	}
	out, err := ocrGenerate(ctx, builder("ocr", "ocr"), []*schema.Message{
		schema.SystemMessage("Read the text in the images. Reply with only that text."),
		BuildUserMessage(text, images),
	})
	if err != nil {
		return "", nil, true, err
	}
	read := ""
	if out != nil {
		read = strings.TrimSpace(out.Content)
	}
	if read == "" {
		read = strings.TrimSpace(text)
	} else if strings.TrimSpace(text) != "" {
		read = strings.TrimSpace(text) + "\n" + read
	}
	return read, nil, true, nil
}

var ocrGenerate = func(ctx context.Context, m model.BaseChatModel, msgs []*schema.Message) (*schema.Message, error) {
	return m.Generate(ctx, msgs)
}
