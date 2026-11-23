package engine

import "github.com/cloudwego/eino/schema"

func knowledgeWriteParams() map[string]*schema.ParameterInfo {
	list := func(element *schema.ParameterInfo) *schema.ParameterInfo {
		return &schema.ParameterInfo{Type: schema.Array, ElemInfo: element}
	}
	object := func(properties map[string]*schema.ParameterInfo) *schema.ParameterInfo {
		return &schema.ParameterInfo{Type: schema.Object, SubParams: properties}
	}
	str := func(desc string, required bool) *schema.ParameterInfo {
		return &schema.ParameterInfo{Type: schema.String, Desc: desc, Required: required}
	}
	strings := func() *schema.ParameterInfo { return list(str("", false)) }
	field := object(map[string]*schema.ParameterInfo{
		"id": str("Field ID.", true), "kind": str("Field kind from the current schema vocabulary.", true),
		"required": {Type: schema.Boolean},
	})
	pageType := object(map[string]*schema.ParameterInfo{
		"id": str("New type slug.", true), "dir": str("New folder slug.", true),
		"description": str("When to use this type.", false), "fields": list(field),
		"sections": strings(), "freshness_days": {Type: schema.Integer},
	})
	relation := object(map[string]*schema.ParameterInfo{
		"id": str("New relation slug.", true), "inverse": str("Label for inbound edges.", false),
		"description": str("When to use this relation.", false),
		"from":        strings(), "to": strings(), "required": {Type: schema.Boolean},
	})
	view := object(map[string]*schema.ParameterInfo{
		"id": str("New view slug.", true), "root_type": str("Root page type ID.", true),
		"follow": strings(), "depth": {Type: schema.Integer, Required: true},
	})
	add := object(map[string]*schema.ParameterInfo{
		"types": list(pageType), "relations": list(relation), "views": list(view),
	})
	add.Desc = "For append_schema, only new type, relation or view IDs are allowed."
	sources := list(object(map[string]*schema.ParameterInfo{
		"uri":       str("Absolute external source URI.", false),
		"kind":      {Type: schema.String, Enum: []string{"message", "event"}},
		"thread_id": str("Exact conversation ID from original history.", false),
		"seq":       {Type: schema.Integer, Desc: "Exact sequence from the indicated message or event table."},
	}))
	sources.Desc = "Each source is either {uri} or {kind, thread_id, seq}. For page updates, omit sources to keep existing entries or use [] to clear them."
	return map[string]*schema.ParameterInfo{
		"action":   {Type: schema.String, Required: true, Enum: []string{"write_page", "delete_page", "append_schema"}},
		"id":       str("Page slug for write_page or delete_page. Keep it stable across title changes.", false),
		"type":     str("Existing page type ID. Required for write_page and fixed on existing pages.", false),
		"title":    str("Page title. Changing it renames the page without changing its ID.", false),
		"aliases":  strings(),
		"fields":   {Type: schema.Object, Desc: "Field values keyed by IDs defined for this type."},
		"links":    list(object(map[string]*schema.ParameterInfo{"rel": str("Relation ID.", true), "to": str("Target page ID.", true)})),
		"sources":  sources,
		"as_of":    str("Optional date this knowledge describes in YYYY-MM-DD format.", false),
		"body":     str("Complete Markdown body for write_page. Use [[page-id|label]] for mentions.", false),
		"revision": str("Exact revision returned by read_knowledge for the page or schema. Omit only when creating a page.", false),
		"add":      add,
	}
}
