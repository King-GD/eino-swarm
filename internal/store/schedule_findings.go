package store

import (
	"fmt"
	"slices"
)

// Standalone findings and provenance both rely on the kind recorded at claim.
// Retargeting freezes the kind on older runs before changing the schedule.
const standaloneRunKind = `(runs.kind = 'standalone' OR
	(COALESCE(runs.kind, '') = '' AND schedule.kind = 'standalone'))`

// StandaloneFindingsThread finds the first surviving result in the requested
// project. Historical duplicates keep their turns and their own run links.
func (s *Store) StandaloneFindingsThread(scheduleID, projectID string) (*Thread, error) {
	var thread Thread
	res := s.db.Model(&Thread{}).
		Joins("JOIN schedule_runs AS runs ON runs.thread_id = threads.id").
		Joins("JOIN schedules AS schedule ON schedule.id = runs.schedule_id").
		Where("schedule.id = ? AND threads.project_id = ? AND runs.status = ?", scheduleID, projectID, ScheduleRunFindings).
		Where(standaloneRunKind).
		Order("runs.created_at, runs.id").Limit(1).Find(&thread)
	if res.Error != nil {
		return nil, fmt.Errorf("store: find standalone result conversation: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return nil, nil
	}
	return &thread, nil
}

// HasStandaloneFindings protects a shared results conversation from being
// archived by a quiet check, including after a project or destination edit.
func (s *Store) HasStandaloneFindings(threadID, scheduleID string) (bool, error) {
	var n int64
	err := s.db.Table("schedule_runs AS runs").
		Joins("JOIN schedules AS schedule ON schedule.id = runs.schedule_id").
		Where("runs.thread_id = ? AND runs.schedule_id = ? AND runs.status = ?", threadID, scheduleID, ScheduleRunFindings).
		Where(standaloneRunKind).Limit(1).Count(&n).Error
	if err != nil {
		return false, fmt.Errorf("store: check standalone findings: %w", err)
	}
	return n > 0, nil
}

// ScheduleSource describes the standalone job responsible for a conversation.
// The origin is optional for schedules created directly in the inbox.
type ScheduleSource struct {
	ScheduleID      string `json:"schedule_id"`
	OriginThreadID  string `json:"origin_thread_id,omitempty"`
	OriginTitle     string `json:"origin_title,omitempty"`
	OriginAvailable bool   `json:"origin_available,omitempty"`
}

// GetThreadWithScheduleSource adds provenance for clients rendering an
// individual conversation row, including the paired phone's live roster.
func (s *Store) GetThreadWithScheduleSource(id string) (*Thread, error) {
	thread, err := s.GetThread(id)
	if err != nil {
		return nil, err
	}
	rows := []Thread{*thread}
	if err := s.loadScheduleSources(rows); err != nil {
		return nil, err
	}
	return &rows[0], nil
}

// ListThreadsWithScheduleSources is the conversation directory view for
// clients. Engine recovery and backups continue to read thread rows directly.
func (s *Store) ListThreadsWithScheduleSources(includeArchived bool, projectID string) ([]Thread, error) {
	threads, err := s.ListThreads(includeArchived, projectID)
	if err != nil {
		return nil, err
	}
	if err := s.loadScheduleSources(threads); err != nil {
		return nil, err
	}
	return threads, nil
}

func (s *Store) loadScheduleSources(threads []Thread) error {
	// Batch within SQLite's parameter limit. The existing run thread_id
	// index supplies the first matching row in insertion order.
	for batch := range slices.Chunk(threads, 500) {
		ids := make([]string, 0, len(batch))
		for _, th := range batch {
			ids = append(ids, th.ID)
		}
		var rows []struct {
			ResultID string
			ScheduleSource
		}
		err := s.db.Raw(`
			SELECT result.id AS result_id, source.id AS schedule_id, source.origin_thread_id,
			       origin.title AS origin_title, origin.id IS NOT NULL AS origin_available
			FROM threads AS result
			JOIN schedule_runs AS chosen ON chosen.id = (
				SELECT runs.id FROM schedule_runs AS runs
				JOIN schedules AS schedule ON schedule.id = runs.schedule_id
				WHERE runs.thread_id = result.id AND `+standaloneRunKind+`
				ORDER BY runs.rowid LIMIT 1
			)
			JOIN schedules AS source ON source.id = chosen.schedule_id
			LEFT JOIN threads AS origin ON origin.id = source.origin_thread_id
			WHERE result.id IN ?`, ids).Scan(&rows).Error
		if err != nil {
			return fmt.Errorf("store: load conversation schedule sources: %w", err)
		}
		sources := make(map[string]*ScheduleSource, len(rows))
		for i := range rows {
			sources[rows[i].ResultID] = &rows[i].ScheduleSource
		}
		for i := range batch {
			batch[i].ScheduleSource = sources[batch[i].ID]
		}
	}
	return nil
}
