package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/LubyRuffy/eino-swarm/internal/memory"
	"github.com/LubyRuffy/eino-swarm/internal/store"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// sideChatSources is the read-only material a side question may use. The
// memory store is held only so a recorded skill can be opened; viewing it
// does not record usage or change the notes.
type sideChatSources struct {
	evidence     []store.SideChatEvidence
	notes        []string
	notesClipped bool
	skills       []memory.SkillInfo
	store        *memory.Store
}

func (e *Engine) sideChatSources(ctx context.Context, thread store.Thread) (sideChatSources, error) {
	evidence, err := e.store.RecentSideChatEvidence(ctx, thread.ID)
	if err != nil {
		return sideChatSources{}, err
	}
	sources := sideChatSources{evidence: evidence}
	mem := e.sideMemory(thread)
	if mem == nil {
		return sources, nil
	}
	if thread.ProjectID != "" {
		snap, err := mem.Read()
		if err != nil {
			if e.log != nil {
				e.log.Warn("could not read project notes for side chat", "project", thread.ProjectID, "err", err)
			}
		} else {
			sources.notes, sources.notesClipped = clipSideNotes(snap.Entries)
		}
	}
	skills, err := mem.ListSkills()
	if err != nil {
		if e.log != nil {
			e.log.Warn("could not list skills for side chat", "err", err)
		}
		return sources, nil
	}
	if max := e.cfg.Memory.IndexMax(); max > 0 && len(skills) > max {
		skills = skills[:max]
	}
	sources.skills = skills
	if len(skills) > 0 {
		sources.store = mem
	}
	return sources, nil
}

func (e *Engine) sideMemory(thread store.Thread) *memory.Store {
	if e.cfg == nil || !e.cfg.Memory.Enabled {
		return nil
	}
	if thread.ProjectID == "" {
		return e.LibraryMemory()
	}
	project, err := e.store.GetProject(thread.ProjectID)
	if err != nil || !e.MemoryEnabled(project) {
		return nil
	}
	return e.ProjectMemory(project.ID)
}

func clipSideNotes(entries []string) ([]string, bool) {
	var out []string
	used := 0
	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if used+len(entry) > sideChatMaxQuestion && len(out) > 0 {
			return out, true
		}
		if len(entry) > sideChatMaxQuestion {
			return []string{clipSideChatText(entry, sideChatMaxQuestion)}, true
		}
		out = append(out, entry)
		used += len(entry)
	}
	return out, false
}

func (e *Engine) answerSideChat(ctx context.Context, threadID string, chatModel model.BaseChatModel, messages []*schema.Message, sources sideChatSources, onDelta func(string), limit int) (string, error) {
	skills := len(sources.skills) > 0
	lookup := []model.Option{model.WithTools(sideChatToolInfos(skills)), model.WithToolChoice(schema.ToolChoiceAllowed), model.WithMaxTokens(limit)}
	final := []model.Option{model.WithTools(nil), model.WithToolChoice(schema.ToolChoiceForbidden), model.WithMaxTokens(limit)}
	for round := 0; ; round++ {
		options := lookup
		if round >= sideChatMaxLookups {
			options = final
		}
		content, calls, err := completeSideRound(ctx, chatModel, messages, onDelta, options)
		if ctx.Err() != nil {
			return "", context.Cause(ctx)
		}
		if err != nil {
			return "", err
		}
		if len(calls) == 0 {
			if strings.TrimSpace(content) == "" {
				return "", ErrSideChatUnavailable
			}
			return clipSideChatText(content, sideChatMaxAnswer), nil
		}
		if round >= sideChatMaxLookups || !sideCallsAllowed(calls, skills) {
			return "", ErrSideChatUnavailable
		}
		messages = append(messages, schema.AssistantMessage(content, calls))
		for i, call := range calls {
			if err := ctx.Err(); err != nil {
				return "", context.Cause(ctx)
			}
			result, err := e.sideLookup(ctx, threadID, sources, call)
			if err != nil {
				return "", err
			}
			id := call.ID
			if id == "" {
				id = fmt.Sprintf("side-lookup-%d-%d", round, i)
			}
			messages = append(messages, schema.ToolMessage(result, id))
		}
	}
}

func completeSideRound(ctx context.Context, chatModel model.BaseChatModel, messages []*schema.Message, onDelta func(string), options []model.Option) (string, []schema.ToolCall, error) {
	if onDelta != nil {
		return receiveSideChat(ctx, chatModel, messages, onDelta, options)
	}
	out, err := chatModel.Generate(ctx, messages, options...)
	if err != nil {
		return "", nil, err
	}
	if out == nil {
		return "", nil, ErrSideChatUnavailable
	}
	return strings.TrimSpace(out.Content), out.ToolCalls, nil
}

func sideChatToolInfos(skills bool) []*schema.ToolInfo {
	search := &schema.ToolInfo{
		Name: sideChatSearchTool,
		Desc: "Search this conversation's task record for a short literal substring. " +
			"Matches user messages, manager and worker messages, and tool results, newest first. " +
			"It does not search reasoning, diagnostics, attachments, or any other conversation. " +
			"Use it before treating a record or field as missing from the preview.",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"query": {Type: schema.String, Required: true, Desc: "a distinctive substring from the record being sought"},
		}),
	}
	if !skills {
		return []*schema.ToolInfo{search}
	}
	return []*schema.ToolInfo{search, {
		Name: memory.ToolSkillView,
		Desc: "Read one recorded skill in full. Only a name from the skill index can be opened. This does not change the skill.",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"name": {Type: schema.String, Required: true, Desc: "the skill name listed in the index"},
		}),
	}}
}

func sideCallsAllowed(calls []schema.ToolCall, skills bool) bool {
	if len(calls) == 0 || len(calls) > sideChatMaxCalls {
		return false
	}
	for _, call := range calls {
		switch call.Function.Name {
		case sideChatSearchTool:
		case memory.ToolSkillView:
			if !skills {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func (e *Engine) sideLookup(ctx context.Context, threadID string, sources sideChatSources, call schema.ToolCall) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", context.Cause(ctx)
	}
	switch call.Function.Name {
	case sideChatSearchTool:
		return e.searchSideTask(ctx, threadID, call.Function.Arguments)
	case memory.ToolSkillView:
		return readSideSkill(sources, call.Function.Arguments), nil
	default:
		return "", ErrSideChatUnavailable
	}
}

func (e *Engine) searchSideTask(ctx context.Context, threadID, raw string) (string, error) {
	var args struct {
		Query string `json:"query"`
	}
	if json.Unmarshal([]byte(raw), &args) != nil {
		return sideLookupJSON(map[string]any{"matches": []any{}, "error": "could not read the arguments"}), nil
	}
	query := strings.TrimSpace(args.Query)
	if utf8.RuneCountInString(query) < sideChatQueryMin || len(query) > sideChatQueryMax {
		return sideLookupJSON(map[string]any{"matches": []any{}, "error": "query must be a short distinctive substring"}), nil
	}
	rows, err := e.store.SearchSideChatEvidence(ctx, threadID, query)
	if err != nil {
		return "", err
	}
	matches := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		text := clipSideChatText(row.Text, sideChatLineBytes)
		item := map[string]any{"seq": row.Seq, "kind": row.Kind, "text": text}
		if row.AgentID != "" {
			item["agent"] = row.AgentID
		}
		matches = append(matches, item)
	}
	return sideLookupJSON(map[string]any{"matches": matches}), nil
}

func readSideSkill(sources sideChatSources, raw string) string {
	if sources.store == nil {
		return sideLookupJSON(map[string]any{"success": false, "error": "no recorded skills"})
	}
	var args struct {
		Name string `json:"name"`
	}
	if json.Unmarshal([]byte(raw), &args) != nil {
		return sideLookupJSON(map[string]any{"success": false, "error": "could not read the arguments"})
	}
	skill, err := sources.store.ReadSkill(strings.TrimSpace(args.Name))
	if err != nil {
		if errors.Is(err, memory.ErrNoMatch) {
			names := make([]string, 0, len(sources.skills))
			for _, item := range sources.skills {
				names = append(names, item.Name)
			}
			return sideLookupJSON(map[string]any{"success": false, "error": "no recorded skill with that name", "available": names})
		}
		return sideLookupJSON(map[string]any{"success": false, "error": "could not read the skill"})
	}
	return sideLookupJSON(map[string]any{
		"success":     true,
		"name":        skill.Name,
		"description": skill.Description,
		"body":        clipSideChatText(skill.Body, sideChatMaxQuestion),
	})
}

func sideLookupJSON(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		return `{"error":"could not encode the lookup"}`
	}
	return string(raw)
}
