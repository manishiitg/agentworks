package knowledgebase

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

type backupDestination struct {
	Remote string `json:"remote_url"`
	Branch string `json:"branch"`
}

// Deployment configuration takes precedence. An app-configured destination is
// private control state, separate from both live Markdown and the Git backup.
func (s *Service) backupDestination() (backupDestination, error) {
	if s.cfg.BackupRemote != "" {
		return backupDestination{s.cfg.BackupRemote, s.cfg.BackupBranch}, nil
	}
	data, err := os.ReadFile(filepath.Join(s.private, "backup-destination.json"))
	if os.IsNotExist(err) {
		return backupDestination{}, nil
	}
	if err != nil {
		return backupDestination{}, err
	}
	var destination backupDestination
	if json.Unmarshal(data, &destination) != nil || !validBackupSSHRemote(destination.Remote) || destination.Branch == "" {
		return backupDestination{}, kbErr("STORAGE_UNAVAILABLE", "The saved backup destination is invalid.")
	}
	return destination, nil
}

func (s *Service) BackupConfigured() (bool, error) {
	destination, err := s.backupDestination()
	return destination.Remote != "", err
}

var backupSCPRemote = regexp.MustCompile(`^[A-Za-z0-9_.-]+@[A-Za-z0-9][A-Za-z0-9.-]*:[A-Za-z0-9_./-]+$`)

func validBackupSSHRemote(remote string) bool {
	if strings.ContainsAny(remote, " \t\r\n\x00") {
		return false
	}
	if backupSCPRemote.MatchString(remote) {
		path := strings.SplitN(remote, ":", 2)[1]
		return !strings.HasPrefix(path, "-") && !strings.Contains(path, "..")
	}
	u, err := url.Parse(remote)
	if err != nil || u.Scheme != "ssh" || u.Hostname() == "" || strings.HasPrefix(u.Hostname(), "-") || u.Path == "" || u.Path == "/" || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return false
	}
	if u.User != nil {
		if _, password := u.User.Password(); password {
			return false
		}
	}
	return true
}

// Called through the normal request journal while holding both publication and
// content locks. Initial setup cannot redirect an existing backup destination.
func (s *Service) configureBackup(ctx context.Context, p Principal, args map[string]any) (any, []fileChange, error) {
	if !p.AccessOnly || !p.IsAdmin || p.Caps != nil || strings.HasPrefix(p.IdentityID, "service_") {
		return nil, nil, kbErr("FORBIDDEN", "An unrestricted administrator is required to configure backup.")
	}
	remote := strings.TrimSpace(stringArg(args, "remote_url"))
	branch := strings.TrimSpace(stringArg(args, "branch"))
	if branch == "" {
		branch = "main"
	}
	if !validBackupSSHRemote(remote) {
		return nil, nil, badArg("Use an SSH repository URL without credentials; configure the server's SSH key separately.")
	}
	command := exec.CommandContext(ctx, "git", "check-ref-format", "refs/heads/"+branch)
	command.Env = gitEnvironment()
	if command.Run() != nil {
		return nil, nil, badArg("Invalid backup branch.")
	}
	destination := backupDestination{remote, branch}
	current, err := s.backupDestination()
	if err != nil {
		return nil, nil, err
	}
	if current.Remote != "" {
		if current != destination {
			return nil, nil, kbErr("BACKUP_REMOTE_CHANGED", "Backup is already configured; changing the destination requires operator reconciliation.")
		}
		return map[string]any{"configured": true, "remote_url": remote, "branch": branch}, nil, nil
	}
	// Removing an environment remote must not allow replacing an old staging repo.
	if _, err := os.Stat(filepath.Join(s.private, "backup-configuration.json")); err == nil || !os.IsNotExist(err) {
		return nil, nil, kbErr("BACKUP_REMOTE_CHANGED", "An existing backup requires operator reconciliation.")
	}
	return map[string]any{"configured": true, "remote_url": remote, "branch": branch}, []fileChange{jsonChange(filepath.Join(s.private, "backup-destination.json"), destination)}, nil
}
