package store

import "fmt"

// SQLite's trim defaults to ASCII spaces. Replay deduplication has always used
// Go's strings.TrimSpace, including its Unicode White_Space characters.
const replayWhitespace = "\t\n\v\f\r \u0085\u00a0\u1680\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200a\u2028\u2029\u202f\u205f\u3000"

// ListReplayMessages reads only conversational rows beyond the compact watermark.
// Reasoning/tool-call columns are not replayed, so their bodies stay in SQLite.
// Pin the role index: ORDER BY alone otherwise favors the old seq index and
// visits every tool row before applying the role filter.
func (s *Store) ListReplayMessages(threadID string, through int64, skipTurnID string) ([]Message, error) {
	q := s.db.Table("messages INDEXED BY idx_msg_thread_role_seq").Select("thread_id", "turn_id", "seq", "role", "content", "images", "event_seq", "source").
		Where("thread_id = ? AND role IN ?", threadID, []string{"user", "assistant"})
	if through > 0 {
		q = q.Where("seq > ?", through)
	}
	if skipTurnID != "" {
		q = q.Where("turn_id <> ?", skipTurnID)
	}
	var rows []Message
	if err := q.Order("seq asc").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("store: list replay messages: %w", err)
	}
	return rows, nil
}

// ListReplayEvents reads retraction evidence and manager answers that still need
// repair. Event seq and message seq have different watermarks: steering evidence
// must remain available even when its seq precedes the message watermark.
//
// Manager answers are not read through the kind index. That plan visits every
// historical manager message in the conversation, including bodies the filter
// then discards, and opening a long conversation waited on it. Live turns are
// the ones that still have a message past the compact watermark; only those
// turns are candidates, and a stored assistant copy still suppresses repair.
func (s *Store) ListReplayEvents(threadID string, through int64, skipTurnID string) ([]Event, error) {
	var steers []Event
	err := s.db.Table("events INDEXED BY idx_evt_thread_kind_seq").
		Where("thread_id = ? AND seq > 0 AND kind IN ?", threadID, []string{"steer_retracted", "steer"}).
		Order("seq asc").Find(&steers).Error
	if err != nil {
		return nil, fmt.Errorf("store: list replay events: %w", err)
	}
	// A turn can be live even if it contains only tool/empty/retracted rows.
	// The message watermark is on seq, not on the event, so the turn list is
	// the identity check. Do not repair an answer already stored below the
	// compact watermark: that would give it a new seq and resurrect compacted
	// context. The turn-id index bounds each lookup to one turn.
	live := `turn_id IN (SELECT turn_id FROM messages WHERE thread_id = ? AND (? = 0 OR seq > ?) AND (? = '' OR turn_id <> ?))`
	missing := `NOT EXISTS (SELECT 1 FROM messages AS stored INDEXED BY idx_messages_turn_id
  WHERE stored.thread_id = events.thread_id AND stored.turn_id = events.turn_id
  AND stored.role = 'assistant' AND trim(stored.content, ?) = trim(events.text, ?))`
	var answers []Event
	err = s.db.Table("events INDEXED BY idx_evt_thread_turn_kind_seq").
		Where("thread_id = ? AND seq > 0 AND kind = ? AND (agent_id = '' OR agent_id = 'manager' OR agent_id IS NULL)", threadID, "agent_message").
		Where(live, threadID, through, through, skipTurnID, skipTurnID).
		Where(missing, replayWhitespace, replayWhitespace).
		Order("seq asc").Find(&answers).Error
	if err != nil {
		return nil, fmt.Errorf("store: list replay events: %w", err)
	}
	return mergeEventsBySeq(steers, answers), nil
}

func mergeEventsBySeq(left, right []Event) []Event {
	out := make([]Event, 0, len(left)+len(right))
	i, j := 0, 0
	for i < len(left) && j < len(right) {
		if left[i].Seq <= right[j].Seq {
			out = append(out, left[i])
			i++
			continue
		}
		out = append(out, right[j])
		j++
	}
	out = append(out, left[i:]...)
	out = append(out, right[j:]...)
	return out
}
