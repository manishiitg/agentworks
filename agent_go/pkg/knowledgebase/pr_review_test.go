package knowledgebase

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
)

func TestLiveOnlyDeletedPathCanBeRecreated(t *testing.T) {
	s, admin, _ := fixture(t, false)
	old := create(t, s, admin, "", "guide.md", "old", "create-old")
	deleted := call(t, s, admin, "delete_knowledgebase", map[string]any{"entry_id": old["entry_id"], "expected_version": old["version"], "request_id": "delete-old"})
	status := call(t, s, admin, "get_knowledgebase_backup_status", nil)
	if asMap(status["deletions"].([]any)[0])["backup_status"] != "not_required" {
		t.Fatal("live-only deletion marked pending backup", status)
	}
	replacement := create(t, s, admin, "", "Guide.md", "replacement", "create-new")
	if replacement["entry_id"] == old["entry_id"] {
		t.Fatal("replacement reused the deleted identity")
	}
	call(t, s, admin, "delete_knowledgebase", map[string]any{"entry_id": old["entry_id"], "expected_version": old["version"], "request_id": "delete-old"})
	if got := call(t, s, admin, "read_knowledgebase", map[string]any{"entry_id": replacement["entry_id"]}); got["content"] != "replacement" {
		t.Fatal("old delete retry affected replacement", got)
	}
	// Configuring backup later must not let a historical tombstone delete
	// the newly created entry from the same path.
	s.cfg.BackupRemote = "unused-remote"
	_, err := s.collect(admin, map[string]any{"deletions": []any{map[string]any{"deletion_id": deleted["deletion_id"]}}})
	if failure, ok := err.(*Error); !ok || failure.Code != "BACKUP_VERSION_CONFLICT" {
		t.Fatal("historical deletion selected over replacement", err)
	}
}

func TestValidateCapsUsesLiveGrantsAndIdentity(t *testing.T) {
	s, admin, reader := fixture(t, false)
	folder(t, s, admin, "", "Payments")
	folder(t, s, admin, "Payments", "Checkout")
	folder(t, s, admin, "", "Private")
	grant(t, s, admin, reader.IdentityID, "Payments", "Reader", "grant")
	entry := create(t, s, admin, "Payments", "guide.md", "text", "create")
	cases := []struct {
		name, identity string
		caps           *[]Cap
		want           string
	}{
		{"unrestricted", reader.IdentityID, nil, ""},
		{"empty", reader.IdentityID, &[]Cap{}, ""},
		{"inherited", reader.IdentityID, &[]Cap{{FolderPath: "Payments/Checkout", Role: "Reader"}}, ""},
		{"overgrant", reader.IdentityID, &[]Cap{{FolderPath: "Payments", Role: "Editor"}}, "FORBIDDEN"},
		{"outside", reader.IdentityID, &[]Cap{{FolderPath: "Private", Role: "Reader"}}, "FORBIDDEN"},
		{"owner", reader.IdentityID, &[]Cap{{FolderPath: "Payments", Role: "Owner"}}, "INVALID_ARGUMENT"},
		{"missing", reader.IdentityID, &[]Cap{{FolderPath: "Missing", Role: "Reader"}}, "NOT_FOUND"},
		{"impersonation", "admin", nil, "FORBIDDEN"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := s.ValidateCaps(context.Background(), reader, tc.identity, tc.caps)
			if tc.want == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if failure, ok := err.(*Error); !ok || failure.Code != tc.want {
				t.Fatalf("got %v, want %s", err, tc.want)
			}
		})
	}
	empty := reader
	empty.Caps = &[]Cap{}
	code(t, s, empty, "read_knowledgebase", map[string]any{"entry_id": entry["entry_id"]}, "NOT_FOUND")
	call(t, s, admin, "manage_knowledgebase_access", map[string]any{"action": "revoke", "folder_path": "Payments", "identity_id": reader.IdentityID, "request_id": "revoke", "expected_acl_version": call(t, s, admin, "get_knowledgebase_access", map[string]any{"folder_path": "Payments"})["acl_version"]})
	if err := s.ValidateCaps(context.Background(), reader, reader.IdentityID, &[]Cap{{FolderPath: "Payments", Role: "Reader"}}); err == nil {
		t.Fatal("revoked grant validated")
	}
	if err := s.ValidateCaps(context.Background(), admin, "missing", nil); err == nil {
		t.Fatal("unknown identity validated")
	}
}

// Someone with no Brain folder opens Brain and is told so, not "Resource not found." (Excellence 2026-10-09).
func TestListingBrainWithoutAnyFolderSaysSo(t *testing.T) {
	s, admin, reader := fixture(t, false)
	folder(t, s, admin, "", "Payments")
	_, err := s.Call(context.Background(), reader, "list_knowledgebase_folders", map[string]any{})
	failure, ok := err.(*Error)
	if !ok || failure.Code != "NOT_FOUND" || !strings.Contains(failure.Message, "do not have access to any Brain folder") {
		t.Fatalf("root listing without access: %v", err)
	}
	// A specific folder they cannot see keeps the uniform answer.
	_, err = s.Call(context.Background(), reader, "list_knowledgebase_folders", map[string]any{"folder_path": "Payments"})
	if failure, ok := err.(*Error); !ok || failure.Message != "Resource not found." {
		t.Fatalf("hidden folder: %v", err)
	}
}

func TestRevokedFolderGrantCannotPublishPreparedReceipt(t *testing.T) {
	s, admin, writer := fixture(t, true)
	folder(t, s, admin, "", "Payments")
	grant(t, s, admin, writer.IdentityID, "Payments", "Editor", "grant")
	entry := create(t, s, writer, "Payments", "guide.md", "private", "create")
	receipt := commit(t, s, writer, entry, "commit")
	call(t, s, admin, "manage_knowledgebase_access", map[string]any{"action": "revoke", "folder_path": "Payments", "identity_id": writer.IdentityID, "request_id": "revoke", "expected_acl_version": call(t, s, admin, "get_knowledgebase_access", map[string]any{"folder_path": "Payments"})["acl_version"]})
	if _, err := s.Call(context.Background(), writer, "push_knowledgebase", map[string]any{"receipt_id": receipt["receipt_id"], "request_id": "push"}); err == nil {
		t.Fatal("revoked folder grant published")
	}
	if tip := gitTest(t, "--git-dir="+s.cfg.BackupRemote, "for-each-ref", "--format=%(refname)", "refs/heads/main"); tip != "" {
		t.Fatal("remote branch changed", tip)
	}
}

func TestEntryAndFolderNamesRejectUnsafePaths(t *testing.T) {
	s, admin, _ := fixture(t, false)
	invalid := []string{"", ".", "..", "../secret", "/absolute", "a/b", `a\b`, " space", "space ", "CON", "nul", "COM1", "LPT9", "a\x00b", strings.Repeat("x", 129)}
	for i, name := range invalid {
		code(t, s, admin, "create_knowledgebase_folder", map[string]any{"folder_path": "", "name": name, "request_id": fmt.Sprintf("folder-%d", i)}, "INVALID_ARGUMENT")
		code(t, s, admin, "create_knowledgebase", map[string]any{"folder_path": "", "filename": name + ".md", "type": "note", "title": "Title", "content": "text", "request_id": fmt.Sprintf("entry-%d", i)}, "INVALID_ARGUMENT")
	}
	// Any file type is allowed (owner, 2026-10-06), but not programs, by extension or by signature.
	for i, name := range []string{"tool.exe", "lib.so", "app.DMG", ".hidden.md", "a..b.md", strings.Repeat("x", 126) + ".md"} {
		code(t, s, admin, "create_knowledgebase", map[string]any{"folder_path": "", "filename": name, "type": "note", "title": "Title", "content": "text", "request_id": fmt.Sprintf("extension-%d", i)}, "INVALID_ARGUMENT")
	}
	code(t, s, admin, "create_knowledgebase", map[string]any{"folder_path": "", "filename": "renamed.png", "type": "source", "title": "Program", "content_base64": base64.StdEncoding.EncodeToString([]byte("\x7fELF\x02\x01\x01\x00binary")), "request_id": "elf"}, "INVALID_ARGUMENT")
	png := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0, 0, 0, 0}
	for i, name := range []string{"guide", "guide.txt", "deck.pptx", "logo.png"} {
		args := map[string]any{"folder_path": "", "filename": name, "type": "source", "title": "Title", "request_id": fmt.Sprintf("ok-%d", i), "content": "text"}
		if name == "logo.png" {
			delete(args, "content")
			args["content_base64"] = base64.StdEncoding.EncodeToString(png)
		}
		if _, err := s.Call(context.Background(), admin, "create_knowledgebase", args); err != nil {
			t.Fatalf("%s must be accepted: %v", name, err)
		}
	}
	read, err := s.Call(context.Background(), admin, "read_knowledgebase", map[string]any{"path": "logo.png"})
	if err != nil || read.(map[string]any)["content_base64"] != base64.StdEncoding.EncodeToString(png) || read.(map[string]any)["binary"] != true {
		t.Fatalf("a binary file must read back whole: %v %v", read, err)
	}
	folder(t, s, admin, "", "Payments")
	code(t, s, admin, "create_knowledgebase_folder", map[string]any{"folder_path": "", "name": "payments", "request_id": "case-folder"}, "NAME_CONFLICT")
	create(t, s, admin, "Payments", "guide.md", "text", "valid")
	code(t, s, admin, "create_knowledgebase", map[string]any{"folder_path": "Payments", "filename": "GUIDE.md", "type": "note", "title": "Title", "content": "text", "request_id": "case-file"}, "NAME_CONFLICT")
}
