package server_test

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestTaskPanelSettingsRejectMalformedBodiesWithoutChangingSavedSettings(t *testing.T) {
	h := newHarness(t)
	before, err := os.ReadFile(h.app.Config.Path())
	if err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"malformed":     `{"concurrency":`,
		"trailing_json": `{"concurrency":3} {}`,
		"trailing_junk": `{"concurrency":3} broken`,
		"null_object":   `null`,
		"non_object":    `[]`,
		"null_field":    `{"enabled":null}`,
		"unknown_field": `{"concurrency":3,"not_a_panel_setting":1}`,
		"too_large":     `{"fofa_client":"` + strings.Repeat("x", 17<<10) + `"}`,
		"wrong_type":    `{"concurrency":"many"}`,
		"invalid_limit": `{"concurrency":257}`,
		"empty":         `{}`,
	} {
		t.Run(name, func(t *testing.T) {
			request, err := http.NewRequestWithContext(t.Context(), http.MethodPut, h.srv.URL+"/api/task-panels/settings", strings.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("Content-Type", "application/json")
			response, err := h.srv.Client().Do(request)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.StatusCode != http.StatusBadRequest {
				t.Fatalf("status=%d", response.StatusCode)
			}
			after, err := os.ReadFile(h.app.Config.Path())
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before, after) || h.app.Config.Snapshot().TaskPanels.WithDefaults().Concurrency != 16 {
				t.Fatal("rejected body changed saved or live settings")
			}
		})
	}
}

func TestTaskPanelSettingsSaveFailureRetainsOriginalConfigAndLiveValues(t *testing.T) {
	h := newHarness(t)
	before, err := os.ReadFile(h.app.Config.Path())
	if err != nil {
		t.Fatal(err)
	}
	// This private fixture blocks only the atomic-save staging path. The actual
	// config file remains present and byte-identical throughout the request.
	staging := h.app.Config.Path() + ".tmp"
	if err := os.Mkdir(staging, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Remove(staging); err != nil {
			t.Error(err)
		}
	})
	result := h.json(http.MethodPut, "/api/task-panels/settings", map[string]any{"concurrency": 3}, http.StatusBadRequest)
	if !strings.Contains(result["error"].(string), "could not save task panel settings") {
		t.Fatalf("missing save failure: %v", result)
	}
	after, err := os.ReadFile(h.app.Config.Path())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) || h.app.Config.Snapshot().TaskPanels.WithDefaults().Concurrency != 16 {
		t.Fatal("failed atomic save changed saved or live settings")
	}
}

func TestTaskPanelReadFailuresAreHTTPFailuresRatherThanEmptySuccessfulPanels(t *testing.T) {
	h := newHarness(t)
	thread := h.newThread()
	h.json(http.MethodGet, "/api/threads/missing/panels/events", nil, http.StatusNotFound)
	if err := h.app.Store.Close(); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		"/api/threads/" + thread + "/panels",
		"/api/threads/" + thread + "/panels/missing-panel",
		"/api/threads/" + thread + "/panels/events",
	} {
		result := h.json(http.MethodGet, path, nil, http.StatusInternalServerError)
		if result["error"] != "task panel state unavailable" {
			t.Fatalf("read error=%v", result)
		}
	}
}

func TestTaskPanelStreamClosesWhenItsStoreBecomesUnavailable(t *testing.T) {
	h := newHarness(t)
	thread := h.newThread()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, h.srv.URL+"/api/threads/"+thread+"/panels/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := h.srv.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", response.StatusCode)
	}
	scanner := bufio.NewScanner(response.Body)
	for scanner.Scan() && !strings.HasPrefix(scanner.Text(), "data: ") {
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if err := h.app.Store.Close(); err != nil {
		t.Fatal(err)
	}
	for scanner.Scan() {
		if strings.HasPrefix(scanner.Text(), "event: panel_update") {
			t.Fatal("unavailable storage produced an update")
		}
	}
	if err := scanner.Err(); err != nil && err != io.EOF {
		t.Fatalf("stream did not close before its deadline: %v", err)
	}
}

type taskPanelSSEFrame struct{ kind, id, data string }

func openTaskPanelEventFixture(t *testing.T, h *harness, path string) <-chan taskPanelSSEFrame {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, h.srv.URL+path, nil)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	response, err := h.srv.Client().Do(request)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		cancel()
		response.Body.Close()
		t.Fatalf("status=%d", response.StatusCode)
	}
	frames := make(chan taskPanelSSEFrame, 16)
	joined := make(chan struct{})
	go func() {
		defer close(joined)
		defer close(frames)
		scanner := bufio.NewScanner(response.Body)
		scanner.Buffer(make([]byte, 4096), 2<<20)
		var frame taskPanelSSEFrame
		for scanner.Scan() {
			line := scanner.Text()
			switch {
			case strings.HasPrefix(line, "event: "):
				frame.kind = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "id: "):
				frame.id = strings.TrimPrefix(line, "id: ")
			case strings.HasPrefix(line, "data: "):
				frame.data = strings.TrimPrefix(line, "data: ")
			case line == "" && frame.kind != "":
				select {
				case frames <- frame:
				case <-ctx.Done():
					return
				}
				frame = taskPanelSSEFrame{}
			}
		}
	}()
	var stop sync.Once
	t.Cleanup(func() {
		stop.Do(func() { cancel(); response.Body.Close() })
		select {
		case <-joined:
		case <-time.After(time.Second):
			t.Error("event fixture reader did not join after cancellation")
		}
	})
	return frames
}

func TestTaskPanelMultiplexFailureStaysSeparateFromChatAndIsCoalesced(t *testing.T) {
	h := newHarness(t)
	thread := h.newThread()
	frames := openTaskPanelEventFixture(t, h, "/api/threads/"+thread+"/events?panels=1")
	wanted := map[string]bool{"panel_ready": false, "ready": false}
	for !wanted["panel_ready"] || !wanted["ready"] {
		select {
		case frame, open := <-frames:
			if !open || frame.kind != "panel_ready" && frame.kind != "ready" || frame.id != "" {
				t.Fatalf("unexpected initial frame=%+v open=%v", frame, open)
			}
			wanted[frame.kind] = true
		case <-time.After(3 * time.Second):
			t.Fatal("multiplex stream never became ready")
		}
	}
	if err := h.app.Store.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case frame, open := <-frames:
		if !open || frame.kind != "panel_error" || frame.id != "" || !strings.Contains(frame.data, "panel state unavailable") {
			t.Fatalf("panel failure contaminated chat framing: %+v open=%v", frame, open)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("unavailable panel storage was not reported")
	}
	// Two further 200ms polling ticks must retry the read without repeating
	// panel_error, closing the chat, or inventing a chat error/sequence ID.
	select {
	case frame, open := <-frames:
		t.Fatalf("failure was not coalesced or closed the chat: %+v open=%v", frame, open)
	case <-time.After(450 * time.Millisecond):
	}
}

func TestTaskPanelMultiplexRemainsOptInForLegacyConversationStreams(t *testing.T) {
	h := newHarness(t)
	thread := h.newThread()
	frames := openTaskPanelEventFixture(t, h, "/api/threads/"+thread+"/events")
	select {
	case frame, open := <-frames:
		if !open || frame.kind != "ready" || frame.id != "" {
			t.Fatalf("legacy stream gained panel frames: %+v open=%v", frame, open)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("legacy stream never became ready")
	}
	select {
	case frame, open := <-frames:
		t.Fatalf("legacy stream gained polling frames: %+v open=%v", frame, open)
	case <-time.After(450 * time.Millisecond):
	}
}
