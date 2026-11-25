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
	Closed         bool `gorm:"not null;default:false"`
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// TaskPanelMutation is a durable accepted operation, scoped by runtime ownership.
type TaskPanelMutation struct {
	SourceStatus string
	ID           int64  `gorm:"primaryKey;autoIncrement"`
	PanelID      string `gorm:"uniqueIndex:idx_panel_operation,priority:1;index;index:idx_panel_observation,priority:2;size:64"`
	Observation  bool   `gorm:"index:idx_panel_observation,priority:1"`
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

// OpenTaskPanel creates a binding once; reopening returns its pinned definition.
func (s *Store) OpenTaskPanel(ctx context.Context, d taskpanel.Definition, src taskpanel.Source) (out taskpanel.Panel, err error) {
	if err = taskpanel.ValidateDefinition(d); err != nil {
		return
	}
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := validatePanelOwner(tx, src); err != nil {
			return err
		}
		var row TaskPanelRow
		found := tx.Where("thread_id = ? AND plugin_id = ?", src.ThreadID, d.ID).Take(&row).Error
		if found == nil && row.Closed {
			return fmt.Errorf("%w: panel instance was closed by the user", taskpanel.ErrConflict)
		}
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
		row.State = bytes.Clone(d.InitialState)
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
		out, err = panelValue(row)
		return err
	})
	return
}

// TaskPanel loads an instance for runtime tools, including its initial state
// before anything has been published to the presentation clients.
func (s *Store) TaskPanel(ctx context.Context, threadID, panelID string) (taskpanel.Panel, error) {
	return readTaskPanelRow(s.db.WithContext(ctx).Where("hidden = ? AND closed = ?", false, false), threadID, panelID)
}

// VisibleTaskPanel loads one instance using the same admission as the manifest.
func (s *Store) VisibleTaskPanel(ctx context.Context, threadID, panelID string) (taskpanel.Panel, error) {
	return readTaskPanelRow(s.db.WithContext(ctx).Where(taskPanelVisibleSQL), threadID, panelID)
}

func readTaskPanelRow(query *gorm.DB, threadID, panelID string) (taskpanel.Panel, error) {
	var row TaskPanelRow
	err := query.Where("id = ? AND thread_id = ?", panelID, threadID).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return taskpanel.Panel{}, ErrNotFound
	}
	if err != nil {
		return taskpanel.Panel{}, err
	}
	return panelValue(row)
}

// TaskPanels lists runtime bindings, including an opened instance awaiting
// its first publication. Human-closed and rewound bindings are excluded.
func (s *Store) TaskPanels(ctx context.Context, threadID string) ([]taskpanel.Panel, error) {
	panels, _, err := s.snapshotTaskPanels(ctx, threadID, "hidden = false AND closed = false")
	return panels, err
}

// SnapshotTaskPanels atomically reads visible instances and their stream cursor.
func (s *Store) SnapshotTaskPanels(ctx context.Context, threadID string) ([]taskpanel.Panel, int64, error) {
	return s.snapshotTaskPanels(ctx, threadID, taskPanelVisibleSQL)
}

func (s *Store) snapshotTaskPanels(ctx context.Context, threadID, scope string) (panels []taskpanel.Panel, cursor int64, err error) {
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
		if err := tx.Where("thread_id = ?", threadID).Where(scope).Order("created_at, id").Find(&rows).Error; err != nil {
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
