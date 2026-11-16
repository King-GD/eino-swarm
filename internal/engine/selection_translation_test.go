package engine

import (
	"net/http"
	"testing"

	"github.com/LubyRuffy/eino-swarm/internal/config"
	"github.com/LubyRuffy/eino-swarm/internal/provider"
	"github.com/LubyRuffy/eino-swarm/internal/store"
)

func TestSelectionTranslationUsesAnEnabledTranslationRole(t *testing.T) {
	const source = "Do not restart 2 hosts."
	const translated = "不要重启 2 台主机。"
	requests := make(chan sideChatWireRequest, 2)
	paths := make(chan string, 2)
	e := newSideChatHTTPEngine(t, config.APIModeChatCompletions, func(w http.ResponseWriter, r *http.Request, req sideChatWireRequest) {
		paths <- r.URL.Path
		requests <- req
		writeContextFlowReply(w, false, map[string]any{"content": translated}, "stop")
	})
	src := accountCatalog(t, e.cfg.Models.Providers[0].BaseURL, "conversation")
	src.SetRoles([]provider.AccountRole{
		{ID: "conversation", Enabled: true},
		{ID: "translation", Enabled: false, Label: "翻译"},
	})
	e.pool.SetAccountSource(src)
	thread, err := e.CreateThread("translation", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.UpdateThread(thread.ID, map[string]any{"provider_id": accountCatalogID, "model": "conversation"}); err != nil {
		t.Fatal(err)
	}
	if got, err := e.TranslateSelection(t.Context(), thread.ID, source); err != nil || got != translated {
		t.Fatalf("disabled translation role = %q, %v", got, err)
	}
	if got := (<-requests).Model; got != "conversation" {
		t.Fatalf("disabled translation role model = %q", got)
	}
	if got := <-paths; got != "/chat/completions" {
		t.Fatalf("disabled translation role path = %q", got)
	}
	src.SetRoles([]provider.AccountRole{
		{ID: "conversation", Enabled: true},
		{ID: "translation", Enabled: true, Label: "翻译"},
	})
	if got, err := e.TranslateSelection(t.Context(), thread.ID, source); err != nil || got != translated {
		t.Fatalf("enabled translation role = %q, %v", got, err)
	}
	if got := (<-requests).Model; got != "translation" {
		t.Fatalf("enabled translation role model = %q", got)
	}
	if got := <-paths; got != "/translations" {
		t.Fatalf("enabled translation role path = %q", got)
	}
}

func TestEnabledTitleRoleUsesTheExclusiveNamingRoute(t *testing.T) {
	requests := make(chan sideChatWireRequest, 2)
	paths := make(chan string, 2)
	e := newSideChatHTTPEngine(t, config.APIModeChatCompletions, func(w http.ResponseWriter, r *http.Request, req sideChatWireRequest) {
		paths <- r.URL.Path
		requests <- req
		writeContextFlowReply(w, false, map[string]any{"content": "迁移计划"}, "stop")
	})
	src := accountCatalog(t, e.cfg.Models.Providers[0].BaseURL, "conversation")
	src.SetRoles([]provider.AccountRole{
		{ID: "conversation", Enabled: true},
		{ID: "title", Enabled: false, Label: "对话命名"},
	})
	e.pool.SetAccountSource(src)
	thread, err := e.CreateThread("title", "", "")
	if err != nil {
		t.Fatal(err)
	}
	turn := &store.Turn{ThreadID: thread.ID, ProviderID: accountCatalogID, Model: "conversation", Status: store.TurnDone}
	if err := e.store.CreateTurn(turn); err != nil {
		t.Fatal(err)
	}
	e.runTitle(thread.ID, turn, "迁移这批主机", "迁移已经完成。")
	if got := (<-requests).Model; got != "conversation" {
		t.Fatalf("disabled title role model = %q", got)
	}
	if got := <-paths; got != "/chat/completions" {
		t.Fatalf("disabled title role path = %q", got)
	}
	src.SetRoles([]provider.AccountRole{
		{ID: "conversation", Enabled: true},
		{ID: "title", Enabled: true, Label: "对话命名"},
	})
	e.runTitle(thread.ID, turn, "迁移这批主机", "迁移已经完成。")
	if got := (<-requests).Model; got != "title" {
		t.Fatalf("enabled title role model = %q", got)
	}
	if got := <-paths; got != "/naming" {
		t.Fatalf("enabled title role path = %q", got)
	}
}
