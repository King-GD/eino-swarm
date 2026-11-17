package fofaworld

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// The logical origin uses the effective default HTTP port, independently of
// the recorded transport port. Equivalent source spellings are one asset and
// must not multiply either regional counts or local HTTP side effects.
func TestEquivalentHTTPOriginsCreateOneAssetAndOneLocalProbe(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	a := serverAsset(t, server.URL)
	input := fixtureInput(t, fixture{Rows: []json.RawMessage{
		row(a.IP, a.Port, "http", "http://same.invalid"),
		row(a.IP, a.Port, "http", "http://same.invalid:80"),
	}})
	sink, _ := snapshotSink(t)
	summary, err := Run(t.Context(), input, sink)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Collection.Received != 2 || summary.Collection.Discovered != 1 || summary.Probe.Responded != 1 || requests.Load() != 1 {
		t.Fatalf("equivalent origins duplicated an asset or local request: received=%d discovered=%d responded=%d actual_HTTP=%d",
			summary.Collection.Received, summary.Collection.Discovered, summary.Probe.Responded, requests.Load())
	}
}

func TestDefaultHTTPPortsShareIdentityAndDistinctOriginsRemainDistinct(t *testing.T) {
	for _, pair := range [][2]string{
		{"http://EXAMPLE.invalid", "http://example.invalid:80/path"},
		{"https://EXAMPLE.invalid", "https://example.invalid:443/"},
		{"http://[::1]", "http://[::1]:80/"},
	} {
		protocol := "http"
		if pair[0][:5] == "https" {
			protocol = "https"
		}
		left, err := decodeAsset(row("127.0.0.1", 49152, protocol, pair[0]))
		if err != nil {
			t.Fatal(err)
		}
		right, err := decodeAsset(row("127.0.0.1", 49152, protocol, pair[1]))
		if err != nil || left.ID != right.ID {
			t.Fatalf("equivalent origins differ: %v err=%v", pair, err)
		}
	}
	base, err := decodeAsset(row("127.0.0.1", 49152, "http", "http://example.invalid"))
	if err != nil {
		t.Fatal(err)
	}
	other, err := decodeAsset(row("127.0.0.1", 49152, "http", "http://example.invalid:8080"))
	if err != nil || base.ID == other.ID {
		t.Fatalf("distinct logical ports collapsed: err=%v", err)
	}
}
