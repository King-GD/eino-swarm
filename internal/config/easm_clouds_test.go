package config

import (
	"strings"
	"testing"
)

// The table is code, not user configuration, so its shape is checked here
// rather than when settings load: an uppercase or untrimmed key would never
// match, an empty display name would show a blank provider, and a category
// outside the published set would reach the renderer as a blank cell.
func TestCloudLabelsAreAddressable(t *testing.T) {
	known := make(map[string]bool, len(EASMCloudCategories))
	for _, category := range EASMCloudCategories {
		known[category] = true
	}
	for name, label := range EASMCloudLabels {
		if name == "" || name != strings.ToLower(strings.TrimSpace(name)) {
			t.Errorf("key %q must be trimmed lowercase", name)
		}
		if label.Display == "" || label.Display != strings.TrimSpace(label.Display) {
			t.Errorf("%s: display name %q must be nonempty and trimmed", name, label.Display)
		}
		if !known[label.Category] {
			t.Errorf("%s: unpublished category %q", name, label.Category)
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

// The portrait's own limits keep the table and each row bounded.
func TestEASMCloudLimitsHaveDefaultsAndBounds(t *testing.T) {
	settings := TaskPanelConfig{}.WithDefaults()
	if settings.EASM.MaxClouds != 5 || settings.EASM.MaxServices != 5 {
		t.Fatalf("defaults = %+v", settings.EASM)
	}
	if err := settings.Validate(); err != nil {
		t.Fatal(err)
	}
	settings.EASM.MaxClouds = 6
	if err := settings.WithDefaults().Validate(); err == nil {
		t.Fatal("an out-of-range provider limit was accepted after applying defaults")
	}
	for name, c := range map[string]EASMPanelConfig{
		"no rows":          {MaxDomains: 20, MaxComponents: 20, MaxServices: 3},
		"no services":      {MaxDomains: 20, MaxComponents: 20, MaxClouds: 5},
		"too many per row": {MaxDomains: 20, MaxComponents: 20, MaxClouds: 5, MaxServices: 11},
	} {
		if err := c.validate(); err == nil {
			t.Fatalf("%s was accepted", name)
		}
	}
}
