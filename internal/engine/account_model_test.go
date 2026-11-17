package engine

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LubyRuffy/eino-swarm/internal/config"
	"github.com/LubyRuffy/eino-swarm/internal/provider"
	"github.com/LubyRuffy/eino-swarm/internal/store"
)

const accountCatalogID = config.AccountSourcePrefix + "0123456789abcdef0123456789abcdef"

func TestCatalogReorderDoesNotChangeTheModelAConversationShows(t *testing.T) {
	var mu sync.Mutex
	var requested []string
	srv := httptest.NewServer(recordCompletion(&mu, &requested))
	defer srv.Close()

	e := newAccountEngine(t, srv.URL+"/v1", "first-model", "second-model")
	th, err := e.CreateThread("", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if th.Model != "first-model" {
		t.Fatalf("new conversation remembered %q, want the name it was created with", th.Model)
	}
	e.pool.SetAccountSource(accountCatalog(t, srv.URL+"/v1", "second-model", "first-model"))
	if _, err := e.StartTurn(th.ID, "Continue the current task."); err != nil {
		t.Fatal(err)
	}
	got := waitModels(t, &mu, &requested)
	for _, name := range got {
		if name == "second-model" {
			t.Fatalf("catalog reorder changed the request to %q; calls %v", name, got)
		}
	}
	if len(got) == 0 || got[0] != "first-model" {
		t.Fatalf("request models %v, want first-model", got)
	}
}

func TestShownModelIsWhatAnUnpinnedAccountConversationSends(t *testing.T) {
	var mu sync.Mutex
	var requested []string
	srv := httptest.NewServer(recordCompletion(&mu, &requested))
	defer srv.Close()

	e := newAccountEngine(t, srv.URL+"/v1", "catalog-first", "shown-model")
	th, err := e.CreateThread("", accountCatalogID, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.UpdateThread(th.ID, map[string]any{"model": ""}); err != nil {
		t.Fatal(err)
	}
	e.pool.SetAccountSource(accountCatalog(t, srv.URL+"/v1", "catalog-first", "shown-model"))
	if _, err := e.StartTurnInput(th.ID, UserInput{Text: "Continue the current task.", ComposerModel: "shown-model"}); err != nil {
		t.Fatal(err)
	}
	got := waitModels(t, &mu, &requested)
	for _, name := range got {
		if name == "catalog-first" {
			t.Fatalf("empty conversation followed the catalog instead of the shown model: %v", got)
		}
	}
	if len(got) == 0 || got[0] != "shown-model" {
		t.Fatalf("request models %v, want shown-model", got)
	}
	stored, err := e.store.GetThread(th.ID)
	if err != nil || stored.Model != "shown-model" {
		t.Fatalf("conversation model %q err=%v", stored.Model, err)
	}
}

func TestAccountSettingStillChangesConversationsThatHaveNotChosen(t *testing.T) {
	var mu sync.Mutex
	var requested []string
	srv := httptest.NewServer(recordCompletion(&mu, &requested))
	defer srv.Close()

	e := newAccountEngine(t, srv.URL+"/v1", "first-model", "saved-model", "other-model")
	if err := e.cfg.Update(func(c *config.Config) error {
		c.Models.AccountModel = "saved-model"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	th, err := e.CreateThread("", accountCatalogID, "")
	if err != nil {
		t.Fatal(err)
	}
	if th.Model != "" {
		t.Fatalf("a configured account default was stored on the conversation: %q", th.Model)
	}
	first, err := e.StartTurnInput(th.ID, UserInput{Text: "Continue the current task.", ComposerModel: "first-model"})
	if err != nil {
		t.Fatal(err)
	}
	waitTurn(t, e, first.ID)
	before := snapshotModels(&mu, &requested)
	if len(before) == 0 || before[0] != "saved-model" {
		t.Fatalf("configured default did not reach the request: %v", before)
	}
	for _, name := range before {
		if name != "saved-model" {
			t.Fatalf("composer name replaced the configured default: %v", before)
		}
	}
	if err := e.cfg.Update(func(c *config.Config) error {
		c.Models.AccountModel = "other-model"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	second, err := e.StartTurn(th.ID, "Continue the current task.")
	if err != nil {
		t.Fatal(err)
	}
	waitTurn(t, e, second.ID)
	got := snapshotModels(&mu, &requested)
	if len(got) <= len(before) || got[len(got)-1] != "other-model" {
		t.Fatalf("a later settings change did not reach the next turn: before %v after %v", before, got)
	}
	stored, err := e.store.GetThread(th.ID)
	if err != nil || stored.Model != "" {
		t.Fatalf("conversation stored %q; a settings default must stay unpinned", stored.Model)
	}
}

func TestMissingAccountSettingIsRequestedByName(t *testing.T) {
	var mu sync.Mutex
	var requested []string
	srv := httptest.NewServer(recordCompletion(&mu, &requested))
	defer srv.Close()

	e := newAccountEngine(t, srv.URL+"/v1", "first-model", "second-model")
	if err := e.cfg.Update(func(c *config.Config) error {
		c.Models.AccountModel = "removed-model"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	th, err := e.CreateThread("", accountCatalogID, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.StartTurn(th.ID, "Continue the current task."); err != nil {
		t.Fatal(err)
	}
	got := waitModels(t, &mu, &requested)
	for _, name := range got {
		if name == "first-model" || name == "second-model" {
			t.Fatalf("a removed account default was replaced: %v", got)
		}
	}
	if len(got) == 0 || got[0] != "removed-model" {
		t.Fatalf("request models %v, want removed-model", got)
	}
}

func TestLocalModeKeepsASignedInGatewayConversation(t *testing.T) {
	var mu sync.Mutex
	var requested []string
	var auth []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		mu.Lock()
		auth = append(auth, authHeader)
		mu.Unlock()
		recordCompletion(&mu, &requested).ServeHTTP(w, r)
	}))
	defer srv.Close()

	e := newAccountEngine(t, srv.URL+"/v1", "conversation")
	personal := e.cfg.Models.Default
	if err := e.cfg.Update(func(c *config.Config) error {
		for i := range c.Models.Providers {
			if c.Models.Providers[i].ID != personal {
				continue
			}
			c.Models.Providers[i].BaseURL = srv.URL + "/v1"
			c.Models.Providers[i].APIKey = "personal-secret"
			c.Models.Providers[i].Model = "personal-model"
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	th, err := e.CreateThread("retained", accountCatalogID, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.UpdateThread(th.ID, map[string]any{"model": "conversation"}); err != nil {
		t.Fatal(err)
	}
	if err := e.cfg.Update(func(c *config.Config) error {
		c.Product.Mode = config.ProductModeLocal
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	turn, err := e.StartTurn(th.ID, "Continue the current task.")
	if err != nil {
		t.Fatal(err)
	}
	waitTurn(t, e, turn.ID)
	if turn.ProviderID != accountCatalogID || turn.Model != "conversation" {
		t.Fatalf("turn used %+v", turn)
	}
	stored, err := e.store.GetThread(th.ID)
	if err != nil || stored.ProviderID != accountCatalogID || stored.Model != "conversation" {
		t.Fatalf("LOCAL rewrote the retained account choice: %+v %v", stored, err)
	}
	mu.Lock()
	gotModel, gotAuth := append([]string(nil), requested...), append([]string(nil), auth...)
	mu.Unlock()
	if len(gotModel) == 0 || gotModel[0] != "conversation" || gotAuth[0] != "Bearer token" {
		t.Fatalf("request model %v auth %v", gotModel, gotAuth)
	}
	for _, header := range gotAuth {
		if header == "Bearer personal-secret" {
			t.Fatal("LOCAL sent the personal credential")
		}
	}

	if err := e.cfg.Update(func(c *config.Config) error {
		c.Product.Mode = ""
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	again, err := e.StartTurn(th.ID, "Continue the current task.")
	if err != nil {
		t.Fatal(err)
	}
	waitTurn(t, e, again.ID)
	if again.ProviderID != accountCatalogID || again.Model != "conversation" {
		t.Fatalf("gateway return used %+v", again)
	}
}

func TestManagedPresetRunsHistoricalConversationsOnItsModel(t *testing.T) {
	var mu sync.Mutex
	var requested []string
	srv := httptest.NewServer(recordCompletion(&mu, &requested))
	defer srv.Close()

	e := newAccountEngine(t, srv.URL+"/v1", "catalog-model", "preset-model")
	personal := e.cfg.Models.Default
	if err := e.cfg.Update(func(c *config.Config) error {
		for i := range c.Models.Providers {
			if c.Models.Providers[i].ID != personal {
				continue
			}
			c.Models.Providers[i].BaseURL = srv.URL + "/v1"
			c.Models.Providers[i].APIKey = "personal-secret"
			c.Models.Providers[i].Model = "personal-model"
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	catalogThread, err := e.CreateThread("catalog", accountCatalogID, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.UpdateThread(catalogThread.ID, map[string]any{"model": "catalog-model"}); err != nil {
		t.Fatal(err)
	}
	personalThread, err := e.CreateThread("personal", personal, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.UpdateThread(personalThread.ID, map[string]any{"model": "kept-personal"}); err != nil {
		t.Fatal(err)
	}

	managed, err := provider.NewAccountSource(accountCatalogID, "Partner", srv.URL+"/v1", "token", provider.Catalog{
		Names: []string{"preset-model"},
		Types: map[string]string{"preset-model": "chat"},
	})
	if err != nil {
		t.Fatal(err)
	}
	managed.SetModelPolicy(false, map[string]string{"conversation": "preset-model"})
	e.pool.SetAccountSource(managed)
	if err := e.SetThreadProvider(catalogThread.ID, accountCatalogID, "catalog-model"); !errors.Is(err, provider.ErrModelPolicy) {
		t.Fatalf("explicit historical selection was accepted: %v", err)
	}

	for _, id := range []string{catalogThread.ID, personalThread.ID} {
		turn, err := e.StartTurn(id, "Continue the current task.")
		if err != nil {
			t.Fatal(err)
		}
		waitTurn(t, e, turn.ID)
		if turn.ProviderID != accountCatalogID || turn.Model != "preset-model" {
			t.Fatalf("turn used %+v", turn)
		}
	}
	got := snapshotModels(&mu, &requested)
	if len(got) < 2 || !onlyModels(got, "preset-model") {
		t.Fatalf("requests %v, want the preset model", got)
	}
	storedCatalog, err := e.store.GetThread(catalogThread.ID)
	if err != nil || storedCatalog.ProviderID != accountCatalogID || storedCatalog.Model != "catalog-model" {
		t.Fatalf("catalog binding rewritten: %+v %v", storedCatalog, err)
	}
	storedPersonal, err := e.store.GetThread(personalThread.ID)
	if err != nil || storedPersonal.ProviderID != personal || storedPersonal.Model != "kept-personal" {
		t.Fatalf("personal binding rewritten: %+v %v", storedPersonal, err)
	}

	localCatalog := accountCatalog(t, srv.URL+"/v1", "catalog-model", "preset-model")
	e.pool.SetLocalAccountSources([]*provider.AccountSource{localCatalog})
	if err := e.cfg.Update(func(c *config.Config) error {
		c.Product.Mode = config.ProductModeLocal
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	before := len(snapshotModels(&mu, &requested))
	restored, err := e.StartTurn(catalogThread.ID, "Continue the current task.")
	if err != nil {
		t.Fatal(err)
	}
	waitTurn(t, e, restored.ID)
	personalAgain, err := e.StartTurn(personalThread.ID, "Continue the current task.")
	if err != nil {
		t.Fatal(err)
	}
	waitTurn(t, e, personalAgain.ID)
	after := snapshotModels(&mu, &requested)
	if !restoredModels(after[before:], "catalog-model", "kept-personal") {
		t.Fatalf("LOCAL did not restore the stored models: %v", after[before:])
	}
}

// onlyModels reports whether every recorded completion used name.
func onlyModels(got []string, name string) bool {
	if len(got) == 0 {
		return false
	}
	for _, item := range got {
		if item != name {
			return false
		}
	}
	return true
}

// restoredModels reports whether completions use each stored model in order.
// One turn can record more than one completion; a later turn must not reuse
// the previous model.
func restoredModels(got []string, names ...string) bool {
	if len(got) < len(names) || len(names) == 0 {
		return false
	}
	at, seen := 0, 0
	for _, item := range got {
		if item == names[at] {
			seen++
			continue
		}
		if at+1 < len(names) && seen > 0 && item == names[at+1] {
			at++
			seen = 1
			continue
		}
		return false
	}
	return at == len(names)-1 && seen > 0
}

func newAccountEngine(t *testing.T, base string, names ...string) *Engine {
	t.Helper()
	t.Setenv("OPENAI_BASE_URL", "")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("OPENAI_MODEL", "")
	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Update(func(c *config.Config) error {
		c.Models.DefaultSource = config.DefaultSourceAccount
		c.Swarm.AutoTitle = false
		c.Memory.Enabled = false
		c.Swarm.ComposerPredictions = false
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(cfg.DBPath())
	if err != nil {
		t.Fatal(err)
	}
	pool := provider.New(cfg)
	pool.SetAccountSource(accountCatalog(t, base, names...))
	e := New(cfg, st, pool, slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(func() {
		e.Shutdown()
		_ = st.Close()
	})
	return e
}

func accountCatalog(t *testing.T, base string, names ...string) *provider.AccountSource {
	t.Helper()
	src, err := provider.NewAccountSource(accountCatalogID, "Server", base, "token", provider.Catalog{Names: names})
	if err != nil {
		t.Fatal(err)
	}
	return src
}

func recordCompletion(mu *sync.Mutex, requested *[]string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model    string `json:"model"`
			Stream   bool   `json:"stream"`
			Messages []struct {
				Content json.RawMessage `json:"content"`
			} `json:"messages"`
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		mu.Lock()
		*requested = append(*requested, body.Model)
		mu.Unlock()
		content := "Checked."
		if len(body.Messages) > 0 && strings.Contains(string(body.Messages[0].Content), "Schedule answer claims") {
			content = `{"claims":[]}`
		}
		if body.Stream {
			w.Header().Set("Content-Type", "text/event-stream")
			rawContent, _ := json.Marshal(content)
			fmt.Fprintf(w, "data: {\"id\":\"c\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":%s},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n", rawContent)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		rawContent, _ := json.Marshal(content)
		fmt.Fprintf(w, `{"id":"c","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":%s},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`, rawContent)
	})
}

func snapshotModels(mu *sync.Mutex, requested *[]string) []string {
	mu.Lock()
	defer mu.Unlock()
	return append([]string(nil), (*requested)...)
}

func waitTurn(t *testing.T, e *Engine, turnID string) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		got, err := e.store.GetTurn(turnID)
		if err == nil && (got.Status == store.TurnDone || got.Status == store.TurnError) && !e.Status(got.ThreadID).Running {
			if got.Status == store.TurnError {
				t.Fatalf("turn failed: %s", got.Error)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	got, err := e.store.GetTurn(turnID)
	if got == nil {
		t.Fatalf("turn did not finish: %v", err)
	}
	t.Fatalf("turn did not finish: status=%s running=%v err=%s", got.Status, e.Status(got.ThreadID).Running, got.Error)
}

func waitModels(t *testing.T, mu *sync.Mutex, requested *[]string) []string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		got := append([]string(nil), (*requested)...)
		mu.Unlock()
		if len(got) > 0 {
			return got
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the conversation made no model request")
	return nil
}
