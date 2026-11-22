// Package easm builds the enterprise asset portrait panel from configured MCP
// lookups. The engine only supplies a tool caller and a publisher; every rule
// about organizations, filings, certificates and metrics lives here.
package easm

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// Step identifies one lookup in the pipeline. Its configured tool name comes
// from settings; the identifier itself is part of the panel state contract.
type Step string

const (
	StepOrganizations Step = "organizations"
	StepRelations     Step = "relations"
	StepBranches      Step = "branches"
	StepFilings       Step = "filings"
	StepAssetRules    Step = "asset_rules"
	StepCertificates  Step = "certificates"
	StepSummary       Step = "summary"
	StepHeat          Step = "heat"
	StepComponents    Step = "components"
)

// Tool roles. Several steps can share one role, e.g. summary and components
// both aggregate through RoleStats.
const (
	RoleOrganizations = "organizations"
	RoleRelations     = "relations"
	RoleBranches      = "branches"
	RoleFilings       = "filings"
	RoleAssetRules    = "asset_rules"
	RoleStats         = "stats"
	RoleHeat          = "heat"
	RoleProductRules  = "product_rules"
)

// Roles lists every role a configuration can bind.
var Roles = []string{RoleOrganizations, RoleRelations, RoleBranches, RoleFilings, RoleAssetRules, RoleStats, RoleHeat, RoleProductRules}

// Steps is the pipeline order. Graph steps run before metrics so the root and
// its first branches are visible while the slower aggregations run.
var Steps = []Step{StepOrganizations, StepRelations, StepBranches, StepFilings, StepAssetRules, StepCertificates, StepSummary, StepHeat, StepComponents}

// Input is the agent-supplied run request. Subject is the confirmed target;
// the descriptive fields come from the agent's own research and are shown on
// the root card as reported, never used to widen a lookup.
type Input struct {
	Subject     string `json:"subject"`
	Query       string `json:"query,omitempty"`
	Description string `json:"description,omitempty"`
	Logo        string `json:"logo,omitempty"`
	Website     string `json:"website,omitempty"`
}

// Settings are resolved by the host from the user's configuration.
type Settings struct {
	// Tools maps each tool role (Role* constants) to the remote tool name it
	// calls. A missing entry marks the steps needing it unavailable.
	Tools map[string]string
	// MaxDomains bounds the per-domain certificate lookups of one run.
	MaxDomains int
	// MaxComponents is the TopN product buckets one aggregation requests.
	MaxComponents int
	// CallTimeout bounds one remote call; zero keeps the server's own timeout.
	CallTimeout time.Duration
}

// Caller runs one remote tool and returns its JSON reply body. ErrUnavailable
// reports that no configured server offers the tool.
type Caller interface {
	Call(ctx context.Context, tool string, arguments map[string]any) (json.RawMessage, error)
}

// CallerFunc adapts a function to Caller.
type CallerFunc func(context.Context, string, map[string]any) (json.RawMessage, error)

func (f CallerFunc) Call(ctx context.Context, tool string, arguments map[string]any) (json.RawMessage, error) {
	return f(ctx, tool, arguments)
}

// Publish receives the complete state; the host diffs and versions it.
type Publish func(context.Context, json.RawMessage) error

// ErrUnavailable reports a step whose tool no configured server offers.
var ErrUnavailable = errors.New("easm tool unavailable")

// ToolError carries a remote failure code without its free-form message, which
// may echo credentials or quota details into a shared panel.
type ToolError struct{ Code string }

func (e *ToolError) Error() string { return "easm tool failed: " + e.Code }

// State is the panel document. Its JSON shape is validated by the catalog
// schema and rendered by frontend/src/components/app/easm.
type State struct {
	SchemaVersion int         `json:"schema_version"`
	Title         string      `json:"title"`
	Query         string      `json:"query,omitempty"`
	Collection    Collection  `json:"collection"`
	Nodes         []Node      `json:"nodes"`
	Edges         []Edge      `json:"edges"`
	Summary       *Summary    `json:"summary,omitempty"`
	Heat          []HeatPoint `json:"heat"`
	Components    []Component `json:"components"`
}

// Collection reports pipeline progress. A step's status is what the renderer
// uses for loading indicators, so no separate transient channel is needed.
type Collection struct {
	Status string       `json:"status"`
	Steps  []StepStatus `json:"steps"`
}

type StepStatus struct {
	ID     Step   `json:"id"`
	Status string `json:"status"`
	// Code is a stable machine reason: unavailable, no_query, tool_error, ...
	Code string `json:"code,omitempty"`
}

const (
	statusPending     = "pending"
	statusRunning     = "running"
	statusDone        = "done"
	statusFailed      = "failed"
	statusSkipped     = "skipped"
	statusUnavailable = "unavailable"
	statusInterrupted = "interrupted"
)

type Node struct {
	ID          string  `json:"id"`
	Kind        string  `json:"kind"`
	FeatureKind string  `json:"featureKind,omitempty"`
	Label       string  `json:"label"`
	IPCount     *int64  `json:"ip_count,omitempty"`
	Detail      *Detail `json:"detail,omitempty"`
}

// Basis records why a node is on the graph as structured evidence. The
// renderer words it in the user's language; the producer never stores prose.
type Basis struct {
	Source string `json:"source"`
	// Via names the record that linked it, e.g. a filing number or rule name.
	Via string `json:"via,omitempty"`
	// Date is the source's own record date when it reports one.
	Date string `json:"date,omitempty"`
}

type Organization struct {
	Name  string `json:"name"`
	Count *int64 `json:"count,omitempty"`
}

type Feature struct {
	Kind   string `json:"kind"`
	Value  string `json:"value"`
	NodeID string `json:"node_id,omitempty"`
}

type Observation struct {
	Host      string `json:"host"`
	Domain    string `json:"domain,omitempty"`
	Source    string `json:"source,omitempty"`
	IsValid   *bool  `json:"is_valid,omitempty"`
	IsMatch   *bool  `json:"is_match,omitempty"`
	IsExpired *bool  `json:"is_expired,omitempty"`
}

type Detail struct {
	Description    string         `json:"description,omitempty"`
	Logo           string         `json:"logo,omitempty"`
	Website        string         `json:"website,omitempty"`
	Region         string         `json:"region,omitempty"`
	Industry       []string       `json:"industry,omitempty"`
	Organizations  []Organization `json:"organizations,omitempty"`
	Basis          []Basis        `json:"basis,omitempty"`
	RegistryID     string         `json:"registry_id,omitempty"`
	RegistryName   string         `json:"registry_name,omitempty"`
	OrganizationID string         `json:"organization_id,omitempty"`
	Status         string         `json:"status,omitempty"`
	Share          string         `json:"share,omitempty"`
	Features       []Feature      `json:"features,omitempty"`
	Serial         string         `json:"serial,omitempty"`
	Issuer         string         `json:"issuer,omitempty"`
	NotBefore      string         `json:"not_before,omitempty"`
	NotAfter       string         `json:"not_after,omitempty"`
	IsValid        *bool          `json:"is_valid,omitempty"`
	IsMatch        *bool          `json:"is_match,omitempty"`
	Observations   []Observation  `json:"observations,omitempty"`
	Names          []string       `json:"names,omitempty"`
	SubjectName    string         `json:"subject_name,omitempty"`
}

type Edge struct {
	Source    string `json:"source"`
	Target    string `json:"target"`
	Confirmed bool   `json:"confirmed"`
}

type Summary struct {
	Clues      *int64 `json:"clues,omitempty"`
	FOFAAssets *int64 `json:"fofa_assets,omitempty"`
	FOFAIPs    *int64 `json:"fofa_ips,omitempty"`
}

type HeatPoint struct {
	Date  string  `json:"date"`
	Value float64 `json:"value"`
}

type Component struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name,omitempty"`
	Count       int64  `json:"count"`
	HotDays     *int   `json:"hot_days,omitempty"`
	Tags        []Tag  `json:"tags,omitempty"`
}

type Tag struct {
	Type     string     `json:"type"`
	Summary  string     `json:"summary,omitempty"`
	Evidence []Evidence `json:"evidence,omitempty"`
}

type Evidence struct {
	Title  string `json:"title"`
	Date   string `json:"date,omitempty"`
	URL    string `json:"url,omitempty"`
	Detail string `json:"detail,omitempty"`
}

// Result is the tool receipt returned to the agent: counts and step outcomes,
// never bulk data, which stays in the panel.
type Result struct {
	Nodes      int          `json:"nodes"`
	Edges      int          `json:"edges"`
	Components int          `json:"components"`
	Steps      []StepStatus `json:"steps"`
}
