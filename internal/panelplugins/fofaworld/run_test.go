package fofaworld

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

type fixture struct {
	Rows      []json.RawMessage `json:"rows"`
	Stderr    []string          `json:"stderr"`
	DelayMS   int               `json:"delay_ms"`
	Gate      string            `json:"gate"`
	WaitAfter int               `json:"wait_after"`
	Raw       string            `json:"raw"`
	Exit      int               `json:"exit"`
	Args      string            `json:"args"`
	Child     string            `json:"child"`
}

func TestMain(m *testing.M) {
	if path := os.Getenv("FOFA_WORLD_FIXTURE"); path != "" {
		runFixture(path)
		return
	}
	os.Exit(m.Run())
}

func runFixture(path string) {
	if heartbeat := os.Getenv("FOFA_WORLD_HEARTBEAT"); heartbeat != "" {
		for {
			_ = os.WriteFile(heartbeat, []byte(time.Now().Format(time.RFC3339Nano)), 0o600)
			time.Sleep(10 * time.Millisecond)
		}
	}
	bytes, err := os.ReadFile(path)
	if err != nil {
		os.Exit(12)
	}
	var f fixture
	if json.Unmarshal(bytes, &f) != nil {
		os.Exit(13)
	}
	if f.Args != "" {
		bytes, _ := json.Marshal(os.Args[1:])
		_ = os.WriteFile(f.Args, bytes, 0o600)
	}
	if f.Child != "" {
		command := exec.Command(os.Args[0])
		command.Env = append(os.Environ(), "FOFA_WORLD_HEARTBEAT="+f.Child)
		if command.Start() != nil {
			os.Exit(14)
		}
	}
	for i, row := range f.Rows {
		fmt.Println(string(row))
		if f.DelayMS > 0 {
			time.Sleep(time.Duration(f.DelayMS) * time.Millisecond)
		}
		if f.Gate != "" && i+1 == f.WaitAfter {
			for {
				if _, err := os.Stat(f.Gate); err == nil {
					break
				}
				time.Sleep(5 * time.Millisecond)
			}
		}
	}
	if f.Raw != "" {
		fmt.Print(f.Raw)
	}
	for _, line := range f.Stderr {
		fmt.Fprintln(os.Stderr, line)
	}
	os.Exit(f.Exit)
}

func fixtureInput(t *testing.T, f fixture) Input {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "fixture.json")
	body, err := json.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FOFA_WORLD_FIXTURE", path)
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return Input{ClientPath: binary, Workspace: dir, Query: `protocol="http"`, MaxAssets: 10,
		BatchSize: 2, Concurrency: 2, Timeout: time.Second, FlushInterval: 5 * time.Millisecond}
}

func snapshotSink(t *testing.T) (Publish, func() State) {
	t.Helper()
	var mutex sync.Mutex
	var latest State
	return func(_ context.Context, body json.RawMessage) error {
			var state State
			if err := json.Unmarshal(body, &state); err != nil {
				return err
			}
			mutex.Lock()
			latest = state
			mutex.Unlock()
			return nil
		}, func() State {
			mutex.Lock()
			defer mutex.Unlock()
			return latest
		}
}

func TestFirstCollectedAssetIsProbedBeforeTheNextBatchArrives(t *testing.T) {
	request := make(chan struct{}, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		request <- struct{}{}
		w.WriteHeader(403)
	}))
	defer server.Close()
	a := serverAsset(t, server.URL)
	gate := filepath.Join(t.TempDir(), "next-batch")
	f := fixture{Rows: []json.RawMessage{row(a.IP, a.Port, "http", server.URL), row(a.IP, 22, "ssh", "")}, Gate: gate, WaitAfter: 1,
		Stderr: []string{"2026/11/17 10:00:00 dump data of query: fixture", "2026/11/17 10:00:00 size: 2/2, 100.00%"}}
	input := fixtureInput(t, f)
	sink, latest := snapshotSink(t)
	done := make(chan error, 1)
	go func() { _, err := Run(t.Context(), input, sink); done <- err }()
	select {
	case <-request:
	case <-time.After(5 * time.Second):
		t.Fatal("first batch did not reach the local probe while collection was waiting")
	}
	deadline := time.Now().Add(time.Second)
	for latest().Probe.Responded != 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if state := latest(); state.Probe.Responded != 1 || state.Collection.Phase != "collecting" {
		t.Fatalf("live state not published before next batch: %+v", state)
	}
	if err := os.WriteFile(gate, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	state := latest()
	if state.Collection.Completeness != "complete" || state.Collection.Discovered != 2 || state.Probe.Responded != 1 || state.Probe.Skipped != 1 {
		t.Fatalf("final state=%+v", state)
	}
}

func TestCollectorRetainsRowsAndDoesNotTrustSuccessfulExit(t *testing.T) {
	for _, tc := range []struct {
		name, completeness, reason string
		fixture                    fixture
		max                        int
		wantErr                    bool
	}{
		{"source_error", "partial", "collection_failed", fixture{Stderr: []string{"fetch error: private-key-and-url"}}, 10, true},
		{"successful_unknown", "unknown", "", fixture{}, 10, false},
		{"warning", "unknown", "", fixture{Stderr: []string{"arbitrary private diagnostic", "size: 1/1, 100.00%"}}, 10, false},
		{"mismatched_count", "unknown", "", fixture{Stderr: []string{"size: 3/3, 100.00%"}}, 10, false},
		{"limit", "partial", "limit_reached", fixture{}, 1, false},
		{"bad_json", "partial", "invalid_output", fixture{Raw: "not-json\n"}, 10, true},
		{"unterminated_json", "partial", "invalid_output", fixture{Raw: "not-json"}, 10, true},
		{"exit", "partial", "client_failed", fixture{Exit: 7}, 10, true},
		{"long_line", "partial", "output_limit", fixture{Raw: strings.Repeat("x", maxLineBytes+1)}, 10, true},
		{"bad_progress", "partial", "invalid_progress", fixture{Stderr: []string{"size: 999999999999999999999999999/1, 1.00%"}}, 10, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.fixture.Rows = []json.RawMessage{row("127.0.0.1", 22, "ssh", "")}
			input := fixtureInput(t, tc.fixture)
			input.MaxAssets = tc.max
			sink, latest := snapshotSink(t)
			summary, err := Run(t.Context(), input, sink)
			if (err != nil) != tc.wantErr || summary.Collection.Reason != tc.reason || summary.Collection.Completeness != tc.completeness || summary.Collection.Discovered != 1 {
				t.Fatalf("summary=%+v err=%v", summary, err)
			}
			encoded, _ := json.Marshal(latest())
			if strings.Contains(string(encoded), "private") || err != nil && strings.Contains(err.Error(), "private") {
				t.Fatal("raw subprocess diagnostic leaked")
			}
		})
	}
}

func TestDuplicateAndCoordinateFreeAssetsHaveStableCounts(t *testing.T) {
	a := row("127.0.0.1", 22, "ssh", "")
	var withoutCoords map[string]string
	if err := json.Unmarshal(a, &withoutCoords); err != nil {
		t.Fatal(err)
	}
	delete(withoutCoords, "latitude")
	delete(withoutCoords, "longitude")
	b, _ := json.Marshal(withoutCoords)
	input := fixtureInput(t, fixture{Rows: []json.RawMessage{b, a}})
	sink, latest := snapshotSink(t)
	summary, err := Run(t.Context(), input, sink)
	if err != nil || summary.Collection.Received != 2 || summary.Collection.Discovered != 1 || summary.Probe.Skipped != 1 {
		t.Fatalf("summary=%+v err=%v", summary, err)
	}
	for _, asset := range latest().AssetsByID {
		if asset.Longitude != nil || asset.Latitude != nil {
			t.Fatal("missing coordinates fabricated")
		}
	}
}

func TestCancellationJoinsCollectorAndRunningProbe(t *testing.T) {
	started := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		started <- struct{}{}
		<-r.Context().Done()
	}))
	defer server.Close()
	a := serverAsset(t, server.URL)
	input := fixtureInput(t, fixture{Rows: []json.RawMessage{row(a.IP, a.Port, "http", server.URL)},
		Gate: filepath.Join(t.TempDir(), "never-created"), WaitAfter: 1})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	sink, latest := snapshotSink(t)
	done := make(chan error, 1)
	go func() { _, err := Run(ctx, input, sink); done <- err }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("probe never started")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancellation did not join owned actors")
	}
	if state := latest(); state.Collection.Phase != "interrupted" || state.Probe.Interrupted != 1 || state.Probe.Running != 0 {
		t.Fatalf("cancelled state=%+v", state)
	}
}

func TestPublisherFailureAndInvalidInputsDoNotLeaveWorkRunning(t *testing.T) {
	input := fixtureInput(t, fixture{Rows: []json.RawMessage{row("127.0.0.1", 22, "ssh", "")},
		Gate: filepath.Join(t.TempDir(), "never-created"), WaitAfter: 1})
	want := errors.New("publisher unavailable")
	if _, err := Run(t.Context(), input, func(context.Context, json.RawMessage) error { return want }); !errors.Is(err, want) {
		t.Fatal(err)
	}
	var calls int
	if _, err := Run(t.Context(), input, func(context.Context, json.RawMessage) error {
		calls++
		if calls > 1 {
			return want
		}
		return nil
	}); !errors.Is(err, want) {
		t.Fatal(err)
	}
	input.ClientPath = filepath.Join(t.TempDir(), "absent")
	if _, err := Run(t.Context(), input, func(context.Context, json.RawMessage) error { return nil }); safeFailure(err) != "client_failed" {
		t.Fatal(err)
	}
	input.Query = "\n"
	if _, err := Run(t.Context(), input, nil); safeFailure(err) != "invalid_input" {
		t.Fatal(err)
	}
	input.Query = "query"
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := Run(ctx, input, func(context.Context, json.RawMessage) error { return nil }); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestQueryCannotBecomeAClientFlag(t *testing.T) {
	input := fixtureInput(t, fixture{})
	input.Query = " --fofaURL=private"
	if _, err := Run(t.Context(), input, func(context.Context, json.RawMessage) error { return nil }); safeFailure(err) != "invalid_input" {
		t.Fatal(err)
	}
}

func TestCollectorConfirmsProgressOnlyForTheRequestedQuery(t *testing.T) {
	const query = `protocol="ssh" && country="ZZ"`
	for _, test := range []struct {
		name, suffix, completeness string
	}{
		{"legacy_progress", "", "complete"},
		{"current_query_progress", " for query: " + query, "complete"},
		{"different_query_progress", " for query: other query", "unknown"},
		{"extra_query_text", " for query: " + query + " extra", "unknown"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// New CLI releases repeat the query in progress. Only this request's
			// exact suffix can establish that its returned rows are complete.
			input := fixtureInput(t, fixture{
				Rows:   []json.RawMessage{row("127.0.0.1", 22, "ssh", "")},
				Stderr: []string{"2026/11/18 12:00:00 size: 1/1, 100.00%" + test.suffix},
			})
			input.Query = query
			sink, latest := snapshotSink(t)
			if _, err := Run(t.Context(), input, sink); err != nil {
				t.Fatal(err)
			}
			if got := latest().Collection; got.Completeness != test.completeness || got.Received != 1 {
				t.Fatalf("query-specific progress changed completeness: %+v", got)
			}
		})
	}
}

func TestRealCLIFlushesTheFirstBatchBeforeTheSecondAPIReply(t *testing.T) {
	binary, err := exec.LookPath("fofa")
	if err != nil {
		t.Skip("optional installed FOFA CLI is unavailable")
	}
	probeSeen := make(chan struct{}, 4)
	probeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		probeSeen <- struct{}{}
		w.WriteHeader(403)
	}))
	defer probeServer.Close()
	a := serverAsset(t, probeServer.URL)
	gate := make(chan struct{})
	var release sync.Once
	defer release.Do(func() { close(gate) })
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/info/my":
			fmt.Fprint(w, `{"error":false,"isvip":true,"vip_level":3}`)
		case "/api/v1/search/next":
			var records [][]string
			next := "second"
			if r.URL.Query().Get("next") == "" {
				records = [][]string{
					{"http://one.invalid", a.IP, strconv.Itoa(a.Port), "http", "US", "fixture", "fixture", "-70", "40"},
					{"http://two.invalid", a.IP, strconv.Itoa(a.Port), "http", "US", "fixture", "fixture", "-70", "40"},
				}
			} else {
				select {
				case <-gate:
				case <-r.Context().Done():
					return
				}
				records = [][]string{{"", a.IP, "22", "ssh", "US", "fixture", "fixture", "", ""}}
				next = ""
			}
			if err := json.NewEncoder(w).Encode(map[string]any{"error": false, "size": 3, "results": records, "next": next}); err != nil {
				t.Error(err)
			}
		default:
			t.Errorf("unexpected local CLI API path: %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer api.Close()
	// Every credential/origin input is fictional and local, including the
	// higher-priority URL variable. This test must never use a saved account.
	t.Setenv("FOFA_CLIENT_URL", api.URL+"/?email=fixture&key=fixture&version=v1")
	t.Setenv("FOFA_SERVER", api.URL)
	t.Setenv("FOFA_EMAIL", "fixture")
	t.Setenv("FOFA_KEY", "fixture")
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	input := Input{ClientPath: binary, Workspace: t.TempDir(), Query: `protocol="http"`, MaxAssets: 10,
		BatchSize: 2, Concurrency: 2, Timeout: time.Second, FlushInterval: 5 * time.Millisecond}
	sink, latest := snapshotSink(t)
	done := make(chan error, 1)
	go func() { _, err := Run(ctx, input, sink); done <- err }()
	select {
	case <-probeSeen:
		release.Do(func() { close(gate) })
	case <-ctx.Done():
		t.Fatal("real CLI did not flush the first batch before its next API reply")
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if state := latest(); state.Collection.Completeness != "complete" || state.Collection.Discovered != 3 || state.Probe.Responded != 2 || state.Probe.Skipped != 1 {
		t.Fatalf("real CLI state=%+v", state)
	}
}

func TestBatchPublicationAlsoFlushesAtTheOperationBound(t *testing.T) {
	var rows []json.RawMessage
	for i := range 120 {
		rows = append(rows, row("127.0.0.1", 1000+i, "ssh", ""))
	}
	input := fixtureInput(t, fixture{Rows: rows})
	input.MaxAssets, input.FlushInterval = 150, time.Hour
	var calls int
	if _, err := Run(t.Context(), input, func(context.Context, json.RawMessage) error { calls++; return nil }); err != nil || calls < 3 {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
	input = fixtureInput(t, fixture{Rows: rows})
	input.MaxAssets, input.FlushInterval = 150, time.Hour
	want := errors.New("bounded flush failed")
	calls = 0
	if _, err := Run(t.Context(), input, func(context.Context, json.RawMessage) error {
		calls++
		if calls > 1 {
			return want
		}
		return nil
	}); !errors.Is(err, want) {
		t.Fatal(err)
	}
}

func TestFinalPublicationFailureIsReported(t *testing.T) {
	input := fixtureInput(t, fixture{})
	input.FlushInterval = time.Hour
	var calls int
	want := errors.New("final state unavailable")
	if _, err := Run(t.Context(), input, func(context.Context, json.RawMessage) error {
		calls++
		if calls > 1 {
			return want
		}
		return nil
	}); !errors.Is(err, want) {
		t.Fatal(err)
	}
}

func TestProbeCountersCoverEveryTerminalClass(t *testing.T) {
	assets := make(map[string]Asset)
	for i, status := range []string{"pending", "running", "responded", "no_response", "skipped", "interrupted"} {
		assets[strconv.Itoa(i)] = Asset{Status: status}
	}
	if got := countStatuses(assets); got != (ProbeProgress{1, 1, 1, 1, 1, 1}) {
		t.Fatal(got)
	}
}
