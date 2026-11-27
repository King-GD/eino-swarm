package easm

import (
	"context"

	"github.com/LubyRuffy/eino-swarm/internal/store"
)

// UpgradeStoredPanels advances only the published EASM definition. Every
// earlier contract moves straight to the current one, so a retried operation
// still resolves to the definition it was published under. Calls occur before
// the current database or an imported database is exposed to clients.
func UpgradeStoredPanels(ctx context.Context, st *store.Store) error {
	published, err := PanelVersions()
	if err != nil {
		return err
	}
	current := published[len(published)-1]
	for _, previous := range published[:len(published)-1] {
		if err := st.UpgradeTaskPanelDefinition(ctx, previous, current); err != nil {
			return err
		}
	}
	return nil
}
