package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"

	"gorm.io/gorm"
)

// HistoryIdentity identifies an original history row. Editable steer messages
// also carry a digest of the caption that currently occupies that row.
type HistoryIdentity struct {
	RowID        uint
	TurnID       string
	OriginTurnID string
	Role         string
	Source       string
	EventKind    string
	CreatedAt    sql.NullTime
	SteerDigest  string `gorm:"-"`
}

// ReadProjectHistoryIdentities resolves canonical locators against one current
// project. Missing, ambiguous, foreign and private rows yield a nil slot.
func (s *Store) ReadProjectHistoryIdentities(ctx context.Context, projectID string, refs []HistoryReference) ([]*HistoryIdentity, error) {
	if projectID == "" {
		return nil, ErrHistoryReferenceInvalid
	}
	for _, ref := range refs {
		if (ref.Kind != "message" && ref.Kind != "event") || ref.ThreadID == "" || ref.Seq <= 0 {
			return nil, ErrHistoryReferenceInvalid
		}
	}
	results := make([]*HistoryIdentity, len(refs))
	if len(results) == 0 {
		return results, nil
	}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for i, ref := range refs {
			row, err := readProjectHistoryIdentity(tx, projectID, ref)
			if err != nil {
				return err
			}
			results[i] = row
		}
		return nil
	}, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	return results, nil
}

func readProjectHistoryIdentity(tx *gorm.DB, projectID string, ref HistoryReference) (*HistoryIdentity, error) {
	table, source, eventKind := "messages", "s.source", "''"
	caption := "CASE WHEN s.role = 'user' AND s.event_seq > 0 THEN s.content END"
	if ref.Kind == "event" {
		table, source, eventKind = "events", "''", "s.kind"
		caption = "NULL"
	}
	query := scopedHistorySourceQuery(tx, table, projectID, ref)
	query = query.Joins("LEFT JOIN turns AS owner ON owner.id=s.turn_id AND owner.thread_id=s.thread_id")
	var rows []struct {
		HistoryIdentity
		EditableCaption sql.NullString
	}
	err := query.Select("s.id AS row_id, COALESCE(owner.id,'') AS turn_id, COALESCE(s.turn_id,'') AS origin_turn_id, s.role, " + source +
		" AS source, " + eventKind + " AS event_kind, s.created_at, " + caption + " AS editable_caption").Limit(2).Find(&rows).Error
	if err != nil || len(rows) != 1 {
		return nil, err
	}
	row := &rows[0].HistoryIdentity
	if caption := rows[0].EditableCaption; caption.Valid {
		digest := sha256.Sum256([]byte(caption.String))
		row.SteerDigest = hex.EncodeToString(digest[:])
	}
	if ref.Kind == "message" && row.Role == "user" && row.Source == "" && row.TurnID != "" {
		// Legacy user rows need original text to distinguish human input from runtime cues.
		var message Message
		if err := tx.Select("thread_id", "turn_id", "seq", "content", "event_seq").Take(&message, row.RowID).Error; err != nil {
			return nil, err
		}
		human, err := messageHasHumanSource(tx, message)
		if err != nil {
			return nil, err
		}
		if human {
			row.Source = MessageSourceHuman
		}
	}
	return row, nil
}
