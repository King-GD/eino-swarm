package knowledge

import (
	"strings"
	"unicode"
)

// WikiNonSubject recognizes workflow and editorial labels from otherwise
// citable sources. Categories outside this narrow set remain open vocabulary.
func WikiNonSubject(kind, category, title, quote string) bool {
	category = strings.Join(strings.FieldsFunc(strings.ToLower(category), func(ch rune) bool {
		return !unicode.IsLetter(ch) && !unicode.IsNumber(ch)
	}), " ")
	switch category {
	case "request", "请求":
		// A named request record can be an entity. Asking someone to do
		// something does not establish an observed occurrence.
		return kind == "event"
	case "user request", "instruction", "user instruction", "workflow instruction",
		"agent action", "assistant action", "tool action", "planned action", "proposed action", "next step",
		"assistant narration", "status message", "progress message", "progress update",
		"page title", "ui title", "section heading", "document heading", "table heading", "table caption",
		"用户请求", "指令", "操作指令", "助手动作", "计划动作", "进度提示", "状态消息", "页面标题", "小节标题":
		return true
	}
	if kind != "event" {
		return false
	}
	line := strings.TrimSpace(quote)
	if strings.ContainsAny(line, "\r\n") {
		return false
	}
	if heading := strings.TrimLeft(line, "#"); heading != line &&
		strings.HasPrefix(heading, " ") && wikiLabelText(heading) == title {
		return true
	}
	if category == "timeline observation" && strings.HasPrefix(line, "|") && wikiTimelineLabel(title) {
		// Keep event names in table cells. A timeline caption describes
		// the organization of the evidence in the row.
		for cell := range strings.SplitSeq(line, "|") {
			if wikiLabelText(cell) == title {
				return true
			}
		}
	}
	return false
}

func wikiTimelineLabel(title string) bool {
	title = strings.TrimRight(normalizedName(title), ")）")
	return title == "timeline" || strings.HasSuffix(title, " timeline") || strings.HasSuffix(title, "(timeline") ||
		strings.HasSuffix(title, "时间线") || strings.HasSuffix(title, "时间轴") || strings.HasSuffix(title, "沿革")
}

func wikiLabelText(text string) string {
	return strings.Trim(strings.TrimSpace(text), "*_` ")
}

func isWikiReviewOnly(page Page) bool {
	if page.Type != wikiNodeType || page.Wiki == nil || len(page.Wiki.Evidence) == 0 {
		return false
	}
	// Different referents can share a name. Ignore the disambiguator
	// added when the generator reserved the page title.
	title := strings.TrimSuffix(page.Title, " ("+page.Wiki.Category+" · "+page.ID+")")
	for _, evidence := range page.Wiki.Evidence {
		if !WikiNonSubject(page.Wiki.Kind, page.Wiki.Category, title, evidence.Quote) {
			return false
		}
	}
	return true
}
