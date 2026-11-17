package fofaworld

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// A response already observed by the real HTTP client must not turn into an
// interrupted asset merely because Stop precedes reducer delivery. The gate is
// after the actual probe returns; a server write alone would not prove that.
func TestStopAfterObservedHTTPResponsePreservesItsTerminalReceipt(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()
	asset := serverAsset(t, server.URL)
	for round := range 32 {
		ctx, cancel := context.WithCancel(t.Context())
		observed := make(chan Result, 1)
		release := make(chan struct{})
		jobs := make(chan Asset, 1)
		jobs <- asset
		close(jobs)
		events := make(chan event)
		startWorkersUsing(ctx, Input{Concurrency: 1, Timeout: time.Second}, jobs, events,
			func(ctx context.Context, asset Asset, timeout time.Duration) Result {
				result := probe(ctx, asset, timeout)
				observed <- result
				<-release
				return result
			})
		select {
		case e := <-events:
			if e.kind != "running" {
				cancel()
				close(release)
				t.Fatalf("round %d: first event=%s", round, e.kind)
			}
		case <-time.After(3 * time.Second):
			cancel()
			close(release)
			t.Fatal("worker did not start")
		}
		select {
		case result := <-observed:
			if result.Status != "responded" || result.StatusCode != http.StatusForbidden {
				cancel()
				close(release)
				t.Fatalf("real probe did not observe fixture response: %+v", result)
			}
		case <-time.After(3 * time.Second):
			cancel()
			close(release)
			t.Fatal("real HTTP observation did not complete")
		}
		// Cancellation is unambiguously after observation and before the worker
		// can submit its terminal event. No elapsed-time guess orders the race.
		cancel()
		close(release)
		receipts := 0
	joined:
		for {
			select {
			case e := <-events:
				switch e.kind {
				case "result":
					receipts++
					if e.asset.ID != asset.ID || e.result.Status != "responded" || e.result.StatusCode != http.StatusForbidden {
						t.Fatalf("observed response changed identity/outcome: %+v", e)
					}
				case "workers_done":
					break joined
				default:
					t.Fatalf("unexpected terminal event: %s", e.kind)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("worker did not join after cancellation")
			}
		}
		if receipts != 1 {
			t.Fatalf("round %d: actual HTTP 403 was observed before Stop, terminal receipts=%d want=1", round, receipts)
		}
	}
}
