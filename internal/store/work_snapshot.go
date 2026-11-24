package store

import (
	"context"
	"database/sql"
	"errors"

	"gorm.io/gorm"
)

const (
	WorkScopeThread  = "thread"
	WorkScopeProject = "project"
)

// Inactive reports whether this record's current state invalidates its use as
// a prerequisite. A later correction can make the same record active again.
func (n NuwaNode) Inactive() bool {
	switch n.Kind {
	case "task":
		return n.Status == "cancelled"
	case "fact", "artifact":
		return n.Status == "retracted" || n.Status == "superseded"
	default:
		return false
	}
}

// WorkRevision identifies a conversation's two visible scopes. A project
// revision belongs to ProjectID and cannot be compared across a move.
type WorkRevision struct {
	ThreadID        string `json:"thread_id"`
	ProjectID       string `json:"project_id"`
	ThreadRevision  int64  `json:"thread_revision"`
	ProjectRevision int64  `json:"project_revision"`
}

type WorkSnapshot struct {
	WorkRevision
	Nodes      []NuwaNode `json:"nodes"`
	HasOwnWork bool       `json:"has_own_work,omitempty"`
}

func migrateWorkScopes(db *gorm.DB) error {
	return db.Transaction(func(tx *gorm.DB) error {
		// AutoMigrate adds the scope with a project default and creates the new
		// indexes before these old owners are removed.
		for _, query := range []string{
			"DROP INDEX IF EXISTS idx_nuwa_revision",
			"DROP INDEX IF EXISTS idx_nuwa_request",
			"UPDATE nuwa_nodes SET scope = 'project' WHERE scope IS NULL OR scope = ''",
		} {
			if err := tx.Exec(query).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func workTurnOwner(tx *gorm.DB, threadID, turnID string, projectID *string, checkRecordedProject, write bool) (Thread, error) {
	var thread Thread
	if err := tx.Select("id", "project_id", "work_revision", "plan_mode").Take(&thread, "id = ?", threadID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return Thread{}, invalidNuwa("source thread does not exist")
		}
		return Thread{}, err
	}
	if projectID != nil && thread.ProjectID != *projectID {
		return Thread{}, invalidNuwa("source thread belongs to another project")
	}
	var turn Turn
	if err := tx.Select("id", "project_id", "thread_id", "status", "work_ledger_read_only", "nuwa_read_only").
		Take(&turn, "id = ?", turnID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return Thread{}, invalidNuwa("source turn does not exist")
		}
		return Thread{}, err
	}
	if turn.ThreadID != thread.ID || turn.Status == TurnCancelled {
		return Thread{}, invalidNuwa("source turn has another owner or was cancelled")
	}
	if checkRecordedProject && turn.ProjectID != thread.ProjectID {
		return Thread{}, invalidNuwa("source turn belongs to another project")
	}
	if write && (thread.PlanMode || turn.WorkLedgerReadOnly || turn.NuwaReadOnly) {
		return Thread{}, invalidNuwa("planning can read work records but cannot write them")
	}
	return thread, nil
}

func visibleWorkNodes(tx *gorm.DB, threadID, projectID string) *gorm.DB {
	query := tx.Model(&NuwaNode{})
	if threadID == "" {
		return query.Where("scope = ? AND project_id = ?", WorkScopeProject, projectID)
	}
	if projectID == "" {
		return query.Where("scope = ? AND thread_id = ?", WorkScopeThread, threadID)
	}
	return query.Where("((scope = ? AND thread_id = ?) OR (scope = ? AND project_id = ?))",
		WorkScopeThread, threadID, WorkScopeProject, projectID)
}

func readProjectWorkRevision(tx *gorm.DB, projectID string) (int64, error) {
	var project Project
	err := tx.Select("nuwa_revision").Take(&project, "id = ?", projectID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, ErrNotFound
	}
	return project.NuwaRevision, err
}

func workRevisionForThread(tx *gorm.DB, thread Thread) (WorkRevision, error) {
	revision := WorkRevision{ThreadID: thread.ID, ProjectID: thread.ProjectID, ThreadRevision: thread.WorkRevision}
	if thread.ProjectID == "" {
		return revision, nil
	}
	var err error
	revision.ProjectRevision, err = readProjectWorkRevision(tx, thread.ProjectID)
	return revision, err
}

func readWorkThread(tx *gorm.DB, threadID string) (Thread, error) {
	var thread Thread
	err := tx.Select("id", "project_id", "work_revision").Take(&thread, "id = ?", threadID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return Thread{}, ErrNotFound
	}
	return thread, err
}

// WorkSnapshotRevision samples both revision counters from one read transaction.
func (s *Store) WorkSnapshotRevision(ctx context.Context, threadID string) (WorkRevision, error) {
	var revision WorkRevision
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		thread, err := readWorkThread(tx, threadID)
		if err != nil {
			return err
		}
		revision, err = workRevisionForThread(tx, thread)
		return err
	}, &sql.TxOptions{ReadOnly: true})
	return revision, err
}

func readAnnotatedWorkNodes(tx *gorm.DB, threadID, projectID string) ([]NuwaNode, error) {
	nodes := []NuwaNode{}
	if err := visibleWorkNodes(tx, threadID, projectID).Order("created_at, id").Find(&nodes).Error; err != nil {
		return nil, err
	}
	present := make(map[string]NuwaNode, len(nodes))
	for _, node := range nodes {
		present[node.ID] = node
	}
	for i := range nodes {
		node := &nodes[i]
		for _, dependency := range node.DependsOn {
			source, exists := present[dependency]
			if !exists || (node.Scope == WorkScopeProject && source.Scope != WorkScopeProject) {
				node.MissingDependencies = append(node.MissingDependencies, dependency)
			} else if node.DependencyVersions[dependency] != source.Version || source.Inactive() {
				node.StaleDependencies = append(node.StaleDependencies, dependency)
			}
		}
		for _, evidence := range node.Evidence {
			if err := validateNuwaEvidence(tx, *node, evidence); err != nil {
				if !errors.Is(err, ErrInvalidNuwaNode) {
					return nil, err
				}
				node.UnavailableEvidence = append(node.UnavailableEvidence, evidence)
			}
		}
	}
	return nodes, nil
}

// WorkSnapshot shows only the conversation's private records and its current
// project's shared records.
func (s *Store) WorkSnapshot(ctx context.Context, threadID string) (WorkSnapshot, error) {
	snapshot, _, err := s.workSnapshot(ctx, threadID, "", "", nil)
	return snapshot, err
}

// WorkSnapshotWithEvents associates the revisions, graph and requested worker
// lifecycle rows from the same read revision.
func (s *Store) WorkSnapshotWithEvents(ctx context.Context, threadID string, kinds []string) (WorkSnapshot, []Event, error) {
	return s.workSnapshot(ctx, threadID, "", "", kinds)
}

func (s *Store) WorkSnapshotForTurn(ctx context.Context, projectID, threadID, turnID string) (WorkSnapshot, error) {
	if turnID == "" {
		return WorkSnapshot{}, invalidNuwa("source turn is required")
	}
	snapshot, _, err := s.workSnapshot(ctx, threadID, turnID, projectID, nil)
	return snapshot, err
}

func (s *Store) workSnapshot(ctx context.Context, threadID, turnID, projectID string, kinds []string) (WorkSnapshot, []Event, error) {
	var snapshot WorkSnapshot
	var events []Event
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var thread Thread
		var err error
		if turnID == "" {
			thread, err = readWorkThread(tx, threadID)
		} else {
			thread, err = workTurnOwner(tx, threadID, turnID, &projectID, true, false)
		}
		if err != nil {
			return err
		}
		snapshot.WorkRevision, err = workRevisionForThread(tx, thread)
		if err != nil {
			return err
		}
		snapshot.Nodes, err = readAnnotatedWorkNodes(tx, thread.ID, thread.ProjectID)
		if err != nil {
			return err
		}
		if turnID == "" {
			snapshot.HasOwnWork, err = threadHasWork(tx, thread.ID)
		}
		if err != nil || len(kinds) == 0 {
			return err
		}
		return tx.Where("thread_id = ? AND kind IN ?", thread.ID, kinds).Order("seq").Find(&events).Error
	}, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return WorkSnapshot{}, nil, err
	}
	return snapshot, events, nil
}

// NuwaSnapshot preserves the project-wide view for the existing API.
func (s *Store) NuwaSnapshot(ctx context.Context, projectID string) (int64, []NuwaNode, error) {
	return s.projectNuwaSnapshot(ctx, projectID, "", "")
}

func (s *Store) NuwaSnapshotForTurn(ctx context.Context, projectID, threadID, turnID string) (int64, []NuwaNode, error) {
	return s.projectNuwaSnapshot(ctx, projectID, threadID, turnID)
}

func (s *Store) projectNuwaSnapshot(ctx context.Context, projectID, threadID, turnID string) (int64, []NuwaNode, error) {
	var revision int64
	var nodes []NuwaNode
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if threadID != "" || turnID != "" {
			if _, err := workTurnOwner(tx, threadID, turnID, &projectID, true, false); err != nil {
				return err
			}
		}
		var err error
		revision, err = readProjectWorkRevision(tx, projectID)
		if err != nil {
			return err
		}
		nodes, err = readAnnotatedWorkNodes(tx, "", projectID)
		return err
	}, &sql.TxOptions{ReadOnly: true})
	return revision, nodes, err
}
