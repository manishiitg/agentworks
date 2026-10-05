package knowledgebase

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func fixture(t *testing.T, remote bool) (*Service, Principal, Principal) {
	t.Helper()
	root := t.TempDir()
	cfg := Config{Root: filepath.Join(root, "kb"), OrganizationID: "org"}
	if remote {
		cfg.BackupRemote = filepath.Join(root, "remote.git")
		if b, e := exec.Command("git", "init", "--bare", cfg.BackupRemote).CombinedOutput(); e != nil {
			t.Fatalf("gitinit %v %s", e, b)
		}
	}
	s, e := New(cfg)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	if e = s.SyncPlatformIdentities(context.Background(), []Identity{{ID: "admin", Name: "Admin"}, {ID: "priya", Name: "Priya"}, {ID: "editor", Name: "Editor"}}); e != nil {
		t.Fatal(e)
	}
	return s, Principal{IdentityID: "admin", IsAdmin: true}, Principal{IdentityID: "priya"}
}
func call(t *testing.T, s *Service, p Principal, tool string, a map[string]any) map[string]any {
	t.Helper()
	v, e := s.Call(context.Background(), p, tool, a)
	if e != nil {
		t.Fatalf("%s %v", tool, e)
	}
	return asMap(v)
}
func code(t *testing.T, s *Service, p Principal, tool string, a map[string]any, want string) {
	t.Helper()
	_, e := s.Call(context.Background(), p, tool, a)
	ke, ok := e.(*Error)
	if !ok || ke.Code != want {
		t.Fatalf("%s %v want %s", tool, e, want)
	}
}
func folder(t *testing.T, s *Service, p Principal, f, n string) {
	call(t, s, p, "create_knowledgebase_folder", map[string]any{"folder_path": f, "name": n, "request_id": "f_" + strings.ReplaceAll(f+"_"+n, "/", "_")})
}
func grant(t *testing.T, s *Service, p Principal, id, f, r, req string) {
	call(t, s, p, "manage_knowledgebase_access", map[string]any{"action": "grant", "folder_path": f, "identity_id": id, "role": r, "request_id": req, "expected_acl_version": call(t, s, p, "get_knowledgebase_access", map[string]any{"folder_path": f})["acl_version"]})
}
func create(t *testing.T, s *Service, p Principal, f, n, c, req string) map[string]any {
	return call(t, s, p, "create_knowledgebase", map[string]any{"folder_path": f, "filename": n, "type": "skill", "title": "Guide", "content": c, "request_id": req})
}
func commit(t *testing.T, s *Service, p Principal, e map[string]any, req string) map[string]any {
	return call(t, s, p, "commit_knowledgebase", map[string]any{"entries": []any{map[string]any{"entry_id": e["entry_id"], "expected_version": e["version"]}}, "message": "Backup guide", "request_id": req})
}
func push(t *testing.T, s *Service, p Principal, r map[string]any, req string) map[string]any {
	return call(t, s, p, "push_knowledgebase", map[string]any{"receipt_id": r["receipt_id"], "request_id": req})
}
func TestPermissionsAtomicPatchPartialReadAndRetries(t *testing.T) {
	s, a, p := fixture(t, false)
	folder(t, s, a, "", "Payments")
	folder(t, s, a, "Payments", "Checkout")
	folder(t, s, a, "Payments", "Billing")
	grant(t, s, a, "priya", "Payments/Checkout", "Reader", "g1")
	grant(t, s, a, "editor", "Payments/Checkout", "Editor", "g2")
	ed := Principal{IdentityID: "editor"}
	e := create(t, s, ed, "Payments/Checkout", "guide.md", "# Deploy\nHello\n## Rollback\nold\n```md\n## Fake\n```\n## Next\nDone\n", "c1")
	create(t, s, a, "Payments/Billing", "secret.md", "secret", "c2")
	r := call(t, s, p, "read_knowledgebase", map[string]any{"entry_id": e["entry_id"]})
	if !strings.Contains(r["content"].(string), "Hello") {
		t.Fatal("live write invisible")
	}
	code(t, s, p, "read_knowledgebase", map[string]any{"path": "Payments/Billing/secret.md"}, "NOT_FOUND")
	code(t, s, p, "read_knowledgebase", map[string]any{"path": "Payments/Billing/missing.md"}, "NOT_FOUND")
	code(t, s, p, "update_knowledgebase", map[string]any{"entry_id": e["entry_id"], "expected_version": e["version"], "content": "bad", "request_id": "w1"}, "FORBIDDEN")
	section := call(t, s, p, "read_knowledgebase", map[string]any{"entry_id": e["entry_id"], "section": map[string]any{"heading": "Rollback"}})
	if section["content"] != "## Rollback\nold\n```md\n## Fake\n```\n" {
		t.Fatalf("section %q", section["content"])
	}
	code(t, s, p, "read_knowledgebase", map[string]any{"entry_id": e["entry_id"], "section": map[string]any{"heading": "Fake"}}, "SECTION_NOT_FOUND")
	code(t, s, p, "read_knowledgebase", map[string]any{"entry_id": e["entry_id"], "start_line": 100, "end_line": 110}, "RANGE_OUT_OF_BOUNDS")
	args := map[string]any{"entry_id": e["entry_id"], "expected_version": e["version"], "diff": "--- a/guide.md\n+++ b/guide.md\n@@ -3,2 +3,2 @@\n ## Rollback\n-old\n+new\n", "request_id": "u1"}
	updated := call(t, s, ed, "update_knowledgebase", args)
	retry := call(t, s, ed, "update_knowledgebase", args)
	if retry["version"] != updated["version"] {
		t.Fatal("retry mutation repeated")
	}
	code(t, s, ed, "update_knowledgebase", map[string]any{"entry_id": e["entry_id"], "expected_version": e["version"], "content": "stale", "request_id": "u2"}, "VERSION_CONFLICT")
	code(t, s, ed, "update_knowledgebase", map[string]any{"entry_id": e["entry_id"], "expected_version": updated["version"], "diff": "--- a/guide.md\n+++ b/guide.md\n@@ -2,1 +2,1 @@\n-Hello\n+Hi\n@@ -9,1 +9,1 @@\n-absent\n+Fail\n", "request_id": "u3"}, "PATCH_FAILED")
	after := call(t, s, p, "read_knowledgebase", map[string]any{"entry_id": e["entry_id"]})
	if !strings.Contains(after["content"].(string), "Hello") {
		t.Fatal("partial patch wrote")
	}
	listed := call(t, s, p, "list_knowledgebase", map[string]any{"depth": 10})
	if strings.Contains(stringJSON(listed), "Billing") {
		t.Fatal("inaccessible sibling leaked")
	}
	call(t, s, a, "manage_knowledgebase_access", map[string]any{"action": "revoke", "folder_path": "Payments/Checkout", "identity_id": "editor", "request_id": "revoke", "expected_acl_version": call(t, s, a, "get_knowledgebase_access", map[string]any{"folder_path": "Payments/Checkout"})["acl_version"]})
	code(t, s, ed, "update_knowledgebase", args, "NOT_FOUND")
}
func stringJSON(v any) string {
	return strings.TrimSpace(strings.ReplaceAll(strings.TrimSpace(fmtAny(v)), "\n", " "))
}
func fmtAny(v any) string { b, _ := json.Marshal(v); return string(b) }
func TestGitSnapshotsConcurrencyMetadataAndDeletion(t *testing.T) {
	s, a, p := fixture(t, true)
	folder(t, s, a, "", "Checkout")
	folder(t, s, a, "", "Billing")
	grant(t, s, a, "priya", "Checkout", "Editor", "g1")
	grant(t, s, a, "editor", "Billing", "Editor", "g2")
	q := Principal{IdentityID: "editor"}
	e := create(t, s, p, "Checkout", "guide.md", "first\n", "c1")
	f := create(t, s, q, "Billing", "billing.md", "billing\n", "c2")
	r := commit(t, s, p, e, "b1")
	other := commit(t, s, q, f, "b2")
	newer := call(t, s, p, "update_knowledgebase", map[string]any{"entry_id": e["entry_id"], "expected_version": e["version"], "content": "second\n", "request_id": "u1"})
	push(t, s, p, r, "p1")
	code(t, s, q, "push_knowledgebase", map[string]any{"receipt_id": other["receipt_id"], "request_id": "p2"}, "BACKUP_BRANCH_ADVANCED")
	tip := s.backupState().Tip
	b, err := exec.Command("git", "--git-dir="+s.cfg.BackupRemote, "show", tip+":Checkout/guide.md").Output()
	if err != nil || string(b) != "first\n" {
		t.Fatalf("snapshot %q %v", b, err)
	}
	if _, err = exec.Command("git", "--git-dir="+s.cfg.BackupRemote, "show", tip+":Billing/billing.md").Output(); err == nil {
		t.Fatal("unselected unpublished content leaked")
	}
	st := call(t, s, p, "get_knowledgebase_backup_status", nil)
	if asMap(st["entries"].([]any)[0])["backup_status"] != "pending" {
		t.Fatal(st)
	}
	fresh := commit(t, s, q, f, "b3")
	push(t, s, q, fresh, "p3")
	latest := commit(t, s, p, newer, "b4")
	push(t, s, p, latest, "p4")
	meta := call(t, s, p, "update_knowledgebase", map[string]any{"entry_id": e["entry_id"], "expected_version": newer["version"], "metadata": map[string]any{"title": "New title"}, "request_id": "m1"})
	st = call(t, s, p, "get_knowledgebase_backup_status", nil)
	if asMap(st["entries"].([]any)[0])["backup_status"] != "backed_up" {
		t.Fatal("metadata pending")
	}
	push(t, s, p, r, "retry_pushed")
	d := call(t, s, p, "delete_knowledgebase", map[string]any{"entry_id": e["entry_id"], "expected_version": meta["version"], "request_id": "d1"})
	code(t, s, p, "create_knowledgebase", map[string]any{"folder_path": "Checkout", "filename": "guide.md", "type": "note", "title": "New", "content": "new", "request_id": "c3"}, "PATH_PENDING_DELETION_BACKUP")
	dr := call(t, s, p, "commit_knowledgebase", map[string]any{"deletions": []any{map[string]any{"deletion_id": d["deletion_id"]}}, "message": "Delete", "request_id": "b5"})
	push(t, s, p, dr, "p5")
	replacement := create(t, s, p, "Checkout", "guide.md", "replacement\n", "c4")
	push(t, s, p, dr, "retry_delete")
	after := call(t, s, p, "read_knowledgebase", map[string]any{"entry_id": replacement["entry_id"]})
	if after["content"] != "replacement\n" {
		t.Fatal("old deletion retry removed new content")
	}
	code(t, s, p, "commit_knowledgebase", map[string]any{"deletions": []any{map[string]any{"deletion_id": d["deletion_id"]}}, "message": "Delete again", "request_id": "b6"}, "BACKUP_VERSION_CONFLICT")
}
func TestCapsIdentitySyncCursorAndRecovery(t *testing.T) {
	s, a, p := fixture(t, false)
	grant(t, s, a, "priya", "", "Editor", "g1")
	folder(t, s, a, "", "One")
	folder(t, s, a, "", "Two")
	create(t, s, a, "One", "one.md", "one", "c1")
	create(t, s, a, "Two", "two.md", "two", "c2")
	caps := []Cap{{FolderPath: "One", Role: "Reader"}}
	narrow := p
	narrow.Caps = &caps
	code(t, s, narrow, "read_knowledgebase", map[string]any{"path": "Two/two.md"}, "NOT_FOUND")
	code(t, s, narrow, "manage_knowledgebase_access", map[string]any{"action": "list"}, "FORBIDDEN")
	page := call(t, s, p, "list_knowledgebase", map[string]any{"depth": 10, "limit": 1})
	s.SyncPlatformIdentities(context.Background(), []Identity{{ID: "admin", Name: "Admin"}, {ID: "priya", Name: "Priya"}, {ID: "editor", Name: "Editor"}})
	call(t, s, p, "list_knowledgebase", map[string]any{"depth": 10, "limit": 1, "cursor": page["next_cursor"]})
	svc := call(t, s, a, "manage_knowledgebase_access", map[string]any{"action": "create_service_account", "name": "Workflow", "request_id": "svc1"})
	id := svc["identity_id"].(string)
	if e := s.ValidateServiceTokenIdentity(context.Background(), a, id); e != nil {
		t.Fatal(e)
	}
	if e := s.ValidateServiceTokenIdentity(context.Background(), a, "priya"); e == nil {
		t.Fatal("human service token")
	}
	grant(t, s, a, id, "One", "Reader", "g2")
	call(t, s, Principal{IdentityID: id}, "read_knowledgebase", map[string]any{"path": "One/one.md"})
	call(t, s, a, "manage_knowledgebase_access", map[string]any{"action": "disable_service_account", "identity_id": id, "request_id": "svc2"})
	code(t, s, Principal{IdentityID: id}, "read_knowledgebase", map[string]any{"path": "One/one.md"}, "FORBIDDEN")
	path := filepath.Join(s.live, "One", "recover.md")
	j := journal{Changes: []fileChange{{Path: path, Data: []byte("recovered")}}}
	atomicJSON(filepath.Join(s.private, "mutation-journal.json"), j)
	call(t, s, a, "list_knowledgebase", nil)
	if b, e := os.ReadFile(path); e != nil || string(b) != "recovered" {
		t.Fatal("journal recovery")
	}
}
