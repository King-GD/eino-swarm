package easm

import (
	"context"

	"github.com/LubyRuffy/eino-swarm/internal/store"
)

// UpgradeStoredPanels advances only the published EASM definition. Calls occur
// before the current database or an imported database is exposed to clients.
func UpgradeStoredPanels(ctx context.Context, st *store.Store) error {
	legacy, current, err := PanelDefinitions()
	if err != nil {
		return err
	}
	return st.UpgradeTaskPanelDefinition(ctx, legacy, current)
}
