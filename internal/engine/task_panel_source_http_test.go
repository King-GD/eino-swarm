package engine

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"testing"
	"time"

	"github.com/LubyRuffy/eino-swarm/internal/config"
	"github.com/LubyRuffy/eino-swarm/internal/store"
	"github.com/LubyRuffy/eino-swarm/internal/taskpanel"
)

// Real model -> Registry tool -> CLI process -> local HTTP -> durable panel.
// Cancelling this same boundary must settle its own source, not the next task.
func TestTaskPanelSourceRunsAndStopsThroughActualManagerHTTP(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("shell fixture; producer process support cross-compiled separately")
	}
	for _, stop := range []bool{false, true} {
		t.Run(fmt.Sprintf("stop=%t", stop), func(t *testing.T) {
			began := make(chan struct{}, 1)
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				began <- struct{}{}
				if stop {
					<-r.Context().Done()
					return
				}
				w.WriteHeader(http.StatusForbidden)
			}))
			defer target.Close()
			_, portText, err := net.SplitHostPort(target.Listener.Addr().String())
			if err != nil {
				t.Fatal(err)
			}
			port, _ := strconv.Atoi(portText)
			row, _ := json.Marshal(map[string]string{"host": "http://map-fixture.test:" + portText, "ip": "127.0.0.1", "port": portText, "protocol": "http", "country": "GB", "region": "", "city": "", "longitude": "-0.1", "latitude": "51.5"})
			client := filepath.Join(t.TempDir(), "fixture-client")
			if err := os.WriteFile(client, []byte("#!/bin/sh\nprintf '%s\\n' '"+string(row)+"'\n"), 0700); err != nil {
				t.Fatal(err)
			}
			e, fixture := newScheduleCompletionHTTPEngine(t, func(w http.ResponseWriter, _ *http.Request, step int, request scheduleCompletionRequest) {
				switch step {
				case 1:
					writeScheduleCompletionTool(w, request, "open_task_panel", `{"plugin_id":"fofa-world-map"}`)
				case 2:
					var panel taskpanel.Panel
					if err := json.Unmarshal([]byte(request.Messages[len(request.Messages)-1].Content), &panel); err != nil {
						t.Error(err)
					}
					writeScheduleCompletionTool(w, request, "run_panel_source", fmt.Sprintf(`{"panel_id":%q,"input":{"query":"fixture supplied by task"}}`, panel.ID))
				default:
					writeContextFlowReply(w, request.Stream, map[string]any{"content": "The collected observations are available."}, "stop")
				}
			})
			if err := e.cfg.Update(func(c *config.Config) error {
				c.TaskPanels.FOFAClient = client
				c.TaskPanels.TimeoutSeconds = 2
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			thread, err := e.CreateThread("Source fixture", "", "")
			if err != nil {
				t.Fatal(err)
			}
			turn, err := e.StartTurn(thread.ID, "Collect the supplied local fixture observations.")
			if err != nil {
				t.Fatal(err)
			}
			select {
			case <-began:
			case <-time.After(10 * time.Second):
				t.Fatal("source did not make local request")
			}
			if stop {
				// A request starting is not proof that its row was committed to
				// the panel. Wait for that durable observation before Stop.
				deadline := time.Now().Add(5 * time.Second)
				for {
					panels, readErr := e.store.TaskPanels(t.Context(), thread.ID)
					if readErr != nil {
						t.Fatal(readErr)
					}
					var visible struct {
						Assets map[string]any `json:"assets_by_id"`
					}
					if len(panels) > 0 {
						if err := json.Unmarshal(panels[0].State, &visible); err != nil {
							t.Fatal(err)
						}
					}
					if len(visible.Assets) == 1 {
						break
					}
					if time.Now().After(deadline) {
						t.Fatal("started request never reached durable panel")
					}
					time.Sleep(10 * time.Millisecond)
				}
				if err := e.Interrupt(thread.ID); err != nil {
					t.Fatal(err)
				}
			}
			got := waitForTurn(t, e, turn.ID)
			waitSettled(t, e, thread.ID)
			panels, err := e.store.TaskPanels(t.Context(), thread.ID)
			if err != nil || len(panels) != 1 {
				t.Fatalf("panels=%v err=%v", panels, err)
			}
			panel := panels[0]
			var state struct {
				Probe struct {
					Responded int `json:"responded"`
				}
				Assets map[string]struct {
					Port int `json:"port"`
				} `json:"assets_by_id"`
			}
			if err := json.Unmarshal(panel.State, &state); err != nil {
				t.Fatal(err)
			}
			if len(state.Assets) != 1 {
				t.Fatalf("state=%s", panel.State)
			}
			for _, asset := range state.Assets {
				if asset.Port != port {
					t.Fatal("asset identity changed")
				}
			}
			if stop {
				if got.Status != store.TurnCancelled || panel.SourceStatus != "interrupted" {
					t.Fatalf("turn=%s source=%s", got.Status, panel.SourceStatus)
				}
			} else if got.Status != store.TurnDone || state.Probe.Responded != 1 || len(fixture.snapshot()) != 3 {
				t.Fatalf("turn=%+v state=%s calls=%d", got, panel.State, len(fixture.snapshot()))
			}
		})
	}
}
