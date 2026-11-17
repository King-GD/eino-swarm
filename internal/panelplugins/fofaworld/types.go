// Package fofaworld adapts FOFA CLI collection and locally owned HTTP probes
// into the business state consumed by the example world-map panel.
package fofaworld

import (
	"context"
	"encoding/json"
	"time"
)

// Input contains caller-configured budgets. Run never reads FOFA credentials.
type Input struct {
	Query         string
	ClientPath    string
	Workspace     string
	MaxAssets     int
	BatchSize     int
	Concurrency   int
	Timeout       time.Duration
	FlushInterval time.Duration
}

// Publish receives an independently encoded complete state, never a mutable map.
type Publish func(context.Context, json.RawMessage) error

// Collection distinguishes a stopped collector from proof of complete results.
type Collection struct {
	Phase         string `json:"phase"`
	Received      int    `json:"received"`
	Discovered    int    `json:"discovered"`
	ReportedTotal *int   `json:"reported_total,omitempty"`
	Completeness  string `json:"completeness"`
	Reason        string `json:"reason,omitempty"`
}

// Asset retains recorded network identity independently of its logical HTTP host.
type Asset struct {
	ID        string   `json:"id"`
	IP        string   `json:"ip"`
	Port      int      `json:"port"`
	Protocol  string   `json:"protocol"`
	Host      string   `json:"host"`
	Country   string   `json:"country"`
	Region    string   `json:"region"`
	City      string   `json:"city"`
	Longitude *float64 `json:"longitude,omitempty"`
	Latitude  *float64 `json:"latitude,omitempty"`
	URL       string   `json:"url,omitempty"`
	Status    string   `json:"status"`
}

// ProbeProgress counts assets, including those without usable coordinates.
type ProbeProgress struct {
	Pending     int `json:"pending"`
	Running     int `json:"running"`
	Responded   int `json:"responded"`
	NoResponse  int `json:"no_response"`
	Skipped     int `json:"skipped"`
	Interrupted int `json:"interrupted"`
}

// Result exposes bounded classifications, never target response bodies/errors.
type Result struct {
	Status     string `json:"status"`
	StatusCode int    `json:"status_code,omitempty"`
	ElapsedMS  int64  `json:"elapsed_ms,omitempty"`
	Error      string `json:"error,omitempty"`
	FinishedAt string `json:"finished_at"`
}

// State is the plugin's versioned schema payload; the host owns wire revisions.
type State struct {
	Collection  Collection        `json:"collection"`
	AssetsByID  map[string]Asset  `json:"assets_by_id"`
	Probe       ProbeProgress     `json:"probe"`
	ResultsByID map[string]Result `json:"results_by_id"`
}

// Summary is the bounded tool receipt; asset rows stay in the panel state.
type Summary struct {
	Collection Collection    `json:"collection"`
	Probe      ProbeProgress `json:"probe"`
}

// Failure is a safe category suitable for model/tool feedback.
type Failure struct{ Code string }

func (e *Failure) Error() string { return "fofa_world: " + e.Code }

func failure(code string) error { return &Failure{Code: code} }
