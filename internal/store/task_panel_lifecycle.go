package store

import (
	"bytes"
	"context"
	"errors"
	"time"

	"github.com/LubyRuffy/eino-swarm/internal/taskpanel"
	"gorm.io/gorm"
)

// Use the retained observation index so projection reads and chunk continuation
// checks never have to load old publication bodies. Rewind removes the receipts.
const taskPanelVisibleSQL = `hidden = false AND closed = false AND (source_status = 'running' OR EXISTS (
	SELECT 1 FROM task_panel_mutations AS m
	WHERE m.observation = true AND m.panel_id = task_panel_rows.id
))`

// The opening mutation has no operation ID. Empty patches include historical
// source claims. Classify legacy rows once before serving their projections.
func backfillTaskPanelObservations(db *gorm.DB) error {
	return db.Exec(`UPDATE task_panel_mutations
		SET observation = CASE WHEN operation_id <> '' AND json_valid(body)
			THEN COALESCE(json_extract(body, '$.kind') = 'snapshot', false)
				OR COALESCE(json_array_length(body, '$.operations'), 0) > 0
			ELSE false END
		WHERE observation IS NULL`).Error
}

func panelUpdateHasObservation(update taskpanel.Update) bool {
	if update.Kind == "snapshot" {
		return true
	}
	// Apply has already validated that operations is a JSON array.
	ops := bytes.TrimSpace(update.Operations)
	return len(bytes.TrimSpace(ops[1:len(ops)-1])) != 0
}

func taskPanelHasPresentation(tx *gorm.DB, row TaskPanelRow) (bool, error) {
	var count int64
	err := tx.Model(&TaskPanelRow{}).Where("id = ?", row.ID).Where(taskPanelVisibleSQL).Count(&count).Error
	return count != 0, err
}

// CloseTaskPanel retains the instance identity and history, and atomically
// removes its presentation and settles any running source. A retry is safe.
func (s *Store) CloseTaskPanel(ctx context.Context, threadID, panelID string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row TaskPanelRow
		err := tx.Where("id = ? AND thread_id = ? AND EXISTS (SELECT 1 FROM threads WHERE id = ?)", panelID, threadID, threadID).Take(&row).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrNotFound
		}
		if err != nil || row.Closed {
			return err
		}
		row.Closed = true
		if row.SourceStatus == "running" {
			row.SourceStatus = "interrupted"
		}
		row.Revision++
		row.UpdatedAt = time.Now().UTC()
		if err := tx.Save(&row).Error; err != nil {
			return err
		}
		return appendPanelChange(tx, row, taskpanel.Update{Kind: "deleted", BaseRevision: row.Revision - 1})
	})
}
