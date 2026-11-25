package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/LubyRuffy/eino-swarm/internal/taskpanel"
	"gorm.io/gorm"
)

// StartTaskPanelSource records an owned source before external work starts.
func (s *Store) StartTaskPanelSource(ctx context.Context, src taskpanel.Source, panelID string) (taskpanel.Panel, error) {
	return s.setTaskPanelSourceStatus(ctx, src, panelID, "running")
}

// FinishTaskPanelSource durably settles a successful source, without changing data.
func (s *Store) FinishTaskPanelSource(ctx context.Context, src taskpanel.Source, panelID string) (taskpanel.Panel, error) {
	return s.setTaskPanelSourceStatus(ctx, src, panelID, "finished")
}

// InterruptTaskPanelSource records only terminal producer metadata after its
// execution context was cancelled. It cannot change plugin data or a successor.
func (s *Store) InterruptTaskPanelSource(ctx context.Context, src taskpanel.Source, panelID string) (taskpanel.Panel, error) {
	return s.setTaskPanelSourceStatus(ctx, src, panelID, "interrupted")
}

func (s *Store) setTaskPanelSourceStatus(ctx context.Context, src taskpanel.Source, panelID, status string) (out taskpanel.Panel, err error) {
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var e error
		out, e = settleTaskPanelSource(tx, src, panelID, status)
		return e
	})
	return
}

func settleTaskPanelSource(tx *gorm.DB, src taskpanel.Source, panelID, status string) (taskpanel.Panel, error) {
	if src.ThreadID == "" || src.TurnID == "" || src.AgentID == "" || src.ToolCallID == "" || src.Generation < 0 {
		return taskpanel.Panel{}, taskpanel.ErrInvalid
	}
	var turn Turn
	err := tx.Where("id = ? AND thread_id = ? AND EXISTS (SELECT 1 FROM threads WHERE id = ?)", src.TurnID, src.ThreadID, src.ThreadID).Take(&turn).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return taskpanel.Panel{}, ErrNotFound
	}
	if err != nil {
		return taskpanel.Panel{}, err
	}
	if turn.Status == TurnCancelled && status != "interrupted" {
		return taskpanel.Panel{}, taskpanel.ErrConflict
	}
	var row TaskPanelRow
	err = tx.Where("id = ? AND thread_id = ? AND hidden = ?", panelID, src.ThreadID, false).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return taskpanel.Panel{}, ErrNotFound
	}
	if err != nil {
		return taskpanel.Panel{}, err
	}
	var recorded taskpanel.Source
	if err = json.Unmarshal(row.SourceJSON, &recorded); err != nil {
		return taskpanel.Panel{}, fmt.Errorf("panel source: %w", err)
	}
	if recorded != src {
		return taskpanel.Panel{}, taskpanel.ErrConflict
	}
	if row.SourceStatus == status {
		return panelValue(row)
	}
	if row.Closed {
		return taskpanel.Panel{}, ErrNotFound
	}
	if status == "running" && row.SourceStatus != "" {
		return taskpanel.Panel{}, taskpanel.ErrConflict
	}
	if status == "finished" && row.SourceStatus != "running" {
		return taskpanel.Panel{}, taskpanel.ErrConflict
	}
	oldRevision, wasRunning := row.Revision, row.SourceStatus == "running"
	row.SourceStatus = status
	row.Revision++
	row.UpdatedAt = time.Now().UTC()
	if err = tx.Save(&row).Error; err != nil {
		return taskpanel.Panel{}, err
	}
	visible, err := taskPanelHasPresentation(tx, row)
	if err != nil {
		return taskpanel.Panel{}, err
	}
	switch {
	case visible:
		err = appendPanelChange(tx, row, taskpanel.Update{Kind: "snapshot", BaseRevision: oldRevision, State: row.State})
	case wasRunning:
		err = appendPanelChange(tx, row, taskpanel.Update{Kind: "deleted", BaseRevision: oldRevision})
	}
	if err != nil {
		return taskpanel.Panel{}, err
	}
	return panelValue(row)
}

// RecoverTaskPanelSources marks interrupted process-owned sources without
// re-executing their external work. It runs before ordinary engine recovery.
func (s *Store) RecoverTaskPanelSources(ctx context.Context) (int, error) {
	return s.recoverTaskPanelSources(ctx, "")
}

// RecoverTaskPanelSourcesForThread isolates a malformed source to its owning
// conversation, so another conversation can still resume its executable work.
func (s *Store) RecoverTaskPanelSourcesForThread(ctx context.Context, threadID string) (int, error) {
	if threadID == "" {
		return 0, taskpanel.ErrInvalid
	}
	return s.recoverTaskPanelSources(ctx, threadID)
}

func (s *Store) recoverTaskPanelSources(ctx context.Context, threadID string) (count int, err error) {
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var rows []TaskPanelRow
		query := tx.Where("source_status = ? AND hidden = ?", "running", false)
		if threadID != "" {
			query = query.Where("thread_id = ?", threadID)
		}
		if err := query.Find(&rows).Error; err != nil {
			return err
		}
		for _, row := range rows {
			var src taskpanel.Source
			if err := json.Unmarshal(row.SourceJSON, &src); err != nil {
				return fmt.Errorf("panel recovery source: %w", err)
			}
			if _, err := settleTaskPanelSource(tx, src, row.ID, "interrupted"); err != nil {
				return err
			}
			count++
		}
		return nil
	})
	if err != nil {
		count = 0
	}
	return
}
