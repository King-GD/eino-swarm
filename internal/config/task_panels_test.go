package config

import (
	"testing"
)

func TestTaskPanelDefaultsAreDetachedAndLimitsAreValidated(t *testing.T) {
	p := (TaskPanelConfig{}).WithDefaults()
	if err := p.Validate(); err != nil || !*p.Enabled || p.FOFAClient != "fofa" || p.Concurrency != 16 {
		t.Fatalf("defaults=%+v err=%v", p, err)
	}
	c := &Config{TaskPanels: p}
	snapshot := c.Snapshot()
	*snapshot.TaskPanels.Enabled = false
	if !*c.TaskPanels.Enabled {
		t.Fatal("snapshot changed live settings")
	}
	for _, edit := range []func(*TaskPanelConfig){
		func(p *TaskPanelConfig) { p.FOFAClient = "" }, func(p *TaskPanelConfig) { p.MaxAssets = -1 },
		func(p *TaskPanelConfig) { p.BatchSize = p.MaxAssets + 1 }, func(p *TaskPanelConfig) { p.Concurrency = 257 },
		func(p *TaskPanelConfig) { p.TimeoutSeconds = 301 }, func(p *TaskPanelConfig) { p.FlushIntervalMS = 1 },
		func(p *TaskPanelConfig) { p.MaxUpdateBytes = 1 }, func(p *TaskPanelConfig) { p.MaxStateBytes = 100 },
	} {
		bad := p
		edit(&bad)
		if bad.Validate() == nil {
			t.Fatalf("accepted %+v", bad)
		}
	}
	clone := p.WithDefaults()
	*clone.Enabled = false
	if !*p.Enabled {
		t.Fatal("defaults shares switch")
	}
}
