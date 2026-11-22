package easm

import (
	"context"
	"encoding/json"
	"strings"
)

func init() {
	stepFuncs[StepOrganizations] = stepDef{roles: []string{RoleOrganizations}, fn: (*run).organizations}
}

// organizations looks the subject up by name. A returned organization with
// the subject's exact name is the subject itself, so its domains hang
// directly under the root; any other organization is a pending related
// company whose reported domains are confirmed under that company.
func (r *run) organizations(ctx context.Context) error {
	subject := r.graph.Root().Label
	var reply struct {
		Results []struct {
			ID      json.RawMessage `json:"id"`
			Name    string          `json:"name"`
			Domains []struct {
				Domain string `json:"domain"`
			} `json:"domains"`
		} `json:"results"`
	}
	if err := r.call(ctx, RoleOrganizations, map[string]any{"names": []string{subject}, "include": []string{"domains"}}, &reply); err != nil {
		return err
	}
	exact := 0
	for _, organization := range reply.Results {
		if strings.TrimSpace(organization.Name) == subject {
			exact++
		}
	}
	for _, organization := range reply.Results {
		name := strings.TrimSpace(organization.Name)
		if name == "" {
			continue
		}
		id := decimalID(organization.ID)
		owner := RootID
		// Several same-name records are different registrations; none of
		// them can be asserted to be the subject.
		if name != subject || exact != 1 {
			owner = organizationNodeID(id, name)
			r.graph.Upsert(Node{ID: owner, Kind: "company", Label: name, Detail: &Detail{OrganizationID: id}})
			r.graph.AddBasis(owner, Basis{Source: "organizations"})
			r.graph.Link(RootID, owner, false)
		} else if id != "" {
			r.graph.Upsert(Node{ID: RootID, Label: subject, Detail: &Detail{OrganizationID: id}})
		}
		for _, entry := range organization.Domains {
			r.graph.AddDomain(owner, entry.Domain, true, Basis{Source: "organizations", Via: name})
		}
	}
	return nil
}

func organizationNodeID(id, name string) string {
	if id != "" {
		return "company:org:" + id
	}
	return "company:org-name:" + name
}

// decimalID accepts a positive decimal identity as a JSON string or an exact
// number. Rounded or fractional values are rejected rather than guessed.
func decimalID(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var value string
	if json.Unmarshal(raw, &value) != nil {
		value = string(raw)
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			return ""
		}
	}
	return value
}
