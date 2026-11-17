package fofaworld

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"
)

// Run collects bounded CLI NDJSON while probing discovered HTTP origins locally.
// A single reducer owns state and publication; every owned goroutine and child
// is joined before Run returns, including cancellation and publisher failure.
func Run(ctx context.Context, input Input, publish Publish) (Summary, error) {
	if err := validate(input, publish); err != nil {
		return Summary{}, err
	}
	if err := ctx.Err(); err != nil {
		return Summary{}, err
	}
	state := State{Collection: Collection{Phase: "collecting", Completeness: "unknown"},
		AssetsByID: map[string]Asset{}, ResultsByID: map[string]Result{}}
	flush := func() error {
		state.Probe = countStatuses(state.AssetsByID)
		body, err := json.Marshal(state)
		if err != nil {
			return failure("state_encoding")
		}
		return publish(ctx, body)
	}
	if err := flush(); err != nil {
		return Summary{}, err
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	collectCtx, cancelCollect := context.WithCancel(runCtx)
	defer cancelCollect()
	events := make(chan event, 2*input.Concurrency+2)
	jobs := make(chan Asset)
	go collect(collectCtx, input, events)
	startWorkers(runCtx, input, jobs, events)
	ticker := time.NewTicker(input.FlushInterval)
	defer ticker.Stop()
	var pending []Asset
	var firstErr error
	var collectionDone, workersDone, jobsClosed, limit, warning bool
	var lastProgress, changes int
	done := runCtx.Done()
	for !collectionDone || !workersDone {
		if (collectionDone && len(pending) == 0 || runCtx.Err() != nil) && !jobsClosed {
			close(jobs)
			jobsClosed = true
			pending = nil
		}
		var submit chan<- Asset
		var next Asset
		if !jobsClosed && len(pending) > 0 {
			submit, next = jobs, pending[0]
		}
		select {
		case <-done:
			done = nil
			cancelCollect()
			state.Collection.Phase, state.Collection.Completeness, state.Collection.Reason = "interrupted", "partial", "cancelled"
			changes++
		case submit <- next:
			pending = pending[1:]
		case e := <-events:
			switch e.kind {
			case "asset":
				if limit || runCtx.Err() != nil {
					continue
				}
				state.Collection.Received++
				if _, exists := state.AssetsByID[e.asset.ID]; !exists {
					state.AssetsByID[e.asset.ID] = e.asset
					state.Collection.Discovered++
					if e.asset.Status == "skipped" {
						state.ResultsByID[e.asset.ID] = Result{Status: "skipped", Error: "non_http", FinishedAt: timestamp()}
					} else {
						pending = append(pending, e.asset)
					}
				}
				// Bound both imported rows and unique assets. Duplicate-heavy
				// sources cannot keep a collector alive beyond its row budget.
				if state.Collection.Received >= input.MaxAssets {
					limit = true
					cancelCollect()
				}
			case "running":
				asset := state.AssetsByID[e.asset.ID]
				asset.Status = "running"
				state.AssetsByID[asset.ID] = asset
			case "result":
				asset := state.AssetsByID[e.asset.ID]
				asset.Status = e.result.Status
				state.AssetsByID[asset.ID], state.ResultsByID[asset.ID] = asset, e.result
			case "progress":
				total := e.total
				state.Collection.ReportedTotal, lastProgress = &total, e.count
			case "source_warning":
				warning = true
			case "source_error":
				if firstErr == nil {
					firstErr = e.err
				}
				state.Collection.Reason = "collection_failed"
			case "collection_done":
				collectionDone = true
				if runCtx.Err() == nil {
					state.Collection.Phase = "finished"
					if limit {
						state.Collection.Completeness, state.Collection.Reason = "partial", "limit_reached"
					} else if e.err != nil || firstErr != nil {
						state.Collection.Phase, state.Collection.Completeness = "failed", "partial"
						if firstErr == nil {
							firstErr = e.err
							state.Collection.Reason = safeFailure(e.err)
						}
					} else if total := state.Collection.ReportedTotal; total != nil && !warning && lastProgress == state.Collection.Received && lastProgress >= *total {
						state.Collection.Completeness = "complete"
					}
				}
			case "workers_done":
				workersDone = true
			}
			changes++
		case <-ticker.C:
			if changes == 0 || runCtx.Err() != nil {
				continue
			}
			if err := flush(); err != nil {
				firstErr = err
				cancel()
			}
			changes = 0
		}
		if changes >= 100 && runCtx.Err() == nil {
			if err := flush(); err != nil {
				firstErr = err
				cancel()
			}
			changes = 0
		}
	}
	if runCtx.Err() != nil {
		state.Collection.Phase, state.Collection.Completeness, state.Collection.Reason = "interrupted", "partial", "cancelled"
		for id, asset := range state.AssetsByID {
			if asset.Status == "pending" || asset.Status == "running" {
				asset.Status = "interrupted"
				state.AssetsByID[id] = asset
				state.ResultsByID[id] = Result{Status: "interrupted", Error: "cancelled", FinishedAt: timestamp()}
			}
		}
	}
	if err := flush(); err != nil && firstErr == nil {
		firstErr = err
	}
	return Summary{Collection: state.Collection, Probe: state.Probe}, errors.Join(firstErr, ctx.Err())
}

func validate(input Input, publish Publish) error {
	if strings.TrimSpace(input.Query) == "" || strings.HasPrefix(strings.TrimSpace(input.Query), "-") || strings.ContainsAny(input.Query, "\x00\r\n") || input.ClientPath == "" || input.Workspace == "" || input.MaxAssets < 1 || input.BatchSize < 1 || input.Concurrency < 1 || input.Timeout <= 0 || input.FlushInterval <= 0 || publish == nil {
		return failure("invalid_input")
	}
	return nil
}

func startWorkers(ctx context.Context, input Input, jobs <-chan Asset, events chan<- event) {
	startWorkersUsing(ctx, input, jobs, events, probe)
}

func startWorkersUsing(ctx context.Context, input Input, jobs <-chan Asset, events chan<- event, probeFn func(context.Context, Asset, time.Duration) Result) {
	var group sync.WaitGroup
	for range input.Concurrency {
		group.Go(func() {
			for {
				select {
				case <-ctx.Done():
					return
				case a, ok := <-jobs:
					if !ok {
						return
					}
					select {
					case events <- event{kind: "running", asset: a}:
					case <-ctx.Done():
						return
					}
					result := probeFn(ctx, a, input.Timeout)
					// The reducer remains alive through workers_done. Even after
					// Stop, join the terminal receipt: cancellation cannot erase
					// a response the actual HTTP client already observed.
					events <- event{kind: "result", asset: a, result: result}
				}
			}
		})
	}
	go func() { group.Wait(); events <- event{kind: "workers_done"} }()
}

func countStatuses(assets map[string]Asset) ProbeProgress {
	var p ProbeProgress
	for _, a := range assets {
		switch a.Status {
		case "pending":
			p.Pending++
		case "running":
			p.Running++
		case "responded":
			p.Responded++
		case "no_response":
			p.NoResponse++
		case "skipped":
			p.Skipped++
		case "interrupted":
			p.Interrupted++
		}
	}
	return p
}

func timestamp() string { return time.Now().UTC().Format(time.RFC3339Nano) }

func safeFailure(err error) string {
	var e *Failure
	if errors.As(err, &e) {
		return e.Code
	}
	return "client_failed"
}
