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
	if p.EASM.Tools["heat"] != "" || p.EASM.Tools["stats"] != "custom_stats" || p.EASM.Tools["product_rules"] != EASMToolRoles["product_rules"] || p.EASM.MaxDomains != 20 {
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

// A configuration saved while the producer still looked companies up itself
// names roles it no longer has. Loading must drop them, or an upgrade would
// refuse to start over a lookup that no longer exists.
func TestRetiredEASMRolesAreDroppedOnLoad(t *testing.T) {
	tools := make(map[string]string, len(EASMToolRoles)+len(retiredEASMToolRoles))
	for role, name := range EASMToolRoles {
		tools[role] = name
	}
	for role := range retiredEASMToolRoles {
		tools[role] = "old_tool"
	}
	settings := (TaskPanelConfig{EASM: EASMPanelConfig{Tools: tools}}).WithDefaults()
	if err := settings.Validate(); err != nil {
		t.Fatal(err)
	}
	for role := range retiredEASMToolRoles {
		if _, kept := settings.EASM.Tools[role]; kept {
			t.Fatalf("retired role %q survived the load", role)
		}
	}
	for role := range EASMToolRoles {
		if _, kept := settings.EASM.Tools[role]; !kept {
			t.Fatalf("live role %q was dropped", role)
		}
	}
	// A settings client that still sends a retired role is not locked out
	// either, while a role nobody ever defined stays an error.
	direct := EASMPanelConfig{Tools: map[string]string{"organizations": "x", "stats": "s"}, MaxDomains: 20, MaxComponents: 20, MaxClouds: 5, MaxServices: 3}
	if err := direct.validate(); err != nil {
		t.Fatalf("a retired role was refused: %v", err)
	}
	direct.Tools["unknown"] = "x"
	if direct.validate() == nil {
		t.Fatal("an unknown role was accepted")
	}
}
