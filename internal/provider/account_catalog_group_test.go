package provider

import "testing"

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
