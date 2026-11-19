package store

import (
	"fmt"
	"time"

	"gorm.io/gorm"
)

// CommitSteerDelivery moves the exact queued revision into the transcript.
// The queue claim, attachment binding and both replay rows commit together.
func (s *Store) CommitSteerDelivery(event *Event, message Message, files []Attachment, queued *Followup) error {
	threadID := event.ThreadID
	seq, err := s.nextEventSeq(threadID)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	event.Seq, event.CreatedAt = seq, now
	message.ThreadID, message.TurnID, message.EventSeq, message.CreatedAt = threadID, event.TurnID, seq, now
	message.Seq = s.nextSeq(s.msgSeq, threadID, func() int64 { return s.maxSeq(&Message{}, "seq", threadID) })
	err = s.db.Transaction(func(tx *gorm.DB) error {
		if queued != nil {
			claimed := tx.Where("id = ? AND thread_id = ? AND seq = ?", queued.ID, threadID, queued.Seq).Delete(&Followup{})
			if claimed.Error != nil {
				return claimed.Error
			}
			if claimed.RowsAffected != 1 {
				return ErrNotFound
			}
		}
		for _, file := range files {
			if err := bindAttachmentTurn(tx, threadID, file.RelPath, event.TurnID); err != nil {
				return err
			}
		}
		if err := tx.Create(event).Error; err != nil {
			return err
		}
		return tx.Create(&message).Error
	})
	if err := s.threadWriteErr(threadID, err); err != nil {
		return fmt.Errorf("store: commit steering delivery: %w", err)
	}
	return nil
}
