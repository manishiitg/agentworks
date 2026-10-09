package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/knowledgebase"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowkb"
)

func knowledgeIntegrationFixture(t *testing.T) (*knowledgebase.Service, knowledgebase.Principal, string, string) {
	t.Helper()
	_, s := knowledgebaseServerTest(t)
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)
	workspace := "Workflow/payments"
	path := filepath.Join(root, workspace)
	if err := os.MkdirAll(filepath.Join(path, "knowledgebase", "notes"), 0700); err != nil {
		t.Fatal(err)
	}
	raw := map[string]any{"id": "payments-workflow", "created_by": "admin", "access": map[string]any{"owners": []string{"admin"}, "readers": []string{"priya"}}}
	b, _ := json.Marshal(raw)
	if err := os.WriteFile(filepath.Join(path, "workflow.json"), b, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "knowledgebase", "notes", "guide.md"), []byte("# Guide\nExisting knowledge.\n"), 0600); err != nil {
		t.Fatal(err)
	}
	a := knowledgebase.Principal{IdentityID: "admin", IsAdmin: true}
	must := func(tool string, args map[string]any) map[string]any {
		v, err := s.Call(t.Context(), a, tool, args)
		if err != nil {
			t.Fatal(err)
		}
		return knowledgeMap(v)
	}
	f := must("create_knowledgebase_folder", map[string]any{"folder_path": "", "name": "Imported", "request_id": "destination"})
	for _, g := range []struct{ id, role string }{{"admin", "Owner"}, {"priya", "Reader"}} {
		acl := must("get_knowledgebase_access", map[string]any{"folder_path": "Imported"})
		must("manage_knowledgebase_access", map[string]any{"action": "grant", "folder_path": "Imported", "identity_id": g.id, "role": g.role, "expected_acl_version": acl["acl_version"], "request_id": "dest_" + g.id})
	}
	return s, a, workspace, f["folder_id"].(string)
}
func knowledgeDispatchTest(t *testing.T, s *knowledgebase.Service, p knowledgebase.Principal, tool string, args map[string]any) any {
	t.Helper()
	v, err := knowledgebaseDispatch(context.WithValue(t.Context(), knowledgebaseMigrationAuthorityKey{}, true), s, p, p.IdentityID, tool, args)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestKnowledgebaseMigrationImportCutoverAndRollback(t *testing.T) {
	s, a, workspace, id := knowledgeIntegrationFixture(t)
	preview := map[string]any{"action": "migration_preview", "workspace_path": workspace, "folder_id": id, "alias": "local", "access": "write", "request_id": "preview"}
	r := knowledgeDispatchTest(t, s, a, "brain_update", preview).(*knowledgeMigrationReceipt)
	if len(r.Files) != 1 || r.State != "PREVIEWED" {
		t.Fatalf("bad preview %#v", r)
	}
	run := func(action, req string) *knowledgeMigrationReceipt {
		return knowledgeDispatchTest(t, s, a, "brain_update", map[string]any{"action": action, "workspace_path": workspace, "migration_id": r.ID, "request_id": req}).(*knowledgeMigrationReceipt)
	}
	r = run("migration_import", "import")
	if r.State != "IMPORTED" || r.Files[0].EntryID == "" {
		t.Fatal(r)
	}
	retried := run("migration_import", "import")
	if retried.Files[0].EntryID != r.Files[0].EntryID {
		t.Fatal("retry duplicated entry")
	}
	// Pausing schedules before cutover edits the manifest; that must not count as a change since the preview
	// (it made the documented order impossible on server A, 2026-10-06).
	manifestPath := filepath.Join(os.Getenv("WORKSPACE_DOCS_PATH"), workspace, "workflow.json")
	var manifest map[string]any
	data, _ := os.ReadFile(manifestPath)
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest["schedules"] = []map[string]any{{"id": "nightly", "enabled": false}}
	data, _ = json.Marshal(manifest)
	if err := os.WriteFile(manifestPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	r = run("migration_cutover", "cutover")
	if r.State != "ACTIVE" {
		t.Fatal(r)
	}
	if shared, _ := workflowkb.SharedConfig(os.Getenv("WORKSPACE_DOCS_PATH"), workspace); !shared {
		t.Fatal("cutover not persisted")
	}
	blocks := workflowkb.LegacyKnowledgeBlocks(os.Getenv("WORKSPACE_DOCS_PATH"), workspace)
	if len(blocks) != 1 {
		t.Fatal("legacy archive readable", blocks)
	}
	session := "migration-runtime"
	common.SetSessionWorkflowPath(session, workspace)
	ctx := context.WithValue(t.Context(), common.ChatSessionIDKey, session)
	if _, err := knowledgebaseExecute(knowledgeTestCaller(ctx, "priya"), "priya", false, "brain_read", map[string]any{"action": "read", "entry_id": r.Files[0].EntryID}); err != nil {
		t.Fatal("workflow reader", err)
	}
	r = run("migration_rollback", "rollback")
	if r.State != "ROLLED_BACK" {
		t.Fatal(r)
	}
	if shared, _ := workflowkb.SharedConfig(os.Getenv("WORKSPACE_DOCS_PATH"), workspace); shared {
		t.Fatal("rollback not persisted")
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("WORKSPACE_DOCS_PATH"), workspace, "knowledgebase", "notes", "guide.md")); err != nil {
		t.Fatal("source removed", err)
	}
	if _, err := s.CallTool(t.Context(), a, "brain_read", map[string]any{"action": "read", "entry_id": r.Files[0].EntryID}); err != nil {
		t.Fatal("rollback deleted imported knowledge", err)
	}
}

func TestKnowledgebaseMigrationRejectsChangedSourceAndSymlink(t *testing.T) {
	s, a, workspace, id := knowledgeIntegrationFixture(t)
	root := filepath.Join(os.Getenv("WORKSPACE_DOCS_PATH"), workspace, "knowledgebase")
	secret := filepath.Join(t.TempDir(), "secret.md")
	os.WriteFile(secret, []byte("host secret"), 0600)
	if err := os.Symlink(secret, filepath.Join(root, "secret.md")); err != nil {
		t.Fatal(err)
	}
	r := knowledgeDispatchTest(t, s, a, "brain_update", map[string]any{"action": "migration_preview", "workspace_path": workspace, "folder_id": id, "alias": "local", "access": "read", "request_id": "preview"}).(*knowledgeMigrationReceipt)
	if len(r.Files) != 1 || len(r.Skipped) != 1 {
		t.Fatalf("symlink imported %#v", r)
	}
	args := map[string]any{"action": "migration_import", "workspace_path": workspace, "migration_id": r.ID, "request_id": "import"}
	if _, err := knowledgebaseDispatch(context.WithValue(t.Context(), knowledgebaseMigrationAuthorityKey{}, true), s, a, a.IdentityID, "brain_update", args); err == nil {
		t.Fatal("skipped file not acknowledged")
	}
	if _, err := knowledgeReadSource(root, "secret.md"); err == nil {
		t.Fatal("followed source symlink")
	}
	os.WriteFile(filepath.Join(root, "notes", "guide.md"), []byte("changed"), 0600)
	args["allow_skipped_files"] = true
	args["request_id"] = "changed_import"
	if _, err := knowledgebaseDispatch(context.WithValue(t.Context(), knowledgebaseMigrationAuthorityKey{}, true), s, a, a.IdentityID, "brain_update", args); err == nil {
		t.Fatal("changed source imported")
	}
}

func TestKnowledgebaseMigrationConflictsAndResumesCheckpoint(t *testing.T) {
	s, a, workspace, id := knowledgeIntegrationFixture(t)
	r := knowledgeDispatchTest(t, s, a, "brain_update", map[string]any{"action": "migration_preview", "workspace_path": workspace, "folder_id": id, "alias": "local", "access": "read", "request_id": "preview"}).(*knowledgeMigrationReceipt)
	imported := knowledgeDispatchTest(t, s, a, "brain_update", map[string]any{"action": "migration_import", "workspace_path": workspace, "migration_id": r.ID, "request_id": "import"}).(*knowledgeMigrationReceipt)
	// Simulate a crash after the entry mutation but before its checkpoint.
	originalID := imported.Files[0].EntryID
	imported.Files[0].EntryID = ""
	imported.Files[0].Version = ""
	imported.State = "IMPORTING"
	if err := knowledgeSaveMigration(imported); err != nil {
		t.Fatal(err)
	}
	resumed := knowledgeDispatchTest(t, s, a, "brain_update", map[string]any{"action": "migration_import", "workspace_path": workspace, "migration_id": r.ID, "request_id": "import"}).(*knowledgeMigrationReceipt)
	if resumed.Files[0].EntryID != originalID {
		t.Fatal("resumed import duplicated entry")
	}
	file := resumed.Files[0]
	if _, err := s.CallTool(t.Context(), a, "brain_update", map[string]any{"action": "update", "entry_id": file.EntryID, "expected_version": file.Version, "content": "new user edit", "request_id": "later-edit"}); err != nil {
		t.Fatal(err)
	}
	if _, err := knowledgebaseDispatch(context.WithValue(t.Context(), knowledgebaseMigrationAuthorityKey{}, true), s, a, "admin", "brain_update", map[string]any{"action": "migration_cutover", "workspace_path": workspace, "migration_id": r.ID, "request_id": "cutover"}); err == nil {
		t.Fatal("cutover ignored destination edit")
	}
}
