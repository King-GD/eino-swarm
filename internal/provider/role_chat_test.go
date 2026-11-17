package provider

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCompleteFixedChatUsesTheExclusiveRoute(t *testing.T) {
	var path, auth, body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		auth = r.Header.Get("Authorization")
		raw, _ := io.ReadAll(r.Body)
		body = string(raw)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"译文"}}]}`))
	}))
	defer srv.Close()

	p := New(configFor(t, true))
	src := accountSourceFor(t, srv.URL, "secret-token", "conversation")
	src.SetRoles([]AccountRole{{ID: "conversation", Enabled: true}, {ID: "translation", Enabled: true, Label: "翻译"}})
	p.SetAccountSource(src)
	got, err := p.CompleteFixedChat(context.Background(), src.ID(), "translation", "system", "source", 32)
	if err != nil {
		t.Fatal(err)
	}
	if got != "译文" || path != "/translations" || auth != "Bearer secret-token" {
		t.Fatalf("result %q path %s auth %q", got, path, auth)
	}
	if strings.Contains(body, `"tools"`) || !strings.Contains(body, `"model":"translation"`) || !strings.Contains(body, `"stream":false`) || !strings.Contains(body, `"max_tokens":32`) {
		t.Fatalf("body %s", body)
	}
}

func TestCompleteFixedChatHidesTheUpstreamBody(t *testing.T) {
	const secret = "upstream-secret-body"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, secret, http.StatusBadGateway)
	}))
	defer srv.Close()

	p := New(configFor(t, true))
	src := accountSourceFor(t, srv.URL, "secret-token", "conversation")
	src.SetRoles([]AccountRole{{ID: "naming", Enabled: true}})
	p.SetAccountSource(src)
	_, err := p.CompleteFixedChat(context.Background(), src.ID(), "naming", "system", "source", 0)
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("error %v", err)
	}
}
