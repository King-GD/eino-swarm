package provider

import (
	"errors"
	"github.com/LubyRuffy/eino-swarm/internal/config"
	"testing"
)

func TestRoleBindingsFollowPresetUntilExplicitlyOverridden(t *testing.T) {
	cfg := config.Default()
	p := New(cfg)
	if policy := p.ModelPolicy(); policy.Ready || policy.Configurable {
		t.Fatalf("unsigned policy=%+v", policy)
	}
	src := accountSourceFor(t, "https://one.invalid/v1", "private")
	defaults := map[string]string{"conversation": "server-model", "title": "server-model-2"}
	src.SetModelPolicy(true, defaults)
	defaults["title"] = "mutated"
	p.SetAccountSource(src)
	policy := p.ModelPolicy()
	if !policy.Configurable || !policy.Ready || policy.Scope == "" {
		t.Fatalf("policy=%+v", policy)
	}
	ref, ok := p.RoleSelection("title", "own", "fallback")
	if !ok || ref.Model != "server-model-2" {
		t.Fatalf("preset=%+v %v", ref, ok)
	}
	cfg.Models.Bindings = map[string]map[string]config.ModelRef{policy.Scope: {"title": {ProviderID: cfg.Models.Default, Model: "custom"}, "conversation": {ProviderID: src.id, Model: "server-model-2"}}}
	ref, ok = p.RoleSelection("title", "own", "fallback")
	if !ok || ref.Model != "custom" {
		t.Fatalf("override=%+v", ref)
	}
	if name, follow, ok := p.ConversationDefault(src.id); !ok || !follow || name != "server-model-2" {
		t.Fatal("conversation override ignored")
	}
	target, configured, err := p.RoleProvider("title")
	if err != nil || !configured || target.Model != "custom" {
		t.Fatalf("target=%+v %v", target, err)
	}
	other := accountSourceFor(t, "https://two.invalid/v1", "private")
	other.SetModelPolicy(true, map[string]string{"title": "server-model"})
	p.SetAccountSource(other)
	if p.ModelPolicy().Scope == policy.Scope {
		t.Fatal("origin reused account bindings")
	}
	ref, _ = p.RoleSelection("title", "own", "fallback")
	if ref.Model != "server-model" {
		t.Fatal("other origin inherited override")
	}
	cfg.Product.Mode = config.ProductModeLocal
	cfg.Models.Providers[0].Model = "personal"
	if ref, configured := p.RoleSelection("title", "own", "fallback"); configured || ref.Model != "fallback" {
		t.Fatal("LOCAL inherited preset")
	}
	prov, err := p.ResolveModel(src.id, "server-model")
	if err != nil || prov.ID != src.id || prov.Model != "server-model" || prov.APIKey != "private" || prov.BaseURL != "https://two.invalid/v1" {
		t.Fatalf("LOCAL dropped the signed-in model: %+v %v", prov, err)
	}
	if err := p.validateModelSelection(src.id, "server-model"); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := p.RoleProvider("ocr"); ok || err != nil {
		t.Fatal("unconfigured OCR should stay optional")
	}
}

func TestManagedRoleCallsNeverResolvePersonalProviders(t *testing.T) {
	cfg := config.Default()
	p := New(cfg)
	src := accountSourceFor(t, "https://one.invalid/v1", "private")
	src.SetRoles([]AccountRole{{ID: "conversation", Enabled: true}, {ID: "title", Enabled: false}, {ID: "embedding", Enabled: true}, {ID: "speech", Enabled: true, Label: "Voice"}})
	p.SetAccountSource(src)
	for role, want := range map[string]string{"conversation": "conversation", "title": "conversation", "compact": "conversation", "embedding": "embedding", "ocr": ""} {
		got, configured := p.RoleSelection(role, "own", "personal")
		if !configured || got.Model != want {
			t.Fatalf("%s=%+v", role, got)
		}
	}
	if target, on, err := p.RoleProvider("speech"); err != nil || !on || target.Model != "speech" || target.Label != "Voice" {
		t.Fatalf("speech=%+v %v", target, err)
	}
	if _, on, err := p.RoleProvider("ocr"); !on || !errors.Is(err, ErrModelPolicy) {
		t.Fatal("disabled role did not refuse")
	}
	if err := p.validateModelSelection("own", "personal"); !errors.Is(err, ErrModelPolicy) {
		t.Fatal("personal selection allowed")
	}
	if err := p.validateModelSelection(src.id, "not-in-preset"); !errors.Is(err, ErrModelPolicy) {
		t.Fatal("gateway selection escaped preset")
	}
	if err := p.validateModelSelection(src.id, "embedding"); err != nil {
		t.Fatal(err)
	}
	if _, err := p.ResolveModel(src.id, "not-in-preset"); !errors.Is(err, ErrModelPolicy) {
		t.Fatal("invalid model resolved")
	}
	src.SetModelPolicy(false, map[string]string{"conversation": "chosen", "title": "namer"})
	p.SetAccountSource(src)
	if ref, _ := p.RoleSelection("title", "own", "personal"); ref.Model != "chosen" {
		t.Fatal("disabled title ignored conversation fallback")
	}
	src.SetRoles(nil)
	src.SetModelPolicy(false, map[string]string{"conversation": "chosen", "title": "namer"})
	if ref, _ := p.RoleSelection("title", "own", "personal"); ref.Model != "namer" {
		t.Fatal("explicit preset not used")
	}
	if p.validateModelSelection(src.id, "namer") != nil {
		t.Fatal("preset name refused")
	}
}

func TestModelPolicySeparatesOwnersAndValidatesExplicitClientChoices(t *testing.T) {
	cfg := config.Default()
	p := New(cfg)
	src := accountSourceFor(t, "https://gateway.invalid/v1", "private")
	src.SetAccountOwner("first")
	src.SetModelPolicy(true, map[string]string{"title": "inherited"})
	p.SetAccountSource(src)
	first := p.ModelPolicy().Scope
	cfg.Models.Bindings = map[string]map[string]config.ModelRef{first: {"title": {ProviderID: cfg.Models.Default, Model: "chosen"}}}
	next := accountSourceFor(t, "https://gateway.invalid/v1", "renewed")
	next.SetAccountOwner("first")
	next.SetModelPolicy(true, map[string]string{"title": "updated"})
	p.SetAccountSource(next)
	if p.ModelPolicy().Scope != first {
		t.Fatal("token renewal lost overrides")
	}
	if ref, _ := p.RoleSelection("title", "", ""); ref.Model != "chosen" {
		t.Fatal("server refresh overwrote explicit choice")
	}
	delete(cfg.Models.Bindings[first], "title")
	if ref, _ := p.RoleSelection("title", "", ""); ref.Model != "updated" {
		t.Fatal("restoring default retained old value")
	}
	next = accountSourceFor(t, "https://gateway.invalid/v1", "private")
	next.SetAccountOwner("other")
	p.SetAccountSource(next)
	if p.ModelPolicy().Scope == first {
		t.Fatal("different account reused overrides")
	}
	next.SetModelPolicy(false, map[string]string{"conversation": "selected", "title": "small"})
	p.SetAccountSource(next)
	for _, ref := range []config.ModelRef{{}, {ProviderID: next.id, Model: "selected"}} {
		if err := p.ValidateConversationSelection(ref.ProviderID, ref.Model); err != nil {
			t.Fatal(err)
		}
	}
	for _, ref := range []config.ModelRef{{ProviderID: cfg.Models.Default, Model: "own"}, {ProviderID: next.id, Model: "small"}} {
		if !errors.Is(p.ValidateConversationSelection(ref.ProviderID, ref.Model), ErrModelPolicy) {
			t.Fatal("client escaped conversation preset")
		}
	}
	if ref, _ := p.RoleSelection("speech", "own", "voice"); ref.Model != "" {
		t.Fatal("missing managed role borrowed a fallback")
	}
	if ref, _ := p.RoleSelection("title", "own", "voice"); ref.Model != "small" {
		t.Fatal(ref)
	}
	if !next.permits("server-model") || next.permits("unknown") {
		t.Fatal("catalog permission check")
	}
	invalid := accountSourceFor(t, ":bad", "private")
	p.SetAccountSource(invalid)
	if p.ModelPolicy().Ready {
		t.Fatal("malformed endpoint published ready")
	}
}

func TestStoredConversationUsesThePresetWhenItsModelLeft(t *testing.T) {
	cfg := config.Default()
	p := New(cfg)
	src := accountSourceFor(t, "https://gateway.invalid/v1", "private", "preset-model")
	src.SetModelPolicy(false, map[string]string{"conversation": "preset-model"})
	p.SetAccountSource(src)
	for _, saved := range []config.ModelRef{{ProviderID: src.id, Model: "catalog-model"}, {ProviderID: cfg.Models.Default, Model: "own-model"}} {
		prov, err := p.StoredConversationModel(saved.ProviderID, saved.Model)
		if err != nil || prov.ID != src.id || prov.Model != "preset-model" {
			t.Fatalf("saved %+v executed as %+v %v", saved, prov, err)
		}
	}
	if _, err := p.ResolveModel(src.id, "catalog-model"); !errors.Is(err, ErrModelPolicy) {
		t.Fatal("explicit name outside the preset resolved")
	}
	if err := p.ValidateConversationSelection(cfg.Models.Default, "own-model"); !errors.Is(err, ErrModelPolicy) {
		t.Fatal("explicit personal selection was accepted")
	}
}

func TestManagedPresetCannotBeUnlockedByPresentation(t *testing.T) {
	cfg := config.Default()
	cfg.Models.AccountPresentation = config.AccountPresentationFull
	p := New(cfg)
	src, err := NewAccountProfile(accountTestID, "https://gateway.invalid/v1", "private", AccountProfile{Label: "Preset"})
	if err != nil {
		t.Fatal(err)
	}
	p.SetAccountSource(src)
	if !p.OfficialOnly() {
		t.Fatal("local presentation unlocked a server-managed preset")
	}
	if rows := p.List(); len(rows) != 1 || rows[0].Source != SourceAccount {
		t.Fatalf("other models exposed: %+v", rows)
	}
}

func TestLocalModeSwitchesAuthorizedServerCatalogs(t *testing.T) {
	cfg := config.Default()
	cfg.Product.Mode = config.ProductModeLocal
	cfg.Models.Providers[0].Model = "personal"
	p := New(cfg)
	primary := accountSourceFor(t, "https://gateway.invalid/client/desktop/v1", "still-signed-in", "conversation")
	primary.SetRoles([]AccountRole{{ID: "conversation", Enabled: true, Label: "对话"}})
	primary.SetModelPolicy(false, nil)
	p.SetAccountSource(primary)
	preset, err := NewAccountSource(primary.ID(), "Partner", "https://gateway.invalid/client/desktop/v1", "still-signed-in", Catalog{
		Names: []string{"conversation", "title", "embedding"},
		Types: map[string]string{"conversation": "chat", "title": "chat", "embedding": "embedding"},
	})
	if err != nil {
		t.Fatal(err)
	}
	const gatewayID = config.AccountSourcePrefix + "abcdefabcdefabcdefabcdefabcdefab"
	gateway, err := NewAccountSource(gatewayID, "服务器", "https://gateway.invalid/client/models/v1/gateway", "still-signed-in", Catalog{
		Names: []string{"conversation", "original"},
		Types: map[string]string{"conversation": "chat", "original": "chat"},
	})
	if err != nil {
		t.Fatal(err)
	}
	p.SetLocalAccountSources([]*AccountSource{preset, gateway})

	got := map[string]string{}
	personal := 0
	for _, row := range p.List() {
		if row.Model == "embedding" {
			t.Fatal("non-conversation catalog row was selectable")
		}
		if row.Source == SourceAccount {
			got[row.ProviderID+"/"+row.Model] = row.ProviderLabel
			continue
		}
		personal++
	}
	if personal == 0 || got[primary.ID()+"/conversation"] != "Partner" || got[primary.ID()+"/title"] != "Partner" || got[gatewayID+"/conversation"] != "服务器" || got[gatewayID+"/original"] != "服务器" {
		t.Fatalf("LOCAL list personal=%d rows=%v", personal, got)
	}
	chosen, err := p.ResolveModel(gatewayID, "original")
	if err != nil || chosen.Model != "original" || chosen.APIKey != "still-signed-in" || chosen.BaseURL != "https://gateway.invalid/client/models/v1/gateway" {
		t.Fatalf("original catalog: %+v %v", chosen, err)
	}
	sameName, err := p.ResolveModel(primary.ID(), "conversation")
	if err != nil || sameName.BaseURL != "https://gateway.invalid/client/desktop/v1" || sameName.APIKey != "still-signed-in" {
		t.Fatalf("preset catalog merged with the original: %+v %v", sameName, err)
	}
	if _, err := p.ResolveModel(config.AccountSourcePrefix+"00000000000000000000000000000000", "original"); !errors.Is(err, ErrAccountLoginRequired) {
		t.Fatalf("unknown account source: %v", err)
	}
	cfg.Product.Mode = ""
	for _, row := range p.List() {
		if row.Model == "original" || row.Model == "title" {
			t.Fatalf("gateway mode listed a LOCAL catalog: %+v", row)
		}
	}
	p.SetAccountSource(nil)
	if _, err := p.ResolveModel(primary.ID(), "conversation"); !errors.Is(err, ErrAccountLoginRequired) {
		t.Fatalf("sign-out kept the catalog: %v", err)
	}
}

func TestLocalModeKeepsASignedInCatalogBesidePersonalModels(t *testing.T) {
	cfg := config.Default()
	cfg.Product.Mode = config.ProductModeLocal
	cfg.Models.Providers[0].Model = "personal"
	p := New(cfg)
	p.SetAccountSource(accountSourceFor(t, "https://gateway.invalid/v1", "private"))
	var account, personal int
	for _, row := range p.List() {
		if row.Source == SourceAccount {
			account++
			continue
		}
		personal++
	}
	if account == 0 || personal == 0 {
		t.Fatalf("account=%d personal=%d", account, personal)
	}
	if p.DefaultProviderID() != cfg.Models.Default {
		t.Fatal("LOCAL changed the default provider")
	}
}
