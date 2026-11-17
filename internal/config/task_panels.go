package config

import "fmt"

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
	return p
}

// Validate refuses unsafe or internally inconsistent resource budgets.
func (p TaskPanelConfig) Validate() error {
	if p.FOFAClient == "" || p.MaxAssets < 1 || p.MaxAssets > 100000 || p.BatchSize < 1 || p.BatchSize > p.MaxAssets || p.Concurrency < 1 || p.Concurrency > 256 || p.TimeoutSeconds < 1 || p.TimeoutSeconds > 300 || p.FlushIntervalMS < 10 || p.FlushIntervalMS > 10000 || p.MaxUpdateBytes < 1024 || p.MaxStateBytes < p.MaxUpdateBytes || p.MaxStateBytes > 64<<20 {
		return fmt.Errorf("invalid task panel limits: require assets 1..100000, batch <= assets, concurrency 1..256, timeout 1..300s, flush 10..10000ms, and 1024 <= update <= state <= 64MiB")
	}
	return nil
}
