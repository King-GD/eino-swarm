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

// The host rejects any publication that fails the catalog schema, so every
// state this producer emits — including the fullest one — must validate.
func TestEveryPublishedStateMatchesTheCatalogSchema(t *testing.T) {
	definition := catalogDefinition(t)
	if err := taskpanel.ValidateDefinition(definition); err != nil {
		t.Fatal(err)
	}
	caller := &fakeCaller{replies: map[string]func(map[string]any) (string, error){
		RoleOrganizations: fixed(`{"results":[{"id":"7","name":"Subject","domains":[{"domain":"subject.test"}]},{"id":"8","name":"Peer","domains":[{"domain":"peer.test"}]}]}`),
	}}
	var out published
	var raw []json.RawMessage
	publish := func(ctx context.Context, body json.RawMessage) error {
		raw = append(raw, append(json.RawMessage(nil), body...))
		return out.publish(ctx, body)
	}
	if _, err := Run(t.Context(), Input{Subject: "Subject", Query: `org="Subject"`, Description: "d", Logo: "https://logo.test/a.png", Website: "https://subject.test"}, testSettings(), caller, publish); err != nil {
		t.Fatal(err)
	}
	for i, state := range raw {
		if err := taskpanel.ValidateState(definition, state); err != nil {
			t.Fatalf("publication %d rejected: %v\n%s", i, err, state)
		}
	}
	if err := taskpanel.ValidateState(taskpanel.Definition{Schema: definition.Source.InputSchema}, json.RawMessage(`{"subject":"S","query":"q"}`)); err != nil {
		t.Fatal(err)
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
