package remote

import (
	"encoding/json"
	"slices"
	"strings"

	"github.com/LubyRuffy/eino-swarm"
	"github.com/LubyRuffy/eino-swarm/internal/config"
	"github.com/LubyRuffy/eino-swarm/internal/engine"
	"github.com/LubyRuffy/eino-swarm/internal/store"
)

// Allow for response metadata and separators when budgeting each item once.
const historyFrameReserve = 1024

// Keep the watch watermark and live delivery established by startWatch.
// A running turn needs its original event window for active controls.
func (p *LinkPump) compactReadyHistory(ready Response) Response {
	if ready.Seq <= 0 || ready.Status == nil || ready.Status.Running {
		return ready
	}
	history := compactPhoneHistory(p.eng.Store(), p.cfg, ready.ID, ready.ThreadID, ready.Path, ready.SessionID, ready.Seq+1)
	if !history.OK || history.Seq <= 0 {
		return ready
	}
	next := ready
	next.Events, next.More = history.Events, history.More
	next.HistoryWork, next.HistoryBefore = history.HistoryWork, history.Seq
	raw, err := json.Marshal(next)
	if err != nil || len(raw) > plaintextBudget() {
		return ready
	}
	return next
}

// Compact pages scan backward to the previous manager response and its prompt.
// Long work runs consume a single range descriptor, independent of their size.
func compactPhoneHistory(st *store.Store, cfg config.RemoteConfig, id, tid, path, sessionID string, before int64) Response {
	resp := okBase(id, path, sessionID)
	resp.ThreadID = tid
	budget := plaintextBudget() - historyFrameReserve
	work := HistoryWork{}
	agents := make(map[string]bool)
	asks := make(map[string]map[string]bool)
	done := false
	for !done {
		page, more, err := st.ListTailEvents(tid, before, watchEvents(cfg))
		if err != nil {
			return fail(id, path, sessionID, "", fmtErr(err))
		}
		if err := collectHistoryAskCalls(st, tid, page, asks); err != nil {
			return fail(id, path, sessionID, "", fmtErr(err))
		}
		for i := len(page) - 1; i >= 0; i-- {
			row := page[i]
			manager := row.AgentID == "" || row.AgentID == swarm.DefaultManagerID
			answer := manager && row.Kind == "agent_message" && strings.TrimSpace(row.Text) != ""
			prompt := manager && row.Kind == "user_message" && !strings.HasPrefix(row.Text, "This turn is a scheduled check.")
			if answer && resp.FocusSeq != 0 {
				resp.More, done = true, true
				break
			}
			if deferredHistoryWork(row, asks[row.TurnID]) {
				agent := row.AgentID
				if agent == "" {
					agent = swarm.DefaultManagerID
				}
				cost := 0
				if work.From == 0 {
					cost += 64
				}
				if !agents[agent] {
					cost += historyItemBytes(agent)
				}
				if cost > budget {
					resp.More, done = true, true
					break
				}
				budget -= cost
				if work.From == 0 {
					work.Through = row.Seq
				}
				if !agents[agent] {
					work.AgentIDs = append(work.AgentIDs, agent)
					agents[agent] = true
				}
				work.From = row.Seq
			} else if phoneHistoryEvent(row.Kind) {
				view := historyEventView(row, cfg)
				size := historyItemBytes(view)
				if size > budget {
					resp.More, done = true, true
					break
				}
				budget -= size
				if work.From != 0 {
					resp.HistoryWork = append(resp.HistoryWork, work)
					work = HistoryWork{}
					clear(agents)
				}
				resp.Events = append(resp.Events, view)
				if answer {
					resp.FocusSeq = row.Seq
				}
			}
			resp.Seq = row.Seq
			if prompt && resp.FocusSeq != 0 {
				resp.More, done = i > 0 || more, true
				break
			}
		}
		if !more || len(page) == 0 {
			break
		}
		before = page[0].Seq
	}
	if work.From != 0 {
		resp.HistoryWork = append(resp.HistoryWork, work)
	}
	slices.Reverse(resp.Events)
	slices.Reverse(resp.HistoryWork)
	return resp
}

func phoneHistoryEvent(kind string) bool {
	switch kind {
	case "generation_started", "generation_completed", "usage", "desktop_recovery", "engine_restart_checkpoint":
		return false
	default:
		return shouldPush(kind)
	}
}

// Keep the original ask and its settlement in the transcript, even when the
// two rows land on different storage pages or a worker reuses the call ID.
func collectHistoryAskCalls(st *store.Store, tid string, rows []store.Event, asks map[string]map[string]bool) error {
	for _, row := range rows {
		if row.Kind != "tool_result" || row.ToolCallID == "" ||
			(row.AgentID != "" && row.AgentID != swarm.DefaultManagerID) || asks[row.TurnID] != nil {
			continue
		}
		var ids []string
		err := st.DB().Model(&store.Event{}).Where(
			"thread_id = ? AND turn_id = ? AND kind = ? AND COALESCE(agent_id, '') IN ?",
			tid, row.TurnID, "tool_call", []string{"", swarm.DefaultManagerID},
		).Where("trim(substr(text, 1, instr(text, '(')-1)) = ?", engine.ToolAskUser).
			Pluck("tool_call_id", &ids).Error
		if err != nil {
			return err
		}
		calls := make(map[string]bool, len(ids))
		for _, id := range ids {
			calls[id] = true
		}
		asks[row.TurnID] = calls
	}
	return nil
}

func deferredHistoryWork(row store.Event, asks map[string]bool) bool {
	switch row.Kind {
	case "reasoning", "reasoning_delta", "tool_delta", "tool_call_delta", "builtin_tool":
		return true
	case "tool_call":
		name, _, _ := strings.Cut(strings.TrimSpace(row.Text), "(")
		name = strings.TrimSpace(name)
		return name != engine.ToolAskUser && name != engine.ToolReportSchedule
	case "tool_result":
		return (row.AgentID != "" && row.AgentID != swarm.DefaultManagerID) || !asks[row.ToolCallID]
	default:
		return false
	}
}

func historyEventView(row store.Event, cfg config.RemoteConfig) EventView {
	view := eventView(row, cfg)
	// Keep a single record representable even with a large configured clip or
	// upstream diagnostic. Real control envelopes retain their usual clip.
	view.Text = truncate(view.Text, config.DefaultRemoteEventChars)
	view.Err = truncate(view.Err, config.DefaultRemoteEventChars)
	return view
}

func historyItemBytes(view any) int {
	raw, _ := json.Marshal(view)
	return len(raw) + 1
}

func readHistoryWork(st *store.Store, cfg config.RemoteConfig, req Request, tid, path, sessionID string) Response {
	if req.WorkFrom <= 0 || req.WorkThrough < req.WorkFrom || req.Since < 0 || req.Since >= req.WorkThrough {
		return fail(req.ID, path, sessionID, "bad_request", "invalid history work range")
	}
	since := max(req.Since, req.WorkFrom-1)
	limit := watchEvents(cfg)
	rows, err := st.ListEvents(tid, since, limit+1)
	if err != nil {
		return fail(req.ID, path, sessionID, "", fmtErr(err))
	}
	resp := okBase(req.ID, path, sessionID)
	resp.ThreadID, resp.Seq = tid, since
	budget := plaintextBudget() - historyFrameReserve
	for i, row := range rows {
		if row.Seq > req.WorkThrough {
			break
		}
		if i == limit {
			resp.More = true
			break
		}
		switch row.Kind {
		case "reasoning", "reasoning_delta", "tool_call", "tool_call_delta", "tool_result", "tool_delta", "builtin_tool":
			view := historyEventView(row, cfg)
			size := historyItemBytes(view)
			if size > budget {
				resp.More = true
				return resp
			}
			budget -= size
			resp.Events = append(resp.Events, view)
		}
		resp.Seq = row.Seq
	}
	return resp
}
