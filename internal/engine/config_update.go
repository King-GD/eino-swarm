package engine

import (
	"reflect"

	"github.com/LubyRuffy/eino-swarm/internal/config"
)

// UpdateConfig orders desktop and phone patches through persistence and live
// application. No pool/runtime lock is taken while Config holds its write lock.
func (e *Engine) UpdateConfig(edit func(*config.Config) error) error {
	return e.UpdateConfigForMode(nil, edit)
}

// UpdateConfigForMode persists a debug source switch while turns and workers
// keep the model they already opened. Chat admission is exclusive with the
// save, while bound chat requests can finish on their original source.
// Backup, restore and restart still refuse the switch.
func (e *Engine) UpdateConfigForMode(mode *config.ProductConfig, edit func(*config.Config) error) error {
	e.configUpdateMu.Lock()
	defer e.configUpdateMu.Unlock()
	if mode != nil && mode.Resolved() != e.cfg.Snapshot().Product.Resolved() {
		release, err := e.holdSourceSwitch()
		if err != nil {
			return err
		}
		defer release()
		unfreeze, ok := e.pool.TryHoldModelPolicy()
		if !ok {
			return ErrBackupBusy
		}
		defer unfreeze()
	}
	previousMCP := e.cfg.Snapshot().MCP
	if err := e.cfg.Update(edit); err != nil {
		return err
	}
	currentMCP := e.cfg.Snapshot().MCP
	if e.mcp != nil && !reflect.DeepEqual(previousMCP, currentMCP) {
		e.mcp.Reload(currentMCP.Servers)
	}
	e.pool.Invalidate()
	e.ApplyLiveSwarmLimits()
	return nil
}
