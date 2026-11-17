package provider

import (
	"strings"

	"github.com/cloudwego/eino/schema"
)

// singleLeadingSystem makes one provider request acceptable to chat templates
// that allow a system message only as the first message. A second system
// message is rejected even when it is still ahead of the user message, which
// is how a turn that already called tools fails on the next request.
//
// Leading plain system messages are joined, in order, into that first
// message. A system message after the conversation stays at that position
// with the user role, so a notice that arrived after tool results is not
// moved above the work it refers to. Agent state is unchanged: callers keep
// the separate system messages they already stored.
func singleLeadingSystem(in []*schema.Message) []*schema.Message {
	if !needsSingleLeadingSystem(in) {
		return in
	}
	out := make([]*schema.Message, 0, len(in))
	var lead *schema.Message
	var before []*schema.Message
	closed := false
	flushBefore := func() {
		out = append(out, before...)
		before = nil
	}
	for _, msg := range in {
		if msg == nil {
			if !closed && lead == nil {
				before = append(before, nil)
				continue
			}
			out = append(out, nil)
			continue
		}
		if !closed && plainSystem(msg) {
			if lead == nil {
				cp := *msg
				cp.Content = strings.TrimSpace(msg.Content)
				lead = &cp
				out = append(out, lead)
				flushBefore()
				continue
			}
			if text := strings.TrimSpace(msg.Content); text != "" {
				if lead.Content != "" {
					lead.Content += "\n\n"
				}
				lead.Content += text
			}
			continue
		}
		if msg.Role == schema.System && !closed && lead == nil {
			out = append(out, msg)
			flushBefore()
			closed = true
			continue
		}
		if msg.Role == schema.System {
			flushBefore()
			cp := *msg
			cp.Role = schema.User
			out = append(out, &cp)
			closed = true
			continue
		}
		flushBefore()
		closed = true
		out = append(out, msg)
	}
	flushBefore()
	return out
}

func needsSingleLeadingSystem(in []*schema.Message) bool {
	seenOther := false
	leading := 0
	for _, msg := range in {
		if msg == nil {
			continue
		}
		if msg.Role != schema.System {
			seenOther = true
			continue
		}
		if seenOther {
			return true
		}
		if !plainSystem(msg) {
			if leading > 0 {
				return true
			}
			seenOther = true
			continue
		}
		leading++
		if leading > 1 {
			return true
		}
	}
	return false
}

func plainSystem(msg *schema.Message) bool {
	return msg != nil && msg.Role == schema.System && len(msg.ToolCalls) == 0 && !hasMulti(msg)
}

func providerMessages(in []*schema.Message) []*schema.Message {
	return exclusiveContent(singleLeadingSystem(in))
}
