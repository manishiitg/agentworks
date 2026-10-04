package knowledgebase

func integrationDefinitions() []ToolDefinition {
	str := func() map[string]any { return map[string]any{"type": "string", "minLength": 1} }
	obj := func(required []any, properties map[string]any) map[string]any {
		return map[string]any{"type": "object", "additionalProperties": false, "required": required, "properties": properties}
	}
	defs := []ToolDefinition{{Name: "kb_inspect_project", InputSchema: obj([]any{"workspace_path"}, map[string]any{"workspace_path": str()})}}
	for _, name := range []string{"kb_migration_preview", "kb_migration_import", "kb_migration_cutover", "kb_migration_rollback"} {
		props := map[string]any{"workspace_path": str(), "request_id": str()}
		props["request_id"] = map[string]any{"type": "string", "pattern": "^[A-Za-z0-9_-]{1,128}$"}
		required := []any{"workspace_path", "request_id"}
		if name == "kb_migration_preview" {
			props["folder_id"] = str()
			props["alias"] = str()
			props["access"] = map[string]any{"type": "string", "enum": []any{"read", "write"}}
			required = append(required, "folder_id", "alias", "access")
		} else {
			props["migration_id"] = str()
			required = append(required, "migration_id")
		}
		if name == "kb_migration_import" {
			props["allow_skipped_files"] = map[string]any{"type": "boolean"}
		}
		defs = append(defs, ToolDefinition{Name: name, InputSchema: obj(required, props), Mutates: true})
	}
	for _, name := range []string{"kb_bind_project", "kb_unbind_project"} {
		props := map[string]any{"workspace_path": str(), "alias": str(), "expected_manifest_version": str(), "request_id": str()}
		props["request_id"] = map[string]any{"type": "string", "pattern": "^[A-Za-z0-9_-]{1,128}$"}
		required := []any{"workspace_path", "alias", "expected_manifest_version", "request_id"}
		if name == "kb_bind_project" {
			props["replace_legacy_alias"] = map[string]any{"type": "boolean"}
			props["folder_id"] = str()
			props["access"] = map[string]any{"type": "string", "enum": []any{"read", "write"}}
			required = append(required, "folder_id", "access")
		}
		defs = append(defs, ToolDefinition{Name: name, InputSchema: obj(required, props), Mutates: true})
	}
	return defs
}
