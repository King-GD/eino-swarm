package fofaworld

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestLocalProbeConcurrencyIsBoundedAcrossACollectedBatch(t *testing.T) {
	var active, maximum atomic.Int32
	started := make(chan struct{}, 5)
	release := make(chan struct{})
	var closeRelease sync.Once
	defer closeRelease.Do(func() { close(release) })
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		current := active.Add(1)
		defer active.Add(-1)
		for old := maximum.Load(); current > old && !maximum.CompareAndSwap(old, current); old = maximum.Load() {
		}
		started <- struct{}{}
		select {
		case <-release:
			w.WriteHeader(204)
		case <-r.Context().Done():
		}
	}))
	defer server.Close()
	a := serverAsset(t, server.URL)
	var rows []json.RawMessage
	for i := range 5 {
		rows = append(rows, row(a.IP, a.Port, "http", fmt.Sprintf("host-%d.invalid", i)))
	}
	input := fixtureInput(t, fixture{Rows: rows})
	input.Timeout = 5 * time.Second
	sink, _ := snapshotSink(t)
	done := make(chan error, 1)
	go func() { _, err := Run(t.Context(), input, sink); done <- err }()
	for range input.Concurrency {
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Fatal("configured concurrent workers did not start")
		}
	}
	if got := maximum.Load(); got != int32(input.Concurrency) {
		t.Fatalf("concurrency=%d", got)
	}
	closeRelease.Do(func() { close(release) })
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got := maximum.Load(); got > int32(input.Concurrency) {
		t.Fatalf("concurrency exceeded budget: %d", got)
	}
}

func TestCancellationStopsOnlyTheCollectorsOwnedProcessGroup(t *testing.T) {
	heartbeat := filepath.Join(t.TempDir(), "child-heartbeat")
	input := fixtureInput(t, fixture{Rows: []json.RawMessage{row("127.0.0.1", 22, "ssh", "")},
		Gate: filepath.Join(t.TempDir(), "never-created"), WaitAfter: 1, Child: heartbeat})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := Run(ctx, input, func(context.Context, json.RawMessage) error { return nil })
		done <- err
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		bytes, err := os.ReadFile(heartbeat)
		if err == nil && len(bytes) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("owned descendant did not start")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	before, err := os.ReadFile(heartbeat)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	after, err := os.ReadFile(heartbeat)
	if err != nil || string(before) != string(after) {
		t.Fatal("owned descendant kept writing after Run joined")
	}
}

func TestLineBoundAndFramingAreIndependentOfPipeChunking(t *testing.T) {
	var lines []string
	w := lineWriter{line: func(line []byte) error { lines = append(lines, string(line)); return nil }}
	for _, part := range []string{"one\r", "\ntwo", "\nlast"} {
		if _, err := w.Write([]byte(part)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.finish(); err != nil || strings.Join(lines, ",") != "one,two,last" {
		t.Fatalf("lines=%v err=%v", lines, err)
	}
	w = lineWriter{line: func([]byte) error { return nil }}
	if _, err := w.Write([]byte(strings.Repeat("x", maxLineBytes+1))); safeFailure(err) != "output_limit" {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("after-limit")); safeFailure(err) != "output_limit" {
		t.Fatal(err)
	}
	if safeFailure(errors.New("private transport data")) != "client_failed" {
		t.Fatal("unclassified errors were exposed")
	}
}

func TestCancelledWorkersJoinWithoutAnEventConsumer(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	events := make(chan event, 1)
	startWorkers(ctx, Input{Concurrency: 2, Timeout: time.Second}, make(chan Asset), events)
	select {
	case e := <-events:
		if e.kind != "workers_done" {
			t.Fatal(e.kind)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled workers did not join")
	}
	ctx, cancel = context.WithCancel(t.Context())
	jobs := make(chan Asset, 1)
	jobs <- Asset{ID: "fixture"}
	events = make(chan event)
	startWorkers(ctx, Input{Concurrency: 1, Timeout: time.Second}, jobs, events)
	deadline := time.Now().Add(time.Second)
	for len(jobs) != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	cancel()
	for {
		select {
		case e := <-events:
			if e.kind == "workers_done" {
				return
			}
		case <-time.After(time.Second):
			t.Fatal("worker blocked delivering its first event after cancellation")
		}
	}
}
