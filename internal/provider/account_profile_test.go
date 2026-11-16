package provider

import (
	"testing"

	"github.com/LubyRuffy/eino-swarm/internal/config"
)

func TestOfficialProfileStaysLockedUntilLocalDebug(t *testing.T) {
	cfg := config.Default()
	cfg.Models.Default = "own"
	cfg.Models.DefaultSource = config.DefaultSourceProvider
	cfg.Models.Providers = []config.Provider{{
		ID: "own", Label: "Own", BaseURL: "http://127.0.0.1:1/v1", Model: "own-model",
	}}
	pool := New(cfg)
	src, err := NewAccountProfile(config.AccountSourcePrefix+"0123456789abcdef0123456789abcdef", "http://127.0.0.1:9/v1", "token", AccountProfile{
		Label: "Partner model", Instruction: "added guidance", Tools: []string{"read"}, RestrictTools: true, ContextWindow: 1000,
	})
	if err != nil {
		t.Fatal(err)
	}
	pool.SetAccountSource(src)
	if !pool.OfficialOnly() || pool.DefaultProviderID() != src.ID() {
		t.Fatalf("official default = %s only=%v", pool.DefaultProviderID(), pool.OfficialOnly())
	}
	rows := pool.List()
	if len(rows) != 1 || rows[0].Model != config.OfficialModelAlias || rows[0].Label != "Partner model" || !rows[0].DefaultFollowsSetting {
		t.Fatalf("official rows: %+v", rows)
	}
	resolved, err := pool.ResolveModel("own", "own-model")
	if err != nil || resolved.ID != src.ID() || resolved.Model != config.OfficialModelAlias {
		t.Fatalf("resolve = %+v %v", resolved, err)
	}
	if note := pool.AccountInstruction("own"); note != "added guidance" {
		t.Fatalf("official instruction = %q", note)
	}
	restrict, names := pool.AccountToolPolicy("own")
	if !restrict || len(names) != 1 || names[0] != "read" {
		t.Fatalf("policy = %v %v", restrict, names)
	}

	cfg.Models.AccountPresentation = config.AccountPresentationFull
	if !pool.OfficialOnly() || len(pool.List()) != 1 {
		t.Fatal("presentation unlocked the preset")
	}
	cfg.Product.Mode = config.ProductModeLocal
	if pool.OfficialOnly() || pool.DefaultProviderID() != "own" {
		t.Fatal("LOCAL did not restore own providers")
	}
	own, err := pool.ResolveModel("own", "own-model")
	if err != nil || own.Model != "own-model" {
		t.Fatalf("LOCAL=%+v %v", own, err)
	}
	for _, row := range pool.List() {
		if row.Source == SourceAccount {
			t.Fatal("LOCAL exposed gateway")
		}
	}

}

func TestProfileRefreshNoticesAnInstructionChange(t *testing.T) {
	first, err := NewAccountProfile(config.AccountSourcePrefix+"0123456789abcdef0123456789abcdef", "http://127.0.0.1:9/v1", "token", AccountProfile{Label: "Partner model", Instruction: "one"})
	if err != nil {
		t.Fatal(err)
	}
	same, err := NewAccountProfile(config.AccountSourcePrefix+"0123456789abcdef0123456789abcdef", "http://127.0.0.1:9/v1", "token", AccountProfile{Label: "Partner model", Instruction: "one"})
	if err != nil {
		t.Fatal(err)
	}
	changed, err := NewAccountProfile(config.AccountSourcePrefix+"0123456789abcdef0123456789abcdef", "http://127.0.0.1:9/v1", "token", AccountProfile{Label: "Partner model", Instruction: "two", Tools: []string{"exec"}, RestrictTools: true})
	if err != nil {
		t.Fatal(err)
	}
	if !first.equal(same) || first.equal(changed) {
		t.Fatal("profile equality did not follow the agreement")
	}
	if !first.Profiled() || first.Instruction() != "one" {
		t.Fatal("the stored agreement was not readable")
	}
	trace, ok := first.profileTrace()
	if !ok || trace.RestrictTools || len(trace.Tools) != 0 {
		t.Fatalf("an absent allowlist was reported: %+v %v", trace, ok)
	}
	left, err := NewAccountProfile(first.ID(), "http://127.0.0.1:9/v1", "token", AccountProfile{Label: "Partner model", Instruction: "one", Tools: []string{"read", "exec"}, RestrictTools: true})
	if err != nil {
		t.Fatal(err)
	}
	right, err := NewAccountProfile(first.ID(), "http://127.0.0.1:9/v1", "token", AccountProfile{Label: "Partner model", Instruction: "one", Tools: []string{"read", "write"}, RestrictTools: true})
	if err != nil {
		t.Fatal(err)
	}
	if left.sameProfile(right) {
		t.Fatal("a different allowlist compared equal")
	}
	plain, err := NewAccountSource(first.ID(), "Partner model", "http://127.0.0.1:9/v1", "token", Catalog{Names: []string{config.OfficialModelAlias}})
	if err != nil {
		t.Fatal(err)
	}
	if plain.Profiled() || plain.Instruction() != "" || plain.sameProfile(first) || !plain.sameProfile(plain) {
		t.Fatal("an ordinary catalog grew an official agreement")
	}
	if restrict, names := plain.ToolAllow(); restrict || names != nil {
		t.Fatalf("ordinary allowlist = %v %v", restrict, names)
	}
	if restrict, names := first.ToolAllow(); restrict || names != nil {
		t.Fatalf("absent allowlist = %v %v", restrict, names)
	}
	var none *AccountSource
	if none.Profiled() || none.Instruction() != "" {
		t.Fatal("a missing source answered")
	}
	if restrict, names := none.ToolAllow(); restrict || names != nil {
		t.Fatalf("nil allowlist = %v %v", restrict, names)
	}
	if _, ok := none.profileTrace(); ok {
		t.Fatal("a missing source produced a trace")
	}
	if _, err := NewAccountProfile("provider", "http://127.0.0.1:9/v1", "token", AccountProfile{Label: "Partner model"}); err == nil {
		t.Fatal("a profile accepted an ordinary provider id")
	}
	var pool *Pool
	if pool.AccountInstruction("own") != "" {
		t.Fatal("a missing pool returned guidance")
	}
	if New(config.Default()).AccountInstruction("") != "" {
		t.Fatal("a pool without an account returned guidance")
	}
	if restrict, names := pool.AccountToolPolicy("own"); restrict || names != nil {
		t.Fatalf("nil policy = %v %v", restrict, names)
	}
}
