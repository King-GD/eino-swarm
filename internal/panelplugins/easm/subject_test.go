package easm

import "testing"

// A registry row for the confirmed subject must enrich the root. A display
// alias can continue the portrait when the agent confirms the same registry ID.
func TestSubjectRegistryRowMergesIntoTheRoot(t *testing.T) {
	input := Input{
		Subject: "Subject Co",
		Companies: []CompanyInput{
			{Name: "Subject Co", RegistryID: "91SUBJECT", Relation: "subject", Confirmed: true, Source: "company_info"},
			{Name: "Child Co", Parent: "Subject Co", RegistryID: "91CHILD", Confirmed: true, Source: "relation"},
		},
		Domains: []DomainInput{{Domain: "subject.test", Parent: "Subject Co", Confirmed: true, Source: "org_lookup"}},
	}
	state, result, err := push(t, nil, input, onlyRoles(), &fakeCaller{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := nodeOf(state, registryCompanyID("91SUBJECT")); ok {
		t.Fatal("the subject's registry row became a separate company")
	}
	root, _ := nodeOf(state, RootID)
	if root.Detail == nil || root.Detail.RegistryID != "91SUBJECT" {
		t.Fatalf("root detail = %+v", root.Detail)
	}
	for _, target := range []string{registryCompanyID("91CHILD"), DomainID("subject.test")} {
		if edge, ok := edgeOf(state, target); !ok || edge.Source != RootID || !edge.Confirmed {
			t.Fatalf("%s edge = %+v %v", target, edge, ok)
		}
	}
	if result.UnresolvedParents != 0 {
		t.Fatalf("result = %+v", result)
	}

	next := Input{Subject: "Subject Co（Brand）", Companies: []CompanyInput{
		{Name: "Subject Co（Brand）", RegistryID: "91SUBJECT", Confirmed: true, Source: "company_info"},
		{Name: "Subject Company Ltd", RegistryID: "91SUBJECT", Source: "branch"},
	}}
	again, _, err := push(t, stateJSON(t, state), next, onlyRoles(), &fakeCaller{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := edgeOf(again, DomainID("subject.test")); !ok {
		t.Fatal("the confirmed registry alias started a new portrait")
	}
	if _, ok := nodeOf(again, registryCompanyID("91SUBJECT")); ok {
		t.Fatal("the subject's registry ID became a separate company")
	}
	if root, _ := nodeOf(again, RootID); root.Label != again.Title {
		t.Fatalf("root %q and panel title %q disagree", root.Label, again.Title)
	}
	originalName, _, err := push(t, stateJSON(t, again), Input{Subject: input.Subject}, onlyRoles(), &fakeCaller{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := nodeOf(originalName, DomainID("subject.test")); !ok {
		t.Fatal("the verified registry name started a new portrait")
	}
}

// A user changing the confirmed region or registration must see only the new
// subject, even if the display names share a base or are exactly the same.
func TestChangingConfirmedSubjectDropsPreviousClues(t *testing.T) {
	t.Run("different parenthesized names", func(t *testing.T) {
		first, _, err := push(t, nil, Input{Subject: "Atlas (West)", Domains: []DomainInput{{Domain: "old.test", Source: "registry"}}}, onlyRoles(), &fakeCaller{})
		if err != nil {
			t.Fatal(err)
		}
		next, _, err := push(t, stateJSON(t, first), Input{Subject: "Atlas (East)"}, onlyRoles(), &fakeCaller{})
		if err != nil || len(next.Nodes) != 1 || next.Nodes[0].Label != "Atlas (East)" || next.Title != next.Nodes[0].Label {
			t.Fatalf("portrait = %+v, error = %v", next, err)
		}
	})
	t.Run("same name with a different registration", func(t *testing.T) {
		first, _, err := push(t, nil, Input{Subject: "Atlas", Companies: []CompanyInput{{Name: "Atlas", RegistryID: "west", Confirmed: true, Source: "registry"}}, Domains: []DomainInput{{Domain: "old.test", Source: "registry"}}}, onlyRoles(), &fakeCaller{})
		if err != nil {
			t.Fatal(err)
		}
		next, _, err := push(t, stateJSON(t, first), Input{Subject: "Atlas", Companies: []CompanyInput{{Name: "Atlas", RegistryID: "east", Confirmed: true, Source: "registry"}}}, onlyRoles(), &fakeCaller{})
		if err != nil || len(next.Nodes) != 1 || registryID(&next.Nodes[0]) != "east" {
			t.Fatalf("portrait = %+v, error = %v", next, err)
		}
	})
}

func TestOnlyOneConfirmedRegistrationCanIdentifyTheRoot(t *testing.T) {
	state, result, err := push(t, nil, Input{Subject: "Atlas", Companies: []CompanyInput{
		{Name: "Atlas", RegistryID: "atlas-1", Confirmed: true, Source: "registry"},
		{Name: "Atlas", RegistryID: "atlas-2", Confirmed: true, Source: "registry"},
	}, Domains: []DomainInput{{Domain: "same.test", Parent: "Atlas", Source: "registry", Confirmed: true}}}, onlyRoles(), &fakeCaller{})
	if err != nil || len(state.Nodes) != 4 {
		t.Fatalf("portrait = %+v, error = %v", state, err)
	}
	root, _ := nodeOf(state, RootID)
	edge, _ := edgeOf(state, DomainID("same.test"))
	if registryID(&root) != "" || result.UnresolvedParents != 1 || edge.Source != RootID || edge.Confirmed {
		t.Fatalf("root = %+v, edge = %+v, receipt = %+v", root, edge, result)
	}
}

// Without an explicit signal the agent kept researching for minutes after the
// panel had its final numbers. Only the call that collected them says so.
func TestOnlyTheFinalAggregationTellsTheAgentToFinish(t *testing.T) {
	_, early, err := push(t, nil, Input{Subject: "Subject Co"}, onlyRoles(), &fakeCaller{})
	if err != nil || early.Next != "" {
		t.Fatalf("batch without query next=%q err=%v", early.Next, err)
	}
	// The final call is the only one that ends the turn. With no aggregation
	// tools configured it names what the panel lacks instead of claiming it;
	// the completed wording is covered in receipt_test.go.
	_, final, err := push(t, nil, Input{Subject: "Subject Co", Query: `domain=="subject.test"`}, onlyRoles(), &fakeCaller{})
	if err != nil || final.Next == "" || final.Next == finishedNext {
		t.Fatalf("final next=%q err=%v", final.Next, err)
	}
}
