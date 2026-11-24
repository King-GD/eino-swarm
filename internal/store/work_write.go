package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"gorm.io/gorm"
)

// UpsertNuwaNode validates the caller and all references, then commits the
// node CAS, scope revision and full audit payload in one transaction. The
// input's caller identity becomes the updater; the previous origin stays fixed.
func (s *Store) UpsertNuwaNode(ctx context.Context, node *NuwaNode, expectedVersion int64, receipts ...NuwaWriteReceipt) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if node == nil {
		return invalidNuwa("node is required")
	}
	candidate := *node
	if candidate.Scope == "" {
		candidate.Scope = WorkScopeThread
		if candidate.ProjectID != "" {
			candidate.Scope = WorkScopeProject
		}
	}
	if err := validateNuwaNode(&candidate); err != nil {
		return err
	}
	if expectedVersion < 0 || (candidate.ID == "" && expectedVersion != 0) {
		return ErrNuwaConflict
	}
	var receipt NuwaWriteReceipt
	if len(receipts) > 1 {
		return invalidNuwa("one tool write receipt is allowed")
	}
	if len(receipts) == 1 {
		receipt = receipts[0]
	}
	if err := validateWorkReceipt(receipt); err != nil {
		return err
	}

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		expectedProject := receipt.CallerProjectID
		if expectedProject == nil && candidate.Scope == WorkScopeProject {
			expectedProject = &candidate.ProjectID
		}
		thread, err := workTurnOwner(tx, candidate.ThreadID, candidate.TurnID, expectedProject, receipt.CallerProjectID != nil, true)
		if err != nil {
			return err
		}
		if candidate.Scope == WorkScopeProject && candidate.ProjectID != thread.ProjectID {
			return invalidNuwa("project record belongs to another project")
		}

		frozen, replay, err := replayWorkReceipt(tx, thread, receipt)
		if err != nil {
			return err
		}
		if replay {
			candidate = frozen
			return nil
		}
		return writeWorkNode(tx, thread, &candidate, expectedVersion, receipt)
	})
	if err != nil {
		return fmt.Errorf("store: save work record: %w", err)
	}
	*node = candidate
	return nil
}

func validateWorkReceipt(receipt NuwaWriteReceipt) error {
	if (receipt.Key == "") != (receipt.Hash == "") {
		return invalidNuwa("write receipt requires both key and hash")
	}
	return nil
}

func replayWorkReceipt(tx *gorm.DB, thread Thread, receipt NuwaWriteReceipt) (NuwaNode, bool, error) {
	if receipt.Key == "" {
		return NuwaNode{}, false, nil
	}
	var row NuwaRevision
	err := tx.Take(&row, "request_key = ?", receipt.Key).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return NuwaNode{}, false, nil
	}
	if err != nil {
		return NuwaNode{}, false, err
	}
	if row.RequestHash != receipt.Hash {
		return NuwaNode{}, false, ErrNuwaConflict
	}
	var visible NuwaNode
	err = visibleWorkNodes(tx, thread.ID, thread.ProjectID).Select("id").Take(&visible, "id = ?", row.NodeID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return NuwaNode{}, false, invalidNuwa("work record is unavailable")
	}
	if err != nil {
		return NuwaNode{}, false, err
	}

	var frozen NuwaNode
	if err := json.Unmarshal([]byte(row.Payload), &frozen); err != nil {
		return NuwaNode{}, false, err
	}
	if frozen.ID != row.NodeID || frozen.Version != row.Version {
		return NuwaNode{}, false, fmt.Errorf("store: invalid work write receipt")
	}
	if frozen.Scope == "" {
		// Legacy receipts hold the original JSON and represented project work.
		frozen.Scope = WorkScopeProject
	}
	return frozen, true, nil
}

func writeWorkNode(tx *gorm.DB, thread Thread, candidate *NuwaNode, expectedVersion int64, receipt NuwaWriteReceipt) error {
	if receipt.OwnToolCallIDs != nil {
		evidence, err := resolveNuwaOwnToolEvidence(tx, *candidate, receipt)
		if err != nil {
			return err
		}
		candidate.Evidence = evidence
	} else {
		for _, evidence := range candidate.Evidence {
			if err := validateNuwaEvidence(tx, *candidate, evidence); err != nil {
				return err
			}
		}
	}

	stamp := workUpdater{threadID: thread.ID, turnID: candidate.TurnID, by: candidate.AgentID, generation: candidate.Generation}
	var previous *NuwaNode
	if candidate.ID != "" {
		var current NuwaNode
		err := visibleWorkNodes(tx, thread.ID, thread.ProjectID).Take(&current, "id = ?", candidate.ID).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return invalidNuwa("work record is unavailable")
		}
		if err != nil {
			return err
		}
		if current.Version != expectedVersion {
			return ErrNuwaConflict
		}
		if current.Scope != candidate.Scope {
			return invalidNuwa("use promotion to change a record's scope")
		}
		previous = &current
	} else {
		candidate.ID = NewID("nw_")
	}

	var nodes []NuwaNode
	if err := visibleWorkNodes(tx, thread.ID, thread.ProjectID).Find(&nodes).Error; err != nil {
		return err
	}
	if candidate.Scope == WorkScopeProject {
		if previous == nil && len(candidate.DependsOn) > 0 {
			dependencies := make(map[string]bool, len(candidate.DependsOn))
			for _, id := range candidate.DependsOn {
				dependencies[id] = true
			}
			var private []string
			for _, node := range nodes {
				if node.Scope == WorkScopeThread && dependencies[node.ID] {
					private = append(private, node.ID)
				}
			}
			if len(private) > 0 {
				promoted, err := promoteWorkClosure(tx, nodes, thread, private, stamp, "", NuwaWriteReceipt{})
				if err != nil {
					return err
				}
				nodes = append(nodes, promoted...)
			}
		}
		nodes = slices.DeleteFunc(nodes, func(node NuwaNode) bool { return node.Scope != WorkScopeProject })
	}
	if err := validateNuwaDependencies(nodes, candidate); err != nil {
		return err
	}
	return saveWorkVersion(tx, candidate, previous, stamp, receipt)
}

type workUpdater struct {
	threadID, turnID, by string
	generation           int
}

func saveWorkVersion(tx *gorm.DB, candidate, previous *NuwaNode, updater workUpdater, receipt NuwaWriteReceipt) error {
	now := time.Now().UTC()
	candidate.UpdatedBy, candidate.UpdatedThreadID, candidate.UpdatedTurnID = updater.by, updater.threadID, updater.turnID
	candidate.UpdatedGeneration, candidate.UpdatedAt = updater.generation, now
	candidate.MissingDependencies, candidate.UnavailableEvidence, candidate.StaleDependencies = nil, nil, nil
	if previous == nil {
		candidate.Version, candidate.CreatedAt = 1, now
		if err := tx.Create(candidate).Error; err != nil {
			return err
		}
	} else {
		candidate.ThreadID, candidate.TurnID, candidate.AgentID = previous.ThreadID, previous.TurnID, previous.AgentID
		candidate.Generation, candidate.CreatedAt, candidate.Version = previous.Generation, previous.CreatedAt, previous.Version+1
		result := tx.Model(&NuwaNode{}).Where("id = ? AND version = ? AND scope = ? AND thread_id = ? AND project_id = ?",
			previous.ID, previous.Version, previous.Scope, previous.ThreadID, previous.ProjectID).Select("*").Updates(candidate)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrNuwaConflict
		}
	}

	row := NuwaRevision{NodeID: candidate.ID, Version: candidate.Version, CreatedAt: now}
	var err error
	if candidate.Scope == WorkScopeThread {
		row.ThreadID = candidate.ThreadID
		row.Revision, err = advanceWorkRevision(tx, row.ThreadID)
	} else {
		row.ProjectID = candidate.ProjectID
		row.Revision, err = advanceNuwaRevision(tx, row.ProjectID)
	}
	if err != nil {
		return err
	}
	payload, err := json.Marshal(candidate)
	if err != nil {
		return err
	}
	row.Payload = string(payload)
	if receipt.Key != "" {
		row.RequestKey, row.RequestHash = &receipt.Key, receipt.Hash
	}
	return tx.Create(&row).Error
}

func bumpWorkRevision(tx *gorm.DB, threadID string) error {
	result := tx.Model(&Thread{}).Where("id = ?", threadID).UpdateColumn("work_revision", gorm.Expr("work_revision + 1"))
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrNotFound
	}
	return nil
}

func advanceWorkRevision(tx *gorm.DB, threadID string) (int64, error) {
	if err := bumpWorkRevision(tx, threadID); err != nil {
		return 0, err
	}
	thread, err := readWorkThread(tx, threadID)
	return thread.WorkRevision, err
}
