package remote

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/LubyRuffy/eino-swarm/internal/config"
	"github.com/LubyRuffy/eino-swarm/internal/provider"
	"github.com/cloudwego/eino/schema"
)

// The phone's debug gesture must change the engine which executes its chat,
// even when that engine's managed preset currently refuses model edits.
func TestPhoneModeSwitchChangesThePCBeforeExposingItsCatalog(t *testing.T) {
	e := accountPhoneEngine(t, "shared-model")
	var got Response
	p := &LinkPump{eng: e, allow: func() (bool, bool) { return true, false }, send: func(raw []byte) error { return json.Unmarshal(raw, &got) }}
	p.Dispatch([]byte(`{"id":"switch","op":"set_product_mode","product_mode":"local"}`))
	if !got.OK || got.ProductMode != "local" || got.ModelPolicy == nil || !got.ModelPolicy.Configurable {
		t.Fatalf("phone did not receive an acknowledged LOCAL policy: %+v", got)
	}
	if e.Config().ProductSnapshot().Resolved() != config.ProductModeLocal {
		t.Fatal("the PC did not retain its account while switching execution")
	}
	cat := Handle(e, config.RemoteConfig{}, Request{Op: OpCatalog}, "relay", "session")
	if !cat.OK || len(cat.Models) < 2 || cat.Models[0].Locked {
		t.Fatalf("LOCAL catalog is still locked: %+v", cat)
	}
	for _, row := range cat.Models {
		if err := validateSelection(e, Request{ProviderID: row.ProviderID, Model: row.Model}); err != nil {
			t.Fatalf("listed model cannot execute: %v", err)
		}
	}
	p.Dispatch([]byte(`{"op":"set_product_mode","product_mode":"fofa"}`))
	if !got.OK || got.ProductMode != "fofa" || e.Config().ProductSnapshot().Resolved() != config.ProductModeFOFA {
		t.Fatalf("switching back did not restore the PC: %+v", got)
	}
}

func TestPhoneModeSwitchLetsAnUnfinishedLocalModelReplyContinue(t *testing.T) {
	oldAccepted := make(chan struct{}, 1)
	finishOld := make(chan struct{}, 1)
	old := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		oldAccepted <- struct{}{}
		select {
		case <-finishOld:
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"old reply"},"finish_reason":"stop"}]}`)
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(old.Close)
	next := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"new reply"},"finish_reason":"stop"}]}`)
	}))
	t.Cleanup(next.Close)
	e := catalogEngine(t, config.Provider{ID: "endpoint", BaseURL: old.URL, Model: "local-model"})
	src, err := provider.NewAccountSource(accountModelsSourceID, "Gateway", next.URL, "credential", provider.Catalog{Names: []string{"conversation"}})
	if err != nil {
		t.Fatal(err)
	}
	src.SetModelPolicy(false, map[string]string{"conversation": "conversation"})
	e.Providers().SetAccountSource(src)
	thread, err := e.CreateThread("Named conversation", "endpoint", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := e.SetThreadProvider(thread.ID, "endpoint", "local-model"); err != nil {
		t.Fatal(err)
	}
	opened, err := e.Providers().Get(t.Context(), "endpoint", "local-model")
	if err != nil {
		t.Fatal(err)
	}
	in := []*schema.Message{schema.UserMessage("fixture")}
	type result struct {
		out *schema.Message
		err error
	}
	oldResult := make(chan result, 1)
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	go func() {
		out, err := opened.Generate(ctx, in)
		oldResult <- result{out, err}
	}()
	select {
	case <-oldAccepted:
	case <-time.After(5 * time.Second):
		t.Fatal("the old model did not receive the request")
	}
	reply := Handle(e, config.RemoteConfig{}, Request{Op: OpSetProductMode, ProductMode: config.ProductModeFOFA}, "relay", "session")
	if !reply.OK || reply.ProductMode != config.ProductModeFOFA || e.Config().ProductSnapshot().Resolved() != config.ProductModeFOFA {
		t.Fatalf("phone could not switch while awaiting the old reply: %+v", reply)
	}
	stored, err := e.Store().GetThread(thread.ID)
	if err != nil || stored.ProviderID != "endpoint" || stored.Model != "local-model" {
		t.Fatalf("the mode save changed the stored conversation binding: %v %v", stored, err)
	}
	nextModel, err := e.Providers().StoredConversationModel(stored.ProviderID, stored.Model)
	if err != nil || nextModel.ID != accountModelsSourceID || nextModel.Model != "conversation" {
		t.Fatalf("the next turn of the same conversation did not resolve its gateway preset: %q/%q %v", nextModel.ID, nextModel.Model, err)
	}
	fresh, err := e.Providers().Get(t.Context(), nextModel.ID, nextModel.Model)
	if err != nil {
		t.Fatal(err)
	}
	newReply, err := fresh.Generate(t.Context(), in)
	if err != nil || newReply == nil || newReply.Content != "new reply" {
		t.Fatalf("new work did not use the gateway: %v %v", newReply, err)
	}
	finishOld <- struct{}{}
	select {
	case got := <-oldResult:
		if got.err != nil || got.out == nil || got.out.Content != "old reply" {
			t.Fatalf("in-flight request did not finish on its original source: %+v", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("old work did not finish")
	}
}

func TestPhoneModeSwitchIsIdempotentAndDoesNotPublishFailedPersistence(t *testing.T) {
	e := accountPhoneEngine(t, "shared-model")
	local := Request{Op: OpSetProductMode, ProductMode: config.ProductModeLocal}
	if got := Handle(e, config.RemoteConfig{}, local, "relay", "session"); !got.OK {
		t.Fatal(got)
	}
	release, err := e.Providers().BeginModelUse()
	if err != nil {
		t.Fatal(err)
	}
	got := Handle(e, config.RemoteConfig{}, local, "relay", "session")
	release()
	if !got.OK || got.ProductMode != config.ProductModeLocal {
		t.Fatalf("identical mode was treated as a new switch: %+v", got)
	}
	// A directory at the config destination deterministically refuses atomic
	// persistence on every platform, without relying on the test user's uid.
	if err := os.Remove(e.Config().Path()); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(e.Config().Path(), 0700); err != nil {
		t.Fatal(err)
	}
	got = Handle(e, config.RemoteConfig{}, Request{Op: OpSetProductMode, ProductMode: config.ProductModeFOFA}, "relay", "session")
	if got.OK || got.Code != "save_failed" || got.Error != "could not save PC execution mode" {
		t.Fatalf("persistence failure was not sanitized: %+v", got)
	}
	if e.Config().ProductSnapshot().Resolved() != config.ProductModeLocal {
		t.Fatal("failed save published a mode")
	}
}

func TestPhoneModeSwitchRefusesInvalidRestoringAndActiveModelRequests(t *testing.T) {
	e := accountPhoneEngine(t, "shared-model")
	for _, raw := range []string{`{}`, `{"product_mode":""}`, `{"product_mode":"account"}`, `{"product_mode":"LOCAL"}`, `{"product_mode":null}`} {
		var req Request
		if err := json.Unmarshal([]byte(raw), &req); err != nil {
			t.Fatal(err)
		}
		req.Op = "set_product_mode"
		got := Handle(e, config.RemoteConfig{}, req, "relay", "session")
		if got.OK || got.Code != "bad_request" {
			t.Fatalf("invalid switch accepted: %s %+v", raw, got)
		}
	}
	var req Request
	if err := json.Unmarshal([]byte(`{"op":"set_product_mode","product_mode":"local"}`), &req); err != nil {
		t.Fatal(err)
	}
	e.BeginRestore()
	if got := Handle(e, config.RemoteConfig{}, req, "relay", "session"); got.OK || got.Code != "restoring" {
		t.Fatalf("restore accepted switch: %+v", got)
	}
	e.FinishRestore()
	release, err := e.Providers().BeginModelUse()
	if err != nil {
		t.Fatal(err)
	}
	got := Handle(e, config.RemoteConfig{}, req, "relay", "session")
	release()
	if got.OK || got.Code != "backup_busy" {
		t.Fatalf("active model did not protect source: %+v", got)
	}
	if e.Config().ProductSnapshot().Resolved() != config.ProductModeFOFA {
		t.Fatal("refused switch changed PC mode")
	}
	raw, _ := json.Marshal(got)
	if strings.Contains(string(raw), "account.invalid") || strings.Contains(string(raw), "token") {
		t.Fatal("switch refusal disclosed a credential or endpoint")
	}
}
