package config

import (
	"strings"
	"testing"
)

// The table is a lookup keyed by lowercase source names: an uppercase key would
// never match and the provider would show untranslated under a blank category.
func TestCloudLabelsAreAddressable(t *testing.T) {
	if err := validateCloudLabels(EASMCloudLabels); err != nil {
		t.Fatal(err)
	}
	for name, label := range EASMCloudLabels {
		if name != strings.ToLower(name) || label.Display != strings.TrimSpace(label.Display) {
			t.Fatalf("label %q = %+v is not addressable", name, label)
		}
	}
}

// A name the table does not know is shown as the source spelled it. Guessing a
// category would file a vendor under a kind nobody verified.
func TestUnmappedCloudNamesStayUncategorised(t *testing.T) {
	if got := CloudLabelFor("  Some New CDN "); got.Display != "" || got.Category != CloudCategoryUnknown {
		t.Fatalf("label = %+v", got)
	}
	// The source spells one provider several ways; the lookup folds them.
	for _, name := range []string{"BaiShanCloud", "baishancloud", " BAISHAN CLOUD "} {
		if got := CloudLabelFor(name); got.Display != "白山云" || got.Category != CloudCategoryCDN {
			t.Fatalf("%q: label = %+v", name, got)
		}
	}
	// Two FOFA values can share a vendor but not a meaning.
	compute, mail := CloudLabelFor("Tencent"), CloudLabelFor("Tencent Mail")
	if compute.Category != CloudCategoryCompute || mail.Category != CloudCategoryMail || compute.Display == mail.Display {
		t.Fatalf("tencent = %+v/%+v", compute, mail)
	}
}

// Vendor names leak into product buckets as their own labels. A Latin name of
// three characters or fewer also occurs inside real product names, so it stays
// out of the set.
func TestCloudVendorNamesSkipAmbiguousShortLabels(t *testing.T) {
	names := CloudVendorNames()
	for _, want := range []string{"白山云", "金山云", "阿里云", "cloudfront"} {
		if !names[want] {
			t.Fatalf("%q is not recognised as a vendor label", want)
		}
	}
	for _, ambiguous := range []string{"360", "aws", "263"} {
		if names[ambiguous] {
			t.Fatalf("%q would match real product names", ambiguous)
		}
	}
}

// The category is the only part the renderer words; a value outside the
// published set reaches it as a blank cell.
func TestCloudLabelsUsePublishedCategories(t *testing.T) {
	known := make(map[string]bool, len(EASMCloudCategories))
	for _, category := range EASMCloudCategories {
		known[category] = true
	}
	for name, label := range EASMCloudLabels {
		if !known[label.Category] {
			t.Fatalf("%s: unpublished category %q", name, label.Category)
		}
	}
}

// A table that cannot be used as a lookup must fail the settings check instead
// of publishing blank cells.
func TestCloudLabelTableRefusesUnusableEntries(t *testing.T) {
	for name, labels := range map[string]map[string]CloudLabel{
		"uppercase key":    {"AWS": {"AWS", CloudCategoryCompute}},
		"empty display":    {"aws": {"", CloudCategoryCompute}},
		"unknown category": {"aws": {"AWS", "iaas"}},
	} {
		if err := validateCloudLabels(labels); err == nil {
			t.Fatalf("%s was accepted", name)
		}
	}
}

// The portrait's own limits keep the table and each row bounded.
func TestEASMCloudLimitsHaveDefaultsAndBounds(t *testing.T) {
	settings := TaskPanelConfig{}.WithDefaults()
	if settings.EASM.MaxClouds != 5 || settings.EASM.MaxServices != 3 {
		t.Fatalf("defaults = %+v", settings.EASM)
	}
	if err := settings.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]EASMPanelConfig{
		"no rows":          {MaxDomains: 20, MaxComponents: 20, MaxServices: 3},
		"too many rows":    {MaxDomains: 20, MaxComponents: 20, MaxClouds: 21, MaxServices: 3},
		"no services":      {MaxDomains: 20, MaxComponents: 20, MaxClouds: 5},
		"too many per row": {MaxDomains: 20, MaxComponents: 20, MaxClouds: 5, MaxServices: 11},
	} {
		if err := c.validate(); err == nil {
			t.Fatalf("%s was accepted", name)
		}
	}
}
