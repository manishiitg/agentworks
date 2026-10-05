package knowledgebase

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestReaderResponsesOmitPrivateEntryFields(t *testing.T) {
	s, admin, reader := fixture(t, true)
	grant(t, s, admin, reader.IdentityID, "", "Reader", "reader")
	entry := create(t, s, admin, "", "guide.md", "needle\n", "create")
	for _, tool := range []string{"list_knowledgebase", "read_knowledgebase", "search_knowledgebase", "get_knowledgebase_backup_status"} {
		t.Run(tool, func(t *testing.T) {
			args := map[string]any{}
			if tool == "read_knowledgebase" {
				args["entry_id"] = entry["entry_id"]
			}
			if tool == "search_knowledgebase" {
				args["query"] = "needle"
			}
			result := call(t, s, reader, tool, args)
			for _, key := range []string{"sequence", "content_sequence", "fingerprint"} {
				if strings.Contains(stringJSON(result), `"`+key+`":`) {
					t.Fatalf("%s exposed %s", tool, key)
				}
			}
			if !strings.Contains(stringJSON(result), entry["version"].(string)) {
				t.Fatal("public version is missing")
			}
		})
	}
}

func TestContentEncodingRejectionIsAtomic(t *testing.T) {
	s, admin, _ := fixture(t, false)
	entry := create(t, s, admin, "", "guide.md", "original\n", "create")
	for i, text := range []string{"nul\x00byte", string([]byte{0xff, 0xfe}), "binary\x01control", "escape\x1bsequence"} {
		args := map[string]any{"folder_path": "", "filename": fmt.Sprintf("invalid%d.md", i), "type": "note", "title": "Invalid", "content": text, "request_id": fmt.Sprintf("create-invalid-%d", i)}
		code(t, s, admin, "create_knowledgebase", args, "INVALID_ARGUMENT")
		for _, mode := range []string{"content", "diff"} {
			value := text
			if mode == "diff" {
				value = "--- a/guide.md\n+++ b/guide.md\n@@ -1 +1 @@\n-original\n+" + text + "\n"
			}
			code(t, s, admin, "update_knowledgebase", map[string]any{"entry_id": entry["entry_id"], "expected_version": entry["version"], mode: value, "metadata": map[string]any{"title": "Must not save"}, "request_id": fmt.Sprintf("update-invalid-%s-%d", mode, i)}, "INVALID_ARGUMENT")
		}
	}
	read := call(t, s, admin, "read_knowledgebase", map[string]any{"entry_id": entry["entry_id"]})
	if read["content"] != "original\n" || read["version"] != entry["version"] || asMap(read["entry"])["title"] != "Guide" {
		t.Fatal("rejected input changed the entry", read)
	}
	if len(call(t, s, admin, "list_knowledgebase", nil)["entries"].([]any)) != 1 {
		t.Fatal("rejected content left an entry behind")
	}
}

func TestNormalizedTextSupportsReadsPatchesAndNoOpUpdates(t *testing.T) {
	s, admin, _ := fixture(t, false)
	entry := create(t, s, admin, "", "guide.md", "# Guide\r\ncafé\rfirst\r\nlast\tline\r", "create")
	read := call(t, s, admin, "read_knowledgebase", map[string]any{"entry_id": entry["entry_id"]})
	if read["content"] != "# Guide\ncafé\nfirst\nlast\tline\n" {
		t.Fatalf("line endings or Unicode changed: %q", read["content"])
	}
	lines := call(t, s, admin, "read_knowledgebase", map[string]any{"entry_id": entry["entry_id"], "start_line": 2, "end_line": 3})
	if lines["content"] != "café\nfirst\n" {
		t.Fatal("line range disagrees with normalized content", lines)
	}
	patch := "--- a/guide.md\r\n+++ b/guide.md\r\n@@ -2,2 +2,2 @@\r\n café\r\n-first\r\n+updated\r\n"
	updated := call(t, s, admin, "update_knowledgebase", map[string]any{"entry_id": entry["entry_id"], "expected_version": entry["version"], "diff": patch, "request_id": "patch"})
	read = call(t, s, admin, "read_knowledgebase", map[string]any{"entry_id": entry["entry_id"]})
	if read["content"] != "# Guide\ncafé\nupdated\nlast\tline\n" {
		t.Fatalf("CRLF patch failed to match LF context: %q", read["content"])
	}
	noop := call(t, s, admin, "update_knowledgebase", map[string]any{"entry_id": entry["entry_id"], "expected_version": updated["version"], "content": strings.ReplaceAll(read["content"].(string), "\n", "\r\n"), "request_id": "noop"})
	if noop["changed"] != false || noop["version"] != updated["version"] {
		t.Fatal("equivalent line endings changed the version", noop)
	}
}

func TestTagsAreUniqueNonEmptyAndUpdatesAreAtomic(t *testing.T) {
	s, admin, _ := fixture(t, false)
	entry := create(t, s, admin, "", "guide.md", "original\n", "create")
	for i, tags := range [][]string{{""}, {" \t "}, {"runbook", "runbook"}} {
		code(t, s, admin, "create_knowledgebase", map[string]any{"folder_path": "", "filename": fmt.Sprintf("invalid%d.md", i), "type": "note", "title": "Invalid", "content": "invalid", "tags": tags, "request_id": fmt.Sprintf("create-tags-%d", i)}, "INVALID_ARGUMENT")
		code(t, s, admin, "update_knowledgebase", map[string]any{"entry_id": entry["entry_id"], "expected_version": entry["version"], "content": "must not save", "metadata": map[string]any{"tags": tags}, "request_id": fmt.Sprintf("update-tags-%d", i)}, "INVALID_ARGUMENT")
	}
	read := call(t, s, admin, "read_knowledgebase", map[string]any{"entry_id": entry["entry_id"]})
	if read["content"] != "original\n" || read["version"] != entry["version"] {
		t.Fatal("invalid tags partially saved a content update", read)
	}
	updated := call(t, s, admin, "update_knowledgebase", map[string]any{"entry_id": entry["entry_id"], "expected_version": entry["version"], "metadata": map[string]any{"tags": []string{"runbook", "支付"}}, "request_id": "valid-tags"})
	cleared := call(t, s, admin, "update_knowledgebase", map[string]any{"entry_id": entry["entry_id"], "expected_version": updated["version"], "metadata": map[string]any{"tags": []string{}}, "request_id": "clear-tags"})
	if len(cleared["tags"].([]any)) != 0 {
		t.Fatal("empty tag list did not clear tags")
	}
}

func TestBackupErrorPersistsAndSuccessfulRetryClearsIt(t *testing.T) {
	s, admin, reader := fixture(t, true)
	grant(t, s, admin, reader.IdentityID, "", "Reader", "reader")
	entry := create(t, s, admin, "", "guide.md", "live\n", "create")
	receipt := commit(t, s, admin, entry, "prepare")
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	originalPath := os.Getenv("PATH")
	dir := t.TempDir()
	script := "#!/bin/sh\nfor arg in \"$@\"; do\n if [ \"$arg\" = push ]; then exit 1; fi\ndone\nexec " + shellQuote(realGit) + " \"$@\"\n"
	if err = os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+originalPath)
	code(t, s, admin, "push_knowledgebase", map[string]any{"receipt_id": receipt["receipt_id"], "request_id": "push"}, "BACKUP_UNAVAILABLE")
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := New(s.cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { restarted.Close() })
	status := call(t, restarted, reader, "get_knowledgebase_backup_status", nil)
	message, _ := status["last_backup_error"].(string)
	if message == "" || strings.Contains(message, dir) || strings.Contains(message, s.cfg.BackupRemote) {
		t.Fatal("backup failure status missing or contains private backend paths", status)
	}
	if call(t, restarted, reader, "read_knowledgebase", map[string]any{"entry_id": entry["entry_id"]})["content"] != "live\n" {
		t.Fatal("failed publication blocked live content")
	}
	code(t, restarted, admin, "commit_knowledgebase", map[string]any{"message": "Invalid selection", "request_id": "invalid"}, "INVALID_ARGUMENT")
	code(t, restarted, reader, "commit_knowledgebase", map[string]any{"entries": []any{map[string]any{"entry_id": entry["entry_id"], "expected_version": entry["version"]}}, "message": "Unauthorized selection", "request_id": "unauthorized"}, "FORBIDDEN")
	if restarted.backupState().LastError != message {
		t.Fatal("argument error changed organization backup status")
	}
	t.Setenv("PATH", originalPath)
	push(t, restarted, admin, receipt, "push")
	status = call(t, restarted, reader, "get_knowledgebase_backup_status", nil)
	if status["last_backup_error"] != "" || restarted.backupState().LastSuccessfulAt == "" {
		t.Fatal("confirmed publication did not clear the failure", status)
	}
}

func TestRecoveredPublicationClearsBackupError(t *testing.T) {
	s, admin, _ := fixture(t, true)
	entry := create(t, s, admin, "", "guide.md", "live\n", "create")
	prepared := commit(t, s, admin, entry, "prepare")
	receipt, err := s.readReceipt(prepared["receipt_id"].(string))
	if err != nil {
		t.Fatal(err)
	}
	// Model process death after delivery but before recording confirmation.
	receipt.State = "PUSH_UNKNOWN"
	receipt.LastError = "Backup delivery needs reconciliation."
	st := s.backupState()
	st.LastError = receipt.LastError
	if err = s.transact([]fileChange{receiptChange(s, receipt), jsonChange(filepath.Join(s.private, "backup-state.json"), st)}); err != nil {
		t.Fatal(err)
	}
	gitTest(t, "--git-dir="+s.repo(), "push", "origin", receipt.Commit+":refs/heads/main")
	recovered := push(t, s, admin, prepared, "recover")
	if recovered["state"] != "PUSHED" || recovered["last_backup_error"] != nil || s.backupState().LastError != "" {
		t.Fatal("confirmed recovery retained backup failure", recovered)
	}
	if status := call(t, s, admin, "get_knowledgebase_backup_status", nil); status["last_backup_error"] != "" || asMap(status["entries"].([]any)[0])["backup_status"] != "backed_up" {
		t.Fatal("recovered publication did not update readable backup status", status)
	}
}

func TestIdentitySyncDuringPublicationPreservesLiveReadsAndServiceIdentities(t *testing.T) {
	s, admin, reader := fixture(t, false)
	grant(t, s, admin, reader.IdentityID, "", "Reader", "reader")
	entry := create(t, s, admin, "", "guide.md", "live\n", "create")
	service := call(t, s, admin, "manage_knowledgebase_access", map[string]any{"action": "create_service_account", "name": "Workflow", "request_id": "service"})
	users := []Identity{{ID: "admin", Name: "Admin"}, {ID: "priya", Name: "Priya"}, {ID: "editor", Name: "Editor"}}
	generation := s.nextSecurityGeneration()
	release, err := s.publicationLock()
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SyncPlatformIdentities(context.Background(), users); err != nil {
		release()
		t.Fatal("unchanged identities blocked by publication", err)
	}
	if call(t, s, reader, "read_knowledgebase", map[string]any{"entry_id": entry["entry_id"]})["content"] != "live\n" {
		t.Fatal("publication blocked a live read")
	}
	changed := append(append([]Identity{}, users...), Identity{ID: "new-user", Name: "New user"})
	err = s.SyncPlatformIdentities(context.Background(), changed)
	if failure, ok := err.(*Error); !ok || failure.Code != "BACKUP_BUSY" {
		release()
		t.Fatal("security mutation was not serialized with publication", err)
	}
	if s.IdentityActive(context.Background(), "new-user") || s.nextSecurityGeneration() != generation {
		t.Fatal("busy identity update changed security state")
	}
	release()
	if err = s.SyncPlatformIdentities(context.Background(), changed); err != nil {
		t.Fatal(err)
	}
	if !s.IdentityActive(context.Background(), "new-user") || !s.IdentityActive(context.Background(), service["identity_id"].(string)) {
		t.Fatal("identity retry omitted a new user or removed a service account")
	}
	if s.nextSecurityGeneration() != generation+1 {
		t.Fatal("identity retry did not increment security generation exactly once")
	}
}
