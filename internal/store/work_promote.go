package store

import (
	"context"
	"fmt"
	"maps"
	"strings"
	"time"

	"gorm.io/gorm"
)

// NuwaPromoteInput carries either a human snapshot expectation or the identity
// and version of one Agent tool call.
type NuwaPromoteInput struct {
	ThreadID string
	IDs      []string
	Expected *WorkRevision

	ProjectID  string
	TurnID     string
	AgentID    string
	Generation int
	Version    int64
	Receipt    NuwaWriteReceipt
}

type NuwaPromotion struct {
	WorkRevision
	Nodes []NuwaNode `json:"nodes"`
}

// PromoteNuwaNodes publishes private roots and their private prerequisites in
// one revision transaction, preserving each node's immutable origin and ID.
func (s *Store) PromoteNuwaNodes(ctx context.Context, in NuwaPromoteInput) (NuwaPromotion, error) {
	if err := ctx.Err(); err != nil {
		return NuwaPromotion{}, err
	}
	if err := validateWorkPromotion(in); err != nil {
		return NuwaPromotion{}, err
	}
	var result NuwaPromotion
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		human := in.Expected != nil
		stamp := workUpdater{threadID: in.ThreadID, by: MessageSourceHuman}
		var thread Thread
		var err error
		if human {
			thread, err = readWorkThread(tx, in.ThreadID)
		} else {
			thread, err = workTurnOwner(tx, in.ThreadID, in.TurnID, &in.ProjectID, true, true)
			stamp.by, stamp.turnID, stamp.generation = in.AgentID, in.TurnID, in.Generation
		}
		if err != nil {
			return err
		}
		current, err := workRevisionForThread(tx, thread)
		if err != nil {
			return err
		}
		if human && current != *in.Expected {
			return ErrNuwaConflict
		}
		if thread.ProjectID == "" {
			return invalidNuwa("a standalone conversation has no project to promote work to")
		}
		frozen, replay, err := replayWorkReceipt(tx, thread, in.Receipt)
		if err != nil {
			return err
		}
		if replay {
			result = NuwaPromotion{WorkRevision: current, Nodes: []NuwaNode{frozen}}
			return nil
		}

		var nodes []NuwaNode
		if err := visibleWorkNodes(tx, thread.ID, thread.ProjectID).Find(&nodes).Error; err != nil {
			return err
		}
		if !human {
			for _, node := range nodes {
				if node.ID == in.IDs[0] && node.Version != in.Version {
					return ErrNuwaConflict
				}
			}
		}
		root := ""
		if !human {
			root = in.IDs[0]
		}
		promoted, err := promoteWorkClosure(tx, nodes, thread, in.IDs, stamp, root, in.Receipt)
		if err != nil {
			return err
		}
		if !human {
			for _, node := range promoted {
				if node.ID == root {
					promoted = []NuwaNode{node}
					break
				}
			}
		}
		thread, err = readWorkThread(tx, thread.ID)
		if err != nil {
			return err
		}
		result.Nodes = promoted
		result.WorkRevision, err = workRevisionForThread(tx, thread)
		return err
	})
	if err != nil {
		return NuwaPromotion{}, fmt.Errorf("store: promote work records: %w", err)
	}
	return result, nil
}

func validateWorkPromotion(in NuwaPromoteInput) error {
	if in.ThreadID == "" || len(in.IDs) == 0 {
		return invalidNuwa("promotion requires a conversation and one or more private record ids")
	}
	seen := make(map[string]bool, len(in.IDs))
	for _, id := range in.IDs {
		if id == "" || strings.TrimSpace(id) != id || seen[id] {
			return invalidNuwa("promotion requires distinct private record ids")
		}
		seen[id] = true
	}
	if in.Expected == nil && (len(in.IDs) != 1 || in.TurnID == "" || in.AgentID == "" || in.Generation < 0 || in.Version <= 0 || in.Receipt.Key == "") {
		return invalidNuwa("agent promotion requires trusted identity, receipt and record version")
	}
	if err := validateWorkReceipt(in.Receipt); err != nil {
		return err
	}
	if in.Expected != nil && in.Receipt.Key != "" {
		return invalidNuwa("human promotion uses the work snapshot revision")
	}
	return nil
}

func promoteWorkClosure(tx *gorm.DB, visible []NuwaNode, thread Thread, roots []string, updater workUpdater, receiptRoot string, receipt NuwaWriteReceipt) ([]NuwaNode, error) {
	nodes := make(map[string]NuwaNode, len(visible))
	for _, node := range visible {
		nodes[node.ID] = node
	}
	closure, err := workPromotionClosure(nodes, thread.ID, roots)
	if err != nil {
		return nil, err
	}

	promoted := make([]NuwaNode, 0, len(closure))
	promotedVersions := make(map[string]int64, len(closure))
	for _, id := range closure {
		previous := nodes[id]
		node := previous
		node.Scope, node.ProjectID = WorkScopeProject, thread.ProjectID
		for _, evidence := range node.Evidence {
			if err := validateNuwaEvidence(tx, node, evidence); err != nil {
				return nil, err
			}
		}
		node.DependencyVersions = maps.Clone(previous.DependencyVersions)
		for _, dependency := range node.DependsOn {
			if version, moved := promotedVersions[dependency]; moved {
				source := nodes[dependency]
				if node.DependencyVersions[dependency] == source.Version && !source.Inactive() {
					node.DependencyVersions[dependency] = version
				}
			}
		}
		var nodeReceipt NuwaWriteReceipt
		if id == receiptRoot {
			nodeReceipt = receipt
		}
		if err := saveWorkVersion(tx, &node, &previous, updater, nodeReceipt); err != nil {
			return nil, err
		}
		promotedVersions[id] = node.Version
		promoted = append(promoted, node)
	}
	revision, err := advanceWorkRevision(tx, thread.ID)
	if err != nil {
		return nil, err
	}
	row := NuwaRevision{ThreadID: thread.ID, Revision: revision, Payload: "{}", CreatedAt: time.Now().UTC()}
	if err := tx.Create(&row).Error; err != nil {
		return nil, err
	}
	return promoted, nil
}

func workPromotionClosure(nodes map[string]NuwaNode, threadID string, roots []string) ([]string, error) {
	var closure []string
	visiting, visited := map[string]bool{}, map[string]bool{}
	var visit func(string) error
	visit = func(id string) error {
		node, ok := nodes[id]
		if !ok {
			return invalidNuwa("promotion has an unavailable prerequisite")
		}
		if node.Scope == WorkScopeProject || visited[id] {
			return nil
		}
		if node.Scope != WorkScopeThread || node.ThreadID != threadID || visiting[id] {
			return invalidNuwa("promotion has an invalid prerequisite")
		}
		visiting[id] = true
		for _, dependency := range node.DependsOn {
			if err := visit(dependency); err != nil {
				return err
			}
		}
		visiting[id], visited[id] = false, true
		closure = append(closure, id)
		return nil
	}
	for _, id := range roots {
		node := nodes[id]
		if node.Scope != WorkScopeThread || node.ThreadID != threadID {
			return nil, invalidNuwa("promotion requires a private record in this conversation")
		}
		if err := visit(id); err != nil {
			return nil, err
		}
	}
	return closure, nil
}
