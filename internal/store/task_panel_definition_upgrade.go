package store

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/LubyRuffy/eino-swarm/internal/taskpanel"
	"gorm.io/gorm"
)

// UpgradeTaskPanelDefinition swaps a known pinned definition for a compatible
// successor before the database becomes visible to readers or producers.
func (s *Store) UpgradeTaskPanelDefinition(ctx context.Context, previous, current taskpanel.Definition) error {
	if previous.ID != current.ID || previous.Version == current.Version {
		return fmt.Errorf("%w: task panel definition upgrade identity", taskpanel.ErrInvalid)
	}
	if err := taskpanel.ValidateDefinition(current); err != nil {
		return err
	}
	definitionJSON, err := json.Marshal(current)
	if err != nil {
		return err
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var rows []TaskPanelRow
		return tx.Select("id", "revision", "definition_json").
			Where("plugin_id = ? AND CASE WHEN json_valid(definition_json) THEN json_extract(definition_json, '$.version') END = ?", previous.ID, previous.Version).
			FindInBatches(&rows, 128, func(_ *gorm.DB, _ int) error {
				for _, row := range rows {
					var pinned taskpanel.Definition
					decoder := json.NewDecoder(bytes.NewReader(row.DefinitionJSON))
					decoder.DisallowUnknownFields()
					if err := decoder.Decode(&pinned); err != nil || !taskpanel.DefinitionsMatch(pinned, previous) {
						continue
					}
					var projection TaskPanelRow
					if err := tx.Select("state").Take(&projection, "id = ?", row.ID).Error; err != nil {
						return err
					}
					if err := taskpanel.ValidateState(current, projection.State); err != nil {
						continue
					}
					// A new revision forces a full pull without writing a producer event.
					revision := row.Revision + 1
					if err := tx.Model(&TaskPanelRow{}).Where("id = ?", row.ID).UpdateColumns(map[string]any{
						"definition_json":          definitionJSON,
						"previous_definition_json": row.DefinitionJSON,
						"definition_revision":      revision,
						"revision":                 revision,
					}).Error; err != nil {
						return err
					}
				}
				return nil
			}).Error
	})
}
