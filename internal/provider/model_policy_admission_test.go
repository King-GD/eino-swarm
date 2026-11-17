package provider

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/LubyRuffy/eino-swarm/internal/config"
	"github.com/cloudwego/eino/schema"
)

// A model stream, including an auxiliary call outside a turn, must finish
// before changing execution source. The assertion crosses the real HTTP client.
func TestModelStreamHoldsSourceUntilUpstreamCompletes(t *testing.T) {
	entered, finish := make(chan struct{}), make(chan struct{})
	var hits atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ready\"}}]}\n\n")
		w.(http.Flusher).Flush()
		close(entered)
		select {
		case <-finish:
		case <-r.Context().Done():
			return
		}
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer upstream.Close()
	cfg := configFor(t, false)
	cfg.Product.Mode = config.ProductModeLocal
	cfg.Models.Providers[0].BaseURL = upstream.URL
	cfg.Models.Providers[0].Model = "configured-model"
	p := New(cfg)
	m, err := p.Get(t.Context(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	stream, err := m.Stream(t.Context(), []*schema.Message{schema.UserMessage("inspect")})
	if err != nil {
		t.Fatal(err)
	}
	<-entered
	if release, ok := p.TryHoldModelPolicy(); ok {
		release()
		t.Error("live stream allowed source change")
	}
	close(finish)
	for {
		_, err = stream.Recv()
		if err != nil {
			break
		}
	}
	stream.Close()
	if !errors.Is(err, io.EOF) {
		t.Fatal(err)
	}
	release, ok := p.TryHoldModelPolicy()
	if !ok {
		t.Fatal("completed stream retained hold")
	}
	if _, err = m.Generate(t.Context(), nil); !errors.Is(err, ErrModelPolicy) {
		t.Fatalf("request entered frozen source: %v", err)
	}
	release()
	if hits.Load() != 1 {
		t.Fatal("refused call reached HTTP")
	}
}

func TestOpenedModelKeepsItsEndpointAfterDebugSourceSwitch(t *testing.T) {
	var personal, account atomic.Int32
	personalSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		personal.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
	}))
	defer personalSrv.Close()
	accountSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		account.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
	}))
	defer accountSrv.Close()

	cfg := configFor(t, false)
	cfg.Product.Mode = config.ProductModeLocal
	cfg.Models.Providers[0].BaseURL = personalSrv.URL
	cfg.Models.Providers[0].Model = "configured-model"
	cfg.Models.Providers[0].APIKey = "personal"
	p := New(cfg)
	opened, err := p.Get(t.Context(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = opened.Generate(t.Context(), []*schema.Message{schema.UserMessage("inspect")}); err != nil {
		t.Fatal(err)
	}
	if err = cfg.Update(func(c *config.Config) error { c.Product.Mode = ""; return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err = opened.Generate(t.Context(), []*schema.Message{schema.UserMessage("inspect")}); err != nil {
		t.Fatalf("opened model stopped after the source switch: %v", err)
	}
	if personal.Load() != 2 {
		t.Fatalf("opened model left its endpoint: hits=%d", personal.Load())
	}

	src := accountSourceFor(t, accountSrv.URL, "current-session")
	src.SetModelPolicy(false, map[string]string{"conversation": "server-model"})
	p.SetAccountSource(src)
	p.Invalidate()
	fresh, err := p.Get(t.Context(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = fresh.Generate(t.Context(), []*schema.Message{schema.UserMessage("inspect")}); err != nil {
		t.Fatal(err)
	}
	if personal.Load() != 2 || account.Load() != 1 {
		t.Fatalf("new request reused the opened endpoint: personal=%d account=%d", personal.Load(), account.Load())
	}
}

func TestOpenedGatewayModelKeepsItsCredentialUntilThatCredentialChanges(t *testing.T) {
	var hits atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
	}))
	defer upstream.Close()
	cfg := configFor(t, true)
	p := New(cfg)
	src := accountSourceFor(t, upstream.URL, "current-session")
	src.SetAccountOwner("owner")
	p.SetAccountSource(src)
	opened, err := p.Get(t.Context(), src.id, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = opened.Generate(t.Context(), []*schema.Message{schema.UserMessage("inspect")}); err != nil {
		t.Fatal(err)
	}
	if err = cfg.Update(func(c *config.Config) error {
		c.Product.Mode = config.ProductModeLocal
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err = opened.Generate(t.Context(), []*schema.Message{schema.UserMessage("inspect")}); err != nil {
		t.Fatalf("opened gateway model stopped after leaving gateway: %v", err)
	}
	if hits.Load() != 2 {
		t.Fatalf("opened gateway model left its endpoint: hits=%d", hits.Load())
	}
	next := accountSourceFor(t, upstream.URL, "rotated-session")
	next.SetAccountOwner("owner")
	p.SetAccountSource(next)
	if _, err = opened.Generate(t.Context(), []*schema.Message{schema.UserMessage("inspect")}); !errors.Is(err, ErrModelPolicy) {
		t.Fatalf("rotated credential stayed usable: %v", err)
	}
	if hits.Load() != 2 {
		t.Fatal("rotated credential reached HTTP")
	}
}

func TestTypedCallsAndFailedAdmissionReleasePolicy(t *testing.T) {
	p := New(config.Default())
	hold, ok := p.TryHoldModelPolicy()
	if !ok {
		t.Fatal("idle source refused")
	}
	if _, err := p.BeginModelUse(); !errors.Is(err, ErrModelPolicy) {
		t.Fatal(err)
	}
	if _, err := p.Embed(t.Context(), "", "embed", []string{"text"}); !errors.Is(err, ErrModelPolicy) {
		t.Fatal(err)
	}
	if _, err := p.Decide(t.Context(), "", "decisions", "text"); !errors.Is(err, ErrModelPolicy) {
		t.Fatal(err)
	}
	hold()
	release, err := p.BeginModelUse()
	if err != nil {
		t.Fatal(err)
	}
	if hold, ok := p.TryHoldModelPolicy(); ok {
		hold()
		t.Error("typed call allowed switch")
	}
	release()
	cfg := configFor(t, false)
	cfg.Product.Mode = config.ProductModeLocal
	p = NewMock(cfg)
	build, err := p.ModelBuilder(t.Context(), "", "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	// Isolated recovery skips cooldowns, but must retain execution-source admission.
	_, err = build("title", "title").Generate(WithIsolatedRecovery(context.Background()), []*schema.Message{schema.UserMessage("inspect")})
	if err != nil {
		t.Fatal(err)
	}
	if hold, ok := p.TryHoldModelPolicy(); !ok {
		t.Fatal("Generate leaked its hold")
	} else {
		hold()
	}
	r := &recordingModel{policyUse: func() (func(), error) { return nil, ErrModelPolicy }}
	if _, _, err := r.admit(t.Context()); !errors.Is(err, ErrModelPolicy) {
		t.Fatal(err)
	}
}

func TestCachedPersonalModelIsRefusedAfterManagedLogin(t *testing.T) {
	cfg := configFor(t, true)
	p := New(cfg)
	cached, err := p.Get(t.Context(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	src := accountSourceFor(t, "https://gateway.invalid/v1", "private")
	src.SetModelPolicy(false, map[string]string{"conversation": "server-model"})
	p.SetAccountSource(src)
	if _, err = cached.Generate(t.Context(), nil); !errors.Is(err, ErrModelPolicy) {
		t.Fatal("cached endpoint escaped managed login", err)
	}
	if release, ok := p.TryHoldModelPolicy(); !ok {
		t.Fatal("refused cache retained a hold")
	} else {
		release()
	}
}

func TestCachedGatewayBuilderCannotUseAnEarlierAccountsCredential(t *testing.T) {
	var hits atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
	}))
	defer upstream.Close()
	p := New(configFor(t, true))
	src := accountSourceFor(t, upstream.URL, "earlier-session")
	src.SetAccountOwner("earlier-owner")
	p.SetAccountSource(src)
	cached, err := p.Get(t.Context(), src.id, "")
	if err != nil {
		t.Fatal(err)
	}
	next := accountSourceFor(t, upstream.URL, "current-session")
	next.SetAccountOwner("current-owner")
	p.SetAccountSource(next)
	if _, err := cached.Generate(t.Context(), []*schema.Message{schema.UserMessage("inspect")}); !errors.Is(err, ErrModelPolicy) {
		t.Fatalf("earlier account builder remained usable: %v", err)
	}
	if hits.Load() != 0 {
		t.Fatal("earlier credential reached HTTP")
	}
}
