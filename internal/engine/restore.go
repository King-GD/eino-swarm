package engine

import (
	"context"
	"errors"
	"fmt"
	"time"

	swarm "github.com/LubyRuffy/eino-swarm"
	"github.com/LubyRuffy/eino-swarm/internal/store"
)

// RestoreFailures reports conversations whose recovery was attempted but could
// not be resumed. Other conversations and future scheduled checks remain usable.
type RestoreFailures struct {
	Failed int
	Err    error
}

func (e *RestoreFailures) Error() string {
	return fmt.Sprintf("could not restore %d conversation(s): %v", e.Failed, e.Err)
}
func (e *RestoreFailures) Unwrap() error { return e.Err }

// Restore owns startup recovery before scheduling. Only indexed conversations
// enter the recovery loop. Resumptions share one immutable bootstrap snapshot.
func (e *Engine) Restore(ctx context.Context, progress func(completed, total int)) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	started := time.Now()
	backfilled, err := e.store.BackfillStartupRecovery(ctx)
	if err != nil {
		return err
	}
	if backfilled > 0 {
		e.log.Info("engine startup", "phase", "recovery_index_backfilled", "threads", backfilled, "duration_ms", time.Since(started).Milliseconds())
	}
	started = time.Now()
	threads, err := e.store.StartupRecoveryCandidates(ctx, e.clock(), e.cfg.SwarmSnapshot().GoalAutoTurns())
	if err != nil {
		return err
	}
	e.log.Info("engine startup", "phase", "recovery_candidates", "threads", len(threads), "duration_ms", time.Since(started).Milliseconds())
	if progress != nil {
		progress(0, len(threads))
	}
	if len(threads) == 0 || ctx.Err() != nil {
		return ctx.Err()
	}
	schedules, err := e.store.ListSchedules()
	if err != nil {
		return err
	}
	byThread := make(map[string][]store.Schedule)
	for _, row := range schedules {
		byThread[row.ThreadID] = append(byThread[row.ThreadID], row)
	}
	var failures []error
	for i, thread := range threads {
		if err := ctx.Err(); err != nil {
			return err
		}
		e.restoreMu.Lock()
		err := e.restoreCandidate(ctx, thread, byThread[thread.ThreadID])
		e.restoreMu.Unlock()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			e.log.Warn("could not restore a conversation from a previous run", "thread", thread.ThreadID, "err", err)
			failures = append(failures, fmt.Errorf("restore conversation %s: %w", thread.ThreadID, err))
		}
		if progress != nil {
			progress(i+1, len(threads))
		}
	}
	if len(failures) > 0 {
		return &RestoreFailures{Failed: len(failures), Err: errors.Join(failures...)}
	}
	return nil
}

func (e *Engine) restoreThread(ctx context.Context, candidate store.StartupRecoveryCandidate, schedules []store.Schedule) (err error) {
	started := time.Now()
	threadID := candidate.ThreadID
	turns, err := e.store.StartupTurns(ctx, threadID)
	if err != nil || len(turns) == 0 {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	snapshot, err := e.loadRestoreSnapshot(ctx, threadID)
	if err != nil {
		return err
	}
	latest := turns[len(turns)-1]
	defer func() { e.recordRestoreResult(&latest, len(snapshot.events), 1, started, err, ctx.Err()) }()
	running := make([]store.Turn, 0)
	for _, turn := range turns {
		if turn.Status == store.TurnRunning {
			running = append(running, turn)
		}
	}
	resume, drop := pickOrphanedTurns(running)
	for _, turn := range drop {
		reason := "superseded by a later unfinished turn"
		if err := e.store.FinishTurn(turn.ID, store.TurnCancelled, "", reason); err != nil {
			return err
		}
		e.finishScheduledRun(&turn, store.TurnCancelled, "", reason)
		e.record(store.Event{ThreadID: turn.ThreadID, TurnID: turn.ID, Kind: swarm.NotifyError.String(), AgentID: swarm.DefaultManagerID, Err: reason})
	}
	if len(resume) > 0 {
		_, err = e.resumeOrphanedTurn(ctx, resume[0], snapshot)
		return err
	}
	th, err := e.store.GetThread(threadID)
	if err != nil {
		return err
	}
	count, err := e.resumeParkedTurn(ctx, th, &latest, snapshot)
	if err != nil {
		return err
	}
	if count == 0 {
		if st := e.Status(threadID); !st.Running && st.Workers == 0 {
			e.stopOrphanedGenerations(threadID, e.turnHasKind(latest.ID, KindUserStopRequested, snapshot), snapshot)
		}
	}
	interrupted := len(processStoppedWorkers(snapshot.events)) > 0
	if err := e.store.RememberSettledStartupWorkers(ctx, threadID, candidate.WorkerRevision,
		!e.hasParkedRestoreWorkers(&latest, snapshot), !interrupted); err != nil {
		return err
	}
	return e.pullInterruptedThreadWake(threadID, interrupted, schedules)
}

func (e *Engine) pullInterruptedThreadWake(threadID string, interrupted bool, rows []store.Schedule) error {
	if !interrupted {
		return nil
	}
	now := e.clock()
	for _, row := range rows {
		if row.ThreadID != threadID || !shouldPullInterruptedWake(row, e.Status(threadID).Running, now) {
			continue
		}
		if _, err := e.store.UpdateActiveSchedule(row.ID, map[string]any{"next_run_at": now}); err != nil {
			return err
		}
	}
	return nil
}
