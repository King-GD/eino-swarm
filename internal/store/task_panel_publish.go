package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/LubyRuffy/eino-swarm/internal/taskpanel"
	"gorm.io/gorm"
)

// PublishTaskPanel commits state, operation identity and delivery together.
func (s *Store) PublishTaskPanel(ctx context.Context, src taskpanel.Source, u taskpanel.Update, limits ...taskpanel.Limits) (out taskpanel.Panel, err error) {
	digest, err := taskpanel.Digest(u)
	if err != nil {
		return out, fmt.Errorf("%w: update JSON", taskpanel.ErrInvalid)
	}
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := validatePanelOwner(tx, src); err != nil {
			return err
		}
		var row TaskPanelRow
		if err := tx.Where("id = ? AND thread_id = ? AND hidden = ?", u.PanelID, src.ThreadID, false).Take(&row).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrNotFound
			}
			return err
		}
		var prior TaskPanelMutation
		err := tx.Where("panel_id = ? AND operation_id = ?", row.ID, u.OperationID).Take(&prior).Error
		if err == nil {
			if prior.Digest != digest {
				return taskpanel.ErrConflict
			}
			out, err = replayTaskPanel(tx, row, prior.Revision)
			return err
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if row.Closed {
			return ErrNotFound
		}
		if len(limits) > 1 {
			return taskpanel.ErrInvalid
		}
		var budget taskpanel.Limits
		if len(limits) == 1 {
			budget = limits[0]
		}
		body, _ := json.Marshal(u)
		if budget.MaxUpdateBytes > 0 && len(body) > budget.MaxUpdateBytes {
			return fmt.Errorf("%w: update exceeds configured byte budget", taskpanel.ErrInvalid)
		}
		if row.Revision != u.BaseRevision {
			return taskpanel.ErrConflict
		}
		panel, err := panelValue(row)
		if err != nil {
			return err
		}
		// A live source owns this panel's writes until its terminal receipt.
		// Otherwise a different data author can erase its durable running lease.
		if row.SourceStatus == "running" && panel.Source != src {
			return taskpanel.ErrConflict
		}
		state, err := taskpanel.Apply(panel.Definition, row.State, u)
		if err != nil {
			return err
		}
		if budget.MaxStateBytes > 0 && len(state) > budget.MaxStateBytes {
			return fmt.Errorf("%w: state exceeds configured byte budget", taskpanel.ErrInvalid)
		}
		if budget.ValidateStateChange != nil {
			if err := budget.ValidateStateChange(panel.Definition, row.State, state); err != nil {
				return fmt.Errorf("%w: %v", taskpanel.ErrInvalid, err)
			}
		}
		if panel.Source != src || row.SourceStatus != "running" {
			row.SourceStatus = ""
		}
		row.State = state
		row.Revision++
		row.UpdatedAt = time.Now().UTC()
		row.SourceJSON, _ = json.Marshal(src)
		if err = tx.Save(&row).Error; err != nil {
			return err
		}
		anchor, err := panelAnchor(tx, src.ThreadID)
		if err != nil {
			return err
		}
		mutation := TaskPanelMutation{Observation: panelUpdateHasObservation(u), SourceStatus: row.SourceStatus, PanelID: row.ID, OperationID: u.OperationID, ThreadID: src.ThreadID, TurnID: src.TurnID, AnchorSeq: anchor, Revision: row.Revision, Digest: digest, Body: body, SourceJSON: row.SourceJSON}
		if err = tx.Create(&mutation).Error; err != nil {
			return err
		}
		visible, err := taskPanelHasPresentation(tx, row)
		if err != nil {
			return err
		}
		if visible {
			if err = appendPanelChange(tx, row, u); err != nil {
				return err
			}
		}
		out, err = panelValue(row)
		return err
	})
	return
}

func replayTaskPanel(tx *gorm.DB, row TaskPanelRow, through int64) (taskpanel.Panel, error) {
	var mutations []TaskPanelMutation
	if err := tx.Where("panel_id = ? AND revision <= ?", row.ID, through).Order("revision").Find(&mutations).Error; err != nil {
		return taskpanel.Panel{}, err
	}
	// A retry of a pre-upgrade operation returns the original pinned contract.
	if through < row.DefinitionRevision {
		row.DefinitionJSON = row.PreviousDefinitionJSON
	}
	panel, err := panelValue(row)
	if err != nil {
		return panel, err
	}
	state := []byte(`{}`)
	for _, m := range mutations {
		var u taskpanel.Update
		if err = json.Unmarshal(m.Body, &u); err != nil {
			return panel, err
		}
		if m.OperationID == "" {
			state = bytes.Clone(u.State)
		} else {
			state, err = taskpanel.Apply(panel.Definition, state, u)
			if err != nil {
				return panel, err
			}
		}
		if err = json.Unmarshal(m.SourceJSON, &panel.Source); err != nil {
			return panel, err
		}
		panel.Revision = m.Revision
		panel.SourceStatus = m.SourceStatus
	}
	panel.State = state
	return panel, nil
}

func deleteTaskPanels(tx *gorm.DB, threadID string) error {
	for _, m := range []any{&TaskPanelMutation{}, &TaskPanelChangeRow{}, &TaskPanelRow{}} {
		if err := tx.Where("thread_id = ?", threadID).Delete(m).Error; err != nil {
			return err
		}
	}
	return nil
}

func rewindTaskPanels(tx *gorm.DB, threadID string, fromSeq int64, turnIDs []string) error {
	var mutations []TaskPanelMutation
	if err := tx.Where("thread_id = ? AND (anchor_seq >= ? OR turn_id IN ?)", threadID, fromSeq, turnIDs).Find(&mutations).Error; err != nil {
		return err
	}
	affected := map[string]bool{}
	for _, m := range mutations {
		affected[m.PanelID] = true
	}
	if err := tx.Where("thread_id = ? AND (anchor_seq >= ? OR turn_id IN ?)", threadID, fromSeq, turnIDs).Delete(&TaskPanelMutation{}).Error; err != nil {
		return err
	}
	for id := range affected {
		var row TaskPanelRow
		if err := tx.First(&row, "id = ?", id).Error; err != nil {
			return err
		}
		var count int64
		if err := tx.Model(&TaskPanelMutation{}).Where("panel_id = ?", id).Count(&count).Error; err != nil {
			return err
		}
		nextRevision := row.Revision + 1
		kind := "snapshot"
		if count == 0 {
			row.Hidden = true
			row.State = []byte(`{}`)
			kind = "deleted"
		} else {
			replay, err := replayTaskPanel(tx, row, row.Revision)
			if err != nil {
				return err
			}
			row.State = replay.State
			row.SourceJSON, _ = json.Marshal(replay.Source)
		}
		row.SourceStatus = ""
		row.Revision = nextRevision
		row.UpdatedAt = time.Now().UTC()
		if err := tx.Save(&row).Error; err != nil {
			return err
		}
		// Truncated content must disappear from both state and the SSE journal.
		if err := tx.Where("panel_id = ?", id).Delete(&TaskPanelChangeRow{}).Error; err != nil {
			return err
		}
		visible, err := taskPanelHasPresentation(tx, row)
		if err != nil {
			return err
		}
		if !visible {
			kind = "deleted"
		}
		change := taskpanel.Update{BaseRevision: nextRevision - 1, Kind: kind}
		if visible {
			change.State = row.State
		}
		if err := appendPanelChange(tx, row, change); err != nil {
			return err
		}
	}
	return nil
}
