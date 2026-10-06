package knowledgebase

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBackupPATEncryptionRotationAndRemoval(t *testing.T) {
	s, admin, _ := fixture(t, false)
	s.cfg.BackupEncryptionKey = strings.Repeat("k", 32)
	admin.AccessOnly = true
	pat := "kb-test-only-token-value"
	args := map[string]any{"action": "configure_backup", "remote_url": "https://github.com/org/knowledge", "username": "kb-user", "pat": pat, "request_id": "pat-setup"}
	got := mcpCall(t, s, admin, "manage_knowledgebase_access", args)
	if got["pat_configured"] != true || got["pat"] != nil || got["encrypted_pat"] != nil {
		t.Fatal("unsafe setup result", got)
	}
	d, err := s.configuredBackupDestination()
	if err != nil {
		t.Fatal(err)
	}
	if d.EncryptedPAT == "" || d.EncryptedPAT == pat {
		t.Fatal("PAT not encrypted")
	}
	if d.Remote != "https://github.com/org/knowledge.git" {
		t.Fatal("GitHub page URL was not normalized", d.Remote)
	}
	if value, err := s.decryptBackupPAT(d); err != nil || value != pat {
		t.Fatal("PAT cannot be used", err)
	}
	if err := filepath.WalkDir(s.private, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bytes.Contains(raw, []byte(pat)) {
			t.Errorf("plaintext PAT in %s", filepath.Base(path))
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	reopened, err := New(s.cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	loaded, err := reopened.configuredBackupDestination()
	if err != nil {
		t.Fatal(err)
	}
	if value, err := reopened.decryptBackupPAT(loaded); err != nil || value != pat {
		t.Fatal("credential lost on restart", err)
	}
	tampered := loaded
	tampered.Username = "other"
	if _, err := reopened.decryptBackupPAT(tampered); err == nil {
		t.Fatal("credential not bound to username")
	}
	// Omitting the field keeps the secret; an explicit empty string removes it.
	without := merge(args, map[string]any{"request_id": "pat-keep"})
	delete(without, "pat")
	mcpCall(t, s, admin, "manage_knowledgebase_access", without)
	mcpCall(t, s, admin, "manage_knowledgebase_access", merge(args, map[string]any{"pat": "replacement-test-only", "request_id": "pat-rotate"}))
	mcpCall(t, s, admin, "manage_knowledgebase_access", merge(args, map[string]any{"pat": "", "request_id": "pat-remove"}))
	d, err = s.configuredBackupDestination()
	if err != nil || d.EncryptedPAT != "" {
		t.Fatal("PAT not removed", err)
	}
	mcpError(t, s, admin, "manage_knowledgebase_access", merge(args, map[string]any{"remote_url": "git@github.com:org/knowledge.git", "request_id": "pat-ssh"}), "INVALID_ARGUMENT")
}
