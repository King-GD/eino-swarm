package server

import (
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	swarm "github.com/LubyRuffy/eino-swarm"
	"github.com/LubyRuffy/eino-swarm/internal/engine"
	"github.com/LubyRuffy/eino-swarm/internal/store"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type historyWork struct {
	From      int64     `json:"from"`
	Through   int64     `json:"through"`
	TurnID    string    `json:"turn_id"`
	CreatedAt time.Time `json:"created_at"`
	AgentIDs  []string  `json:"agent_ids"`
	Thinking  bool      `json:"thinking"`
	Tools     int       `json:"tools"`
	Failed    bool      `json:"failed,omitempty"`
}

// A stored schedule turn owns its generated input. Text written by the
// human can resemble a schedule cue without changing the message boundary.
const scheduledHistoryUser = `(events.kind = ? AND EXISTS (
	SELECT 1 FROM turns
	WHERE turns.id = events.turn_id AND turns.thread_id = events.thread_id
	  AND turns.schedule_continue = ?
))`

type historyScanRow struct {
	store.Event
	ScheduledUser bool
}

type messageHistory struct {
	Events      []store.Event `json:"events"`
	Roster      []store.Event `json:"roster,omitempty"`
	HistoryWork []historyWork `json:"history_work,omitempty"`
	Before      int64         `json:"before"`
	Seq         int64         `json:"seq"`
	HasMore     bool          `json:"has_more"`
}

func (s *Server) listMessageHistory(c *gin.Context, threadID string, before int64) {
	var page messageHistory
	st := s.engine.Store()
	limit := parseLogLimit(c)
	err := st.DB().WithContext(c.Request.Context()).Transaction(func(db *gorm.DB) error {
		var err error
		page, err = readMessageHistory(db, threadID, before, limit)
		return err
	})
	if err == nil && before == 0 {
		var roster []store.Event
		roster, err = st.ListRosterEvents(threadID)
		page.Roster = rosterOutsidePage(roster, page.Events)
	}
	if err != nil {
		s.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, page)
}

// A tail includes the latest visible message and its local work up to the
// preceding message. An older read includes that preceding message and stops
// there, except at the start where we also return any leading markers.
func readMessageHistory(db *gorm.DB, threadID string, before int64, limit int) (messageHistory, error) {
	page := messageHistory{Events: []store.Event{}}
	reader := historyReader{db: db, threadID: threadID}
	var activeTurn string
	if err := db.Model(&store.Turn{}).Where("thread_id = ? AND status = ?", threadID, store.TurnRunning).
		Select("id").Order("seq desc").Limit(1).Scan(&activeTurn).Error; err != nil {
		return page, err
	}
	if before == 0 && activeTurn != "" {
		// Keep the same bounded live window for asks, pending tools and
		// restored worker controls. Older reads still use message boundaries.
		return reader.activeTail(page, limit)
	}
	messages, err := reader.previousMessages(before)
	if err != nil {
		return page, err
	}
	var floor int64
	if len(messages) == 2 {
		page.HasMore = true
		if before == 0 {
			floor = messages[1].Seq + 1
		} else {
			floor = messages[0].Seq
		}
	}
	compact := historyProjection{reader: reader, calls: make(map[string]map[string][]store.Event)}
	for cursor := before; ; {
		var rows []historyScanRow
		if err := reader.before(cursor).Where("seq >= ?", floor).
			Select("events.*, "+scheduledHistoryUser+" AS scheduled_user", engine.KindUser, true).
			Order("seq desc").Limit(maxLogLimit).Scan(&rows).Error; err != nil {
			return page, err
		}
		for _, row := range rows {
			if page.Seq == 0 {
				page.Seq = row.Seq
			}
			page.Before = row.Seq
			if row.ScheduledUser {
				continue
			}
			active := activeTurn != "" && row.TurnID == activeTurn
			local := before == 0 && len(messages) > 0 && row.TurnID == messages[0].TurnID
			if err := compact.add(&page, row.Event, local, active); err != nil {
				return page, err
			}
		}
		if len(rows) < maxLogLimit {
			break
		}
		cursor = rows[len(rows)-1].Seq
	}
	compact.flush(&page)
	slices.Reverse(page.Events)
	slices.Reverse(page.HistoryWork)
	return page, nil
}

type historyReader struct {
	db       *gorm.DB
	threadID string
}

func (r historyReader) query() *gorm.DB {
	return r.db.Model(&store.Event{}).Where("thread_id = ?", r.threadID)
}

func (r historyReader) before(seq int64) *gorm.DB {
	q := r.query()
	if seq > 0 {
		q = q.Where("seq < ?", seq)
	}
	return q
}

func (r historyReader) activeTail(page messageHistory, limit int) (messageHistory, error) {
	if err := r.query().Order("seq desc").Limit(limit + 1).Find(&page.Events).Error; err != nil {
		return page, err
	}
	page.HasMore = len(page.Events) > limit
	if page.HasMore {
		page.Events = page.Events[:limit]
	}
	if len(page.Events) > 0 {
		page.Seq, page.Before = page.Events[0].Seq, page.Events[len(page.Events)-1].Seq
		slices.Reverse(page.Events)
	}
	return page, nil
}

func (r historyReader) previousMessages(before int64) ([]store.Event, error) {
	var messages []store.Event
	for {
		var rows []store.Event
		err := r.before(before).Where("kind IN ?", []string{"user_message", "agent_message"}).
			Where("COALESCE(agent_id, '') IN ?", []string{"", swarm.DefaultManagerID}).
			Where("NOT "+scheduledHistoryUser, engine.KindUser, true).
			Select("seq", "turn_id", "kind", "text").Order("seq desc").Limit(maxLogLimit).Find(&rows).Error
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			if row.Kind == "agent_message" && strings.TrimSpace(row.Text) == "" {
				continue
			}
			messages = append(messages, row)
			if len(messages) == 2 {
				return messages, nil
			}
		}
		if len(rows) < maxLogLimit {
			return messages, nil
		}
		before = rows[len(rows)-1].Seq
	}
}

type historyProjection struct {
	reader historyReader
	calls  map[string]map[string][]store.Event
	work   historyWork
	tools  map[historyTool]bool
}

type historyTool struct {
	id         string
	generation int
	seq        int64
}

var historyWorkKinds = []string{
	"reasoning", "reasoning_delta", "tool_call", "tool_call_delta", "tool_result", "tool_delta", "builtin_tool", engine.KindHistoryReadStarted,
}

func managerEvent(row store.Event) bool {
	return row.AgentID == "" || row.AgentID == swarm.DefaultManagerID
}

func historyToolName(text string) string {
	name, args, _ := strings.Cut(strings.TrimSpace(text), "(")
	name = strings.TrimSpace(name)
	if name == engine.ToolAskUser {
		body, closed := strings.CutSuffix(args, ")")
		if _, err := engine.ParseAskArguments(body); !closed || err != nil {
			return ""
		}
	}
	return name
}

func (p *historyProjection) specialTool(row store.Event) (string, error) {
	if row.Kind == "tool_call" || row.Kind == "tool_call_delta" {
		return historyToolName(row.Text), nil
	}
	if row.Kind != "tool_result" || row.ToolCallID == "" {
		return "", nil
	}
	byID, ok := p.calls[row.TurnID]
	if !ok {
		byID = make(map[string][]store.Event)
		var calls []store.Event
		// The nearest preceding call owns the result even if a legacy
		// provider reused a call ID. Read arguments only for ask cards.
		name := "trim(substr(text, 1, instr(text, '(')-1))"
		err := p.reader.query().Where("turn_id = ? AND kind = ?", row.TurnID, "tool_call").
			Where("COALESCE(agent_id, '') IN ?", []string{"", swarm.DefaultManagerID}).
			Select("tool_call_id, seq, CASE WHEN "+name+" = ? OR instr(text, '(') = 0 THEN text ELSE "+name+" END AS text", engine.ToolAskUser).
			Order("seq asc").Find(&calls).Error
		if err != nil {
			return "", err
		}
		for _, call := range calls {
			byID[call.ToolCallID] = append(byID[call.ToolCallID], call)
		}
		p.calls[row.TurnID] = byID
	}
	calls := byID[row.ToolCallID]
	i := sort.Search(len(calls), func(i int) bool { return calls[i].Seq >= row.Seq })
	if i == 0 {
		return "", nil
	}
	return historyToolName(calls[i-1].Text), nil
}

func (p *historyProjection) add(page *messageHistory, row store.Event, local, active bool) error {
	manager := managerEvent(row)
	if p.work.From != 0 && row.TurnID != p.work.TurnID {
		p.flush(page)
	}
	if slices.Contains(historyWorkKinds, row.Kind) {
		if active || (local && manager) {
			page.Events = append(page.Events, row)
			return nil
		}
		if !manager {
			p.extendWork(row, row.AgentID)
			return nil
		}
		name, err := p.specialTool(row)
		if err != nil {
			return err
		}
		if name == engine.ToolAskUser && (row.Kind == "tool_call" || row.Kind == "tool_result") {
			p.flush(page)
			page.Events = append(page.Events, row)
			return nil
		}
		p.deferWork(row, name)
		return nil
	}
	if !active && !manager && (row.Kind == "agent_message" || row.Kind == "delta") {
		return nil
	}
	if splitsHistoryWork(row) {
		p.flush(page)
	}
	page.Events = append(page.Events, row)
	return nil
}

func splitsHistoryWork(row store.Event) bool {
	switch row.Kind {
	case "agent_message":
		return strings.TrimSpace(row.Text) != "" && managerEvent(row)
	case "spawned", "finished", "workers_retained", "turn", "usage", "title", "session_memory",
		"generation_started", "generation_completed", "engine_restart_checkpoint", "desktop_recovery",
		"steer_consumed", "steer_preempted", "steer_retracted", "steer_revised", "schedule_skipped",
		"decision", "composer_prediction", "history_report_requested",
		"history_registry_inherited", "mcp_catalog", "mcp_execution_blocked", "account_profile":
		return false
	default:
		return true
	}
}

func (p *historyProjection) extendWork(row store.Event, agent string) {
	if p.work.From == 0 {
		p.work = historyWork{Through: row.Seq, TurnID: row.TurnID}
		p.tools = make(map[historyTool]bool)
	}
	if !slices.Contains(p.work.AgentIDs, agent) {
		p.work.AgentIDs = append(p.work.AgentIDs, agent)
	}
	p.work.From, p.work.CreatedAt = row.Seq, row.CreatedAt
}

func (p *historyProjection) deferWork(row store.Event, name string) {
	p.extendWork(row, swarm.DefaultManagerID)
	switch row.Kind {
	case "reasoning", "reasoning_delta":
		p.work.Thinking = true
	case "tool_call", "tool_result", "tool_call_delta", "builtin_tool":
		if name == "spawn_agent" || name == "close_agent" {
			return
		}
		key := historyTool{id: row.ToolCallID, generation: row.WorkerGeneration}
		if key.id == "" || row.Kind == "builtin_tool" {
			key.seq = row.Seq
		}
		if !p.tools[key] {
			p.tools[key] = true
			p.work.Tools++
		}
		p.work.Failed = p.work.Failed || row.Err != ""
	}
}

func (p *historyProjection) flush(page *messageHistory) {
	if p.work.Thinking || p.work.Tools > 0 {
		page.HistoryWork = append(page.HistoryWork, p.work)
	}
	p.work = historyWork{}
}

func (s *Server) listHistoryWork(c *gin.Context, threadID string) {
	from, fromErr := strconv.ParseInt(c.Query("work_from"), 10, 64)
	through, throughErr := strconv.ParseInt(c.Query("work_through"), 10, 64)
	since, sinceErr := strconv.ParseInt(c.DefaultQuery("since", "0"), 10, 64)
	if fromErr != nil || throughErr != nil || sinceErr != nil || from <= 0 || through < from || since < 0 || since >= through {
		badRequest(c, "invalid history work range")
		return
	}
	limit := parseLogLimit(c)
	reader := historyReader{db: s.engine.Store().DB().WithContext(c.Request.Context()), threadID: threadID}
	events := []store.Event{}
	err := reader.query().Where("seq >= ? AND seq <= ? AND seq > ?", from, through, since).
		Where("COALESCE(agent_id, '') IN ?", []string{"", swarm.DefaultManagerID}).
		Where("kind IN ?", historyWorkKinds).Order("seq asc").Limit(limit + 1).Find(&events).Error
	if err != nil {
		s.fail(c, err)
		return
	}
	more := len(events) > limit
	if more {
		events = events[:limit]
	}
	seq := through
	if more {
		seq = events[len(events)-1].Seq
	}
	c.JSON(http.StatusOK, gin.H{"events": events, "seq": seq, "has_more": more})
}
