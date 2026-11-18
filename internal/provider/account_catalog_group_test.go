package provider

import (
	"reflect"
	"testing"
)

// Functional aliases can all use Chat Completions, but only the conversation
// binding belongs in a third-party conversation picker.
func TestThirdPartyCatalogListsOnlyItsConversationBinding(t *testing.T) {
	for _, tc := range []struct {
		name, group string
		names       []string
		defaults    map[string]string
		types       map[string]string
		want        []string
	}{
		{name: "unclassified roles", group: "third_party", names: []string{"title", "conversation", "compact", "translation"}, want: []string{"conversation"}},
		{name: "chat transport does not make auxiliary roles selectable", group: "third_party", names: []string{"title", "conversation", "compact"}, types: map[string]string{"title": "chat", "conversation": "chat", "compact": "chat"}, want: []string{"conversation"}},
		{name: "explicit conversation binding", group: "third_party", names: []string{"namer", "chat-target", "translator"}, defaults: map[string]string{"conversation": "chat-target", "title": "namer", "translation": "translator"}, want: []string{"chat-target"}},
		{name: "no conversation alias", group: "third_party", names: []string{"title", "compact"}, want: []string{}},
		{name: "non-chat conversation refused", group: "third_party", names: []string{"conversation"}, types: map[string]string{"conversation": "embedding"}, want: []string{}},
		{name: "original catalog keeps model names", group: "original", names: []string{"title", "conversation", "compact"}, want: []string{"title", "conversation", "compact"}},
		{name: "legacy catalog keeps model names", names: []string{"title", "conversation", "compact"}, want: []string{"title", "conversation", "compact"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src, err := NewAccountSource("gateway:test", "Server", "https://gateway.invalid/v1", "private", Catalog{Names: tc.names, Types: tc.types})
			if err != nil {
				t.Fatal(err)
			}
			src.SetCatalogGroup(tc.group)
			src.SetModelPolicy(true, tc.defaults)
			got := make([]string, 0)
			for _, row := range src.rows(false, "") {
				got = append(got, row.Model)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("conversation choices = %v, want %v", got, tc.want)
			}
			if src.ModelCount() != len(tc.names) {
				t.Fatal("filter discarded the functional catalog")
			}
			for _, name := range tc.names {
				if !src.permits(name) {
					t.Fatalf("hidden functional binding %q stopped resolving", name)
				}
			}
		})
	}
}

func TestCatalogGroupSurvivesListingAndInvalidatesChangedMetadata(t *testing.T) {
	a, err := NewAccountSource("gateway:test", "Server", "https://gateway.invalid/v1", "private", Catalog{Names: []string{"shared"}})
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewAccountSource("gateway:test", "Server", "https://gateway.invalid/v1", "private", Catalog{Names: []string{"shared"}})
	if err != nil {
		t.Fatal(err)
	}
	a.SetCatalogGroup("original")
	if a.equal(b) {
		t.Fatal("changed source metadata must be republished")
	}
	b.SetCatalogGroup("original")
	if !a.equal(b) {
		t.Fatal("unchanged source metadata invalidates model clients")
	}
	rows := a.rows(true, "removed-default")
	if len(rows) != 2 {
		t.Fatalf("rows=%v", rows)
	}
	for _, row := range rows {
		if row.CatalogGroup != "original" {
			t.Fatalf("group disappeared: %+v", row)
		}
	}
}
