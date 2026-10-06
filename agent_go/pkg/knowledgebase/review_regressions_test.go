package knowledgebase

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func gitTest(t *testing.T, args ...string) string {
	t.Helper()
	b, e := exec.Command("git", args...).CombinedOutput()
	if e != nil {
		t.Fatalf("Git %v: %v %s", args, e, b)
	}
	return strings.TrimSpace(string(b))
}
func TestGrantCASAdminBootstrapAndBuilderDiscovery(t *testing.T) {
	s, a, p := fixture(t, false)
	folder(t, s, a, "", "Checkout")
	folder(t, s, a, "", "Billing")
	v := call(t, s, a, "get_knowledgebase_access", map[string]any{"folder_path": "Checkout"})["acl_version"]
	args := map[string]any{"action": "grant", "identity_id": "priya", "folder_path": "Checkout", "role": "Owner", "expected_acl_version": v, "request_id": "g1"}
	out := call(t, s, a, "manage_knowledgebase_access", args)
	if out["acl_version"] == v {
		t.Fatal("ACL version unchanged")
	}
	args["request_id"] = "g2"
	args["role"] = "Editor"
	code(t, s, a, "manage_knowledgebase_access", args, "ACL_VERSION_CONFLICT")
	builder := p
	builder.AccessOnly = true
	list := call(t, s, builder, "manage_knowledgebase_access", map[string]any{"action": "list"})
	if !strings.Contains(stringJSON(list), "Checkout") || strings.Contains(stringJSON(list), "Billing") {
		t.Fatal("builder scope discovery leaked or omitted folders")
	}
	call(t, s, Principal{IdentityID: "new_admin", IsAdmin: true}, "list_knowledgebase", nil)
	if !s.IdentityActive(context.Background(), p.IdentityID) {
		t.Fatal("admin bootstrap disabled peer")
	}
	if _, e := New(Config{Root: s.cfg.Root, OrganizationID: "another"}); e == nil {
		t.Fatal("data root accepted another organization")
	}
}
func TestSQLPatchBodyAndLiteralScopedSearch(t *testing.T) {
	s, a, p := fixture(t, false)
	folder(t, s, a, "", "One")
	folder(t, s, a, "", "Two")
	grant(t, s, a, "priya", "One", "Editor", "g1")
	e := create(t, s, p, "One", "sql.md", "```sql\n-- old\nSELECT 1;\n```\n", "c1")
	create(t, s, a, "Two", "secret.md", "-- new secret", "c2")
	call(t, s, p, "update_knowledgebase", map[string]any{"entry_id": e["entry_id"], "expected_version": e["version"], "diff": "--- a/sql.md\n+++ b/sql.md\n@@ -1,4 +1,4 @@\n ```sql\n--- old\n+-- new\n SELECT 1;\n ```\n", "request_id": "u1"})
	found := call(t, s, p, "search_knowledgebase", map[string]any{"query": "-- new"})
	if !strings.Contains(stringJSON(found), "sql.md") || strings.Contains(stringJSON(found), "secret") {
		t.Fatal("literal search failed scope filter")
	}
}
func TestRemoteBindingHooksAndFailClosedAbsence(t *testing.T) {
	s, a, _ := fixture(t, true)
	e := create(t, s, a, "", "guide.md", "one\n", "c1")
	r := commit(t, s, a, e, "b1")
	hooks := filepath.Join(t.TempDir(), "hooks")
	os.Mkdir(hooks, 0700)
	marker := filepath.Join(hooks, "ran")
	os.WriteFile(filepath.Join(hooks, "pre-push"), []byte("#!/bin/sh\ntouch '"+marker+"'\n"), 0700)
	gitTest(t, "--git-dir="+s.repo(), "config", "core.hooksPath", hooks)
	push(t, s, a, r, "p1")
	if _, e := os.Stat(marker); !os.IsNotExist(e) {
		t.Fatal("host Git hook ran")
	}
	other := filepath.Join(t.TempDir(), "other.git")
	gitTest(t, "init", "--bare", other)
	gitTest(t, "--git-dir="+s.repo(), "config", "remote.origin.pushurl", other)
	code(t, s, a, "commit_knowledgebase", map[string]any{"entries": []any{map[string]any{"entry_id": e["entry_id"], "expected_version": e["version"]}}, "message": "Backup", "request_id": "b2"}, "BACKUP_REMOTE_CHANGED")
	gitTest(t, "--git-dir="+s.repo(), "config", "--unset", "remote.origin.pushurl")
	s.cfg.BackupRemote = other
	code(t, s, a, "commit_knowledgebase", map[string]any{"entries": []any{map[string]any{"entry_id": e["entry_id"], "expected_version": e["version"]}}, "message": "Backup", "request_id": "b3"}, "BACKUP_REMOTE_CHANGED")
	s.cfg.BackupRemote = filepath.Join(filepath.Dir(s.cfg.Root), "remote.git")
	call(t, s, a, "delete_knowledgebase", map[string]any{"entry_id": e["entry_id"], "expected_version": e["version"], "request_id": "d1"})
	if e := os.Rename(s.repo(), s.repo()+".offline"); e != nil {
		t.Fatal(e)
	}
	code(t, s, a, "get_knowledgebase_backup_status", nil, "BACKUP_UNAVAILABLE")
}
func TestUnknownExpiryAndRecheckBeforePublication(t *testing.T) {
	s, a, _ := fixture(t, true)
	e := create(t, s, a, "", "guide.md", "one\n", "c1")
	r := commit(t, s, a, e, "b1")
	receipt, err := s.readReceipt(r["receipt_id"].(string))
	if err != nil {
		t.Fatal(err)
	}
	receipt.State = "PUSH_UNKNOWN"
	receipt.ExpiresAt = time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)
	if err = s.transact([]fileChange{receiptChange(s, receipt)}); err != nil {
		t.Fatal(err)
	}
	code(t, s, a, "push_knowledgebase", map[string]any{"receipt_id": r["receipt_id"], "request_id": "p1"}, "SNAPSHOT_EXPIRED")
	fresh := commit(t, s, a, e, "b2")
	checks := 0
	a.Recheck = func(context.Context) error {
		checks++
		if checks >= 3 {
			return kbErr("FORBIDDEN", "Credential revoked.")
		}
		return nil
	}
	code(t, s, a, "push_knowledgebase", map[string]any{"receipt_id": fresh["receipt_id"], "request_id": "p2"}, "FORBIDDEN")
	if gitTest(t, "--git-dir="+s.cfg.BackupRemote, "for-each-ref", "--format=%(refname)", "refs/heads/main") != "" {
		t.Fatal("revoked credential published")
	}
}
func TestHistoricalRemoteRewindRequiresReconciliation(t *testing.T) {
	s, a, _ := fixture(t, true)
	e := create(t, s, a, "", "guide.md", "one\n", "c1")
	r := commit(t, s, a, e, "b1")
	push(t, s, a, r, "p1")
	old := s.backupState().Tip
	e = call(t, s, a, "update_knowledgebase", map[string]any{"entry_id": e["entry_id"], "expected_version": e["version"], "content": "two\n", "request_id": "u1"})
	newer := commit(t, s, a, e, "b2")
	push(t, s, a, newer, "p2")
	gitTest(t, "--git-dir="+s.cfg.BackupRemote, "update-ref", "refs/heads/main", old)
	code(t, s, a, "commit_knowledgebase", map[string]any{"entries": []any{map[string]any{"entry_id": e["entry_id"], "expected_version": e["version"]}}, "message": "retry", "request_id": "b3"}, "BACKUP_REMOTE_CHANGED")
	status := call(t, s, a, "get_knowledgebase_backup_status", nil)
	if status["external_change"] != true {
		t.Fatal("untrusted remote was reported backed up")
	}
	if _, err := s.ReconcileBackup(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	commit(t, s, a, e, "b4")
}
func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
func TestExactBaseCASRejectsRaceWithRemoteRewind(t *testing.T) {
	s, a, _ := fixture(t, true)
	e := create(t, s, a, "", "guide.md", "one\n", "c1")
	push(t, s, a, commit(t, s, a, e, "b1"), "p1")
	old := s.backupState().Tip
	e = call(t, s, a, "update_knowledgebase", map[string]any{"entry_id": e["entry_id"], "expected_version": e["version"], "content": "two\n", "request_id": "u1"})
	push(t, s, a, commit(t, s, a, e, "b2"), "p2")
	e = call(t, s, a, "update_knowledgebase", map[string]any{"entry_id": e["entry_id"], "expected_version": e["version"], "content": "three\n", "request_id": "u2"})
	r := commit(t, s, a, e, "b3")
	real, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	script := "#!/bin/sh\nfor arg in \"$@\"; do\n if [ \"$arg\" = push ]; then\n " + shellQuote(real) + " --git-dir=" + shellQuote(s.cfg.BackupRemote) + " update-ref refs/heads/main " + shellQuote(old) + "\n fi\ndone\nexec " + shellQuote(real) + " \"$@\"\n"
	if err = os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	code(t, s, a, "push_knowledgebase", map[string]any{"receipt_id": r["receipt_id"], "request_id": "p3"}, "BACKUP_BRANCH_ADVANCED")
	if got := gitTest(t, "--git-dir="+s.cfg.BackupRemote, "rev-parse", "refs/heads/main"); got != old {
		t.Fatal("CAS overwrote a changed remote")
	}
}

func TestGrantJournalRecoversAfterSQLiteBeforeRequestOutcome(t *testing.T) {
	s, a, p := fixture(t, false)
	folder(t, s, a, "", "Checkout")
	args := map[string]any{"action": "grant", "folder_path": "Checkout", "identity_id": p.IdentityID, "role": "Reader", "request_id": "g_crash", "expected_acl_version": call(t, s, a, "get_knowledgebase_access", map[string]any{"folder_path": "Checkout"})["acl_version"]}
	result, changes, e := s.manage(context.Background(), a, args)
	if e != nil {
		t.Fatal(e)
	}
	requestPath, hash, e := s.requestPath(a, "manage_knowledgebase_access", args)
	if e != nil {
		t.Fatal(e)
	}
	changes = append(changes, jsonChange(requestPath, requestRecord{Hash: hash, At: stamp(), Result: result}))
	if e = atomicJSON(filepath.Join(s.private, "mutation-journal.json"), journal{Changes: changes}); e != nil {
		t.Fatal(e)
	}
	if e = s.applyAccess(*changes[0].Access); e != nil {
		t.Fatal(e)
	}
	generation := s.nextSecurityGeneration()
	if _, e = os.Stat(requestPath); !os.IsNotExist(e) {
		t.Fatal("fixture already wrote outcome")
	}
	retried := call(t, s, a, "manage_knowledgebase_access", args)
	if retried["acl_version"] != asMap(result)["acl_version"] {
		t.Fatal("recovered grant changed result")
	}
	if s.nextSecurityGeneration() != generation {
		t.Fatal("replay advanced security generation twice")
	}
	if s.effective(p, "Checkout") != roleReader {
		t.Fatal("grant not recovered")
	}
}
func TestDisabledIdentityWaitingForLockCannotRead(t *testing.T) {
	s, a, p := fixture(t, false)
	grant(t, s, a, p.IdentityID, "", "Reader", "g1")
	create(t, s, a, "", "guide.md", "protected", "c1")
	unlock, e := s.lock(context.Background(), false)
	if e != nil {
		t.Fatal(e)
	}
	started := make(chan struct{})
	first := true
	p.Recheck = func(context.Context) error {
		if first {
			first = false
			close(started)
		}
		return nil
	}
	done := make(chan error, 1)
	go func() {
		_, e := s.Call(context.Background(), p, "read_knowledgebase", map[string]any{"path": "guide.md"})
		done <- e
	}()
	<-started
	ids, e := s.identities()
	if e != nil {
		t.Fatal(e)
	}
	id := ids[p.IdentityID]
	id.Disabled = true
	ids[p.IdentityID] = id
	if e = atomicJSON(filepath.Join(s.private, "identities.json"), ids); e != nil {
		t.Fatal(e)
	}
	unlock()
	e = <-done
	ke, ok := e.(*Error)
	if !ok || ke.Code != "FORBIDDEN" {
		t.Fatalf("disabled queued reader %v", e)
	}
}
func TestReconciliationInvalidatesOutstandingSnapshots(t *testing.T) {
	s, a, _ := fixture(t, true)
	other := create(t, s, a, "", "other.md", "other", "c1")
	push(t, s, a, commit(t, s, a, other, "b1"), "p1")
	base := s.backupState().Tip
	e := create(t, s, a, "", "guide.md", "older", "c2")
	old := commit(t, s, a, e, "b2")
	newer := call(t, s, a, "update_knowledgebase", map[string]any{"entry_id": e["entry_id"], "expected_version": e["version"], "content": "newer", "request_id": "u1"})
	push(t, s, a, commit(t, s, a, newer, "b3"), "p2")
	gitTest(t, "--git-dir="+s.cfg.BackupRemote, "update-ref", "refs/heads/main", base)
	if _, e := s.ReconcileBackup(context.Background(), a); e != nil {
		t.Fatal(e)
	}
	code(t, s, a, "push_knowledgebase", map[string]any{"receipt_id": old["receipt_id"], "request_id": "p3"}, "BACKUP_BRANCH_ADVANCED")
	if s.backupState().Paths["guide.md"].Sequence < 2 {
		t.Fatal("reconciliation lost published high-water sequence")
	}
}

func TestControlledFullRestoreKeepsUnpublishedFilesMetadataGrantsAndReceipts(t *testing.T) {
	s, a, p := fixture(t, true)
	folder(t, s, a, "", "Checkout")
	folder(t, s, a, "", "Other")
	grant(t, s, a, p.IdentityID, "Checkout", "Reader", "g1")
	e := create(t, s, a, "Checkout", "guide.md", "snapshot\n", "c1")
	receipt := commit(t, s, a, e, "b1")
	updated := call(t, s, a, "update_knowledgebase", map[string]any{"entry_id": e["entry_id"], "expected_version": e["version"], "content": "unpublished update\n", "metadata": map[string]any{"title": "Restored title", "tags": []any{"restore"}}, "request_id": "u1"})
	create(t, s, a, "Other", "secret.md", "secret", "c2")
	if _, err := s.db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	restoredRoot := filepath.Join(t.TempDir(), "restored")
	copyTree(t, s.cfg.Root, restoredRoot)
	cfg := s.cfg
	cfg.Root = restoredRoot
	restored, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	caps := []Cap{{FolderPath: "Checkout", Role: "Reader"}}
	p.Caps = &caps
	read := call(t, restored, p, "read_knowledgebase", map[string]any{"entry_id": e["entry_id"]})
	if read["content"] != "unpublished update\n" || read["title"] != "Restored title" || read["version"] != updated["version"] {
		t.Fatal("restore lost unpublished file, metadata, or version")
	}
	code(t, restored, p, "read_knowledgebase", map[string]any{"path": "Other/secret.md"}, "NOT_FOUND")
	push(t, restored, a, receipt, "p1")
	status := call(t, restored, p, "get_knowledgebase_backup_status", nil)
	if asMap(status["entries"].([]any)[0])["backup_status"] != "pending" {
		t.Fatal("restored earlier snapshot marked later live update backed up")
	}
	cached := call(t, restored, a, "update_knowledgebase", map[string]any{"entry_id": e["entry_id"], "expected_version": e["version"], "content": "unpublished update\n", "metadata": map[string]any{"title": "Restored title", "tags": []any{"restore"}}, "request_id": "u1"})
	if cached["version"] != updated["version"] {
		t.Fatal("restore lost mutation retry outcome")
	}
}
func copyTree(t *testing.T, from, to string) {
	t.Helper()
	err := filepath.WalkDir(from, func(p string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		rel, e := filepath.Rel(from, p)
		if e != nil {
			return e
		}
		dest := filepath.Join(to, rel)
		if d.IsDir() {
			return os.MkdirAll(dest, 0700)
		}
		if d.Type()&os.ModeSymlink != 0 {
			t.Fatal("unexpected symlink in recovery source")
		}
		b, e := os.ReadFile(p)
		if e != nil {
			return e
		}
		return os.WriteFile(dest, b, 0600)
	})
	if err != nil {
		t.Fatal(err)
	}
}
func TestCopiedPendingJournalCannotWriteOldRoot(t *testing.T) {
	s, _, _ := fixture(t, false)
	oldPath := filepath.Join(s.live, "outside.md")
	if err := atomicJSON(filepath.Join(s.private, "mutation-journal.json"), journal{Changes: []fileChange{{Path: oldPath, Data: []byte("must not write")}}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	newRoot := filepath.Join(t.TempDir(), "restore")
	copyTree(t, s.cfg.Root, newRoot)
	cfg := s.cfg
	cfg.Root = newRoot
	if restored, err := New(cfg); err == nil {
		restored.Close()
		t.Fatal("copied pending absolute journal accepted")
	}
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatal("restore wrote original root")
	}
}
func TestRecreatedPathHasFreshOpaqueVersion(t *testing.T) {
	s, a, _ := fixture(t, true)
	e := create(t, s, a, "", "guide.md", "same", "c1")
	r := commit(t, s, a, e, "b1")
	push(t, s, a, r, "p1")
	d := call(t, s, a, "delete_knowledgebase", map[string]any{"entry_id": e["entry_id"], "expected_version": e["version"], "request_id": "d1"})
	dr := call(t, s, a, "commit_knowledgebase", map[string]any{"deletions": []any{map[string]any{"deletion_id": d["deletion_id"]}}, "message": "delete", "request_id": "b2"})
	push(t, s, a, dr, "p2")
	fresh := create(t, s, a, "", "guide.md", "same", "c2")
	if fresh["version"] == e["version"] {
		t.Fatal("replacement reused retired opaque version")
	}
	code(t, s, a, "update_knowledgebase", map[string]any{"path": "guide.md", "expected_version": e["version"], "content": "stale overwrite", "request_id": "u1"}, "VERSION_CONFLICT")
}
