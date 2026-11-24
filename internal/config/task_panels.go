package config

import (
	"fmt"
	"strings"
)

// TaskPanelConfig contains local panel limits and the example producer settings.
// Credentials remain with the external client's existing configuration.
type TaskPanelConfig struct {
	Enabled         *bool  `yaml:"enabled,omitempty" json:"enabled"`
	FOFAClient      string `yaml:"fofa_client" json:"fofa_client"`
	MaxAssets       int    `yaml:"max_assets" json:"max_assets"`
	BatchSize       int    `yaml:"batch_size" json:"batch_size"`
	Concurrency     int    `yaml:"concurrency" json:"concurrency"`
	TimeoutSeconds  int    `yaml:"timeout_seconds" json:"timeout_seconds"`
	FlushIntervalMS int    `yaml:"flush_interval_ms" json:"flush_interval_ms"`
	MaxStateBytes   int    `yaml:"max_state_bytes" json:"max_state_bytes"`
	MaxUpdateBytes  int    `yaml:"max_update_bytes" json:"max_update_bytes"`
	// EASM configures the enterprise asset portrait producer.
	EASM EASMPanelConfig `yaml:"easm" json:"easm"`
}

// EASMPanelConfig binds the portrait's aggregation roles to MCP tools. An empty
// server ID selects the one enabled server whose catalog offers each tool; an
// empty tool name turns off the steps that need that role. The agent pushes the
// graph itself, so no discovery role is configured here.
type EASMPanelConfig struct {
	ServerID      string            `yaml:"server_id" json:"server_id"`
	Tools         map[string]string `yaml:"tools" json:"tools"`
	MaxDomains    int               `yaml:"max_domains" json:"max_domains"`
	MaxComponents int               `yaml:"max_components" json:"max_components"`
	// MaxClouds bounds the hosting provider rows; MaxServices the services
	// listed inside each row.
	MaxClouds   int `yaml:"max_clouds" json:"max_clouds"`
	MaxServices int `yaml:"max_services" json:"max_services"`
}

// EASMToolRoles are the aggregation roles of the portrait producer, with the
// tool names of the FOFA MCP server as defaults. The agent performs the
// discovery lookups itself and pushes their results into the panel.
var EASMToolRoles = map[string]string{
	"stats":         "mcp_fofa_stats",
	"heat":          "fofa_asset_attention_trend",
	"product_rules": "fofa_product_rule_lookup",
}

// retiredEASMToolRoles are lookups the producer no longer performs. A saved
// configuration still names them; ignoring them keeps an upgrade from failing
// validation over a role that no longer exists.
var retiredEASMToolRoles = map[string]bool{
	"organizations": true,
	"relations":     true,
	"branches":      true,
	"filings":       true,
	"asset_rules":   true,
}

// WithDefaults returns independent settings for old and partially filled files.
func (p TaskPanelConfig) WithDefaults() TaskPanelConfig {
	enabled := true
	if p.Enabled != nil {
		enabled = *p.Enabled
	}
	p.Enabled = &enabled
	if p.FOFAClient == "" {
		p.FOFAClient = "fofa"
	}
	if p.MaxAssets == 0 {
		p.MaxAssets = 1000
	}
	if p.BatchSize == 0 {
		p.BatchSize = 100
	}
	if p.Concurrency == 0 {
		p.Concurrency = 16
	}
	if p.TimeoutSeconds == 0 {
		p.TimeoutSeconds = 8
	}
	if p.FlushIntervalMS == 0 {
		p.FlushIntervalMS = 100
	}
	if p.MaxStateBytes == 0 {
		p.MaxStateBytes = 8 << 20
	}
	if p.MaxUpdateBytes == 0 {
		p.MaxUpdateBytes = 1 << 20
	}
	p.EASM = p.EASM.withDefaults()
	return p
}

// Validate refuses unsafe or internally inconsistent resource budgets.
func (p TaskPanelConfig) Validate() error {
	if p.FOFAClient == "" || p.MaxAssets < 1 || p.MaxAssets > 100000 || p.BatchSize < 1 || p.BatchSize > p.MaxAssets || p.Concurrency < 1 || p.Concurrency > 256 || p.TimeoutSeconds < 1 || p.TimeoutSeconds > 300 || p.FlushIntervalMS < 10 || p.FlushIntervalMS > 10000 || p.MaxUpdateBytes < 1024 || p.MaxStateBytes < p.MaxUpdateBytes || p.MaxStateBytes > 64<<20 {
		return fmt.Errorf("invalid task panel limits: require assets 1..100000, batch <= assets, concurrency 1..256, timeout 1..300s, flush 10..10000ms, and 1024 <= update <= state <= 64MiB")
	}
	return p.EASM.validate()
}

// withDefaults returns a detached copy; a configured empty name is kept so a
// user can turn one role off.
func (c EASMPanelConfig) withDefaults() EASMPanelConfig {
	tools := make(map[string]string, len(EASMToolRoles))
	for role, name := range EASMToolRoles {
		if configured, ok := c.Tools[role]; ok {
			name = configured
		}
		tools[role] = name
	}
	for role, name := range c.Tools {
		if _, known := tools[role]; !known && !retiredEASMToolRoles[role] {
			tools[role] = name
		}
	}
	c.Tools = tools
	if c.MaxDomains == 0 {
		c.MaxDomains = 20
	}
	if c.MaxComponents == 0 {
		c.MaxComponents = 20
	}
	if c.MaxClouds == 0 {
		c.MaxClouds = 5
	}
	if c.MaxServices == 0 {
		c.MaxServices = 3
	}
	return c
}

func (c EASMPanelConfig) validate() error {
	if c.MaxDomains < 1 || c.MaxDomains > 200 || c.MaxComponents < 5 || c.MaxComponents > 100 {
		return fmt.Errorf("invalid EASM settings: max_domains must be 1..200 and max_components 5..100")
	}
	if c.MaxClouds < 1 || c.MaxClouds > 20 || c.MaxServices < 1 || c.MaxServices > 10 {
		return fmt.Errorf("invalid EASM settings: max_clouds must be 1..20 and max_services 1..10")
	}
	for role, name := range c.Tools {
		if retiredEASMToolRoles[role] {
			// Saved files and older settings clients still carry a retired
			// lookup; it is neither an unknown role nor a live binding.
			continue
		}
		if _, known := EASMToolRoles[role]; !known {
			return fmt.Errorf("invalid EASM settings: unknown tool role %q", role)
		}
		if name != strings.TrimSpace(name) || len(name) > 128 {
			return fmt.Errorf("invalid EASM settings: tool name for %q must be trimmed and at most 128 characters", role)
		}
	}
	if c.ServerID != strings.TrimSpace(c.ServerID) {
		return fmt.Errorf("invalid EASM settings: server_id must be trimmed")
	}
	return validateCloudLabels(EASMCloudLabels)
}

// validateCloudLabels keeps the label table usable as a lookup: a key that is
// not lowercase would never match, and a category outside the published set
// would reach the renderer as a blank cell.
func validateCloudLabels(labels map[string]CloudLabel) error {
	known := make(map[string]bool, len(EASMCloudCategories))
	for _, category := range EASMCloudCategories {
		known[category] = true
	}
	for name, label := range labels {
		if name == "" || name != strings.ToLower(strings.TrimSpace(name)) {
			return fmt.Errorf("invalid EASM cloud label %q: keys must be trimmed lowercase", name)
		}
		if strings.TrimSpace(label.Display) == "" {
			return fmt.Errorf("invalid EASM cloud label %q: display name is empty", name)
		}
		if !known[label.Category] {
			return fmt.Errorf("invalid EASM cloud label %q: unknown category %q", name, label.Category)
		}
	}
	return nil
}
