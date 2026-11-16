package engine

import (
	"errors"
	"testing"

	"github.com/LubyRuffy/eino-swarm/internal/config"
	"github.com/LubyRuffy/eino-swarm/internal/provider"
)

func TestRunningTurnDoesNotBlockDebugSourceSwitch(t *testing.T) {
	e := newTestEngine(t)
	if err := e.UpdateConfig(func(c *config.Config) error {
		c.Product.Mode = config.ProductModeLocal
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	rt := e.runtimeFor("th_running")
	rt.mu.Lock()
	rt.running = true
	rt.turnID = "tn_running"
	rt.mu.Unlock()

	mode := config.ProductConfig{}
	if err := e.UpdateConfigForMode(&mode, func(c *config.Config) error {
		c.Product = mode
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if e.Config().Snapshot().Product.Resolved() != config.ProductModeFOFA {
		t.Fatal("running turn blocked the source switch")
	}
	if !e.Status("th_running").Running {
		t.Fatal("source switch stopped the turn")
	}
}

func TestDebugSourceChangeIsExclusiveAndFailedSaveReleasesAdmission(t *testing.T) {
	e := newTestEngine(t)
	if err := e.UpdateConfig(func(c *config.Config) error { c.Product.Mode = config.ProductModeLocal; return nil }); err != nil {
		t.Fatal(err)
	}
	mode := config.ProductConfig{}
	release, err := e.pool.BeginModelUse()
	if err != nil {
		t.Fatal(err)
	}
	called := false
	edit := func(c *config.Config) error { called = true; c.Product = mode; return nil }
	if err = e.UpdateConfigForMode(&mode, edit); !errors.Is(err, ErrBackupBusy) || called {
		t.Fatalf("active model changed source: %v", err)
	}
	release()
	refused := errors.New("save refused")
	if err = e.UpdateConfigForMode(&mode, func(*config.Config) error { return refused }); !errors.Is(err, refused) {
		t.Fatal(err)
	}
	if e.Config().Snapshot().Product.Resolved() != config.ProductModeLocal {
		t.Fatal("failed save changed source")
	}
	if err = e.UpdateConfigForMode(&mode, edit); err != nil {
		t.Fatal(err)
	}
	if e.Config().Snapshot().Product.Resolved() != config.ProductModeFOFA || !called {
		t.Fatal("idle switch did not persist")
	}
	if err = e.UpdateConfig(func(c *config.Config) error { c.UI.Locale = "en"; return nil }); err != nil {
		t.Fatal(err)
	}
	if release, ok := e.pool.TryHoldModelPolicy(); !ok {
		t.Fatal("save leaked model hold")
	} else {
		release()
	}
}

func TestManagedNuwaUsesPresetWithoutReplacingSavedPersonalPair(t *testing.T) {
	e := newTestEngine(t)
	if err := e.UpdateConfig(func(c *config.Config) error { c.Product.Mode = ""; return nil }); err != nil {
		t.Fatal(err)
	}
	src, err := provider.NewAccountProfile("gateway:0123456789abcdef0123456789abcdef", "https://gateway.invalid/v1", "private", provider.AccountProfile{Label: "Managed"})
	if err != nil {
		t.Fatal(err)
	}
	// The mock pool intentionally ignores account sources; exercise policy on a
	// real pool without sending a model request.
	e.pool = provider.New(e.Config())
	e.pool.SetAccountSource(src)
	if pair, err := e.ResolveNuwaModelPair(true, nil, nil); err != nil || pair != nil {
		t.Fatalf("implicit preset was stored as a personal pair: %v %v", pair, err)
	}
	if _, err := e.ResolveNuwaModelPair(true, nil, &NuwaModelPair{}); !errors.Is(err, provider.ErrModelPolicy) {
		t.Fatalf("explicit pair bypassed preset: %v", err)
	}
	if _, err := e.SaveNuwaModelPair("unused", NuwaModelPair{}); !errors.Is(err, provider.ErrModelPolicy) {
		t.Fatalf("saved pair bypassed preset: %v", err)
	}
}
