package easm

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/LubyRuffy/eino-swarm/frontend"
	"github.com/LubyRuffy/eino-swarm/internal/config"
	"github.com/LubyRuffy/eino-swarm/internal/taskpanel"
)

func catalogDefinition(t *testing.T) taskpanel.Definition {
	t.Helper()
	var definitions []taskpanel.Definition
	if err := json.Unmarshal(frontend.TaskPanelCatalog(), &definitions); err != nil {
		t.Fatal(err)
	}
	for _, definition := range definitions {
		if definition.ID == "easm-asset-portrait" {
			return definition
		}
	}
	t.Fatal("catalog has no EASM definition")
	return taskpanel.Definition{}
}

// inputSchema is the catalog's own contract for one run_panel_source batch.
func inputSchema(t *testing.T, definition taskpanel.Definition) taskpanel.Definition {
	t.Helper()
	if definition.Source == nil {
		t.Fatal("the EASM definition has no registered source")
	}
	return taskpanel.Definition{Schema: definition.Source.InputSchema}
}

// The host rejects any publication that fails the catalog schema, so every
// state this producer emits — the merge-only one and the fullest one — must
// validate, with the fields the agent's own batches introduce.
func TestEveryPublishedStateMatchesTheCatalogSchema(t *testing.T) {
	definition := catalogDefinition(t)
	if err := taskpanel.ValidateDefinition(definition); err != nil {
		t.Fatal(err)
	}
	caller := &fakeCaller{replies: map[string]func(map[string]any) (string, error){
		RoleStats: func(arguments map[string]any) (string, error) {
			if arguments["fields"] == "ip" {
				return `{"query":"org=\"Subject\"","size":12,"distinct":{"ip":"4"}}`, nil
			}
			return `{"aggs":{"product":[{"name":"nginx","count":9}]}}`, nil
		},
		RoleHeat:         fixed(`{"hot_trend":[{"date":"2026-01-02","value":1.5}]}`),
		RoleProductRules: fixed(`{"results":[]}`),
	}}
	var out published
	var raw [][]byte
	publish := func(ctx context.Context, body json.RawMessage) error {
		raw = append(raw, append(json.RawMessage(nil), body...))
		return out.publish(ctx, body)
	}
	input := Input{
		Subject:     "Subject",
		Description: "d",
		Logo:        "https://logo.test/a.png",
		Website:     "https://subject.test",
		Companies: []CompanyInput{
			{Name: "Parent", RegistryID: "9", Relation: "organization", Share: "51%", Confirmed: true, Source: "org_tool", Via: "lookup"},
			{Name: "Child", Parent: "Parent", Confirmed: true, Source: "relation_tool"},
		},
		Domains: []DomainInput{
			{Domain: "subject.test", Parent: "Parent", Confirmed: true, Source: "org_tool"},
			{Domain: "child.test", Parent: "Child", Source: "relation_tool"},
		},
		Certificates: []CertificateInput{{
			Serial: "0a1b", Domain: "subject.test", Issuer: "CA", SubjectCN: "subject.test", SubjectOrg: "Subject",
			Names: []string{"subject.test"}, NotBefore: "2026-01-01", NotAfter: "2027-01-01",
			Valid: boolean(true), Match: boolean(true), Expired: boolean(false), Source: "stats_tool",
		}},
		Query: `org="Subject"`,
	}
	// The first batch carries no query, the second the metrics, so both shapes
	// of published state are validated against the schema.
	for _, step := range []Input{{Subject: input.Subject, Companies: input.Companies, Domains: input.Domains}, input} {
		if _, err := Run(t.Context(), step, testSettings(), nil, caller, publish); err != nil {
			t.Fatal(err)
		}
	}
	if len(raw) < 6 {
		t.Fatalf("publications = %d", len(raw))
	}
	for i, state := range raw {
		if err := taskpanel.ValidateState(definition, state); err != nil {
			t.Fatalf("publication %d rejected: %v\n%s", i, err, state)
		}
	}
}

// A saved panel from the previous pipeline must keep rendering: its graph and
// step identifiers are still valid, and a new batch continues it.
func TestAStoredPanelFromThePreviousPipelineValidates(t *testing.T) {
	definition := catalogDefinition(t)
	stored := json.RawMessage(`{"schema_version":1,"title":"Subject","query":"org=\"Subject\"","collection":{"status":"done","steps":[{"id":"organizations","status":"done"},{"id":"summary","status":"done"},{"id":"heat","status":"failed","code":"tool_error"}]},"nodes":[{"id":"root","kind":"root","label":"Subject"},{"id":"domain:old.test","kind":"domain","label":"old.test","detail":{"basis":[{"source":"organizations"}]}}],"edges":[{"source":"root","target":"domain:old.test","confirmed":true}],"heat":[],"components":[]}`)
	if err := taskpanel.ValidateState(definition, stored); err != nil {
		t.Fatal(err)
	}
	state, _, err := push(t, stored, Input{Subject: "Subject", Domains: []DomainInput{{Domain: "new.test", Source: "t"}}}, onlyRoles(), &fakeCaller{})
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Nodes) != 3 || state.Collection.Status != statusIdle {
		t.Fatalf("state = %+v", state)
	}
	if len(state.Collection.Steps) != len(Steps) {
		t.Fatalf("retired steps were republished: %+v", state.Collection.Steps)
	}
	if got := stepStatus(state, StepHeat); got.Status != statusFailed || got.Code != "tool_error" {
		t.Fatalf("heat = %+v", got)
	}
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := taskpanel.ValidateState(definition, raw); err != nil {
		t.Fatal(err)
	}
}

// The input schema is the agent's contract: it accepts every documented field
// and refuses one the producer would ignore.
func TestInputSchemaAcceptsEveryDocumentedField(t *testing.T) {
	schema := inputSchema(t, catalogDefinition(t))
	full := json.RawMessage(`{"subject":"Subject","description":"d","logo":"https://logo.test/a.png","website":"https://subject.test","query":"org=\"Subject\"","companies":[{"name":"Company","parent":"Upper","relation":"subsidiary","share":"51%","registry_id":"9","confirmed":true,"source":"tool","via":"record"}],"domains":[{"domain":"subject.test","parent":"Company","confirmed":true,"source":"tool","via":"record"}],"certificates":[{"serial":"0a1b","domain":"subject.test","issuer":"CA","subject_cn":"subject.test","subject_org":"Subject","names":["subject.test"],"not_before":"2026-01-01","not_after":"2027-01-01","valid":true,"match":true,"expired":false,"source":"tool","via":"record"}]}`)
	if err := taskpanel.ValidateState(schema, full); err != nil {
		t.Fatal(err)
	}
	if err := taskpanel.ValidateState(schema, json.RawMessage(`{"subject":"S"}`)); err != nil {
		t.Fatalf("subject alone was refused: %v", err)
	}
	for _, rejected := range []string{
		`{}`,
		`{"subject":""}`,
		`{"subject":"S","unknown":1}`,
		`{"subject":"S","companies":[{"name":"Company"}]}`,
		`{"subject":"S","companies":[{"name":"Company","source":"t","owner":"x"}]}`,
		`{"subject":"S","domains":[{"domain":"a.test","source":""}]}`,
		`{"subject":"S","certificates":[{"serial":"1","domain":"","source":"t"}]}`,
		`{"subject":"S","certificates":[{"serial":"1","domain":"a.test","source":"t","valid":"yes"}]}`,
		`{"subject":"S","logo":"javascript:x"}`,
		`{"subject":"S","website":"ftp://a.test"}`,
	} {
		if err := taskpanel.ValidateState(schema, json.RawMessage(rejected)); err == nil {
			t.Fatalf("accepted %s", rejected)
		}
	}
}

// Settings and the producer must name the same roles, or a configured tool
// would silently never be called.
func TestConfiguredRolesMatchTheProducerRoles(t *testing.T) {
	var configured []string
	for role := range config.EASMToolRoles {
		configured = append(configured, role)
	}
	slices.Sort(configured)
	roles := slices.Clone(Roles)
	slices.Sort(roles)
	if !slices.Equal(configured, roles) {
		t.Fatalf("config roles %v != producer roles %v", configured, roles)
	}
}
