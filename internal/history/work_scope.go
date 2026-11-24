package history

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// Work tool traffic stays in the originating conversation even when the
// history caller can read ordinary events and prose from other conversations.
func applyWorkPayloadScope(ctx context.Context, tx *sql.Tx, tables []Table, projectID, threadID string) error {
	if strings.ContainsRune(threadID, 0) {
		return fmt.Errorf("history requires a valid conversation identity")
	}
	ordinary := "COALESCE(work_record_payload, 0) = 0"
	visible := ordinary
	if threadID != "" {
		visible = "(" + ordinary + " OR thread_id = '" + strings.ReplaceAll(threadID, "'", "''") + "')"
	}
	var project map[string]string
	if projectID != "" {
		project = projectScopePredicates(projectID)
	}
	for _, table := range tables {
		if table.Name != "messages" && table.Name != "events" {
			continue
		}
		columns := workPayloadProjection(table, visible)
		if len(columns) == 0 {
			continue // The source file predates private work records.
		}
		predicate := visible
		if table.Name == "events" {
			predicate += " AND kind IS NOT 'engine_restart_checkpoint'"
		} else {
			predicate = "(" + predicate + " OR role IS NOT 'tool')"
		}
		if member := project[table.Name]; member != "" {
			predicate += " AND " + member
		}
		name := quote(table.Name)
		if _, err := tx.ExecContext(ctx, "DROP VIEW IF EXISTS temp."+name); err != nil {
			return fmt.Errorf("replace history work payload view: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "CREATE TEMP VIEW "+name+
			" AS SELECT "+strings.Join(columns, ", ")+" FROM main."+name+" WHERE "+predicate); err != nil {
			return fmt.Errorf("create history work payload view: %w", err)
		}
	}
	return nil
}

func workPayloadProjection(table Table, visible string) []string {
	columns := make([]string, 0, len(table.Columns))
	marked := false
	for _, col := range table.Columns {
		switch col.Name {
		case "work_record_payload":
			marked = true
			columns = append(columns, quote(col.Name))
		case "tool_calls":
			// An assistant row can contain regular prose and both private
			// and ordinary calls. The call events are separate; retain
			// the prose and mask the mixed serialized batch.
			if table.Name == "messages" {
				columns = append(columns, "CASE WHEN "+visible+" THEN tool_calls END AS tool_calls")
			} else {
				columns = append(columns, quote(col.Name))
			}
		default:
			columns = append(columns, quote(col.Name))
		}
	}
	if !marked {
		return nil
	}
	return columns
}
