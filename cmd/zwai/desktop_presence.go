package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/LubyRuffy/eino-swarm/internal/lease"
	"github.com/LubyRuffy/eino-swarm/internal/server"
)

type desktopPresence struct {
	id      string
	release context.CancelFunc
	fatal   <-chan error
}

type desktopPresenceStream struct {
	surface string
	id      string
	body    io.ReadCloser
	close   func()
	started time.Time
}

func holdDesktopPresence(base string) (*desktopPresence, error) {
	return holdShellPresence(context.Background(), base, "desktop")
}

func holdShellPresence(parent context.Context, base, surface string) (*desktopPresence, error) {
	ctx, cancel := context.WithCancel(parent)
	stream, err := openShellPresence(ctx, base, engineBootWait, surface)
	if err != nil {
		cancel()
		return nil, err
	}
	fatal := make(chan error, 1)
	go maintainDesktopPresence(ctx, base, stream, fatal)
	return &desktopPresence{id: stream.id, release: cancel, fatal: fatal}, nil
}

// Only a stable hold renews the recovery budget. Repeated acknowledged short
// streams must not turn a broken holder into an unbounded reconnect loop.
func maintainDesktopPresence(ctx context.Context, base string, stream *desktopPresenceStream, fatal chan<- error) {
	defer close(fatal)
	remaining := 3
	for {
		_, last := io.Copy(io.Discard, stream.body)
		stable := time.Since(stream.started) >= lease.IdleGrace
		stream.close()
		if ctx.Err() != nil {
			return
		}
		if stable {
			remaining = 3
		}
		if last == nil {
			last = io.EOF
		}
		for {
			if remaining == 0 {
				fatal <- fmt.Errorf("%s: presence recovery exhausted: %w", stream.surface, last)
				return
			}
			remaining--
			timer := time.NewTimer(50 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
			// Three attempts, including their complete POST/GET handshakes,
			// fit inside the engine's idle grace. Never ensure another engine.
			next, err := openShellPresence(ctx, base, min(engineBootWait, lease.IdleGrace/6), stream.surface)
			if ctx.Err() != nil {
				if next != nil {
					next.close()
				}
				return
			}
			if err == nil {
				stream = next
				break
			}
			last = err
		}
	}
}

// The timer bounds the complete handshake, then is stopped after acknowledgement.
// A context deadline on the successful GET would also kill its healthy lifetime.
func openShellPresence(parent context.Context, base string, wait time.Duration, surface string) (stream *desktopPresenceStream, err error) {
	ctx, cancel := context.WithCancelCause(parent)
	timer := time.AfterFunc(wait, func() { cancel(context.DeadlineExceeded) })
	var resp *http.Response
	client := &http.Client{Transport: &http.Transport{
		Proxy: http.ProxyFromEnvironment, DialContext: (&net.Dialer{Timeout: wait}).DialContext,
		ResponseHeaderTimeout: wait, TLSHandshakeTimeout: wait,
	}}
	defer func() {
		timer.Stop()
		if err != nil {
			cancel(err)
			if resp != nil {
				resp.Body.Close()
			}
			client.CloseIdleConnections()
		}
	}()
	id, err := reserveShellPresence(ctx, base, surface)
	if err != nil {
		return nil, err
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/presence/"+id, nil)
	resp, err = client.Do(req)
	if err != nil {
		return nil, err
	}
	if err = checkDesktopPresenceResponse(resp); err != nil {
		return nil, err
	}
	if !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		return nil, errors.New("desktop: presence hold did not acknowledge an event stream")
	}
	if !timer.Stop() {
		return nil, context.DeadlineExceeded
	}
	if cause := context.Cause(ctx); cause != nil {
		return nil, cause
	}
	return &desktopPresenceStream{surface: surface, id: id, body: resp.Body, started: time.Now(), close: func() {
		cancel(context.Canceled)
		resp.Body.Close()
		client.CloseIdleConnections()
	}}, nil
}

func reserveShellPresence(ctx context.Context, base, surface string) (string, error) {
	body, _ := json.Marshal(struct {
		Surface string `json:"surface"`
		PID     int    `json:"pid"`
	}{surface, os.Getpid()})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/api/presence", strings.NewReader(string(body)))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if err := checkDesktopPresenceResponse(resp); err != nil {
		return "", err
	}
	var reserved struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&reserved); err != nil {
		return "", fmt.Errorf("desktop: reserve presence: %w", err)
	}
	if len(reserved.ID) != 19 || !strings.HasPrefix(reserved.ID, "pc_") || strings.ToLower(reserved.ID) != reserved.ID {
		return "", errors.New("desktop: invalid presence id")
	}
	if _, err := hex.DecodeString(reserved.ID[3:]); err != nil {
		return "", errors.New("desktop: invalid presence id")
	}
	return reserved.ID, nil
}

func checkDesktopPresenceResponse(resp *http.Response) error {
	if resp.StatusCode == http.StatusOK {
		return nil
	}
	var failure struct {
		Code string `json:"code"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&failure)
	if resp.StatusCode == http.StatusServiceUnavailable && failure.Code == "engine_stopping" {
		return server.ErrEngineStopping
	}
	return fmt.Errorf("desktop: presence HTTP %d", resp.StatusCode)
}
