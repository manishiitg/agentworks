package knowledgebase

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestBackupSetupIsAdminOnlyDurableAndDoesNotPublish(t *testing.T) {
	s, admin, owner := fixture(t, false)
	grant(t, s, admin, owner.IdentityID, "", "Owner", "owner")
	admin.AccessOnly = true
	owner.AccessOnly = true
	args := map[string]any{"action": "configure_backup", "username": "git", "remote_url": "https://github.com/org/knowledge-backup.git", "branch": "main", "request_id": "setup"}
	mcpError(t, s, owner, "manage_knowledgebase_access", args, "FORBIDDEN")
	content := admin
	content.AccessOnly = false
	mcpError(t, s, content, "manage_knowledgebase_access", args, "FORBIDDEN")
	capped := admin
	caps := []Cap{}
	capped.Caps = &caps
	mcpError(t, s, capped, "manage_knowledgebase_access", args, "FORBIDDEN")
	if result := mcpCall(t, s, admin, "manage_knowledgebase_access", args); result["configured"] != true {
		t.Fatal(result)
	}
	mcpCall(t, s, admin, "manage_knowledgebase_access", args)
	if _, err := os.Stat(s.repo()); !os.IsNotExist(err) {
		t.Fatal("setup initialized or contacted Git repository", err)
	}
	info, err := os.Stat(filepath.Join(s.private, "backup-destination.json"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("setup state not private", err)
	}
	reopened, err := New(s.cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if configured, err := reopened.BackupConfigured(); err != nil || !configured {
		t.Fatal("setup lost after restart", err)
	}
	if err := s.ensureRepo(t.Context()); err != nil {
		t.Fatal("staging repository ignored saved destination", err)
	}
	// A service opened before setup also observes the persisted destination.
	if configured, err := s.BackupConfigured(); err != nil || !configured {
		t.Fatal("live service did not observe setup", err)
	}
	changed := merge(args, map[string]any{"remote_url": "https://github.com/org/other.git"})
	mcpError(t, s, admin, "manage_knowledgebase_access", changed, "REQUEST_ID_REUSE")
	changed["request_id"] = "redirect"
	// Brain's folder is a plain Git repository (PLAT-633): an app-set destination can be pointed elsewhere, and the
	// folder's origin follows it.
	mcpCall(t, s, admin, "manage_knowledgebase_access", changed)
	if folder, err := s.GitFolder(); err != nil || folder.Remote != "https://github.com/org/other.git" {
		t.Fatalf("destination not changed: %+v %v", folder, err)
	}
	out, err := exec.Command("git", "-C", s.live, "remote", "get-url", "origin").Output()
	if err != nil || strings.TrimSpace(string(out)) != "https://github.com/org/other.git" {
		t.Fatalf("the folder's origin must follow the destination: %q %v", out, err)
	}
}

func TestBackupSetupRejectsUnsafeDestinationsAndKeepsDeploymentConfiguration(t *testing.T) {
	s, admin, _ := fixture(t, false)
	admin.AccessOnly = true
	for _, remote := range []string{"-upload-pack=bad", "https://user:password@github.com/org/repo.git", "/tmp/repo.git", "file:///tmp/repo.git", "git@github.com:-bad", "git@github.com:org/\nrepo.git", "ssh://git:password@github.com/org/repo.git"} {
		mcpError(t, s, admin, "manage_knowledgebase_access", map[string]any{"action": "configure_backup", "username": "git", "remote_url": remote, "request_id": "invalid"}, "INVALID_ARGUMENT")
	}
	mcpError(t, s, admin, "manage_knowledgebase_access", map[string]any{"action": "configure_backup", "username": "git", "remote_url": "git@github.com:org/repo.git", "branch": "bad..branch", "request_id": "invalid-branch"}, "INVALID_ARGUMENT")
	if configured, err := s.BackupConfigured(); err != nil || configured {
		t.Fatal("invalid input configured backup", err)
	}
	configured, admin2, _ := fixture(t, true)
	admin2.AccessOnly = true
	mcpError(t, configured, admin2, "manage_knowledgebase_access", map[string]any{"action": "configure_backup", "username": "git", "remote_url": "https://github.com/org/repo.git", "request_id": "replace-env"}, "BACKUP_REMOTE_CHANGED")
	for _, remote := range []string{"git@github.com:org/repo.git", "ssh://git@git.example:2222/org/repo.git"} {
		if !validBackupSSHRemote(remote) {
			t.Fatal("valid SSH URL denied", remote)
		}
	}
}
