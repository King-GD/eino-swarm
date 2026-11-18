package store

import (
	"context"
	"fmt"
)

// KindImageGeneration is the private-media receipt shared by replay and recovery.
const KindImageGeneration = "image_generation"

// LatestImageGeneration returns the last stage of this turn's image operation.
func (s *Store) LatestImageGeneration(ctx context.Context, threadID, turnID string) (*Event, error) {
	var event Event
	result := s.db.WithContext(ctx).Where("thread_id = ? AND turn_id = ? AND kind = ?", threadID, turnID, KindImageGeneration).
		Order("seq desc").Limit(1).Find(&event)
	if result.Error != nil {
		return nil, fmt.Errorf("store: read image generation: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return nil, nil
	}
	return &event, nil
}

// GeneratedImageRef resolves a committed image for this exact conversation.
func (s *Store) GeneratedImageRef(threadID, imageID string) (ImageRef, error) {
	var event Event
	result := s.db.Select("images").Where("thread_id = ? AND kind = ? AND text = ? AND message_id = ?", threadID, KindImageGeneration, "completed", imageID).
		Limit(1).Find(&event)
	if result.Error != nil {
		return ImageRef{}, fmt.Errorf("store: read generated image reference: %w", result.Error)
	}
	if len(event.Images) != 1 || event.Images[0].ID != imageID {
		return ImageRef{}, ErrNotFound
	}
	return event.Images[0], nil
}
