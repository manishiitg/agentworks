package server

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/chathistory"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"
)

func selectionMigrationFixture(t *testing.T) (secretSelectionMigrationOptions, chathistory.Store) {
	t.Helper()
	docs, store := productSecretsMigrationTestDocs(t)
	return secretSelectionMigrationOptions{DocsRoot: docs, StateRoot: t.TempDir()}, store
}

func selectionManifest(t *testing.T, docs, workspace, file string, local, global []string) []byte {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"id": "test", "created_by": "alice", "access": map[string]any{"owners": []string{"alice"}}, "extra": map[string]any{"keep": 9007199254740993}, "capabilities": map[string]any{"selected_secrets": local, "selected_global_secret_names": global, "selected_servers": []string{"Notion"}}})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(docs, workspace, file)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0640); err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestSecretSelectionMigrationDryRunAndOneTimeApply(t *testing.T) {
	opts, store := selectionMigrationFixture(t)
	workspace := "Workflow/confida-login"
	raw := selectionManifest(t, opts.DocsRoot, workspace, "workflow.json", []string{"ADMIN_EMAIL", "MISSING_PROJECT"}, []string{"ADMIN_EMAIL", "ADMIN_USER", "LOGIN_PASSWORD", "MEMBER_USER", "DENIED_GLOBAL", "ENV_TOKEN"})
	seedPersonalSecret(t, store, managedGlobalSecretsUserID, "DENIED_GLOBAL", "preserve-existing-ciphertext")
	// Deliberately corrupt ciphertext must remain selected: this migration must
	// never mistake a decryption error for an absent record.
	if err := store.UpsertWorkflowSecret(t.Context(), chathistory.SharedWorkflowSecretsUserID, workspace, "ADMIN_EMAIL", "not-decryptable"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GLOBAL_SECRET_ENV_TOKEN", "test-global-value")
	beforeStore, err := store.ListWorkflowSecrets(t.Context(), chathistory.SharedWorkflowSecretsUserID, workspace)
	if err != nil {
		t.Fatal(err)
	}
	report, err := runSecretSelectionMigration(opts)
	if err != nil {
		t.Fatal(err)
	}
	if report.Applied || len(report.Changes) != 1 || len(report.Changes[0].Removed["selected_global_secret_names"]) != 3 {
		t.Fatalf("unexpected dry-run: %+v", report)
	}
	path := filepath.Join(opts.DocsRoot, workspace, "workflow.json")
	got, _ := os.ReadFile(path)
	if !bytes.Equal(got, raw) {
		t.Fatal("dry-run changed manifest")
	}
	if _, err := os.Stat(filepath.Join(opts.StateRoot, "migrations")); !os.IsNotExist(err) {
		t.Fatal("dry-run wrote state")
	}
	opts.Apply, opts.Once = true, true
	report, err = runSecretSelectionMigration(opts)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Applied || len(report.Changes) != 1 {
		t.Fatalf("unexpected apply: %+v", report)
	}
	backup, err := os.ReadFile(report.Changes[0].Backup)
	if err != nil || !bytes.Equal(backup, raw) {
		t.Fatal("original manifest not backed up")
	}
	info, _ := os.Stat(report.Changes[0].Backup)
	if info.Mode().Perm() != 0600 {
		t.Fatal("backup must be private")
	}
	got, _ = os.ReadFile(path)
	var manifest struct {
		Capabilities struct {
			Local   []string `json:"selected_secrets"`
			Global  []string `json:"selected_global_secret_names"`
			Servers []string `json:"selected_servers"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal(got, &manifest); err != nil {
		t.Fatal(err)
	}
	if strings.Join(manifest.Capabilities.Local, ",") != "ADMIN_EMAIL" || strings.Join(manifest.Capabilities.Global, ",") != "ADMIN_EMAIL,DENIED_GLOBAL,ENV_TOKEN" {
		t.Fatalf("wrong selections: %+v", manifest.Capabilities)
	}
	if len(manifest.Capabilities.Servers) != 1 || !bytes.Contains(got, []byte("9007199254740993")) {
		t.Fatal("unrelated fields or numeric precision changed")
	}
	info, _ = os.Stat(path)
	if info.Mode().Perm() != 0640 {
		t.Fatal("manifest permissions changed")
	}
	afterStore, _ := store.ListWorkflowSecrets(t.Context(), chathistory.SharedWorkflowSecretsUserID, workspace)
	if len(afterStore) != len(beforeStore) || afterStore[0].EncryptedValue != beforeStore[0].EncryptedValue {
		t.Fatal("ciphertext changed")
	}
	again, err := runSecretSelectionMigration(opts)
	if err != nil || !again.AlreadyComplete || len(again.Changes) != 0 {
		t.Fatalf("once did not skip: %+v %v", again, err)
	}
	opts.Apply = false
	check, err := runSecretSelectionMigration(opts)
	if err != nil || len(check.Changes) != 0 {
		t.Fatalf("post-apply dry-run should be clean: %+v %v", check, err)
	}
}

func TestSecretSelectionMigrationPreservesScopedLegacyAndProjectBoundaries(t *testing.T) {
	opts, store := selectionMigrationFixture(t)
	crew := workspaceref.PhysicalPath("alice", workspaceref.CrewProjectsRoot, "one")
	code := workspaceref.PhysicalPath("alice", workspaceref.CodeProjectsRoot, "two")
	selectionManifest(t, opts.DocsRoot, crew, "product.json", []string{"LEGACY", "OTHER_PROJECT"}, nil)
	selectionManifest(t, opts.DocsRoot, code, "product.json", []string{"OTHER_PROJECT"}, nil)
	if err := store.UpsertWorkflowSecret(t.Context(), "alice", crew, "LEGACY", "legacy-ciphertext"); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertWorkflowSecret(t.Context(), chathistory.SharedWorkflowSecretsUserID, code, "OTHER_PROJECT", "project-ciphertext"); err != nil {
		t.Fatal(err)
	}
	r, err := runSecretSelectionMigration(opts)
	if err != nil {
		t.Fatal(err)
	}
	if r.Scanned != 2 || len(r.Changes) != 1 || r.Changes[0].Manifest != filepath.ToSlash(filepath.Join(crew, "product.json")) || strings.Join(r.Changes[0].Removed["selected_secrets"], ",") != "OTHER_PROJECT" {
		t.Fatalf("cross-project source incorrectly retained: %+v", r)
	}
}

func TestSecretSelectionMigrationUnreadableStoreAbortsBeforeAnyWrite(t *testing.T) {
	opts, _ := selectionMigrationFixture(t)
	raw := selectionManifest(t, opts.DocsRoot, "Workflow/a", "workflow.json", []string{"MISSING"}, nil)
	selectionManifest(t, opts.DocsRoot, "Workflow/b", "workflow.json", []string{"MISSING"}, nil)
	// A malformed global store cannot be interpreted as "no globals".
	global := filepath.Join(opts.DocsRoot, workspaceref.PhysicalPath(managedGlobalSecretsUserID, "secrets.json"))
	if err := os.MkdirAll(filepath.Dir(global), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(global, []byte("{broken"), 0600); err != nil {
		t.Fatal(err)
	}
	opts.Apply, opts.Once = true, true
	if _, err := runSecretSelectionMigration(opts); err == nil {
		t.Fatal("malformed store accepted")
	}
	got, _ := os.ReadFile(filepath.Join(opts.DocsRoot, "Workflow/a/workflow.json"))
	if !bytes.Equal(raw, got) {
		t.Fatal("partial scan wrote a manifest")
	}
	if _, err := os.Stat(filepath.Join(opts.StateRoot, "migrations")); !os.IsNotExist(err) {
		t.Fatal("failed scan wrote completion or backups")
	}
}

func TestSecretSelectionMigrationRejectsSymlinksAndAbsentDocs(t *testing.T) {
	opts, _ := selectionMigrationFixture(t)
	outside := t.TempDir()
	raw := selectionManifest(t, outside, "project", "workflow.json", []string{"MISSING"}, nil)
	if err := os.MkdirAll(filepath.Join(opts.DocsRoot, "Workflow"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "project"), filepath.Join(opts.DocsRoot, "Workflow/linked")); err != nil {
		t.Fatal(err)
	}
	opts.Apply, opts.Once = true, true
	if _, err := runSecretSelectionMigration(opts); err == nil {
		t.Fatal("symlink accepted")
	}
	got, _ := os.ReadFile(filepath.Join(outside, "project/workflow.json"))
	if !bytes.Equal(raw, got) {
		t.Fatal("modified outside document root")
	}
	opts.DocsRoot = filepath.Join(t.TempDir(), "unmounted")
	if _, err := runSecretSelectionMigration(opts); err == nil {
		t.Fatal("missing docs root stamped as complete")
	}
	if _, err := os.Stat(filepath.Join(opts.StateRoot, "migrations")); !os.IsNotExist(err) {
		t.Fatal("failed migration created marker")
	}
}
