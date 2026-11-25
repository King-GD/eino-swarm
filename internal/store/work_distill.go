package store

import (
	"context"
	"database/sql"

	"gorm.io/gorm"
)

// WorkDistillationState binds an empty conversation ledger to the history
// captured for its extraction. Another conversation's project writes are independent.
type WorkDistillationState struct {
	ThreadID, ProjectID string
	ThreadRevision      int64
	LastEventID         uint
	HasRecords          bool
}

func (s *Store) WorkDistillationState(ctx context.Context, threadID string) (state WorkDistillationState, err error) {
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		state, err = readWorkDistillationState(tx, threadID)
		return err
	}, &sql.TxOptions{ReadOnly: true})
	return
}

func readWorkDistillationState(tx *gorm.DB, threadID string) (WorkDistillationState, error) {
	thread, err := readWorkThread(tx, threadID)
	if err != nil {
		return WorkDistillationState{}, err
	}
	state := WorkDistillationState{ThreadID: thread.ID, ProjectID: thread.ProjectID, ThreadRevision: thread.WorkRevision}
	state.HasRecords, err = threadHasWork(tx, threadID)
	if err != nil {
		return state, err
	}
	err = tx.Model(&Event{}).Where("thread_id = ?", threadID).Select("COALESCE(MAX(id), 0)").Scan(&state.LastEventID).Error
	return state, err
}

func threadHasWork(tx *gorm.DB, threadID string) (bool, error) {
	var exists bool
	err := tx.Raw("SELECT EXISTS(SELECT 1 FROM nuwa_nodes WHERE thread_id = ?)", threadID).Scan(&exists).Error
	return exists, err
}

// DistilledWorkRecord uses batch-local keys rather than taking record IDs from
// the model. The transaction resolves each reference to a record it created.
type DistilledWorkRecord struct {
	Key       string
	DependsOn []string
	Node      NuwaNode
}

// SaveDistilledWork commits one historical extraction without allowing a
// partial batch, a second extraction, or a write after its source changes.
func (s *Store) SaveDistilledWork(ctx context.Context, expected WorkDistillationState, records []DistilledWorkRecord) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		current, err := readWorkDistillationState(tx, expected.ThreadID)
		if err != nil {
			return err
		}
		if current != expected || current.HasRecords {
			return ErrNuwaConflict
		}
		thread := Thread{ID: current.ThreadID, ProjectID: current.ProjectID, WorkRevision: current.ThreadRevision}
		saved := make(map[string]NuwaNode, len(records))
		for _, record := range records {
			node := record.Node
			if node.ThreadID != thread.ID || node.Scope != WorkScopeThread || node.ID != "" || len(node.Evidence) == 0 || len(node.DependsOn) != 0 {
				return invalidNuwa("distillation requires new conversation records with source evidence")
			}
			if _, exists := saved[record.Key]; exists || len(record.Key) == 0 || len(record.Key) > 64 {
				return invalidNuwa("distillation requires unique short record keys")
			}
			node.DependsOn = make([]string, 0, len(record.DependsOn))
			for _, key := range record.DependsOn {
				source, ok := saved[key]
				if !ok || source.Kind == "artifact" {
					return invalidNuwa("distillation dependencies must reference an earlier task or fact")
				}
				node.DependsOn = append(node.DependsOn, source.ID)
			}
			if err := validateNuwaNode(&node); err != nil {
				return err
			}
			if err := writeWorkNode(tx, thread, &node, 0, NuwaWriteReceipt{}); err != nil {
				return err
			}
			saved[record.Key] = node
		}
		return nil
	})
}
