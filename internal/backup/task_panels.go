package backup

// Panel stream cursors have their own monotonic primary key rather than id.
func autoincColumn(table string) string {
	if table == "task_panel_change_rows" {
		return "cursor"
	}
	return "id"
}
