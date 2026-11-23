package store

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"gorm.io/gorm"
)

const (
	sideChatTaskCharacters  = 4096
	sideChatEventCharacters = 1536
	sideChatTailRows        = 96
	sideChatContextRows     = 12
	sideChatEvidenceRows    = 8
	sideChatSearchRows      = 8
)

// SideChatContext is the bounded, user-facing text preview for a side question.
type SideChatContext struct {
	Thread Thread
	Latest Turn
	Events []Event
}

// ReadSideChatContext pins the thread, latest turn and transcript tail to one
// read snapshot. Only the text preview crosses the boundary, and the SQLite
// connection is released before the caller contacts a model.
func (s *Store) ReadSideChatContext(ctx context.Context, threadID string) (SideChatContext, error) {
	var out SideChatContext
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		err := tx.Model(&Thread{}).
			Select("id, project_id, provider_id, model, reasoning_effort, substr(goal, 1, ?) AS goal, goal_complete, goal_blocked, goal_capped", sideChatTaskCharacters).
			Where("id = ?", threadID).Take(&out.Thread).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		// A selected-text send places the human's request after its quotes.
		// Start at that boundary before truncating, even with a large quote.
		// The composer escapes this closing tag when it occurs inside a quote.
		const requestBoundary = "\n</selected_text>\n\n<user_request>\n"
		if err := tx.Model(&Turn{}).
			Select(`status, provider_id, model, reasoning_effort, quiet, goal_continue, schedule_continue,
				substr(user_text, CASE WHEN user_text GLOB '<selected_text>*'
					THEN max(1, instr(user_text, ?)) ELSE 1 END, ?) AS user_text`, requestBoundary, sideChatTaskCharacters).
			Where("thread_id = ?", threadID).Order("seq DESC").Limit(1).Find(&out.Latest).Error; err != nil {
			return err
		}
		// Search a fixed number of newest rows first, even in a long task
		// that is producing tool updates but very little visible prose.
		return tx.Raw(`
			SELECT recent.kind, substr(recent.text, 1, ?) AS text
			FROM (
				SELECT turn_id, seq, kind, agent_id, text
				FROM events WHERE thread_id = ? AND seq > 0
				ORDER BY seq DESC LIMIT ?
			) recent
			LEFT JOIN turns t ON t.id = recent.turn_id AND t.thread_id = ?
			WHERE COALESCE(t.quiet, false) = false
			  AND (recent.kind = 'user_message'
			    OR (recent.kind IN ('agent_message', 'done')
			      AND COALESCE(recent.agent_id, '') IN ('', 'manager')))
			ORDER BY recent.seq DESC LIMIT ?`,
			sideChatEventCharacters, threadID, sideChatTailRows, threadID, sideChatContextRows).Scan(&out.Events).Error
	})
	if err != nil {
		return SideChatContext{}, fmt.Errorf("store: read side chat context: %w", err)
	}
	slices.Reverse(out.Events)
	return out, nil
}

// SideChatEvidence is one visible task record a side question can read.
// Reasoning, diagnostics and other conversations are not included.
type SideChatEvidence struct {
	Seq     int64
	Kind    string
	AgentID string
	Text    string
}

// RecentSideChatEvidence is the newest tool results and worker findings, in
// chronological order. Manager prose is already in the transcript preview.
func (s *Store) RecentSideChatEvidence(ctx context.Context, threadID string) ([]SideChatEvidence, error) {
	rows, err := s.readSideEvidence(ctx, `
		SELECT e.seq, e.kind, e.agent_id, substr(e.text, 1, ?) AS text
		FROM events e
		LEFT JOIN turns t ON t.id = e.turn_id AND t.thread_id = e.thread_id
		WHERE e.thread_id = ?
		  AND e.seq > 0
		  AND COALESCE(t.quiet, false) = false
		  AND (
		    e.kind = 'tool_result'
		    OR (e.kind = 'agent_message' AND COALESCE(e.agent_id, '') NOT IN ('', 'manager'))
		  )
		ORDER BY e.seq DESC
		LIMIT ?`, sideChatEventCharacters, threadID, sideChatEvidenceRows)
	if err != nil {
		return nil, err
	}
	slices.Reverse(rows)
	return rows, nil
}

// SearchSideChatEvidence returns the newest records in this conversation whose
// text contains query. The query is a literal substring, not a pattern.
func (s *Store) SearchSideChatEvidence(ctx context.Context, threadID, query string) ([]SideChatEvidence, error) {
	return s.readSideEvidence(ctx, `
		SELECT e.seq, e.kind, e.agent_id, substr(e.text, 1, ?) AS text
		FROM events e
		LEFT JOIN turns t ON t.id = e.turn_id AND t.thread_id = e.thread_id
		WHERE e.thread_id = ?
		  AND e.seq > 0
		  AND COALESCE(t.quiet, false) = false
		  AND e.kind IN ('user_message', 'agent_message', 'done', 'tool_result')
		  AND e.text LIKE ? ESCAPE '\'
		ORDER BY e.seq DESC
		LIMIT ?`, sideChatEventCharacters, threadID, sideChatLike(query), sideChatSearchRows)
}

func (s *Store) readSideEvidence(ctx context.Context, query string, args ...any) ([]SideChatEvidence, error) {
	var rows []SideChatEvidence
	err := s.db.WithContext(ctx).Raw(query, args...).Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("store: read side chat evidence: %w", err)
	}
	out := make([]SideChatEvidence, 0, len(rows))
	for _, row := range rows {
		row.Text = strings.TrimSpace(row.Text)
		if row.Text == "" {
			continue
		}
		out = append(out, row)
	}
	return out, nil
}

func sideChatLike(query string) string {
	var b strings.Builder
	b.Grow(len(query) + 2)
	b.WriteByte('%')
	for _, r := range query {
		if r == '\\' || r == '%' || r == '_' {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	b.WriteByte('%')
	return b.String()
}
