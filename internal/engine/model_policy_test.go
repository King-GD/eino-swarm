package engine

import (
	"context"
	"errors"
	"testing"

	"github.com/LubyRuffy/eino-swarm/internal/backup"
	"github.com/LubyRuffy/eino-swarm/internal/config"
	"github.com/LubyRuffy/eino-swarm/internal/provider"
)

// A running turn already resolved its model. The debug source switch changes
// the next lookup and must leave that turn running.
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
		t.Fatalf("running turn blocked a source switch: %v", err)
	}
	if e.Config().Snapshot().Product.Resolved() != config.ProductModeFOFA {
		t.Fatal("source switch did not persist")
	}
	if !e.Status("th_running").Running {
		t.Fatal("source switch stopped the turn")
	}
	rt.mu.Lock()
	rt.running = false
	rt.turnID = ""
	rt.mu.Unlock()
	if err := e.UpdateConfigForMode(&mode, func(c *config.Config) error { c.Product = mode; return nil }); err != nil {
		t.Fatalf("idle source switch failed: %v", err)
	}
	if e.Config().Snapshot().Product.Resolved() != config.ProductModeFOFA {
		t.Fatal("idle source switch did not persist")
	}
}

func TestDebugSourceSwitchExcludesBackupWithoutStoppingTheTurn(t *testing.T) {
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
	e.StartScheduler()
	t.Cleanup(e.StopScheduler)

	entered := make(chan struct{})
	releaseEdit := make(chan struct{})
	done := make(chan error, 1)
	mode := config.ProductConfig{}
	go func() {
		done <- e.UpdateConfigForMode(&mode, func(c *config.Config) error {
			close(entered)
			<-releaseEdit
			c.Product = mode
			return nil
		})
	}()
	<-entered
	e.schedMu.Lock()
	ticking := e.schedStop != nil
	e.schedMu.Unlock()
	if !ticking {
		t.Fatal("source switch paused the scheduler")
	}
	if _, err := e.BackupData(context.Background(), backup.Options{Kind: backup.KindFull, Output: t.TempDir() + "/during-switch.zwai-backup"}); !errors.Is(err, ErrBackupBusy) {
		t.Fatalf("backup entered during the source switch: %v", err)
	}
	close(releaseEdit)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !e.Status("th_running").Running {
		t.Fatal("source switch stopped the turn")
	}
	if e.Config().Snapshot().Product.Resolved() != config.ProductModeFOFA {
		t.Fatal("source switch did not persist")
	}
}

func TestDebugSourceSwitchRefusesWhileDataIsBeingReplaced(t *testing.T) {
	e := newTestEngine(t)
	if err := e.UpdateConfig(func(c *config.Config) error {
		c.Product.Mode = config.ProductModeLocal
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	mode := config.ProductConfig{}
	edit := func(c *config.Config) error { c.Product = mode; return nil }
	e.mu.Lock()
	e.backupRunning = true
	e.mu.Unlock()
	if err := e.UpdateConfigForMode(&mode, edit); !errors.Is(err, ErrBackupBusy) {
		t.Fatalf("backup allowed the source switch: %v", err)
	}
	e.mu.Lock()
	e.backupRunning = false
	e.startupRestoring = true
	e.mu.Unlock()
	if err := e.UpdateConfigForMode(&mode, edit); !errors.Is(err, ErrRestoring) {
		t.Fatalf("startup recovery allowed the source switch: %v", err)
	}
	e.mu.Lock()
	e.startupRestoring = false
	e.restart.status.Stage = "preparing"
	e.mu.Unlock()
	if err := e.UpdateConfigForMode(&mode, edit); !errors.Is(err, ErrBackupBusy) {
		t.Fatalf("restart allowed the source switch: %v", err)
	}
	if e.Config().Snapshot().Product.Resolved() != config.ProductModeLocal {
		t.Fatal("refused switch changed the source")
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
