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
	if err := ctx.Err(); err != nil {
		return err
	}
	a.runMu.Lock()
	done := a.done
	if done == nil {
		a.runMu.Unlock()
		return errors.New("remote: account discovery is stopped")
	}
	if a.waiting == nil {
		a.waiting = make(chan struct{})
		a.request <- struct{}{}
	}
	reply := a.waiting
	a.runMu.Unlock()
	select {
	case <-reply:
		return ctx.Err()
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
		return errors.New("remote: account discovery is stopped")
	}
}
