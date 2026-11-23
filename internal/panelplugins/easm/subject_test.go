package easm

import "testing"

// The agent may write the subject with its short name in brackets and also
// list the subject's own registry row among the companies. That row must merge
// into the root: a separate copy took the subject's name and every company
// and domain naming the subject as parent hung under the copy, leaving the
// root with one edge.
func TestSubjectRegistryRowMergesIntoTheRoot(t *testing.T) {
	input := Input{
		Subject: "Subject Co（Brand）",
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

	// A later batch writing the bare legal name continues the same portrait,
	// and the registry ID alone also identifies the subject.
	next := Input{Subject: "Subject Co", Companies: []CompanyInput{{Name: "Subject Company Ltd", RegistryID: "91SUBJECT", Source: "branch"}}}
	again, _, err := push(t, stateJSON(t, state), next, onlyRoles(), &fakeCaller{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := edgeOf(again, DomainID("subject.test")); !ok {
		t.Fatal("the bare legal name started a new portrait")
	}
	if _, ok := nodeOf(again, registryCompanyID("91SUBJECT")); ok {
		t.Fatal("the subject's registry ID became a separate company")
	}
}

func TestLegalNameDropsOneTrailingAlias(t *testing.T) {
	for raw, want := range map[string]string{
		"Subject Co（Brand）":  "Subject Co",
		"Subject Co (Brand)": "Subject Co",
		" Subject Co ":       "Subject Co",
		"(Brand)":            "(Brand)",
		"Subject (A) Co":     "Subject (A) Co",
	} {
		if got := legalName(raw); got != want {
			t.Fatalf("legalName(%q) = %q, want %q", raw, got, want)
		}
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
