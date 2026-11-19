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

// TaskPanelRow stores the latest projection; mutation rows preserve replay input.
type TaskPanelRow struct {
	SourceStatus   string
	ID             string `gorm:"primaryKey;size:64"`
	ThreadID       string `gorm:"uniqueIndex:idx_panel_binding,priority:1;index;size:64"`
	PluginID       string `gorm:"uniqueIndex:idx_panel_binding,priority:2;size:128"`
	Revision       int64
	DefinitionJSON []byte
	State          []byte
	SourceJSON     []byte
	Hidden         bool
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// TaskPanelMutation is a durable accepted operation, scoped by runtime ownership.
type TaskPanelMutation struct {
	SourceStatus string
	ID           int64  `gorm:"primaryKey;autoIncrement"`
	PanelID      string `gorm:"uniqueIndex:idx_panel_operation,priority:1;index;size:64"`
	OperationID  string `gorm:"uniqueIndex:idx_panel_operation,priority:2;size:128"`
	ThreadID     string `gorm:"index;size:64"`
	TurnID       string `gorm:"index;size:64"`
	AnchorSeq    int64
	Revision     int64
	Digest       string
	Body         []byte
	SourceJSON   []byte
}

// TaskPanelChangeRow is a separate bounded-page SSE journal, never a chat seq.
type TaskPanelChangeRow struct {
	SourceStatus string
	Cursor       int64  `gorm:"primaryKey;autoIncrement"`
	ThreadID     string `gorm:"index:idx_panel_change_thread_cursor,priority:1;size:64"`
	PanelID      string `gorm:"index;size:64"`
	Revision     int64
	BaseRevision int64
	Kind         string
	State        []byte
	Operations   []byte
}

func validatePanelOwner(tx *gorm.DB, src taskpanel.Source) error {
	if src.ThreadID == "" || src.TurnID == "" || src.AgentID == "" || src.ToolCallID == "" || src.Generation < 0 {
		return fmt.Errorf("%w: missing runtime source", taskpanel.ErrInvalid)
	}
	var turn Turn
	err := tx.Where("id = ? AND thread_id = ? AND EXISTS (SELECT 1 FROM threads WHERE id = ?)", src.TurnID, src.ThreadID, src.ThreadID).Take(&turn).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if turn.Status == TurnCancelled {
		return fmt.Errorf("%w: cancelled turn", taskpanel.ErrConflict)
	}
	return nil
}

func panelValue(row TaskPanelRow) (taskpanel.Panel, error) {
	var d taskpanel.Definition
	var src taskpanel.Source
	if err := json.Unmarshal(row.DefinitionJSON, &d); err != nil {
		return taskpanel.Panel{}, fmt.Errorf("panel definition: %w", err)
	}
	if err := json.Unmarshal(row.SourceJSON, &src); err != nil {
		return taskpanel.Panel{}, fmt.Errorf("panel source: %w", err)
	}
	return taskpanel.Panel{SourceStatus: row.SourceStatus, ID: row.ID, ThreadID: row.ThreadID, PluginID: row.PluginID, PluginVersion: d.Version, Revision: row.Revision, State: bytes.Clone(row.State), Definition: d, Source: src, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}, nil
}

func appendPanelChange(tx *gorm.DB, row TaskPanelRow, u taskpanel.Update) error {
	return tx.Create(&TaskPanelChangeRow{SourceStatus: row.SourceStatus, ThreadID: row.ThreadID, PanelID: row.ID, Revision: row.Revision, BaseRevision: u.BaseRevision, Kind: u.Kind, State: bytes.Clone(u.State), Operations: bytes.Clone(u.Operations)}).Error
}

func panelAnchor(tx *gorm.DB, threadID string) (int64, error) {
	var anchor int64
	err := tx.Model(&Event{}).Where("thread_id = ?", threadID).Select("COALESCE(MAX(seq),0)").Scan(&anchor).Error
	return anchor, err
}

// OpenTaskPanel creates a binding once. An optional starting state seeds only a
// new binding or one reopened after rewind; a visible binding keeps its state.
func (s *Store) OpenTaskPanel(ctx context.Context, d taskpanel.Definition, src taskpanel.Source, initialState ...json.RawMessage) (out taskpanel.Panel, err error) {
	if err = taskpanel.ValidateDefinition(d); err != nil {
		return
	}
	state := d.InitialState
	if len(initialState) != 0 {
		state = initialState[0]
		if err = taskpanel.ValidateState(d, state); err != nil {
			return
		}
	}
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := validatePanelOwner(tx, src); err != nil {
			return err
		}
		var row TaskPanelRow
		found := tx.Where("thread_id = ? AND plugin_id = ?", src.ThreadID, d.ID).Take(&row).Error
		if found == nil && !row.Hidden {
			out, err = panelValue(row)
			return err
		}
		if found != nil && !errors.Is(found, gorm.ErrRecordNotFound) {
			return found
		}
		if row.ID == "" {
			row.ID = NewID("pnl_")
			row.CreatedAt = time.Now().UTC()
		}
		row.ThreadID, row.PluginID = src.ThreadID, d.ID
		row.DefinitionJSON, _ = json.Marshal(d)
		row.SourceJSON, _ = json.Marshal(src)
		row.State = bytes.Clone(state)
		if len(row.State) == 0 {
			row.State = []byte(`{}`)
		}
		row.Hidden = false
		row.SourceStatus = ""
		row.UpdatedAt = time.Now().UTC()
		row.Revision++
		if err := tx.Save(&row).Error; err != nil {
			return err
		}
		anchor, err := panelAnchor(tx, src.ThreadID)
		if err != nil {
			return err
		}
		u := taskpanel.Update{ProtocolVersion: taskpanel.ProtocolVersion, PanelID: row.ID, Kind: "snapshot", State: row.State}
		body, _ := json.Marshal(u)
		mutation := TaskPanelMutation{PanelID: row.ID, ThreadID: src.ThreadID, TurnID: src.TurnID, AnchorSeq: anchor, Revision: row.Revision, Body: body, SourceJSON: row.SourceJSON}
		if err = tx.Create(&mutation).Error; err != nil {
			return err
		}
		if err = appendPanelChange(tx, row, u); err != nil {
			return err
		}
		out, err = panelValue(row)
		return err
	})
	return
}

// TaskPanel loads only a visible instance owned by this conversation.
func (s *Store) TaskPanel(ctx context.Context, threadID, panelID string) (taskpanel.Panel, error) {
	return s.taskPanel(ctx, threadID, "id = ?", panelID)
}

// TaskPanelByPlugin reads the visible instance for one plugin in a conversation.
func (s *Store) TaskPanelByPlugin(ctx context.Context, threadID, pluginID string) (taskpanel.Panel, error) {
	return s.taskPanel(ctx, threadID, "plugin_id = ?", pluginID)
}

func (s *Store) taskPanel(ctx context.Context, threadID, match, value string) (taskpanel.Panel, error) {
	var row TaskPanelRow
	err := s.db.WithContext(ctx).Where("thread_id = ? AND hidden = ?", threadID, false).Where(match, value).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return taskpanel.Panel{}, ErrNotFound
	}
	if err != nil {
		return taskpanel.Panel{}, err
	}
	return panelValue(row)
}

// TaskPanels returns this conversation's visible instances in creation order.
func (s *Store) TaskPanels(ctx context.Context, threadID string) ([]taskpanel.Panel, error) {
	panels, _, err := s.SnapshotTaskPanels(ctx, threadID)
	return panels, err
}

// SnapshotTaskPanels atomically reads instances and their independent stream cursor.
func (s *Store) SnapshotTaskPanels(ctx context.Context, threadID string) (panels []taskpanel.Panel, cursor int64, err error) {
	panels = []taskpanel.Panel{}
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var n int64
		if err := tx.Model(&Thread{}).Where("id = ?", threadID).Count(&n).Error; err != nil {
			return err
		}
		if n == 0 {
			return ErrNotFound
		}
		var rows []TaskPanelRow
		if err := tx.Where("thread_id = ? AND hidden = ?", threadID, false).Order("created_at, id").Find(&rows).Error; err != nil {
			return err
		}
		for _, row := range rows {
			panel, e := panelValue(row)
			if e != nil {
				return e
			}
			panels = append(panels, panel)
		}
		return tx.Model(&TaskPanelChangeRow{}).Where("thread_id = ?", threadID).Select("COALESCE(MAX(cursor),0)").Scan(&cursor).Error
	})
	return
}

// TaskPanelCursor returns the latest committed delivery cursor for a conversation.
func (s *Store) TaskPanelCursor(ctx context.Context, threadID string) (int64, error) {
	var cursor int64
	err := s.db.WithContext(ctx).Model(&TaskPanelChangeRow{}).Where("thread_id = ?", threadID).Select("COALESCE(MAX(cursor),0)").Scan(&cursor).Error
	return cursor, err
}

// TaskPanelChanges pages already committed changes. A reconnect may use a snapshot.
func (s *Store) TaskPanelChanges(ctx context.Context, threadID string, afterID int64) ([]taskpanel.Change, error) {
	var rows []TaskPanelChangeRow
	if err := s.db.WithContext(ctx).Where("thread_id = ? AND cursor > ?", threadID, afterID).Order("cursor").Limit(256).Find(&rows).Error; err != nil {
		return nil, err
	}
	changes := make([]taskpanel.Change, 0, len(rows))
	for _, r := range rows {
		changes = append(changes, taskpanel.Change{SourceStatus: r.SourceStatus, Cursor: r.Cursor, ThreadID: r.ThreadID, PanelID: r.PanelID, Revision: r.Revision, BaseRevision: r.BaseRevision, Kind: r.Kind, State: bytes.Clone(r.State), Operations: bytes.Clone(r.Operations)})
	}
	return changes, nil
}
