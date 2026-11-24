package store

import (
	"errors"
	"slices"

	"gorm.io/gorm"
)

func deleteNuwaThread(tx *gorm.DB, threadID string) error {
	thread, err := readWorkThread(tx, threadID)
	if err != nil {
		return err
	}
	projects, err := deleteOwnedWork(tx, "", []string{threadID})
	if err != nil {
		return err
	}
	if err := deleteNuwaVerifySlots(tx, "", threadID, nil); err != nil {
		return err
	}
	if thread.ProjectID != "" && !slices.Contains(projects, thread.ProjectID) {
		projects = append(projects, thread.ProjectID)
	}
	return bumpWorkProjects(tx, projects)
}

func deleteNuwaProject(tx *gorm.DB, projectID string, threadIDs []string) error {
	projects, err := deleteOwnedWork(tx, projectID, threadIDs)
	if err != nil {
		return err
	}
	if err := tx.Where("project_id = ? OR thread_id IN ?", projectID, threadIDs).Delete(&NuwaVerifySlot{}).Error; err != nil {
		return err
	}
	return bumpWorkProjects(tx, projects)
}

func ownedWorkNodes(tx *gorm.DB, projectID string, threadIDs []string) *gorm.DB {
	query := tx.Model(&NuwaNode{})
	if projectID == "" {
		return query.Where("thread_id IN ?", threadIDs)
	}
	return query.Where("(scope = ? AND project_id = ?) OR thread_id IN ?", WorkScopeProject, projectID, threadIDs)
}

// A moved conversation still owns the origins and audit payloads it wrote.
// Project deletion additionally owns every published record in that project.
func deleteOwnedWork(tx *gorm.DB, projectID string, threadIDs []string) ([]string, error) {
	var projects []string
	if err := ownedWorkNodes(tx, projectID, threadIDs).
		Where("scope = ? AND project_id <> ?", WorkScopeProject, projectID).
		Distinct("project_id").Pluck("project_id", &projects).Error; err != nil {
		return nil, err
	}
	nodeIDs := ownedWorkNodes(tx, projectID, threadIDs).Select("id")
	revisions := tx.Where("thread_id IN ? OR node_id IN (?)", threadIDs, nodeIDs)
	if projectID != "" {
		revisions = revisions.Or("project_id = ?", projectID)
	}
	if err := revisions.Delete(&NuwaRevision{}).Error; err != nil {
		return nil, err
	}
	if err := ownedWorkNodes(tx, projectID, threadIDs).Delete(&NuwaNode{}).Error; err != nil {
		return nil, err
	}
	return projects, nil
}

func bumpWorkProjects(tx *gorm.DB, projects []string) error {
	slices.Sort(projects)
	for _, id := range projects {
		if err := bumpNuwaRevision(tx, id); err != nil {
			return err
		}
	}
	return nil
}

func invalidatesWorkRoster(event *Event) bool {
	if event.ThreadID == "" {
		return false
	}
	switch event.Kind {
	case "spawned", "finished", "cleanup", "restart_preparing", "engine_restart_checkpoint", "restart_ready", "restart_resumed":
		return true
	default:
		return false
	}
}

func appendWorkRosterEvent(tx *gorm.DB, event *Event) error {
	if err := tx.Create(event).Error; err != nil {
		return err
	}
	if err := bumpWorkRevision(tx, event.ThreadID); err != nil {
		return err
	}
	// Leave the project-only view's existing activation rule intact. Private
	// worker activity does not create a new project-only view.
	var project Project
	err := tx.Select("id").Where(`id = (SELECT project_id FROM threads WHERE id = ?)
		AND (nuwa_enabled = ? OR nuwa_revision > 0)`, event.ThreadID, true).Take(&project).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	return bumpNuwaRevision(tx, project.ID)
}
