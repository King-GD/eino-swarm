package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/LubyRuffy/eino-swarm/internal/config"
	"github.com/cloudwego/eino/schema"
)

const accountTestID = config.AccountSourcePrefix + "0123456789abcdef0123456789abcdef"

func accountSourceFor(t *testing.T, base, token string, names ...string) *AccountSource {
	t.Helper()
	if len(names) == 0 {
		names = []string{"server-model", "server-model-2"}
	}
	src, err := NewAccountSource(accountTestID, "Server", base, token, Catalog{Names: names, Windows: map[string]int{names[0]: 64000}})
	if err != nil {
		t.Fatal(err)
	}
	return src
}

func TestAccountModelsAreListedFirstWithoutAnyCredential(t *testing.T) {
	cfg := configFor(t, true)
	p := New(cfg)
	p.SetAccountSource(accountSourceFor(t, "https://gateway.invalid/client/desktop/v1", "secret-host-token"))

	list := p.List()
	if len(list) != 3 {
		t.Fatalf("listed %d rows, want 2 account + 1 own: %+v", len(list), list)
	}
	for i, row := range list[:2] {
		if row.ProviderID != accountTestID || row.Source != SourceAccount || !row.Ready || row.ID != ChoiceID(accountTestID, row.Model) {
			t.Fatalf("account row %d: %+v", i, row)
		}
	}
	if list[2].Source != SourceProvider || list[2].ProviderID != cfg.Models.Default {
		t.Fatalf("own provider row: %+v", list[2])
	}
	if list[0].ContextWindow != 64000 || list[1].ContextWindow != 0 {
		t.Fatalf("discovered windows were not carried over: %+v", list[:2])
	}
	raw, err := json.Marshal(list)
	if err != nil || strings.Contains(string(raw), "secret-host-token") {
		t.Fatalf("model list exposes the account credential: %s %v", raw, err)
	}
}

// The account object can end up in a log line or an error message; its
// printable forms must not carry the credential.
func TestAccountSourceNeverPrintsItsCredential(t *testing.T) {
	src := accountSourceFor(t, "https://gateway.invalid/client/desktop/v1", "secret-host-token")
	for _, text := range []string{fmt.Sprint(src), fmt.Sprintf("%+v", src), fmt.Sprintf("%#v", src), fmt.Sprintf("%v", *src)} {
		if strings.Contains(text, "secret-host-token") {
			t.Fatalf("credential printed: %s", text)
		}
	}
}

func TestAccountSourceRejectsIncompleteProjections(t *testing.T) {
	good := Catalog{Names: []string{"m"}}
	for name, build := range map[string]func() (*AccountSource, error){
		"id outside the reserved prefix": func() (*AccountSource, error) {
			return NewAccountSource("own", "L", "https://g.invalid/v1", "t", good)
		},
		"no endpoint": func() (*AccountSource, error) { return NewAccountSource(accountTestID, "L", " ", "t", good) },
		"no credential": func() (*AccountSource, error) {
			return NewAccountSource(accountTestID, "L", "https://g.invalid/v1", "", good)
		},
		"no models": func() (*AccountSource, error) {
			return NewAccountSource(accountTestID, "L", "https://g.invalid/v1", "t", Catalog{})
		},
	} {
		if _, err := build(); err == nil {
			t.Fatalf("%s: projection accepted", name)
		}
	}
}

func TestAccountModelCallsUseTheServerRouteAndCredential(t *testing.T) {
	var calls atomic.Int32
	var gotAuth, gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		gotAuth, gotPath = r.Header.Get("Authorization"), r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"c","object":"chat.completion","model":"server-model","choices":[{"index":0,"message":{"role":"assistant","content":"from the server"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	defer server.Close()
	cfg := configFor(t, false)
	p := New(cfg)
	p.SetAccountSource(accountSourceFor(t, server.URL+"/client/desktop/v1", "secret-host-token"))

	build, err := p.ModelBuilder(context.Background(), accountTestID, "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	out, err := build("manager", "agent").Generate(context.Background(), []*schema.Message{schema.UserMessage("hello")})
	if err != nil || out.Content != "from the server" {
		t.Fatalf("account model call: %v, %v", out, err)
	}
	if gotPath != "/client/desktop/v1/chat/completions" || gotAuth != "Bearer secret-host-token" || calls.Load() != 1 {
		t.Fatalf("server saw %q with %q (%d calls)", gotPath, gotAuth, calls.Load())
	}
}

// A conversation that picked a server model must not silently continue on a
// private provider after sign-out, even when that provider has the same name.
func TestSignedOutAccountConversationNeverFallsBackToAPrivateProvider(t *testing.T) {
	cfg := configFor(t, true)
	cfg.Models.Providers[0].Model = "server-model"
	p := New(cfg)
	p.SetAccountSource(accountSourceFor(t, "https://gateway.invalid/client/desktop/v1", "t"))
	p.SetAccountSource(nil)

	if _, err := p.Resolve(accountTestID); !errors.Is(err, ErrAccountLoginRequired) {
		t.Fatalf("Resolve after sign-out: %v", err)
	}
	if _, err := p.ResolveModel(accountTestID, "server-model"); !errors.Is(err, ErrAccountLoginRequired) {
		t.Fatalf("ResolveModel after sign-out: %v", err)
	}
	if _, err := p.ModelBuilder(context.Background(), accountTestID, "server-model", "", nil); !errors.Is(err, ErrAccountLoginRequired) {
		t.Fatalf("ModelBuilder after sign-out: %v", err)
	}
	if _, err := p.Get(context.Background(), accountTestID, "server-model"); !errors.Is(err, ErrAccountLoginRequired) {
		t.Fatalf("Get after sign-out: %v", err)
	}
	for _, row := range p.List() {
		if row.ProviderID == accountTestID {
			t.Fatalf("signed-out account still listed: %+v", row)
		}
	}
	if !strings.Contains(ErrAccountLoginRequired.Error(), "account_login_required") {
		t.Fatalf("the error does not carry its stable code: %v", ErrAccountLoginRequired)
	}
}

// A saved account model that left the catalog is still the name a conversation
// asks for. Substituting the catalog's new first name sends a model the
// composer is not showing.
func TestMissingAccountModelIsNotReplacedByCatalogOrder(t *testing.T) {
	cfg := configFor(t, true)
	cfg.Models.DefaultSource = config.DefaultSourceAccount
	cfg.Models.AccountModel = "removed-model"
	p := New(cfg)
	p.SetAccountSource(accountSourceFor(t, "https://gateway.invalid/client/desktop/v1", "t", "first-model", "second-model"))

	prov, err := p.ResolveModel(accountTestID, "")
	if err != nil || prov.Model != "removed-model" {
		t.Fatalf("missing account model resolved to %q (%v)", prov.Model, err)
	}
	name, follows, ok := p.ConversationDefault(accountTestID)
	if !ok || !follows || name != "removed-model" {
		t.Fatalf("conversation default = %q follows=%v ok=%v", name, follows, ok)
	}
	var sawMissing, catalogDefault bool
	for _, row := range p.List() {
		switch row.Model {
		case "removed-model":
			if row.Ready || !row.Default || !row.DefaultFollowsSetting || row.Source != SourceAccount {
				t.Fatalf("missing default row: %+v", row)
			}
			sawMissing = true
		case "first-model", "second-model":
			if row.Default {
				catalogDefault = true
			}
		}
	}
	if !sawMissing || catalogDefault {
		t.Fatalf("catalog order replaced the saved default: %+v", p.List())
	}
}

func TestDefaultProviderFollowsTheConfiguredSource(t *testing.T) {
	cfg := configFor(t, true)
	p := New(cfg)
	src := accountSourceFor(t, "https://gateway.invalid/client/desktop/v1", "t")

	cfg.Models.DefaultSource = config.DefaultSourceAccount
	if got := p.DefaultProviderID(); got != cfg.Models.Default {
		t.Fatalf("no account yet: %q", got)
	}
	p.SetAccountSource(src)
	if got := p.DefaultProviderID(); got != accountTestID {
		t.Fatalf("account preferred: %q", got)
	}
	if prov, err := p.Resolve(""); err != nil || prov.ID != accountTestID || prov.Model != "server-model" {
		t.Fatalf("empty id must follow the preference: %+v %v", prov, err)
	}
	list := p.List()
	if !list[0].Default || list[1].Default || list[2].Default {
		t.Fatalf("exactly the first account row is the default: %+v", list)
	}

	cfg.Models.DefaultSource = config.DefaultSourceProvider
	if got := p.DefaultProviderID(); got != cfg.Models.Default {
		t.Fatalf("own provider preferred: %q", got)
	}
	list = p.List()
	if list[0].Default || list[1].Default || !list[2].Default {
		t.Fatalf("own provider row must be the default: %+v", list)
	}
}

func TestAuthorizedGatewayConversationDefaultUsesTheExactCatalogRow(t *testing.T) {
	cfg := configFor(t, true)
	cfg.Models.DefaultSource = config.DefaultSourceAccount
	cfg.Models.Providers[0].Catalog = []string{"other-personal"}
	p := New(cfg)
	src := accountSourceFor(t, "https://gateway.invalid/v1", "fixture", "first-model", "server-model")
	src.SetModelPolicy(true, map[string]string{"conversation": "server-model"})
	src.SetCapabilities(&SourceCapabilities{Authorized: true, Available: true, Configurable: true, Capabilities: map[string]RoleCapability{
		"conversation": {Supported: true, Available: true, Configurable: true, Endpoints: []string{"chat_completions"}},
	}})
	p.SetAccountSource(src)
	check := func(wantID, wantModel string) {
		t.Helper()
		if got := p.DefaultProviderID(); got != wantID {
			t.Errorf("default provider = %q, want %q", got, wantID)
		}
		for _, id := range []string{"", wantID} {
			got, err := p.ResolveModel(id, "")
			if err != nil || got.ID != wantID || got.Model != wantModel {
				t.Errorf("resolved %q to %q/%q (%v), want %q/%q", id, got.ID, got.Model, err, wantID, wantModel)
			}
		}
		if name, follows, ok := p.ConversationDefault(""); !ok || !follows || name != wantModel {
			t.Errorf("conversation default = %q, follows=%v, ok=%v", name, follows, ok)
		}
		var found bool
		for _, row := range p.List() {
			if row.ID == ChoiceID(wantID, wantModel) {
				if !row.Default || !row.DefaultFollowsSetting || !row.Ready {
					t.Errorf("expected ready default row: %+v", row)
				}
				found = true
			} else if row.Default {
				t.Errorf("incorrect default row: %+v", row)
			}
		}
		if !found {
			t.Errorf("no catalog row for %q/%q", wantID, wantModel)
		}
	}
	check(accountTestID, "server-model")
	scope := p.ModelPolicy().Scope
	cfg.Models.Bindings = map[string]map[string]config.ModelRef{
		scope: {"conversation": {ProviderID: cfg.Models.Default, Model: "other-personal"}},
	}
	check(cfg.Models.Default, "other-personal")
	cfg.Models.Bindings[scope]["conversation"] = config.ModelRef{ProviderID: cfg.Models.Default, Model: "unlisted-personal"}
	var missing *Info
	for _, row := range p.List() {
		if row.Default {
			if missing != nil {
				t.Fatalf("more than one default with an unlisted binding: %+v", row)
			}
			missing = &row
		}
	}
	if missing == nil || missing.ID != ChoiceID(cfg.Models.Default, "unlisted-personal") || missing.Ready || !missing.DefaultFollowsSetting {
		t.Fatalf("unlisted binding lost its exact identity: %+v", missing)
	}
	delete(cfg.Models.Bindings, scope)
	check(accountTestID, "server-model")
	cfg.Models.DefaultSource = config.DefaultSourceProvider
	check(cfg.Models.Default, "some-model")
}

func TestLocalAccountDefaultPrefersTheAuthorizedOriginalGateway(t *testing.T) {
	cfg := configFor(t, true)
	cfg.Product.Mode = config.ProductModeLocal
	p := New(cfg)
	capability := &SourceCapabilities{Authorized: true, Available: true, Configurable: true, Capabilities: map[string]RoleCapability{
		"conversation": {Supported: true, Available: true, Configurable: true, Endpoints: []string{"chat_completions"}},
	}}
	preset := accountSourceFor(t, "https://gateway.invalid/v1", "fixture", "conversation")
	preset.SetCatalogGroup("third_party")
	preset.SetCapabilities(capability)
	const originalID = config.AccountSourcePrefix + "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	original, err := NewAccountSource(originalID, "Gateway", "https://gateway.invalid/openai/v1", "fixture", Catalog{Names: []string{"gateway-chat"}})
	if err != nil {
		t.Fatal(err)
	}
	original.SetCatalogGroup("original")
	original.SetCapabilities(capability)
	p.SetLocalAccountSources([]*AccountSource{preset, original})

	check := func(wantID, wantModel string, follows bool) {
		t.Helper()
		if got := p.DefaultProviderID(); got != wantID {
			t.Errorf("default provider = %q, want %q", got, wantID)
		}
		name, gotFollows, ok := p.ConversationDefault("")
		if !ok || name != wantModel || gotFollows != follows {
			t.Errorf("default model = %q, follows=%v, ok=%v", name, gotFollows, ok)
		}
		rows := p.List()
		at := slices.IndexFunc(rows, func(row Info) bool { return row.Default })
		if at < 0 || rows[at].ID != ChoiceID(wantID, wantModel) || rows[at].DefaultFollowsSetting != follows {
			t.Errorf("incorrect first default model: %+v", rows)
		}
		personal := slices.IndexFunc(rows, func(row Info) bool {
			return row.ID == ChoiceID(cfg.Models.Default, "some-model") && row.Default && row.DefaultFollowsSetting
		})
		if personal < 0 {
			t.Error("the existing personal provider lost its selected model")
		}
	}
	check(originalID, "gateway-chat", false)
	cfg.Models.DefaultSource = config.DefaultSourceProvider
	check(cfg.Models.Default, "some-model", true)
	cfg.Models.DefaultSource = config.DefaultSourceAccount
	p.SetLocalAccountSources([]*AccountSource{preset})
	check(preset.ID(), "conversation", false)
	p.SetLocalAccountSources(nil)
	check(cfg.Models.Default, "some-model", true)
}

func TestAccountSourceChangeDropsTheCachedClient(t *testing.T) {
	p := New(configFor(t, false))
	p.SetAccountSource(accountSourceFor(t, "https://gateway.invalid/client/desktop/v1", "first"))
	first, err := p.Get(context.Background(), accountTestID, "")
	if err != nil {
		t.Fatal(err)
	}
	again, _ := p.Get(context.Background(), accountTestID, "")
	if first != again {
		t.Fatal("the client is not shared")
	}
	p.SetAccountSource(accountSourceFor(t, "https://gateway.invalid/client/desktop/v1", "second"))
	next, err := p.Get(context.Background(), accountTestID, "")
	if err != nil || next == first {
		t.Fatalf("a replaced credential kept using the old client: %v", err)
	}
}

func TestRepublishingAnIdenticalAccountKeepsTheSharedClient(t *testing.T) {
	p := New(configFor(t, false))
	p.SetAccountSource(accountSourceFor(t, "https://gateway.invalid/client/desktop/v1", "same"))
	first, err := p.Get(context.Background(), accountTestID, "")
	if err != nil {
		t.Fatal(err)
	}
	p.SetAccountSource(accountSourceFor(t, "https://gateway.invalid/client/desktop/v1", "same"))
	if again, _ := p.Get(context.Background(), accountTestID, ""); again != first {
		t.Fatal("a periodic refresh with no change dropped the shared client")
	}
	// Any difference in what is served, however small, is a different account.
	for name, other := range map[string]*AccountSource{
		"models": accountSourceFor(t, "https://gateway.invalid/client/desktop/v1", "same", "other-model"),
		"window": func() *AccountSource {
			src, _ := NewAccountSource(accountTestID, "Server", "https://gateway.invalid/client/desktop/v1", "same",
				Catalog{Names: []string{"server-model", "server-model-2"}, Windows: map[string]int{"server-model": 1}})
			return src
		}(),
		"label": func() *AccountSource {
			src, _ := NewAccountSource(accountTestID, "Renamed", "https://gateway.invalid/client/desktop/v1", "same",
				Catalog{Names: []string{"server-model", "server-model-2"}, Windows: map[string]int{"server-model": 64000}})
			return src
		}(),
	} {
		p.SetAccountSource(accountSourceFor(t, "https://gateway.invalid/client/desktop/v1", "same"))
		p.SetAccountSource(other)
		if p.account.Load() != other {
			t.Fatalf("%s: the changed projection was not published", name)
		}
	}
}

// Offline runs never reach a network, so a real account must not leak in.
func TestScriptedPoolIgnoresTheAccountSource(t *testing.T) {
	p := NewMock(configFor(t, false))
	p.SetAccountSource(accountSourceFor(t, "https://gateway.invalid/client/desktop/v1", "t"))
	for _, row := range p.List() {
		if row.Source == SourceAccount {
			t.Fatalf("scripted pool listed a server model: %+v", row)
		}
	}
	if got := p.DefaultProviderID(); strings.HasPrefix(got, config.AccountSourcePrefix) {
		t.Fatalf("scripted pool defaulted to the server: %q", got)
	}
}

// The remote package reads the server's model list through this entry point;
// duplicated ids collapse and a malformed body is an error, not an empty list.
func TestParseCatalogReadsTheServerModelList(t *testing.T) {
	cat, err := ParseCatalog([]byte(`{"data":[{"id":"a","context_window":4096},{"id":"b"},{"id":"a"},{"id":""}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(cat.Names, ",") != "a,b" || cat.Windows["a"] != 4096 {
		t.Fatalf("catalog %+v", cat)
	}
	if _, err := ParseCatalog([]byte("not json")); err == nil {
		t.Fatal("a malformed list was accepted")
	}
}

// The first catalog name becomes the model a new conversation starts on, so a
// leading embedding, OCR or decisions row must not take that place.
func TestAccountCatalogDropsNonChatModelsBeforeTheDefault(t *testing.T) {
	cat, err := ParseCatalog([]byte(`{"data":[
		{"id":"embed-model","model_type":"embedding"},
		{"id":"ocr-model","model_type":"ocr"},
		{"id":"decision-model","model_type":"decisions"},
		{"id":"chat-model","model_type":"chat"},
		{"id":"legacy-model"}
	]}`))
	if err != nil {
		t.Fatal(err)
	}
	src, err := NewAccountSource(accountTestID, "Server", "https://gateway.invalid/client/desktop/v1", "secret-host-token", cat)
	if err != nil {
		t.Fatal(err)
	}
	prov := src.provider("")
	if prov.Model != "chat-model" || strings.Join(prov.Catalog, ",") != "chat-model,legacy-model" {
		t.Fatalf("model %q catalog %v", prov.Model, prov.Catalog)
	}
	kinds := src.Kinds()
	if kinds.Chat != 1 || kinds.Embedding != 1 || kinds.OCR != 1 || kinds.Decisions != 1 || kinds.Unclassified != 1 || strings.Join(kinds.EmbeddingModels, ",") != "embed-model" {
		t.Fatalf("kinds %+v", kinds)
	}
	if pinned := src.provider("legacy-model"); pinned.Model != "legacy-model" {
		t.Fatalf("chosen model %q", pinned.Model)
	}
	// An explicit saved name remains the request model even when the catalog
	// excludes it from available conversation models.
	pinned := src.provider("embed-model")
	if pinned.Model != "embed-model" || strings.Join(pinned.Catalog, ",") != "chat-model,legacy-model" {
		t.Fatalf("saved model %q, available catalog %v", pinned.Model, pinned.Catalog)
	}
	rows := src.rows(true, "embed-model")
	if len(rows) != 3 || rows[0].Default || rows[1].Default || rows[2].Model != "embed-model" ||
		rows[2].Ready || !rows[2].Default || !rows[2].DefaultFollowsSetting {
		t.Fatalf("saved hidden model should be unavailable and remain selected: %+v", rows)
	}
	p := New(configFor(t, true))
	p.SetAccountSource(src)
	var listed []string
	for _, row := range p.List() {
		if row.Source != SourceAccount {
			continue
		}
		listed = append(listed, row.Model)
		if strings.Contains(row.Label, "secret-host-token") || strings.Contains(row.ID, "secret-host-token") {
			t.Fatal("the model list included the account credential")
		}
	}
	if strings.Join(listed, ",") != "chat-model,legacy-model" {
		t.Fatalf("listed %v", listed)
	}
}

// Callers decide whether the account is the active source from this id, so it
// must follow publication and disappear on sign-out.
func TestAccountSourceIDFollowsPublication(t *testing.T) {
	p := New(configFor(t, true))
	if id := p.AccountSourceID(); id != "" {
		t.Fatalf("signed-out pool reports %q", id)
	}
	p.SetAccountSource(accountSourceFor(t, "https://gateway.invalid/client/desktop/v1", "secret-host-token"))
	if id := p.AccountSourceID(); id != accountTestID {
		t.Fatalf("published id %q", id)
	}
	p.SetAccountSource(nil)
	if id := p.AccountSourceID(); id != "" {
		t.Fatalf("cleared pool reports %q", id)
	}
}

// A refresh that changes only a model's window must still replace the source,
// otherwise the composer keeps a stale context meter.
func TestAccountSourcesDifferingOnlyByWindowAreNotEqual(t *testing.T) {
	a := accountSourceFor(t, "https://gateway.invalid/v1", "token", "m")
	b := accountSourceFor(t, "https://gateway.invalid/v1", "token", "m")
	if !a.equal(b) {
		t.Fatal("identical projections were treated as different")
	}
	b.windows["m"] = 1
	if a.equal(b) {
		t.Fatal("a changed context window was ignored")
	}
	c := accountSourceFor(t, "https://gateway.invalid/v1", "token", "m", "n")
	d := accountSourceFor(t, "https://gateway.invalid/v1", "token", "m", "x")
	if c.equal(d) {
		t.Fatal("a different model list was ignored")
	}
}
