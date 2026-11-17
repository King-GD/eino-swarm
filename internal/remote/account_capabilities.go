package remote

import (
	"context"
	"errors"

	"github.com/LubyRuffy/eino-swarm/internal/provider"
)

func (b accountBootstrap) sourceCapabilities(kind string) *provider.SourceCapabilities {
	if b.capabilities == nil {
		return nil
	}
	return b.capabilities.Sources[kind]
}

// Discovery is owned by Start/Stop; HTTP and phone requests never create an
// independent refresher that could publish after host shutdown.
func (a *AccountModels) refreshOwned(ctx context.Context) error {
	a.runMu.Lock()
	done := a.done
	a.runMu.Unlock()
	if done == nil {
		return errors.New("remote: account discovery is stopped")
	}
	reply := make(chan struct{})
	select {
	case a.requests <- reply:
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
		return errors.New("remote: account discovery is stopped")
	}
	select {
	case <-reply:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
		return errors.New("remote: account discovery is stopped")
	}
}
