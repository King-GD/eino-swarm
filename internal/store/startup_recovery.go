package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// StartupRecoveryCandidate combines persisted recovery state with outstanding
// owners in their original tables. Running turns, panels and schedule reports
// already have durable states, so no second copy of those states is needed.
type StartupRecoveryCandidate struct {
	ThreadID       string
	WorkerRevision int64
	Running        bool
	Parked         bool
	Wake           bool
	Generations    bool
	Panel          bool
	Report         bool
	MCP            bool
}

func (s *Store) StartupRecoveryCandidates(ctx context.Context, now time.Time, goalLimit int) ([]StartupRecoveryCandidate, error) {
	var out []StartupRecoveryCandidate
	err := s.db.WithContext(ctx).Raw(`
		WITH evidence AS (
			SELECT t.id AS thread_id, t.sort_rank, t.last_active_at,
				COALESCE(state.worker_revision,0) AS worker_revision,
				state.no_parked_at,state.no_wake_at,COALESCE(state.worker_stop_hint,0) AS worker_stop_hint,
				latest.id AS latest_id,latest.status,latest.schedule_continue,
				EXISTS(SELECT 1 FROM turns WHERE thread_id=t.id AND status='running') AS running,
				EXISTS(SELECT 1 FROM task_panel_rows WHERE thread_id=t.id AND source_status='running' AND hidden=0) AS panel,
				EXISTS(SELECT 1 FROM pending_schedule_answers WHERE thread_id=t.id AND kind='report' AND payload<>'') AS report,
				EXISTS(SELECT 1 FROM startup_recovery_generations WHERE thread_id=t.id) AS generations,
				EXISTS(SELECT 1 FROM schedules WHERE thread_id=t.id AND kind='thread' AND status='active') AS armed,
				EXISTS(SELECT 1 FROM schedules WHERE thread_id=t.id AND kind='thread' AND status='active' AND next_run_at>?) AS future_wake,
				COALESCE(t.plan_mode,0)=0 AS execution_allowed,
				TRIM(COALESCE(t.goal,''))<>'' AND COALESCE(t.goal_complete,0)=0 AND COALESCE(t.goal_blocked,0)=0
					AND COALESCE(t.goal_capped,0)=0 AND COALESCE(t.goal_idle,0)=0 AND COALESCE(t.goal_auto_turns,0)<? AS goal_owner
			FROM threads t
			LEFT JOIN startup_recovery_threads state ON state.thread_id=t.id
			LEFT JOIN turns latest ON latest.id=(SELECT id FROM turns WHERE thread_id=t.id ORDER BY seq DESC LIMIT 1)
		), planned AS (
			SELECT *,
				(status='done' OR (status='error' AND schedule_continue=1 AND EXISTS(
					SELECT 1 FROM events WHERE thread_id=evidence.thread_id AND turn_id=latest_id AND kind='workers_retained'))) AS may_retain
			FROM evidence
		), candidates AS (
			SELECT thread_id,sort_rank,last_active_at,worker_revision,running,panel,
				report AND latest_id IS NOT NULL AS report,
				generations AND latest_id IS NOT NULL AS generations,
				COALESCE(may_retain AND execution_allowed AND (armed OR goal_owner) AND
					(no_parked_at IS NULL OR no_parked_at<>worker_revision) AND EXISTS(
					SELECT 1 FROM events WHERE thread_id=planned.thread_id AND turn_id=latest_id AND kind='engine_restart_checkpoint'),0) AS parked,
				future_wake AND latest_id IS NOT NULL AND worker_stop_hint=1 AND (no_wake_at IS NULL OR no_wake_at<>worker_revision) AS wake,
				CASE WHEN may_retain THEN
					COALESCE((SELECT MAX(seq) FROM events INDEXED BY idx_evt_thread_turn_kind_seq
						WHERE thread_id=planned.thread_id AND turn_id=latest_id AND kind='mcp_execution_blocked'),0)>
					COALESCE((SELECT MAX(seq) FROM events INDEXED BY idx_evt_thread_turn_kind_seq
						WHERE thread_id=planned.thread_id AND turn_id=latest_id AND kind='failure_continued'),0)
				ELSE 0 END AS mcp
			FROM planned
		)
		SELECT thread_id,worker_revision,running,parked,wake,generations,panel,report,mcp
		FROM candidates WHERE running OR parked OR wake OR generations OR panel OR report OR mcp
		ORDER BY sort_rank ASC,last_active_at DESC,thread_id ASC`, now.UTC(), goalLimit).Find(&out).Error
	if err != nil {
		return nil, fmt.Errorf("store: list startup recovery candidates: %w", err)
	}
	return out, nil
}

// RememberSettledStartupWorkers certifies a negative result at the exact
// revision examined. A later receipt or rewind invalidates it atomically.
func (s *Store) RememberSettledStartupWorkers(ctx context.Context, threadID string, revision int64, noParked, noWake bool) error {
	err := s.db.WithContext(ctx).Exec(`UPDATE startup_recovery_threads SET
		no_parked_at=CASE WHEN ? THEN worker_revision ELSE no_parked_at END,
		no_wake_at=CASE WHEN ? THEN worker_revision ELSE no_wake_at END
		WHERE thread_id=? AND worker_revision=?`, noParked, noWake, threadID, revision).Error
	if err != nil {
		return fmt.Errorf("store: certify settled startup workers: %w", err)
	}
	return nil
}

// BackfillStartupRecovery repairs only conversations with missing or
// invalidated projections. Each finished conversation is committed separately
// so cancellation or power loss resumes at the remaining dirty rows.
func (s *Store) BackfillStartupRecovery(ctx context.Context) (int, error) {
	var threads []string
	if err := s.db.WithContext(ctx).Table("startup_recovery_threads").Where("generation_dirty=1").Order("thread_id").Pluck("thread_id", &threads).Error; err != nil {
		return 0, fmt.Errorf("store: list generation backfill: %w", err)
	}
	for i, threadID := range threads {
		if err := s.backfillStartupGenerationThread(ctx, threadID); err != nil {
			return i, fmt.Errorf("store: rebuild startup generations for %s: %w", threadID, err)
		}
	}
	return len(threads), nil
}

func (s *Store) backfillStartupGenerationThread(ctx context.Context, threadID string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		active, err := readStartupGenerationProjection(tx, threadID)
		if err != nil {
			return err
		}
		table := tx.Table("startup_recovery_generations")
		if err := table.Where("thread_id=?", threadID).Delete(&startupRecoveryGeneration{}).Error; err != nil {
			return err
		}
		if len(active) != 0 {
			if err := table.CreateInBatches(active, 100).Error; err != nil {
				return err
			}
		}
		return tx.Exec(`UPDATE startup_recovery_threads SET generation_dirty=0, worker_stop_hint=EXISTS(
			SELECT 1 FROM events AS e INDEXED BY idx_evt_thread_kind_seq
			WHERE e.thread_id=? AND e.kind='finished' AND `+fmt.Sprintf(startupWorkerStopHint, "e")+`)
			WHERE thread_id=?`, threadID, threadID).Error
	})
}

func readStartupGenerationProjection(tx *gorm.DB, threadID string) (active []startupRecoveryGeneration, err error) {
	rows, err := tx.Raw(`SELECT id,kind,COALESCE(message_id,''),
			CASE WHEN kind='generation_interrupted' THEN (`+startupPendingGeneration("text")+`) ELSE 0 END
		FROM events INDEXED BY idx_evt_thread_kind_seq
		WHERE thread_id=? AND kind IN (`+startupGenerationKinds+`) ORDER BY seq,id`, threadID).Rows()
	if rows != nil {
		defer func() { err = errors.Join(err, rows.Close()) }()
	}
	if err != nil {
		return nil, err
	}
	byMessage := map[string]startupRecoveryGeneration{}
	for rows.Next() {
		var id uint
		var kind, messageID string
		var pending bool
		if err := rows.Scan(&id, &kind, &messageID, &pending); err != nil {
			return nil, err
		}
		entry := byMessage[messageID]
		switch kind {
		case "generation_started":
			entry.OpenEventID = id
		case "generation_completed":
			entry.OpenEventID = 0
		case "generation_interrupted":
			entry.OpenEventID, entry.PendingEventID = 0, 0
			if pending {
				entry.PendingEventID = id
			}
		case "generation_recovered":
			entry.PendingEventID = 0
		}
		if entry.OpenEventID == 0 && entry.PendingEventID == 0 {
			delete(byMessage, messageID)
		} else {
			entry.ThreadID, entry.MessageID = threadID, messageID
			byMessage[messageID] = entry
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, entry := range byMessage {
		active = append(active, entry)
	}
	return active, nil
}

// StartupGenerationReceipts fetches only the original open and pending
// receipts. Historical completions and other event bodies stay on disk.
func (s *Store) StartupGenerationReceipts(ctx context.Context, threadID string) ([]Event, error) {
	var out []Event
	err := s.db.WithContext(ctx).Raw(`SELECT e.* FROM events e WHERE e.id IN (
		SELECT open_event_id FROM startup_recovery_generations WHERE thread_id=? AND open_event_id<>0
		UNION ALL
		SELECT pending_event_id FROM startup_recovery_generations WHERE thread_id=? AND pending_event_id<>0
	) AND e.thread_id=? ORDER BY e.seq,e.id`, threadID, threadID, threadID).Find(&out).Error
	if err != nil {
		return nil, fmt.Errorf("store: read startup generation receipts: %w", err)
	}
	return out, nil
}

// StartupTurns loads the latest turn and the original identities of any
// superseded running turns. Finished historical turn bodies are not needed.
func (s *Store) StartupTurns(ctx context.Context, threadID string) ([]Turn, error) {
	var turns []Turn
	err := s.db.WithContext(ctx).
		Where("thread_id=? AND (status='running' OR seq=(SELECT MAX(seq) FROM turns WHERE thread_id=?))", threadID, threadID).
		Order("seq ASC").Find(&turns).Error
	if err != nil {
		return nil, fmt.Errorf("store: read startup turns: %w", err)
	}
	return turns, nil
}

// StartupTurnReceipts reads the targeted terminal decisions needed by
// recovery that only settles lifecycle receipts.
func (s *Store) StartupTurnReceipts(ctx context.Context, threadID, turnID string, kinds ...string) ([]Event, error) {
	var out []Event
	err := s.db.WithContext(ctx).Where("thread_id=? AND turn_id=? AND kind IN ?", threadID, turnID, kinds).
		Order("seq ASC").Find(&out).Error
	if err != nil {
		return nil, fmt.Errorf("store: read startup turn receipts: %w", err)
	}
	return out, nil
}
