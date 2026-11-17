package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LubyRuffy/eino-swarm/internal/config"
	"github.com/LubyRuffy/eino-swarm/internal/lease"
	"github.com/LubyRuffy/eino-swarm/internal/server"
	"github.com/LubyRuffy/eino-swarm/internal/store"
)

// A supervisor needs a counted, acknowledged client and a meaningful exit on
// connection loss. Probe the real web entry and HTTP handshakes, without a tab.
func TestHeadlessWebOwnsPresenceAndReportsItsLoss(t *testing.T) {
	for _, scenario := range []string{"healthy", "configured headless", "reserve refused", "short streams", "cancel handshake"} {
		t.Run(scenario, func(t *testing.T) {
			dir := t.TempDir()
			cfg, err := config.Load(dir)
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "configured headless" {
				cfg.Server.OpenBrowser = false
				if err := cfg.Save(); err != nil {
					t.Fatal(err)
				}
			}
			holder := startLeaseHolder(t, dir)
			var posts, ready atomic.Int32
			requestStarted := make(chan struct{}, 1)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/meta" {
					_ = json.NewEncoder(w).Encode(map[string]any{"data_dir": dir, "mock": true, "version": version})
					return
				}
				if r.Method == http.MethodPost {
					var body struct {
						Surface string `json:"surface"`
						PID     int    `json:"pid"`
					}
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Surface != "web" || body.PID != os.Getpid() {
						t.Errorf("wrong web presence: %+v %v", body, err)
					}
					posts.Add(1)
					if scenario == "reserve refused" {
						w.WriteHeader(503)
						io.WriteString(w, `{"code":"engine_stopping"}`)
					} else {
						io.WriteString(w, `{"id":"pc_0123456789abcdef"}`)
					}
					return
				}
				if scenario == "cancel handshake" {
					requestStarted <- struct{}{}
					<-r.Context().Done()
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				io.WriteString(w, ": held\n\n")
				w.(http.Flusher).Flush()
				if scenario != "short streams" {
					<-r.Context().Done()
				}
			}))
			defer srv.Close()
			self := lease.ThisBuild(version)
			if err := lease.Publish(dir, lease.Record{PID: holder.Process.Pid, URL: srv.URL, Mock: true, Version: self.Version, Dev: self.Dev, Ino: self.Ino}); err != nil {
				t.Fatal(err)
			}
			stop := make(chan os.Signal, 1)
			defer func() {
				select {
				case stop <- os.Interrupt:
				default:
				}
			}()
			done := make(chan error, 1)
			args := []string{"--mock", "--data-dir", dir}
			if scenario != "configured headless" {
				args = append(args, "--no-open")
			}
			go func() {
				done <- serveWeb(args, stop, func(string) {
					ready.Add(1)
					if scenario != "short streams" {
						stop <- os.Interrupt
					}
				})
			}()
			if scenario == "cancel handshake" {
				select {
				case <-requestStarted:
					stop <- os.Interrupt
				case <-time.After(5 * time.Second):
					t.Fatal("no handshake")
				}
			}
			select {
			case err := <-done:
				switch scenario {
				case "reserve refused":
					if !errors.Is(err, server.ErrEngineStopping) || ready.Load() != 0 {
						t.Fatalf("refused startup became ready: %v", err)
					}
				case "short streams":
					if !errors.Is(err, io.EOF) || posts.Load() != 4 || ready.Load() != 1 {
						t.Fatalf("unbounded/silent recovery: %v posts=%d ready=%d", err, posts.Load(), ready.Load())
					}
				default:
					if err != nil || posts.Load() != 1 {
						t.Fatalf("stop failed: %v posts=%d", err, posts.Load())
					}
				}
			case <-time.After(5 * time.Second):
				t.Fatal("web waiter did not terminate")
			}
			if !lease.Busy(dir) {
				t.Fatal("web stopped another engine")
			}
		})
	}
}

// Advancing the actual idle watcher past its grace must not end a headless
// service. Releasing only this waiter lets an otherwise idle engine exit.
func TestHeadlessWebSurvivesIdleAndReleasesItsPresence(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	engineReady := make(chan string, 1)
	engineDone := make(chan error, 1)
	var advance atomic.Bool
	var samples atomic.Int32
	at := time.Now().Add(time.Hour)
	go func() {
		engineDone <- serveEngine(ctx, engineOpts{DataDir: dir, Addr: "127.0.0.1:0", Mock: true,
			Ready: func(base string) { engineReady <- base },
			Watch: lease.Watcher{StartGrace: time.Millisecond, IdleGrace: time.Millisecond, Poll: time.Millisecond,
				Now: func() time.Time {
					if advance.Load() {
						return at.Add(time.Hour + time.Duration(samples.Add(1))*time.Millisecond)
					}
					return at
				}},
		})
	}()
	select {
	case <-engineReady:
	case err := <-engineDone:
		t.Fatal(err)
	case <-time.After(10 * time.Second):
		t.Fatal("no engine")
	}
	stop := make(chan os.Signal, 1)
	defer func() {
		select {
		case stop <- os.Interrupt:
		default:
		}
	}()
	webReady := make(chan string, 1)
	webDone := make(chan error, 1)
	go func() {
		webDone <- serveWeb([]string{"--mock", "--no-open", "--data-dir", dir}, stop, func(base string) { webReady <- base })
	}()
	var base string
	select {
	case base = <-webReady:
	case err := <-webDone:
		t.Fatal(err)
	case <-time.After(10 * time.Second):
		t.Fatal("no web client")
	}
	advance.Store(true)
	deadline := time.Now().Add(time.Second)
	for samples.Load() < 5 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if samples.Load() < 5 {
		t.Fatal("idle watcher never advanced")
	}
	if body := httpGet(t, base+"/api/meta"); !strings.Contains(body, `"surface":"web"`) {
		t.Fatalf("no headless presence: %s", body)
	}
	stop <- os.Interrupt
	select {
	case err := <-webDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("web did not stop")
	}
	select {
	case err := <-engineDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("web leaked engine presence")
	}
}

// Ctrl-C in `zwai web` stops the waiter, not the engine. A turn left running
// is not a user Stop, so it must not be marked cancelled.
func TestWebWaiterStopLeavesTheEngineUp(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("OPENAI_BASE_URL", "")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("OPENAI_MODEL", "")
	t.Cleanup(stopTestEngine)

	stop := make(chan os.Signal, 1)
	urls := make(chan string, 1)
	errs := make(chan error, 1)
	go func() {
		errs <- serveWeb(
			[]string{"--mock", "--no-open", "--addr", "127.0.0.1:0", "--data-dir", dir},
			stop,
			func(url string) { urls <- url },
		)
	}()

	var url string
	select {
	case url = <-urls:
	case err := <-errs:
		t.Fatalf("the server stopped before it was listening: %v", err)
	case <-time.After(30 * time.Second):
		t.Fatal("the server never came up")
	}

	body := httpGet(t, url+"/api/meta")
	if !strings.Contains(body, `"surface":"web"`) {
		t.Fatalf("headless waiter does not hold a web client: %s", body)
	}
	if !strings.Contains(body, `"mode":"engine"`) {
		t.Fatalf("meta: %s", body)
	}
	resp, err := http.Post(url+"/api/threads", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	var created struct {
		Thread store.Thread `json:"thread"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&created)
	resp.Body.Close()
	resp, err = http.Post(url+"/api/threads/"+created.Thread.ID+"/turns",
		"application/json", strings.NewReader(`{"text":"something long"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	stop <- os.Interrupt
	select {
	case err := <-errs:
		if err != nil {
			t.Fatalf("web returned %v", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("the waiter did not return on a signal")
	}
	if got := httpGet(t, url+"/api/meta"); !strings.Contains(got, `"mode":"engine"`) {
		t.Fatalf("the waiter stopped the engine: %s", got)
	}
	stopTestEngine()
	deadline := time.Now().Add(10 * time.Second)
	var down bool
	for time.Now().Before(deadline) {
		resp, err := http.Get(url + "/api/meta")
		if err != nil {
			down = true
			break
		}
		resp.Body.Close()
		time.Sleep(20 * time.Millisecond)
	}
	if !down {
		t.Fatal("the engine did not exit after it was stopped")
	}

	st, err := store.Open(filepath.Join(dir, "zwai.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	turns, err := st.ListTurns(created.Thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) == 0 {
		t.Fatal("the turn was not recorded")
	}
	for _, turn := range turns {
		if turn.Status == store.TurnCancelled {
			t.Fatalf("quit recorded a user stop: %+v", turn)
		}
	}
}
