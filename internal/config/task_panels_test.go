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

// A user can retarget or switch off one portrait lookup without losing the
// defaults of the others, and a snapshot never shares the live tool map.
func TestEASMToolDefaultsKeepUserOverrides(t *testing.T) {
	p := (TaskPanelConfig{EASM: EASMPanelConfig{Tools: map[string]string{"heat": "", "stats": "custom_stats"}}}).WithDefaults()
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	if p.EASM.Tools["heat"] != "" || p.EASM.Tools["stats"] != "custom_stats" || p.EASM.Tools["organizations"] != EASMToolRoles["organizations"] || p.EASM.MaxDomains != 20 {
		t.Fatalf("tools=%v max=%d", p.EASM.Tools, p.EASM.MaxDomains)
	}
	copy := p.WithDefaults()
	copy.EASM.Tools["stats"] = "changed"
	if p.EASM.Tools["stats"] != "custom_stats" {
		t.Fatal("defaults share the tool map")
	}
	for _, edit := range []func(*EASMPanelConfig){
		func(c *EASMPanelConfig) { c.MaxDomains = 201 },
		func(c *EASMPanelConfig) { c.MaxComponents = 4 },
		func(c *EASMPanelConfig) { c.Tools = map[string]string{"unknown": "x"} },
		func(c *EASMPanelConfig) { c.Tools = map[string]string{"stats": " padded"} },
		func(c *EASMPanelConfig) { c.ServerID = "id " },
	} {
		bad := p
		bad.EASM = p.EASM.withDefaults()
		edit(&bad.EASM)
		if bad.Validate() == nil {
			t.Fatalf("accepted %+v", bad.EASM)
		}
	}
}
