package fofaworld

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

func row(ip string, port int, protocol, host string) json.RawMessage {
	body, _ := json.Marshal(map[string]any{"ip": ip, "port": strconv.Itoa(port), "protocol": protocol, "host": host,
		"country": "US", "region": "fixture", "city": "fixture", "longitude": "-70.5", "latitude": "40.2"})
	return body
}

func serverAsset(t *testing.T, address string) Asset {
	t.Helper()
	u, err := url.Parse(address)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatal(err)
	}
	a, err := decodeAsset(row(u.Hostname(), port, u.Scheme, address))
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestAssetIdentityAndOptionalCoordinatesSurviveCollection(t *testing.T) {
	a, err := decodeAsset(row("::ffff:127.0.0.1", 80, "HTTP", "http://EXAMPLE.test/a?private=x"))
	if err != nil || a.IP != "127.0.0.1" || a.URL != "http://example.test/" || a.Status != "pending" || a.Longitude == nil {
		t.Fatalf("asset=%+v err=%v", a, err)
	}
	b, err := decodeAsset(row("127.0.0.1", 80, "http", "http://example.test/other"))
	if err != nil || a.ID != b.ID {
		t.Fatalf("logical origin identity moved: %+v %v", b, err)
	}
	var record map[string]any
	if err := json.Unmarshal(row("127.0.0.1", 443, "https", ""), &record); err != nil {
		t.Fatal(err)
	}
	record["longitude"], record["latitude"], record["port"] = "", "NaN", 443
	raw, _ := json.Marshal(record)
	c, err := decodeAsset(raw)
	if err != nil || c.Longitude != nil || c.Latitude != nil || c.URL != "https://127.0.0.1:443/" {
		t.Fatalf("optional coordinates/numeric field: %+v %v", c, err)
	}
	d, err := decodeAsset(row("::1", 22, "ssh", ""))
	if err != nil || d.Status != "skipped" || d.URL != "" {
		t.Fatalf("non-http=%+v err=%v", d, err)
	}
	e, err := decodeAsset(row("::1", 80, "http", ""))
	if err != nil || e.URL != "http://[::1]:80/" {
		t.Fatalf("IPv6=%+v err=%v", e, err)
	}
}

func TestInvalidAssetCannotBecomeAnotherTarget(t *testing.T) {
	for name, raw := range map[string]json.RawMessage{
		"json":       []byte("{"),
		"null":       []byte("null"),
		"bool":       []byte(`{"ip":true}`),
		"null_field": []byte(`{"ip":null}`),
		"object":     []byte(`{"ip":{}}`),
		"ip":         row("not an ip", 80, "http", ""),
		"zone":       row("fe80::1%en0", 80, "http", ""),
		"port":       row("127.0.0.1", 0, "http", ""),
		"userinfo":   row("127.0.0.1", 80, "http", "http://user:secret@example.test"),
		"scheme":     row("127.0.0.1", 80, "http", "https://example.test"),
		"host":       row("127.0.0.1", 80, "http", "http://"),
		"escape":     row("127.0.0.1", 80, "http", "http://bad%zz"),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeAsset(raw); err == nil {
				t.Fatal("invalid asset was accepted")
			}
		})
	}
}

func TestProbeTargetsRecordedIPWithLogicalHostAndDoesNotFollowRedirects(t *testing.T) {
	var hits int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.Host != "logical.invalid:8443" || r.Method != "GET" || r.URL.Path != "/" {
			t.Errorf("request identity: %+v", r)
		}
		w.Header().Set("Location", "/again")
		w.WriteHeader(http.StatusFound)
	}))
	defer server.Close()
	a := serverAsset(t, server.URL)
	a.Host, a.URL = "logical.invalid:8443", "http://logical.invalid:8443/"
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	result := probe(t.Context(), a, time.Second)
	if result.Status != "responded" || result.StatusCode != 302 || hits != 1 || result.FinishedAt == "" {
		t.Fatalf("result=%+v hits=%d", result, hits)
	}
}

func TestProbeKeepsHTTPRefusalTLSFailureTimeoutAndCancellationDistinct(t *testing.T) {
	refusal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusForbidden) }))
	defer refusal.Close()
	if result := probe(t.Context(), serverAsset(t, refusal.URL), time.Second); result.Status != "responded" || result.StatusCode != 403 {
		t.Fatalf("HTTP refusal is a response: %+v", result)
	}
	tlsServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) }))
	defer tlsServer.Close()
	if result := probe(t.Context(), serverAsset(t, tlsServer.URL), time.Second); result.Status != "no_response" || result.Error != "tls" {
		t.Fatalf("invalid certificate=%+v", result)
	}
	slow := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer slow.Close()
	if result := probe(t.Context(), serverAsset(t, slow.URL), 20*time.Millisecond); result.Error != "timeout" {
		t.Fatalf("timeout=%+v", result)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if result := probe(ctx, serverAsset(t, refusal.URL), time.Second); result.Status != "interrupted" || result.Error != "cancelled" {
		t.Fatalf("cancelled=%+v", result)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := "http://" + listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	if result := probe(t.Context(), serverAsset(t, address), time.Second); result.Error != "request_failed" {
		t.Fatalf("connect=%+v", result)
	}
	bad := serverAsset(t, refusal.URL)
	bad.URL = "://"
	if result := probe(t.Context(), bad, time.Second); result.Error != "request_failed" {
		t.Fatalf("request=%+v", result)
	}
	if got := probeError(t.Context(), errors.New(strings.Repeat("private", 100))); got != "request_failed" {
		t.Fatal(got)
	}
}
