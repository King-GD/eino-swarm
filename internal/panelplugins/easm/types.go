// Package easm builds the enterprise asset portrait panel. The agent performs
// every discovery lookup itself and pushes each finished batch through the
// source; this package merges those batches into one graph and owns the asset
// aggregations the agent should not repeat. The engine only supplies a tool
// caller, the panel state the call starts from, and a publisher.
package easm

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// Step identifies one aggregation the producer performs. The graph comes from
// the agent, so only the asset metrics remain. The state schema keeps the
// retired graph identifiers so an already stored panel still validates.
type Step string

const (
	StepSummary    Step = "summary"
	StepHeat       Step = "heat"
	StepComponents Step = "components"
	StepClouds     Step = "clouds"
)

// Tool roles. Several steps can share one role: summary and components both
// aggregate through RoleStats.
const (
	RoleStats        = "stats"
	RoleHeat         = "heat"
	RoleProductRules = "product_rules"
)

// Roles lists every role a configuration can bind.
var Roles = []string{RoleStats, RoleHeat, RoleProductRules}

// Steps is the aggregation order. They are independent, so the producer runs
// them concurrently and publishes each as it finishes.
var Steps = []Step{StepSummary, StepHeat, StepComponents, StepClouds}

// Input is one batch of the agent's own findings. Subject is the confirmed
// target; every list is merged into the portrait already in the panel.
// Query, when given, is the final FOFA asset query behind the metrics.
type Input struct {
	Subject      string             `json:"subject"`
	Description  string             `json:"description,omitempty"`
	Logo         string             `json:"logo,omitempty"`
	Website      string             `json:"website,omitempty"`
	Companies    []CompanyInput     `json:"companies,omitempty"`
	Domains      []DomainInput      `json:"domains,omitempty"`
	Certificates []CertificateInput `json:"certificates,omitempty"`
	Query        string             `json:"query,omitempty"`
}

// CompanyInput is one reported company. Parent names the upper company it was
// reported under; without it the company hangs under the subject. Source is the
// tool that reported it.
type CompanyInput struct {
	Name       string `json:"name"`
	Parent     string `json:"parent,omitempty"`
	Relation   string `json:"relation,omitempty"`
	Share      string `json:"share,omitempty"`
	RegistryID string `json:"registry_id,omitempty"`
	Confirmed  bool   `json:"confirmed,omitempty"`
	Source     string `json:"source"`
	Via        string `json:"via,omitempty"`
}

// DomainInput is one reported domain. Parent names the company it belongs to;
// without it the domain hangs under the subject.
type DomainInput struct {
	Domain    string `json:"domain"`
	Parent    string `json:"parent,omitempty"`
	Confirmed bool   `json:"confirmed,omitempty"`
	Source    string `json:"source"`
	Via       string `json:"via,omitempty"`
}

// CertificateInput is one certificate as the aggregation reported it. Serial
// identifies it; the first observed Domain is its source.
type CertificateInput struct {
	Serial     string   `json:"serial"`
	Domain     string   `json:"domain"`
	Issuer     string   `json:"issuer,omitempty"`
	SubjectCN  string   `json:"subject_cn,omitempty"`
	SubjectOrg string   `json:"subject_org,omitempty"`
	Names      []string `json:"names,omitempty"`
	NotBefore  string   `json:"not_before,omitempty"`
	NotAfter   string   `json:"not_after,omitempty"`
	Valid      *bool    `json:"valid,omitempty"`
	Match      *bool    `json:"match,omitempty"`
	Expired    *bool    `json:"expired,omitempty"`
	Source     string   `json:"source"`
	Via        string   `json:"via,omitempty"`
}

// Settings are resolved by the host from the user's configuration.
type Settings struct {
	// Tools maps each tool role (Role* constants) to the remote tool name it
	// calls. A missing entry marks the steps needing it unavailable.
	Tools map[string]string
	// MaxDomains bounds the domain nodes of one portrait; entries beyond it are
	// refused and counted in the receipt.
	MaxDomains int
	// MaxComponents is the TopN product buckets one aggregation requests.
	MaxComponents int
	// MaxClouds is the configured number of hosting provider rows.
	MaxClouds int
	// MaxServices is the TopN services one cloud row requests.
	MaxServices int
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
	Clouds        []Cloud     `json:"clouds"`
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
	statusUnavailable = "unavailable"
	statusInterrupted = "interrupted"
	statusIdle        = "idle"
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
	Relation       string         `json:"relation,omitempty"`
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

// Cloud is one hosting provider row: the company's assets behind that provider,
// not the provider's own footprint. Category is a stable key the renderer words
// in the user's language. Name is FOFA's spelling; DisplayName is the maintained
// Chinese name, which the renderer shows only in Chinese, as FOFA AI does.
type Cloud struct {
	Name        string         `json:"name"`
	DisplayName string         `json:"display_name,omitempty"`
	Category    string         `json:"category"`
	Assets      int64          `json:"assets"`
	IPs         int64          `json:"ips"`
	Services    []CloudService `json:"services"`
}

type CloudService struct {
	Name  string `json:"name"`
	Count int64  `json:"count"`
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

// Result is the tool receipt returned to the agent: counts, step outcomes and
// what could not be placed, never bulk data, which stays in the panel.
type Result struct {
	Nodes      int          `json:"nodes"`
	Edges      int          `json:"edges"`
	Components int          `json:"components"`
	Steps      []StepStatus `json:"steps"`
	// UnresolvedParents counts entries whose reported parent was not on the
	// graph; they were attached to the subject as clues instead.
	UnresolvedParents int `json:"unresolved_parents"`
	// RefusedDomains counts domain entries over the configured domain budget.
	RefusedDomains int `json:"refused_domains"`
	// Next tells the agent what to do once the final aggregations are in.
	Next string `json:"next,omitempty"`
}
