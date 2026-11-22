package provider

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	openai "github.com/meguminnnnnnnnn/go-openai"
)

// ResponsesContinuation holds one conversation's latest verified prefix and
// upstream response ID in memory. The local transcript remains authoritative.
type ResponsesContinuation struct {
	mu   sync.Mutex
	last *responsesCheckpoint
}

type responsesCheckpoint struct {
	id    string
	shape [32]byte
	items int
	proof [32]byte
}

type responsesRequest struct {
	cache *ResponsesContinuation
	shape [32]byte
	items [][32]byte
	prev  *responsesCheckpoint
}

// WithResponsesContinuation gives the conversation manager a private cursor
// on an admitted account Responses model while sharing its existing client.
func WithResponsesContinuation(inner model.BaseChatModel, state *ResponsesContinuation) model.BaseChatModel {
	tracked, ok := inner.(*recordingModel)
	if !ok || tracked.roleSource == nil || state == nil {
		return inner
	}
	wire, ok := tracked.inner.(*responsesModel)
	if !ok {
		return inner
	}
	source := tracked.roleSource()
	if source == nil || !source.negotiated || !source.capabilities.Allows("conversation") {
		return inner
	}
	capability := source.capabilities.Capabilities["conversation"]
	if capability.ResponsesDeveloperHistory != 1 || !slices.Contains(capability.ResponsesModes, "native") {
		return inner
	}
	copy := *tracked
	copy.inner = wire.withContinuation(state)
	return &copy
}

func (m *responsesModel) withContinuation(state *ResponsesContinuation) *responsesModel {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return &responsesModel{provider: m.provider, client: m.client, tools: m.tools, continuation: state}
}

func (m *responsesModel) planContinuation(body map[string]any, input []any) (responsesRequest, error) {
	if m.continuation == nil || !gatewayResponsesDeveloperInput(input) {
		return responsesRequest{}, nil
	}
	// Compute the shape before filling the request's input and transport flags.
	// The endpoint and credential are part of the private identity of the ID.
	shape, err := json.Marshal([]any{m.provider.ID, m.provider.BaseURL, m.provider.APIKey, body})
	if err != nil {
		return responsesRequest{}, err
	}
	items, err := responsesItemDigests(input)
	if err != nil {
		return responsesRequest{}, err
	}
	plan := responsesRequest{cache: m.continuation, shape: sha256.Sum256(shape), items: items}
	m.continuation.mu.Lock()
	prior := m.continuation.last
	m.continuation.mu.Unlock()
	if prior != nil && prior.shape == plan.shape && prior.items < len(items) && prior.proof == responsesPrefixDigest(items[:prior.items]) {
		plan.prev = prior
	}
	return plan, nil
}

// Only the initial System messages represent the fixed rules of a PC request.
// A System item inserted later must take the full stateless path so an
// upstream renderer cannot discard it or move it across prior turns.
func gatewayResponsesDeveloperInput(input []any) bool {
	leading := 0
	for i, item := range input {
		fields, _ := item.(map[string]any)
		if fields["role"] == schema.System {
			if i != leading {
				return false
			}
			leading++
		}
	}
	for _, item := range input[:leading] {
		item.(map[string]any)["role"] = "developer"
	}
	return true
}

func responsesItemDigests(items []any) ([][32]byte, error) {
	digests := make([][32]byte, 0, len(items))
	for _, item := range items {
		// Raw provider items and locally reconstructed maps may order keys
		// differently. Keep number precision and all opaque item fields.
		fields, ok := item.(map[string]any)
		if !ok {
			raw, ok := item.(json.RawMessage)
			if !ok {
				return nil, fmt.Errorf("unexpected Responses input item %T", item)
			}
			decoder := json.NewDecoder(bytes.NewReader(raw))
			decoder.UseNumber()
			if err := decoder.Decode(&fields); err != nil {
				return nil, err
			}
		}
		if text, ok := plainResponsesAnswer(fields); ok {
			// The desktop transcript reconstructs plain assistant text without
			// the response item's type, id and completed status.
			digests = append(digests, sha256.Sum256([]byte("assistant\x00"+text)))
			continue
		}
		canonical, err := json.Marshal(fields)
		if err != nil {
			return nil, err
		}
		digests = append(digests, sha256.Sum256(canonical))
	}
	return digests, nil
}

func plainResponsesAnswer(fields map[string]any) (string, bool) {
	if (fields["role"] != "assistant" && fields["role"] != schema.Assistant) || (fields["type"] != nil && fields["type"] != "message") ||
		(fields["status"] != nil && fields["status"] != "completed") {
		return "", false
	}
	for key := range fields {
		switch key {
		case "type", "id", "role", "status", "content":
		default:
			return "", false
		}
	}
	var part map[string]any
	switch parts := fields["content"].(type) {
	case []any:
		if len(parts) == 1 {
			part, _ = parts[0].(map[string]any)
		}
	case []map[string]any:
		if len(parts) == 1 {
			part = parts[0]
		}
	}
	if part["type"] != "input_text" && part["type"] != "output_text" {
		return "", false
	}
	for key, value := range part {
		switch key {
		case "type", "text":
		case "annotations", "logprobs":
			if values, ok := value.([]any); value != nil && (!ok || len(values) != 0) {
				return "", false
			}
		default:
			return "", false
		}
	}
	text, ok := part["text"].(string)
	return text, ok
}

func responsesPrefixDigest(items [][32]byte) [32]byte {
	h := sha256.New()
	for _, item := range items {
		_, _ = h.Write(item[:]) // hash.Hash.Write never fails.
	}
	return [32]byte(h.Sum(nil))
}

func (p *responsesRequest) remember(ctx context.Context, response responsesResponse) {
	if p.cache == nil || ctx.Err() != nil || len(response.ID) == 0 || len(response.ID) > 256 || strings.ContainsAny(response.ID, " \t\r\n") || len(response.Output) == 0 {
		return
	}
	raw := make([]any, 0, len(response.Output))
	for _, item := range response.Output {
		raw = append(raw, item.raw)
	}
	output, err := responsesItemDigests(raw)
	if err != nil {
		return
	}
	prefix := append(p.items, output...)
	p.cache.mu.Lock()
	p.cache.last = &responsesCheckpoint{id: response.ID, shape: p.shape, items: len(prefix), proof: responsesPrefixDigest(prefix)}
	p.cache.mu.Unlock()
}

func (p *responsesRequest) forgetParent() {
	p.cache.mu.Lock()
	if p.cache.last == p.prev {
		p.cache.last = nil
	}
	p.cache.mu.Unlock()
	p.prev = nil
}

// Only an explicit pre-execution refusal may be replayed. An error after SSE
// admission may already have generated output or executed a hosted tool.
func responsesRebuildReason(err error, hasParent bool) (rebuild, withoutStore bool) {
	var api *openai.APIError
	if !errors.As(err, &api) {
		return false, false
	}
	code, _ := api.Code.(string)
	if api.HTTPStatusCode == http.StatusServiceUnavailable && code == "response_state_unavailable" && api.Message == "response state storage is unavailable" {
		return true, true
	}
	if !hasParent {
		return false, false
	}
	param := ""
	if api.Param != nil {
		param = *api.Param
	}
	switch {
	case api.HTTPStatusCode == http.StatusNotFound:
		// vLLM uses response_id on this POST even though the missing ID
		// belongs to the parent. It may be an upstream ID the client can't see.
		return code == "previous_response_not_found" || param == "previous_response_id" ||
			code == "not_found" && api.Message == "previous response was not found" ||
			api.Type == "invalid_request_error" && param == "response_id" && strings.HasPrefix(api.Message, "Response with id '") && strings.HasSuffix(api.Message, "' not found."), false
	case api.HTTPStatusCode == http.StatusBadRequest:
		return code == "previous_response_not_found" || code == "invalid_previous_response_id" && param == "previous_response_id", false
	case api.HTTPStatusCode == http.StatusServiceUnavailable:
		return code == "response_state_unavailable" && (api.Message == "previous response state is incomplete" || api.Message == "previous response state has an invalid mode") ||
			code == "provider_unavailable" && (api.Message == "no healthy provider can serve this model" || api.Message == "the selected provider configuration is unavailable"), false
	default:
		return false, false
	}
}

func (m *responsesModel) sendResponses(ctx context.Context, body map[string]any, input []any, stream bool) (*http.Response, responsesRequest, error) {
	plan, err := m.planContinuation(body, input)
	if err != nil {
		return nil, responsesRequest{}, fmt.Errorf("provider: compare responses request prefix: %w", err)
	}
	body["stream"] = stream
	body["store"] = plan.cache != nil
	body["input"] = input
	if plan.prev != nil {
		body["previous_response_id"] = plan.prev.id
		body["input"] = input[plan.prev.items:]
	}
	resp, err := m.postResponses(ctx, body)
	if plan.cache != nil && err != nil && ctx.Err() == nil {
		if rebuild, withoutStore := responsesRebuildReason(err, plan.prev != nil); rebuild {
			if plan.prev != nil {
				plan.forgetParent()
				// A pin refusal says nothing about the unpinned full-history
				// route. Let the second request supply its own health evidence.
				if observation, _ := ctx.Value(routeRejectionKey{}).(*routeRejection); observation != nil {
					observation.confirmed.Store(false)
				}
			}
			delete(body, "previous_response_id")
			body["input"] = input
			if withoutStore {
				body["store"] = false
				plan = responsesRequest{}
			}
			resp, err = m.postResponses(ctx, body)
		}
	}
	if resp != nil && (resp.Header.Get("X-AIGateway-Responses-Mode") != "native" ||
		resp.Header.Get("X-AIGateway-Responses-Developer-History") != "1") {
		// Only an actual native response on a Gateway that acknowledged
		// fixed instruction inheritance may become a continuation parent.
		plan = responsesRequest{}
	}
	return resp, plan, err
}
