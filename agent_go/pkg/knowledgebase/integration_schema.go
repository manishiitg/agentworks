package knowledgebase

func integrationDefinitions() []ToolDefinition {
	str := func() map[string]any { return map[string]any{"type": "string", "minLength": 1} }
	obj := func(required []any, properties map[string]any) map[string]any {
		return map[string]any{"type": "object", "additionalProperties": false, "required": required, "properties": properties}
	}
	gitSchema := obj([]any{"op"}, map[string]any{
		"op":   map[string]any{"type": "string", "enum": []any{"status", "diff", "log", "branches", "stashes", "blame", "show", "stage", "unstage", "commit", "discard", "checkout", "create_branch", "delete_branch", "stash", "stash_apply", "stash_pop", "stash_drop", "resolve", "pull", "push"}},
		"file": str(), "commit": str(), "files": map[string]any{"type": "array", "maxItems": 500, "items": str()}, "all": map[string]any{"type": "boolean"}, "message": map[string]any{"type": "string", "maxLength": 5000}, "branch": str(), "remote": map[string]any{"type": "boolean"}, "ref": str(), "choice": map[string]any{"type": "string", "enum": []any{"ours", "theirs", "both"}}, "request_id": map[string]any{"type": "string", "pattern": "^[A-Za-z0-9_-]{1,128}$"},
	})
	gitSchema["allOf"] = []any{map[string]any{"if": map[string]any{"properties": map[string]any{"op": map[string]any{"enum": []any{"status", "diff", "log", "branches", "stashes", "blame", "show"}}}}, "else": map[string]any{"required": []any{"request_id"}}}}
	defs := []ToolDefinition{{Name: "kb_inspect_project", InputSchema: obj([]any{"workspace_path"}, map[string]any{"workspace_path": str()})}}
	defs = append(defs, ToolDefinition{Name: "kb_git", InputSchema: gitSchema, Mutates: true})
	defs = append(defs, ToolDefinition{Name: "kb_configure_backup", InputSchema: obj([]any{"remote_url", "username", "request_id"}, map[string]any{
		"remote_url": map[string]any{"type": "string", "description": "Public HTTPS Git repository URL without embedded credentials. SSH requires deployment configuration."}, "branch": str(), "username": map[string]any{"type": "string", "pattern": "^[A-Za-z0-9_.-]{1,128}$"},
		"pat":        map[string]any{"type": "string", "maxLength": 4096, "description": "Optional PAT for an HTTPS private repository. Stored encrypted by KB and never returned. Omit to retain it on reconfiguration; supply an empty string to remove it. In app chat enter it only in the secure setup field."},
		"request_id": map[string]any{"type": "string", "pattern": "^[A-Za-z0-9_-]{1,128}$"},
	}), Mutates: true})
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
	defs = append(defs, ToolDefinition{Name: "kb_set_project_access", InputSchema: obj([]any{"workspace_path", "mode", "expected_manifest_version", "request_id"}, map[string]any{
		"workspace_path": str(), "expected_manifest_version": str(),
		"mode":       map[string]any{"type": "string", "enum": []any{"off", "read", "folders"}, "description": "off: no Brain access. read: read-only on folders the owner and every output reader can read. folders: only the bound folders, with their read or write access."},
		"request_id": map[string]any{"type": "string", "pattern": "^[A-Za-z0-9_-]{1,128}$"},
	}), Mutates: true})
	return defs
}
