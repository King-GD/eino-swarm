package store

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"gorm.io/gorm"
)

// ErrNuwaConflict means the observed record or work revision has changed.
var ErrNuwaConflict = errors.New("store: shared node version conflict")

// ErrInvalidNuwaNode identifies invalid content, ownership or evidence.
var ErrInvalidNuwaNode = errors.New("store: invalid shared node")

var errWorkPayloadEvidence = invalidNuwa("project evidence cannot reference a work tool payload; cite its original event")

// NuwaEvidence references an actual durable event, never a model-authored quote.
type NuwaEvidence struct {
	ThreadID string `json:"thread_id"`
	TurnID   string `json:"turn_id,omitempty"`
	Seq      int64  `json:"seq"`
	Label    string `json:"label,omitempty"`
}

// NuwaNode is a task, reported observation or artifact scoped to a thread or project.
// Origin fields remain fixed; Updated* records the last trusted caller.
type NuwaNode struct {
	ID                 string           `gorm:"primaryKey;size:64" json:"id"`
	ProjectID          string           `gorm:"index;size:64" json:"project_id"`
	Scope              string           `gorm:"not null;default:project;size:16" json:"scope"`
	Kind               string           `gorm:"size:16" json:"kind"`
	Title              string           `json:"title"`
	Summary            string           `json:"summary"`
	Detail             string           `json:"detail"`
	Status             string           `gorm:"size:24" json:"status"`
	DependsOn          []string         `gorm:"serializer:json" json:"depends_on"`
	DependencyVersions map[string]int64 `gorm:"serializer:json" json:"dependency_versions"`
	Evidence           []NuwaEvidence   `gorm:"serializer:json" json:"evidence"`
	ArtifactPath       string           `json:"artifact_path,omitempty"`
	ThreadID           string           `gorm:"index;size:64" json:"thread_id"`
	TurnID             string           `gorm:"size:64" json:"turn_id"`
	AgentID            string           `gorm:"size:64" json:"agent_id"`
	Generation         int              `json:"generation"`
	UpdatedBy          string           `json:"updated_by"`
	UpdatedThreadID    string           `json:"updated_thread_id"`
	UpdatedTurnID      string           `json:"updated_turn_id"`
	UpdatedGeneration  int              `json:"updated_generation"`
	Version            int64            `json:"version"`
	CreatedAt          time.Time        `json:"created_at"`
	UpdatedAt          time.Time        `json:"updated_at"`
	// Deleted sources stay explicit instead of silently removing a prerequisite.
	MissingDependencies []string       `gorm:"-" json:"missing_dependencies,omitempty"`
	StaleDependencies   []string       `gorm:"-" json:"stale_dependencies,omitempty"`
	UnavailableEvidence []NuwaEvidence `gorm:"-" json:"unavailable_evidence,omitempty"`
}

// NuwaRevision preserves every committed full node version. An empty NodeID
// records an invalidation without inventing a work record.
type NuwaRevision struct {
	ID          uint      `gorm:"primaryKey" json:"-"`
	ProjectID   string    `gorm:"not null;default:'';uniqueIndex:idx_nuwa_scoped_revision,priority:1;size:64" json:"project_id"`
	ThreadID    string    `gorm:"not null;default:'';uniqueIndex:idx_nuwa_scoped_revision,priority:2;size:64" json:"thread_id,omitempty"`
	Revision    int64     `gorm:"uniqueIndex:idx_nuwa_scoped_revision,priority:3" json:"revision"`
	NodeID      string    `gorm:"index;size:64" json:"node_id,omitempty"`
	Version     int64     `json:"version,omitempty"`
	Payload     string    `json:"payload"`
	RequestKey  *string   `gorm:"uniqueIndex:idx_nuwa_request_key" json:"request_key,omitempty"`
	RequestHash string    `json:"request_hash,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

// NuwaWriteReceipt binds a trusted runtime tool call to its canonical arguments.
// A repeated call returns the exact committed version without adding a node.
type NuwaWriteReceipt struct {
	Key, Hash string
	// CallerProjectID freezes the trusted turn owner, including standalone turns.
	CallerProjectID *string
	// A nil list preserves literal evidence. An empty list clears it.
	OwnToolCallIDs    []string
	CurrentToolCallID string
	ToolReceiptScope  string
}

func invalidNuwa(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidNuwaNode, fmt.Sprintf(format, args...))
}

func validateNuwaNode(node *NuwaNode) error {
	if strings.TrimSpace(node.Title) == "" || node.ThreadID == "" || node.TurnID == "" || node.AgentID == "" || node.Generation < 0 {
		return invalidNuwa("title and trusted thread/turn/agent identity are required")
	}
	if (node.Scope != WorkScopeThread || node.ProjectID != "") && (node.Scope != WorkScopeProject || node.ProjectID == "") {
		return invalidNuwa("scope does not match the work owner")
	}
	var statuses []string
	switch node.Kind {
	case "task":
		statuses = []string{"pending", "running", "done", "blocked", "cancelled"}
		if node.Status == "" {
			node.Status = "pending"
		}
	case "fact", "artifact":
		statuses = []string{"reported", "superseded", "retracted"}
		if node.Status == "" {
			node.Status = "reported"
		}
	default:
		return invalidNuwa("kind must be task, fact or artifact")
	}
	if !slices.Contains(statuses, node.Status) {
		return invalidNuwa("status is invalid for %s", node.Kind)
	}
	if node.Kind == "artifact" && strings.TrimSpace(node.ArtifactPath) == "" {
		return invalidNuwa("artifact_path is required for an artifact")
	}
	return nil
}

func validateNuwaOwner(tx *gorm.DB, node NuwaNode, checkRecordedProject bool) error {
	_, err := workTurnOwner(tx, node.ThreadID, node.TurnID, &node.ProjectID, checkRecordedProject, false)
	return err
}

func validateNuwaEvidence(tx *gorm.DB, owner NuwaNode, evidence NuwaEvidence) error {
	if evidence.ThreadID == "" || evidence.Seq <= 0 {
		return invalidNuwa("evidence requires thread_id and a positive event seq")
	}
	var event Event
	query := tx.Table("events").Select("events.turn_id", "events.work_record_payload").
		Joins("JOIN threads ON threads.id = events.thread_id").
		Where("events.thread_id = ? AND events.seq = ?", evidence.ThreadID, evidence.Seq)
	if owner.Scope == WorkScopeThread {
		query = query.Where("threads.id = ?", owner.ThreadID)
	} else {
		query = query.Where("threads.project_id = ?", owner.ProjectID)
	}
	err := query.First(&event).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return invalidNuwa("evidence event is missing or outside this record's scope")
	}
	if err != nil {
		return err
	}
	if owner.Scope == WorkScopeProject && event.WorkRecordPayload {
		return errWorkPayloadEvidence
	}
	if evidence.TurnID != "" && evidence.TurnID != event.TurnID {
		return invalidNuwa("evidence turn does not match its event")
	}
	return nil
}

func validateNuwaDependencies(nodes []NuwaNode, candidate *NuwaNode) error {
	graph := make(map[string][]string, len(nodes)+1)
	versions := map[string]int64{}
	for _, node := range nodes {
		graph[node.ID] = node.DependsOn
		versions[node.ID] = node.Version
	}
	for _, dependency := range candidate.DependsOn {
		if dependency == candidate.ID {
			return invalidNuwa("a node cannot depend on itself")
		}
		if _, exists := graph[dependency]; !exists {
			return invalidNuwa("dependency %q is missing or outside this record's scope", dependency)
		}
	}
	graph[candidate.ID] = candidate.DependsOn
	visiting, visited := map[string]bool{}, map[string]bool{}
	var visit func(string) bool
	visit = func(id string) bool {
		if visiting[id] {
			return true
		}
		if visited[id] {
			return false
		}
		visiting[id] = true
		for _, next := range graph[id] {
			if visit(next) {
				return true
			}
		}
		visiting[id], visited[id] = false, true
		return false
	}
	for id := range graph {
		if visit(id) {
			return invalidNuwa("dependencies contain a cycle")
		}
	}
	if candidate.DependencyVersions == nil {
		candidate.DependencyVersions = map[string]int64{}
		for _, dependency := range candidate.DependsOn {
			candidate.DependencyVersions[dependency] = versions[dependency]
		}
	} else {
		if len(candidate.DependencyVersions) != len(candidate.DependsOn) {
			return invalidNuwa("dependency_versions must match depends_on")
		}
		for _, dependency := range candidate.DependsOn {
			if candidate.DependencyVersions[dependency] <= 0 {
				return invalidNuwa("dependency_versions must record each positive source version")
			}
		}
	}
	return nil
}

func advanceNuwaRevision(tx *gorm.DB, projectID string) (int64, error) {
	result := tx.Model(&Project{}).Where("id = ?", projectID).UpdateColumn("nuwa_revision", gorm.Expr("COALESCE(nuwa_revision, 0) + 1"))
	if result.Error != nil {
		return 0, result.Error
	}
	if result.RowsAffected == 0 {
		return 0, invalidNuwa("project does not exist")
	}
	var project Project
	if err := tx.Select("nuwa_revision").First(&project, "id = ?", projectID).Error; err != nil {
		return 0, err
	}
	return project.NuwaRevision, nil
}

// BumpNuwaRevision invalidates the shared snapshot after a worker-state change.
func (s *Store) BumpNuwaRevision(ctx context.Context, projectID string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return bumpNuwaRevision(tx, projectID)
	})
}

func bumpNuwaRevision(tx *gorm.DB, projectID string) error {
	revision, err := advanceNuwaRevision(tx, projectID)
	if err != nil {
		return err
	}
	return tx.Create(&NuwaRevision{ProjectID: projectID, Revision: revision, Payload: "{}", CreatedAt: time.Now().UTC()}).Error
}
