package store

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/LubyRuffy/eino-swarm/internal/taskpanel"
	"gorm.io/gorm"
)

// TaskPanelManifest is the small, read-only identity of a visible instance.
// It deliberately contains neither state nor plugin code/schema.
type TaskPanelManifest struct {
	ID            string `json:"id"`
	ThreadID      string `json:"thread_id"`
	PluginID      string `json:"plugin_id"`
	PluginVersion string `json:"plugin_version"`
	Revision      int64  `json:"revision"`
	SourceStatus  string `json:"source_status,omitempty"`
}

// TaskPanelManifests reads only metadata. More means the caller must refuse an
// oversized atomic manifest, not present a truncated list as complete.
func (s *Store) TaskPanelManifests(ctx context.Context, threadID string, limit int) (out []TaskPanelManifest, more bool, err error) {
	out = []TaskPanelManifest{}
	if limit < 1 {
		return nil, false, taskpanel.ErrInvalid
	}
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var thread Thread
		if e := tx.Select("id").Take(&thread, "id = ?", threadID).Error; e != nil {
			if errors.Is(e, gorm.ErrRecordNotFound) {
				return ErrNotFound
			}
			return e
		}
		if e := tx.Model(&TaskPanelRow{}).Select("id, thread_id, plugin_id, revision, source_status, json_extract(definition_json, '$.version') AS plugin_version").Where("thread_id = ?", threadID).Where(taskPanelVisibleSQL).Order("created_at, id").Limit(limit + 1).Scan(&out).Error; e != nil {
			return e
		}
		more = len(out) > limit
		return nil
	})
	return
}

// TaskPanelReadForTransfer freezes one complete state or an exact latest patch
// from one SQLite snapshot. A base from an earlier incarnation cannot use a patch.
func (s *Store) TaskPanelReadForTransfer(ctx context.Context, threadID, panelID string, base *int64) (panel taskpanel.Panel, change *taskpanel.Change, err error) {
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row TaskPanelRow
		if e := tx.Where("id = ? AND thread_id = ? AND EXISTS (SELECT 1 FROM threads WHERE id = ?)", panelID, threadID, threadID).Where(taskPanelVisibleSQL).Take(&row).Error; e != nil {
			if errors.Is(e, gorm.ErrRecordNotFound) {
				return ErrNotFound
			}
			return e
		}
		var e error
		panel, e = panelValue(row)
		if e != nil {
			return e
		}
		if base == nil || *base < 0 {
			return nil
		}
		var latest TaskPanelChangeRow
		e = tx.Where("panel_id = ? AND thread_id = ? AND revision = ?", panelID, threadID, row.Revision).Order("cursor desc").Take(&latest).Error
		if errors.Is(e, gorm.ErrRecordNotFound) {
			return nil
		}
		if e != nil {
			return e
		}
		if latest.Kind != "patch" || latest.BaseRevision != *base {
			return nil
		}
		var opening TaskPanelMutation
		e = tx.Select("revision").Where("panel_id = ? AND operation_id = ?", panelID, "").Take(&opening).Error
		if errors.Is(e, gorm.ErrRecordNotFound) {
			return nil
		}
		if e != nil {
			return e
		}
		if opening.Revision > *base {
			return nil
		}
		change = &taskpanel.Change{Cursor: latest.Cursor, ThreadID: latest.ThreadID, PanelID: latest.PanelID, Revision: latest.Revision, BaseRevision: latest.BaseRevision, Kind: latest.Kind, Operations: json.RawMessage(append([]byte(nil), latest.Operations...)), SourceStatus: latest.SourceStatus}
		return nil
	})
	return
}

// TaskPanelVisible checks lifetime without loading an instance's bulk state.
func (s *Store) TaskPanelVisible(ctx context.Context, threadID, panelID string) (bool, error) {
	var count int64
	err := s.db.WithContext(ctx).Model(&TaskPanelRow{}).Where("id = ? AND thread_id = ? AND EXISTS (SELECT 1 FROM threads WHERE id = ?)", panelID, threadID, threadID).Where(taskPanelVisibleSQL).Count(&count).Error
	return count > 0, err
}
