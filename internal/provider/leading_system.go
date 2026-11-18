package provider

import (
	"context"
	"errors"
	"fmt"
	"strings"

	einoopenai "github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/schema"
	openai "github.com/meguminnnnnnnnn/go-openai"
)

// systemOrderRetry is admitted only by an explicit typed input rejection, before
// any model output. Normal endpoints retain the original roles and chronology.
func systemOrderRetry(ctx context.Context, err error, in []*schema.Message) ([]*schema.Message, bool) {
	if ctx.Err() != nil || (HTTPStatus(err) != 400 && HTTPStatus(err) != 422) {
		return nil, false
	}
	var message string
	var eino *einoopenai.APIError
	var sdk *openai.APIError
	switch {
	case errors.As(err, &eino):
		message = eino.Message
	case errors.As(err, &sdk):
		message = sdk.Message
	}
	switch strings.TrimSuffix(strings.ToLower(strings.TrimSpace(message)), ".") {
	case "system message must be at the beginning", "system role must be at the beginning", "only one system message is allowed":
		if needsSingleLeadingSystem(in) {
			return singleLeadingSystem(in), true
		}
	}
	return nil, false
}

// singleLeadingSystem retains system authority and text without inventing a
// human message. Original transcript indices preserve the placement of later
// sections in this compatibility representation; stored ADK state stays intact.
// Non-text systems cannot be represented without loss and are never rewritten.
func singleLeadingSystem(in []*schema.Message) []*schema.Message {
	if !needsSingleLeadingSystem(in) {
		return in
	}
	var lead *schema.Message
	out := make([]*schema.Message, 1, len(in))
	for i, msg := range in {
		if msg == nil || msg.Role != schema.System {
			out = append(out, msg)
			continue
		}
		if lead == nil {
			cp := *msg
			lead = &cp
			out[0] = lead
			if i > 0 {
				lead.Content = fmt.Sprintf("[System message at transcript index %d]\n", i) + lead.Content
			}
		} else {
			lead.Content += fmt.Sprintf("\n\n[System message at transcript index %d]\n", i) + msg.Content
		}
	}
	return out
}

func needsSingleLeadingSystem(in []*schema.Message) bool {
	count, first := 0, -1
	for i, msg := range in {
		if msg == nil || msg.Role != schema.System {
			continue
		}
		if len(msg.ToolCalls) > 0 || hasMulti(msg) {
			return false
		}
		if first < 0 {
			first = i
		}
		count++
	}
	return count > 1 || count == 1 && first > 0
}
