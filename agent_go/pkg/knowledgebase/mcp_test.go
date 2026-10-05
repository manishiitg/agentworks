package knowledgebase

import (
	"context"
	"reflect"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

func mcpCall(t *testing.T, s *Service, p Principal, tool string, args map[string]any) map[string]any {
	t.Helper()
	result, err := s.CallTool(context.Background(), p, tool, args)
	if err != nil {
		t.Fatalf("%s/%v: %v", tool, args["action"], err)
	}
	return asMap(result)
}
func mcpError(t *testing.T, s *Service, p Principal, tool string, args map[string]any, want string) {
	t.Helper()
	_, err := s.CallTool(context.Background(), p, tool, args)
	failure, ok := err.(*Error)
	if !ok || failure.Code != want {
		t.Fatalf("%s/%v: %v, want %s", tool, args["action"], err, want)
	}
}

func TestFiveMCPToolsCompleteContentAndBackupLifecycle(t *testing.T) {
	s, admin, editor := fixture(t, true)
	grant(t, s, admin, editor.IdentityID, "", "Editor", "grant-editor")
	folder := mcpCall(t, s, editor, "update_knowledgebase", map[string]any{"action": "create_folder", "folder_path": "", "name": "Payments", "request_id": "create-folder"})
	args := map[string]any{"action": "create", "folder_id": folder["folder_id"], "filename": "guide.md", "type": "skill", "title": "Guide", "content": "# Guide\r\noriginal\r\n", "request_id": "create-entry"}
	entry := mcpCall(t, s, editor, "update_knowledgebase", args)
	if retry := mcpCall(t, s, editor, "update_knowledgebase", args); retry["entry_id"] != entry["entry_id"] {
		t.Fatal("create retry repeated the save")
	}
	if folders := mcpCall(t, s, editor, "browse_knowledgebase", map[string]any{"action": "folders"}); len(folders["folders"].([]any)) != 1 {
		t.Fatal("folder browsing omitted the created folder", folders)
	}
	if listing := mcpCall(t, s, editor, "browse_knowledgebase", map[string]any{"action": "entries", "folder_id": folder["folder_id"]}); len(listing["entries"].([]any)) != 1 {
		t.Fatal("entry browsing omitted the live save", listing)
	}
	read := mcpCall(t, s, editor, "read_knowledgebase", map[string]any{"action": "read", "entry_id": entry["entry_id"], "section": map[string]any{"heading": "Guide"}})
	if read["content"] != "# Guide\noriginal\n" {
		t.Fatal("public read lost section or encoding semantics", read)
	}
	update := map[string]any{"action": "update", "entry_id": entry["entry_id"], "expected_version": entry["version"], "diff": "--- a/guide.md\n+++ b/guide.md\n@@ -1,2 +1,2 @@\n # Guide\n-original\n+updated\n", "metadata": map[string]any{"tags": []string{"payments"}}, "request_id": "update-entry"}
	updated := mcpCall(t, s, editor, "update_knowledgebase", update)
	if retry := mcpCall(t, s, editor, "update_knowledgebase", update); retry["version"] != updated["version"] {
		t.Fatal("update retry repeated the patch")
	}
	if found := mcpCall(t, s, editor, "read_knowledgebase", map[string]any{"action": "search", "query": "updated", "tag": "payments"}); len(found["matches"].([]any)) != 1 {
		t.Fatal("public search did not find updated content", found)
	}
	commitArgs := map[string]any{"action": "commit", "entries": []any{map[string]any{"entry_id": entry["entry_id"], "expected_version": updated["version"]}}, "message": "Backup", "request_id": "commit-entry"}
	receipt := mcpCall(t, s, editor, "backup_knowledgebase", commitArgs)
	mcpCall(t, s, editor, "backup_knowledgebase", map[string]any{"action": "push", "receipt_id": receipt["receipt_id"], "request_id": "push-entry"})
	status := mcpCall(t, s, editor, "backup_knowledgebase", map[string]any{"action": "status", "folder_id": folder["folder_id"]})
	if asMap(status["entries"].([]any)[0])["backup_status"] != "backed_up" {
		t.Fatal("public backup did not publish the selection", status)
	}
	deleted := mcpCall(t, s, editor, "update_knowledgebase", map[string]any{"action": "delete", "entry_id": entry["entry_id"], "expected_version": updated["version"], "request_id": "delete-entry"})
	receipt = mcpCall(t, s, editor, "backup_knowledgebase", map[string]any{"action": "commit", "deletions": []any{map[string]any{"deletion_id": deleted["deletion_id"]}}, "message": "Back up deletion", "request_id": "commit-deletion"})
	mcpCall(t, s, editor, "backup_knowledgebase", map[string]any{"action": "push", "receipt_id": receipt["receipt_id"], "request_id": "push-deletion"})
	mcpError(t, s, editor, "read_knowledgebase", map[string]any{"action": "read", "entry_id": entry["entry_id"]}, "NOT_FOUND")
}

func TestMCPActionsKeepPurposeIsolationAndRequestNamespace(t *testing.T) {
	s, admin, _ := fixture(t, true)
	entry := mcpCall(t, s, admin, "update_knowledgebase", map[string]any{"action": "create", "folder_path": "", "filename": "guide.md", "type": "note", "title": "Guide", "content": "live", "request_id": "same-id"})
	mcpError(t, s, admin, "update_knowledgebase", map[string]any{"action": "delete", "entry_id": entry["entry_id"], "expected_version": entry["version"], "request_id": "same-id"}, "REQUEST_ID_REUSE")
	receipt := mcpCall(t, s, admin, "backup_knowledgebase", map[string]any{"action": "commit", "entries": []any{map[string]any{"entry_id": entry["entry_id"], "expected_version": entry["version"]}}, "message": "Backup", "request_id": "backup-id"})
	mcpError(t, s, admin, "backup_knowledgebase", map[string]any{"action": "push", "receipt_id": receipt["receipt_id"], "request_id": "backup-id"}, "REQUEST_ID_REUSE")
	acl := mcpCall(t, s, admin, "manage_knowledgebase_access", map[string]any{"action": "inspect"})["acl_version"]
	grantArgs := map[string]any{"action": "grant", "folder_path": "", "identity_id": "priya", "role": "Reader", "expected_acl_version": acl, "request_id": "grant"}
	mcpError(t, s, admin, "manage_knowledgebase_access", grantArgs, "FORBIDDEN")
	builder := admin
	builder.AccessOnly = true
	mcpCall(t, s, builder, "manage_knowledgebase_access", grantArgs)
	mcpCall(t, s, builder, "manage_knowledgebase_access", map[string]any{"action": "list"})
	mcpError(t, s, builder, "read_knowledgebase", map[string]any{"action": "read", "entry_id": entry["entry_id"]}, "FORBIDDEN")
	mcpError(t, s, admin, "read_knowledgebase", map[string]any{"entry_id": entry["entry_id"]}, "INVALID_ARGUMENT")
	mcpError(t, s, admin, "read_knowledgebase", map[string]any{"action": "search", "query": "live", "entry_id": entry["entry_id"]}, "INVALID_ARGUMENT")
	mcpError(t, s, admin, "update_knowledgebase", map[string]any{"action": "delete", "entry_id": entry["entry_id"], "expected_version": entry["version"], "content": "unexpected", "request_id": "invalid"}, "INVALID_ARGUMENT")
	for _, old := range []string{"create_knowledgebase", "list_knowledgebase", "get_knowledgebase_activity", "commit_knowledgebase", "push_knowledgebase"} {
		mcpError(t, s, admin, old, nil, "INVALID_ARGUMENT")
	}
}

func TestMCPDiscoveryLimitsActionsByConnection(t *testing.T) {
	fullNames := []string{}
	for _, def := range ToolDefinitions() {
		fullNames = append(fullNames, def.Name)
	}
	if !reflect.DeepEqual(fullNames, []string{"browse_knowledgebase", "read_knowledgebase", "update_knowledgebase", "backup_knowledgebase", "manage_knowledgebase_access"}) {
		t.Fatal("unexpected public surface", fullNames)
	}
	for _, writable := range []bool{false, true} {
		for _, def := range ConnectionToolDefinitions(writable) {
			compiler := jsonschema.NewCompiler()
			uri := "https://knowledgebase.invalid/test/" + def.Name
			if err := compiler.AddResource(uri, asMap(def.InputSchema)); err != nil {
				t.Fatal(err)
			}
			validator, err := compiler.Compile(uri)
			if err != nil {
				t.Fatal(err)
			}
			switch def.Name {
			case "manage_knowledgebase_access":
				if err = validator.Validate(map[string]any{"action": "inspect"}); err != nil {
					t.Fatal("connection cannot inspect access", err)
				}
				if err = validator.Validate(map[string]any{"action": "list"}); err == nil {
					t.Fatal("connection schema exposed builder-only discovery")
				}
			case "backup_knowledgebase":
				if err = validator.Validate(map[string]any{"action": "status"}); err != nil {
					t.Fatal(err)
				}
				allowed := validator.Validate(map[string]any{"action": "push", "receipt_id": "receipt", "request_id": "push"}) == nil
				if allowed != writable {
					t.Fatal("backup actions do not match connection scope")
				}
			case "update_knowledgebase":
				if !writable {
					t.Fatal("read-only discovery exposed update tool")
				}
			}
		}
	}
}
