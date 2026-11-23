package main

import (
	"context"
	"errors"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"github.com/LubyRuffy/eino-swarm/internal/desktop"
)

func collectDesktopRecoveryResources(engineURL string) *desktop.RecoveryResources {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cpu := make(chan string, 1)
	go func() {
		output, err := exec.CommandContext(ctx, "/usr/bin/top", "-l", "2", "-s", "1", "-n", "0").Output()
		usage := "unavailable"
		if err == nil {
			for _, line := range strings.Split(string(output), "\n") {
				if strings.HasPrefix(line, "CPU usage: ") {
					usage = strings.TrimSpace(strings.TrimPrefix(line, "CPU usage: "))
				}
			}
		}
		cpu <- usage
	}()
	resources := probeDesktopRecoveryEngine(ctx, engineURL)
	resources.HostCPU = <-cpu
	return resources
}

func probeDesktopRecoveryEngine(ctx context.Context, engineURL string) *desktop.RecoveryResources {
	resources := &desktop.RecoveryResources{EngineHTTP: "unavailable"}
	probeCtx, stopProbe := context.WithTimeout(ctx, 750*time.Millisecond)
	defer stopProbe()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	request, err := http.NewRequestWithContext(probeCtx, http.MethodGet, engineURL+"/api/meta", nil)
	if err == nil {
		response, probeErr := client.Do(request)
		switch {
		case probeErr == nil:
			resources.EngineHTTP = "responded"
			resources.EngineStatus = response.StatusCode
			_ = response.Body.Close()
		case errors.Is(probeErr, context.DeadlineExceeded):
			resources.EngineHTTP = "timeout"
		}
	}
	return resources
}
