package easm

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"
)

// Cancellation ends the request's authority, but Run still owns the started
// aggregation until it returns. This checks local joining, not remote cancellation.
func TestCancellationWaitsForStartedAggregationToReturn(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	started, release, callExited := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var once sync.Once
	releaseCall := func() { once.Do(func() { close(release) }) }
	caller := CallerFunc(func(callCtx context.Context, _ string, _ map[string]any) (json.RawMessage, error) {
		close(started)
		defer close(callExited)
		<-release // An admitted call may take time to acknowledge cancellation.
		return nil, callCtx.Err()
	})
	var out published
	returned, exited := make(chan error, 1), make(chan struct{})
	go func() {
		defer close(exited)
		_, err := Run(ctx, Input{Subject: "Subject", Query: metricQuery}, onlyRoles(RoleHeat), nil, caller, out.publish)
		returned <- err
	}()
	t.Cleanup(func() {
		cancel()
		releaseCall()
		select {
		case <-started:
			select {
			case <-callExited:
			case <-time.After(5 * time.Second):
				t.Error("aggregation did not exit after release")
			}
		default:
		}
		select {
		case <-exited:
		case <-time.After(5 * time.Second):
			t.Error("producer did not exit after releasing its aggregation")
		}
	})
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("aggregation never started")
	}
	cancel()
	var runErr error
	returnedEarly := false
	select {
	case runErr = <-returned:
		returnedEarly = true
		t.Errorf("Run returned before its started aggregation exited: %v", runErr)
	case <-time.After(100 * time.Millisecond):
	}
	releaseCall()
	if !returnedEarly {
		select {
		case runErr = <-returned:
		case <-time.After(5 * time.Second):
			t.Fatal("Run did not return after its aggregation exited")
		}
	}
	if !errors.Is(runErr, context.Canceled) {
		t.Errorf("Run returned %v, want context cancellation", runErr)
	}
	select {
	case <-callExited:
	default:
		t.Error("Run returned without joining its caller")
	}
}
