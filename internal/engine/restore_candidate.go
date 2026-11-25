package engine

import (
	"context"
	"errors"
	"fmt"
	"time"

	swarm "github.com/LubyRuffy/eino-swarm"
	"github.com/LubyRuffy/eino-swarm/internal/store"
)

func (e *Engine) restoreCandidate(ctx context.Context, candidate store.StartupRecoveryCandidate, schedules []store.Schedule) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	id := candidate.ThreadID
	if candidate.Panel {
		if _, err := e.store.RecoverTaskPanelSourcesForThread(ctx, id); err != nil {
			return fmt.Errorf("recover panel source observations: %w", err)
		}
	}
	if candidate.Running || candidate.Parked {
		return e.restoreThread(ctx, candidate, schedules)
	}
	if candidate.Report {
		if _, err := e.startupScheduleReportCalls(id); err != nil {
			return err
		}
	}
	if candidate.MCP || candidate.Generations {
		// Stop and reconciliation use the original latest turn identity. The
		// remainder of the conversation can stay unopened.
		turns, err := e.store.StartupTurns(ctx, id)
		if err != nil {
			return err
		}
		if len(turns) != 0 {
			if err := e.settleStartupTurnReceipts(ctx, candidate, &turns[len(turns)-1]); err != nil {
				return err
			}
		}
	}
	if !candidate.Wake {
		return nil
	}
	events, err := e.store.ListRosterEvents(id)
	if err != nil {
		return err
	}
	interrupted := len(processStoppedWorkers(events)) > 0
	if !interrupted {
		return e.store.RememberSettledStartupWorkers(ctx, id, candidate.WorkerRevision, false, true)
	}
	return e.pullInterruptedThreadWake(id, true, schedules)
}

func (e *Engine) settleStartupTurnReceipts(ctx context.Context, candidate store.StartupRecoveryCandidate, turn *store.Turn) error {
	started := time.Now()
	kinds := []string{KindUserStopRequested}
	if candidate.MCP {
		kinds = append(kinds, store.KindMCPExecutionBlocked, KindFailureContinued, KindWorkersRetained, KindCleanup,
			swarm.NotifyDone.String(), swarm.NotifyError.String())
	}
	events, err := e.store.StartupTurnReceipts(ctx, turn.ThreadID, turn.ID, kinds...)
	if err != nil {
		return err
	}
	snapshot := &restoreSnapshot{threadID: turn.ThreadID, events: events}
	if candidate.MCP && (turn.Status == store.TurnDone || len(e.retainedWorkerIDs(turn, snapshot)) > 0) {
		if err := e.restoredMCPFailure(turn.ID, snapshot); err != nil {
			e.recordRestoreResult(turn, len(events), 0, started, err, ctx.Err())
			return err
		}
	}
	if !candidate.Generations {
		return nil
	}
	if st := e.Status(turn.ThreadID); st.Running || st.Workers > 0 {
		return nil
	}
	read, checkpoints, err := e.settleIndexedGenerations(ctx, turn.ThreadID, e.turnHasKind(turn.ID, KindUserStopRequested, snapshot))
	e.recordRestoreResult(turn, len(events)+read, checkpoints, started, err, ctx.Err())
	return err
}

func (e *Engine) settleIndexedGenerations(ctx context.Context, threadID string, human bool) (read, checkpoints int, err error) {
	events, err := e.store.StartupGenerationReceipts(ctx, threadID)
	if err != nil {
		return 0, 0, err
	}
	log := &generationLog{open: map[string]store.Event{}, pending: map[string]store.Event{}}
	for _, ev := range events {
		log.apply(ev)
	}
	var snapshot restoreSnapshot
	if len(log.open) > 0 && !human {
		checkpoints++
		snapshot.checkpointEvent, _, err = e.readStartupCheckpoint(ctx, threadID)
		if err != nil {
			return len(events), checkpoints, err
		}
	}
	e.generations.Lock()
	defer e.generations.Unlock()
	if err := ctx.Err(); err != nil {
		return len(events), checkpoints, err
	}
	if cached := e.generations.threads[threadID]; cached != nil {
		log = cached
	}
	// Keep the ordinary lazy generation cache intact. The compact index
	// omits historical nonpending interruptions it may later reconcile.
	if !e.settleOrphanedGenerationLog(log, human, &snapshot) {
		return len(events), checkpoints, errors.New("could not persist the startup generation settlement")
	}
	return len(events), checkpoints, nil
}
