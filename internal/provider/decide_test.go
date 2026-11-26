package provider

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LubyRuffy/eino-swarm/internal/config"
)

func TestDecidePostsTheFixedQuestionAndFormatsTheAnswer(t *testing.T) {
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/decisions" {
			t.Errorf("path %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer secret-token" {
			t.Errorf("auth %q", r.Header.Get("Authorization"))
		}
		raw, _ := io.ReadAll(r.Body)
		body = string(raw)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"answers":[{"name":"proceed","probability":0.8}]}`))
	}))
	defer srv.Close()

	p := New(configFor(t, true))
	src := accountSourceFor(t, srv.URL, "secret-token", "conversation")
	p.SetAccountSource(src)
	got, err := p.Decide(context.Background(), src.ID(), "decisions", "the request")
	if err != nil {
		t.Fatal(err)
	}
	if got != "proceed 0.80" {
		t.Fatalf("answer %q", got)
	}
	if !strings.Contains(body, `"model":"decisions"`) || !strings.Contains(body, `"name":"proceed"`) || !strings.Contains(body, "the request") {
		t.Fatalf("body %s", body)
	}
}

func TestDecisionPredicateRequiresAValidNamedProbability(t *testing.T) {
	for _, raw := range []string{
		`{`, `{"answers":[]}`, `{"answers":[{"name":"gate"}]}`, `{"answers":[{"name":"other","probability":0}]}`,
		`{"answers":[{"name":"gate","probability":-1}]}`, `{"answers":[{"name":"gate","probability":1.1}]}`,
		`{"answers":[{"name":"gate","probability":0},{"name":"gate","probability":0}]}`,
	} {
		if _, err := parseDecisionPredicate([]byte(raw), "gate"); !errors.Is(err, ErrInvalidDecisionResponse) {
			t.Errorf("accepted invalid probability %s: %v", raw, err)
		}
	}
	if got, err := parseDecisionPredicate([]byte(`{"answers":[{"name":"gate","probability":0}]}`), "gate"); err != nil || got != 0 {
		t.Fatalf("valid zero probability: got %g, %v", got, err)
	}
}

func TestDecisionPredicateDoesNotBorrowGatewayAccessForAPersonalConversation(t *testing.T) {
	cfg := configFor(t, true)
	cfg.Product.Mode = config.ProductModeLocal
	pool := New(cfg)
	source := accountSourceFor(t, "http://127.0.0.1:1", "fixture-key", "decisions")
	source.SetRoles([]AccountRole{{ID: "decisions", Enabled: true}})
	pool.SetAccountSource(source)
	if _, err := pool.DecidePredicate(t.Context(), config.DefaultProviderID, "private input", "gate", "Decide", nil); !errors.Is(err, ErrDecisionModelUnavailable) {
		t.Fatalf("personal conversation borrowed the Gateway route: %v", err)
	}
}

func TestDecisionPredicateCancelsTheGatewayRequest(t *testing.T) {
	entered, cancelled := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		close(entered)
		select {
		case <-r.Context().Done():
			close(cancelled)
		case <-time.After(3 * time.Second):
			t.Error("the gateway decision request kept running after cancellation")
		}
	}))
	defer server.Close()
	cfg := configFor(t, true)
	cfg.Product.Mode = config.ProductModeLocal
	pool := New(cfg)
	source := accountSourceFor(t, server.URL, "fixture-key", "decisions")
	source.SetRoles([]AccountRole{{ID: "decisions", Enabled: true}})
	pool.SetLocalAccountSources([]*AccountSource{source})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := pool.DecidePredicate(ctx, source.ID(), "answer", "gate", "Decide", nil)
		done <- err
	}()
	select {
	case <-entered:
	case err := <-done:
		t.Fatalf("decision call ended before the authorized endpoint received it: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("no decision request reached the authorized endpoint")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("lost cancellation error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("decision call did not return after cancellation")
	}
	select {
	case <-cancelled:
	case <-time.After(3 * time.Second):
		t.Fatal("the endpoint did not observe cancellation")
	}
}

func TestGatewayDecisionModelDiscoveryRespectsRoleAndCatalog(t *testing.T) {
	const name = "typed-model"
	catalog := Catalog{Names: []string{"chat", name}, Types: map[string]string{name: "decisions"}}
	for _, tc := range []struct {
		name, want string
		roles      []AccountRole
		defaults   map[string]string
		negotiate  bool
	}{
		{name: "default", want: name, defaults: map[string]string{"decisions": name}},
		{name: "typed catalog", want: name},
		{name: "legacy enabled role", want: "decisions", roles: []AccountRole{{ID: "decisions", Enabled: true}}},
		{name: "disabled role", roles: []AccountRole{{ID: "decisions"}}},
		{name: "unavailable negotiated capability", negotiate: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source, err := NewAccountSource(config.AccountSourcePrefix+"fixture", "fixture", "http://127.0.0.1:1", "fixture-key", catalog)
			if err != nil {
				t.Fatal(err)
			}
			source.SetRoles(tc.roles)
			source.SetModelPolicy(true, tc.defaults)
			if tc.negotiate {
				source.SetCapabilities(nil)
			}
			if got := decisionModelName(source); got != tc.want {
				t.Fatalf("decision model %q, want %q", got, tc.want)
			}
		})
	}
}

func TestDecideRejectsAnEmptyAnswer(t *testing.T) {
	if _, err := formatDecision([]byte(`{"answers":[]}`)); err == nil {
		t.Fatal("empty answers were accepted")
	}
	if _, err := formatDecision([]byte(`not json`)); err == nil {
		t.Fatal("malformed body was accepted")
	}
	got, err := formatDecision([]byte(`{"answers":[{"probability":1}]}`))
	if err != nil || got != "proceed 1.00" {
		t.Fatalf("blank name = %q %v", got, err)
	}
}
