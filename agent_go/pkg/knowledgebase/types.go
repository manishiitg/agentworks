// Package knowledgebase implements the live, permissioned knowledge-base domain.
package knowledgebase

import (
	"context"
	"encoding/json"
	"fmt"
)

type Config struct {
	Root           string
	OrganizationID string
	BackupRemote   string
	BackupBranch   string
}

type Cap struct {
	FolderPath string `json:"folder_path"`
	Role       string `json:"role"`
}

type Principal struct {
	IdentityID string                      `json:"identity_id"`
	IsAdmin    bool                        `json:"is_admin"`
	AccessOnly bool                        `json:"access_only"`
	Caps       *[]Cap                      `json:"caps,omitempty"`
	Recheck    func(context.Context) error `json:"-"`
}

type Error struct {
	Code       string `json:"code"`
	Message    string `json:"message"`
	Retryable  bool   `json:"retryable,omitempty"`
	RetryAfter *int   `json:"retry_after,omitempty"`
	Details    any    `json:"details,omitempty"`
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

type ToolDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
	Mutates     bool           `json:"mutates"`
}

const (
	roleReader = 1
	roleEditor = 2
	roleOwner  = 3
)

func kbErr(code, message string) *Error { return &Error{Code: code, Message: message} }

func asMap(v any) map[string]any {
	b, _ := json.Marshal(v)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return m
}

func ToolDefinitions() []ToolDefinition {
	obj := func(required []string, props map[string]any) map[string]any {
		req := make([]any, len(required))
		for i, v := range required {
			req[i] = v
		}
		return map[string]any{"type": "object", "additionalProperties": false, "required": req, "properties": props}
	}
	str := func(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }
	locator := map[string]any{"entry_id": str("Opaque entry ID (exclusive with path)."), "path": str("Organization-relative Markdown path (exclusive with entry_id).")}
	defs := []ToolDefinition{
		{"list_knowledgebase_folders", "List readable folders, including only required ancestor breadcrumbs.", obj(nil, map[string]any{"folder_path": str("Relative folder path; empty means root."), "depth": map[string]any{"type": "integer", "minimum": 0}, "cursor": str("Opaque cursor."), "limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 100}}), false},
		{"create_knowledgebase_folder", "Create a child folder inheriting current grants.", obj([]string{"folder_path", "name", "request_id"}, map[string]any{"folder_path": str("Parent folder."), "name": str("Folder name."), "request_id": str("Idempotency key.")}), true},
		{"list_knowledgebase", "List readable knowledge-base entries and folders.", obj(nil, map[string]any{"folder_path": str("Relative folder path."), "depth": map[string]any{"type": "integer", "minimum": 0}, "glob": str("Optional filename glob."), "type": str("Optional skill, note, fact, or source filter."), "tag": str("Optional exact tag filter."), "cursor": str("Opaque cursor."), "limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 100}}), false},
		{"read_knowledgebase", "Read a whole entry, a line range, or a Markdown heading section.", obj(nil, merge(locator, map[string]any{"start_line": map[string]any{"type": "integer", "minimum": 1}, "end_line": map[string]any{"type": "integer", "minimum": 1}, "section": map[string]any{"type": "object"}})), false},
		{"search_knowledgebase", "Literal-search readable Markdown content.", obj([]string{"query"}, map[string]any{"query": str("Literal text."), "folder_path": str("Optional scope."), "type": str("Optional entry type."), "tag": str("Optional tag."), "cursor": str("Opaque cursor."), "limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 100}}), false},
		{"create_knowledgebase", "Create a live Markdown entry.", obj([]string{"folder_path", "filename", "type", "title", "content", "request_id"}, map[string]any{"folder_path": str("Parent folder."), "filename": str("Lowercase .md filename."), "type": str("skill, note, fact, or source."), "title": str("Display title."), "content": str("Markdown content, max 10 MiB."), "description": str("Optional description."), "tags": map[string]any{"type": "array", "items": str("Tag.")}, "request_id": str("Idempotency key.")}), true},
		{"update_knowledgebase", "Atomically patch or replace content and/or metadata with version checking.", obj([]string{"expected_version", "request_id"}, merge(locator, map[string]any{"expected_version": str("Opaque current version."), "diff": str("Unified diff, max 2 MiB."), "content": str("Replacement Markdown."), "metadata": map[string]any{"type": "object"}, "request_id": str("Idempotency key.")})), true},
		{"delete_knowledgebase", "Delete an entry and return an opaque deletion token.", obj([]string{"expected_version", "request_id"}, merge(locator, map[string]any{"expected_version": str("Opaque current version."), "request_id": str("Idempotency key.")})), true},
		{"get_knowledgebase_access", "Read effective access; Owners/admins may enumerate grants and identities.", obj(nil, map[string]any{"folder_path": str("Relative folder path.")}), false},
		{"get_knowledgebase_activity", "List authorized activity records.", obj(nil, map[string]any{"folder_path": str("Optional folder scope."), "cursor": str("Opaque cursor."), "limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 100}}), false},
		{"get_knowledgebase_backup_status", "Inspect backup state for readable content and deletions.", obj(nil, map[string]any{"folder_path": str("Optional folder scope.")}), false},
		{"commit_knowledgebase", "Prepare an isolated Git snapshot containing only explicitly selected authorized changes.", obj([]string{"message", "request_id"}, map[string]any{"entries": map[string]any{"type": "array", "maxItems": 100, "items": obj([]string{"expected_version"}, merge(locator, map[string]any{"expected_version": str("Current opaque entry version.")}))}, "deletions": map[string]any{"type": "array", "maxItems": 100, "items": obj([]string{"deletion_id"}, map[string]any{"deletion_id": str("Opaque deletion token.")})}, "message": str("Commit message."), "request_id": str("Idempotency key.")}), true},
		{"push_knowledgebase", "Publish an owned prepared receipt after rechecking access and generation.", obj([]string{"receipt_id", "request_id"}, map[string]any{"receipt_id": str("Opaque receipt ID."), "request_id": str("Idempotency key.")}), true},
		{"manage_knowledgebase_access", "List, grant, or revoke folder access and create/disable service accounts.", obj([]string{"action"}, map[string]any{"action": str("list, grant, revoke, create_service_account, or disable_service_account."), "folder_path": str("Relative folder path."), "identity_id": str("Target identity."), "role": str("Reader, Editor, or Owner."), "name": str("Service account name."), "request_id": str("Idempotency key.")}), true},
	}

	folderLocator := map[string]any{"folder_id": str("Opaque folder ID, exclusive with folder_path."), "folder_path": str("Relative folder path, exclusive with folder_id.")}
	oneLocator := func(first, second string) []any {
		return []any{map[string]any{"required": []any{first}}, map[string]any{"required": []any{second}}}
	}
	for i := range defs {
		schema := defs[i].InputSchema
		props := schema["properties"].(map[string]any)
		if _, ok := props["folder_path"]; ok {
			props["folder_id"] = folderLocator["folder_id"]
		}
		switch defs[i].Name {
		case "create_knowledgebase_folder":
			schema["required"] = []any{"name", "request_id"}
			schema["oneOf"] = oneLocator("folder_id", "folder_path")
		case "create_knowledgebase":
			schema["required"] = []any{"filename", "type", "title", "content", "request_id"}
			schema["oneOf"] = oneLocator("folder_id", "folder_path")
			props["type"] = map[string]any{"type": "string", "enum": []any{"skill", "note", "fact", "source"}}
			props["title"] = map[string]any{"type": "string", "minLength": 1, "maxLength": 200}
			props["description"] = map[string]any{"type": "string", "maxLength": 4000}
			props["tags"] = map[string]any{"type": "array", "maxItems": 50, "items": map[string]any{"type": "string", "maxLength": 64}}
		case "read_knowledgebase":
			schema["oneOf"] = oneLocator("entry_id", "path")
			props["section"] = obj([]string{"heading"}, map[string]any{"heading": str("Exact case-sensitive plain heading text."), "occurrence": map[string]any{"type": "integer", "minimum": 1}})
			schema["dependentRequired"] = map[string]any{"start_line": []any{"end_line"}, "end_line": []any{"start_line"}}
			schema["allOf"] = []any{map[string]any{"not": map[string]any{"required": []any{"section", "start_line"}}}, map[string]any{"not": map[string]any{"required": []any{"section", "end_line"}}}}
		case "update_knowledgebase":
			schema["oneOf"] = oneLocator("entry_id", "path")
			props["metadata"] = obj(nil, map[string]any{"title": map[string]any{"type": "string", "minLength": 1, "maxLength": 200}, "description": map[string]any{"type": "string", "maxLength": 4000}, "type": map[string]any{"type": "string", "enum": []any{"skill", "note", "fact", "source"}}, "tags": map[string]any{"type": "array", "maxItems": 50, "items": map[string]any{"type": "string", "maxLength": 64}}})
			schema["allOf"] = []any{map[string]any{"not": map[string]any{"required": []any{"diff", "content"}}}, map[string]any{"anyOf": []any{map[string]any{"required": []any{"diff"}}, map[string]any{"required": []any{"content"}}, map[string]any{"required": []any{"metadata"}}}}}
		case "delete_knowledgebase":
			schema["oneOf"] = oneLocator("entry_id", "path")
		case "commit_knowledgebase":
			item := props["entries"].(map[string]any)["items"].(map[string]any)
			item["oneOf"] = oneLocator("entry_id", "path")
		case "manage_knowledgebase_access":
			props["cursor"] = str("Opaque eligible-folder pagination cursor.")
			props["limit"] = map[string]any{"type": "integer", "minimum": 1, "maximum": 100}
			props["expected_acl_version"] = str("Current opaque folder ACL version, required for grant or revoke.")
			props["action"] = map[string]any{"type": "string", "enum": []any{"list", "grant", "revoke", "create_service_account", "disable_service_account"}}
			props["role"] = map[string]any{"type": "string", "enum": []any{"Reader", "Editor", "Owner"}}
			schema["allOf"] = []any{map[string]any{"if": map[string]any{"properties": map[string]any{"action": map[string]any{"const": "list"}}}, "else": map[string]any{"required": []any{"request_id"}}}, map[string]any{"if": map[string]any{"properties": map[string]any{"action": map[string]any{"enum": []any{"grant", "revoke"}}}}, "then": map[string]any{"required": []any{"expected_acl_version"}}}}
		}
	}
	return defs
}

func merge(a, b map[string]any) map[string]any {
	o := make(map[string]any, len(a)+len(b))
	for k, v := range a {
		o[k] = v
	}
	for k, v := range b {
		o[k] = v
	}
	return o
}

func badArg(format string, a ...any) error {
	return kbErr("INVALID_ARGUMENT", fmt.Sprintf(format, a...))
}
