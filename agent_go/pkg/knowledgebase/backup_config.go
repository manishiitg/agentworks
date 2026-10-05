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
	Remote       string `json:"remote_url"`
	Branch       string `json:"branch"`
	Username     string `json:"username,omitempty"`
	EncryptedPAT string `json:"encrypted_pat,omitempty"`
}

// Deployment configuration takes precedence. An app-configured destination is
// private control state, separate from both live Markdown and the Git backup.
func (s *Service) configuredBackupDestination() (backupDestination, error) {
	if s.cfg.BackupRemote != "" {
		return backupDestination{Remote: s.cfg.BackupRemote, Branch: s.cfg.BackupBranch}, nil
	}
	data, err := os.ReadFile(filepath.Join(s.private, "backup-destination.json"))
	if os.IsNotExist(err) {
		return backupDestination{}, nil
	}
	if err != nil {
		return backupDestination{}, err
	}
	var destination backupDestination
	if json.Unmarshal(data, &destination) != nil || !validBackupRemote(destination.Remote) || destination.Branch == "" {
		return backupDestination{}, kbErr("STORAGE_UNAVAILABLE", "The saved backup destination is invalid.")
	}
	return destination, nil
}

// The remote remains pinned; Files chooses the active local branch.
func (s *Service) backupDestination() (backupDestination, error) {
	destination, err := s.configuredBackupDestination()
	if err != nil {
		return destination, err
	}
	state, err := s.readGitWorkspace()
	if err != nil {
		return destination, err
	}
	if state.Branch != "" {
		destination.Branch = state.Branch
	}
	return destination, nil
}

func (s *Service) BackupConfigured() (bool, error) {
	destination, err := s.backupDestination()
	return destination.Remote != "", err
}

var backupSCPRemote = regexp.MustCompile(`^[A-Za-z0-9_.-]+@[A-Za-z0-9][A-Za-z0-9.-]*:[A-Za-z0-9_./-]+$`)
var backupUsername = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,128}$`)

func validBackupRemote(remote string) bool {
	if validBackupSSHRemote(remote) {
		return true
	}
	u, err := url.Parse(remote)
	return err == nil && u.Scheme == "https" && u.Hostname() != "" && u.User == nil && u.Path != "" && u.Path != "/" && u.RawQuery == "" && u.Fragment == "" && u.Opaque == "" && !strings.ContainsAny(remote, " \t\r\n\x00\\")
}

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
	// Accept the normal GitHub repository page URL as well as its clone URL.
	if u, err := url.Parse(remote); err == nil && u.Scheme == "https" && u.Host == "github.com" && u.User == nil && u.RawQuery == "" && u.Fragment == "" {
		u.Path = strings.TrimSuffix(u.Path, "/")
		if len(strings.Split(strings.TrimPrefix(u.Path, "/"), "/")) == 2 && !strings.HasSuffix(u.Path, ".git") {
			u.Path += ".git"
			u.RawPath = ""
			remote = u.String()
		}
	}
	username := strings.TrimSpace(stringArg(args, "username"))
	branch := strings.TrimSpace(stringArg(args, "branch"))
	if branch == "" {
		branch = "main"
	}
	if !validBackupRemote(remote) || validBackupSSHRemote(remote) {
		return nil, nil, badArg("Use an HTTPS repository URL without embedded credentials. SSH backups require deployment configuration.")
	}
	if err := validateBackupLiteralHost(remote, s.cfg.AllowPrivateBackup); err != nil {
		return nil, nil, err
	}
	if !backupUsername.MatchString(username) {
		return nil, nil, badArg("A repository username is required.")
	}
	pat, hasPAT := args["pat"].(string)
	if len(pat) > 4096 || strings.ContainsAny(pat, "\r\n\x00") {
		return nil, nil, badArg("Invalid repository PAT.")
	}
	if pat != "" && !strings.HasPrefix(remote, "https://") {
		return nil, nil, badArg("Use an HTTPS repository URL when supplying a PAT.")
	}
	command := exec.CommandContext(ctx, "git", "check-ref-format", "refs/heads/"+branch)
	command.Env = gitEnvironment()
	if command.Run() != nil {
		return nil, nil, badArg("Invalid backup branch.")
	}
	destination := backupDestination{Remote: remote, Branch: branch, Username: username}
	current, err := s.configuredBackupDestination()
	if err != nil {
		return nil, nil, err
	}
	if current.Remote != "" {
		if current.Remote != remote || current.Branch != branch {
			return nil, nil, kbErr("BACKUP_REMOTE_CHANGED", "Backup is already configured; changing the destination requires operator reconciliation.")
		}
		if s.cfg.BackupRemote != "" && (hasPAT || current.Username != username) {
			return nil, nil, kbErr("BACKUP_REMOTE_CHANGED", "Deployment-managed backup credentials must be configured by the operator.")
		}
		if !hasPAT && current.Username != "" && current.Username != username && current.EncryptedPAT != "" {
			return nil, nil, badArg("Supply a PAT when changing its username, or an empty PAT to remove it.")
		}
		destination.EncryptedPAT = current.EncryptedPAT
	}
	// Removing an environment remote must not allow replacing an old staging repo.
	if current.Remote == "" {
		if _, err := os.Stat(filepath.Join(s.private, "backup-configuration.json")); err == nil || !os.IsNotExist(err) {
			return nil, nil, kbErr("BACKUP_REMOTE_CHANGED", "An existing backup requires operator reconciliation.")
		}
	}
	if hasPAT {
		destination.EncryptedPAT = ""
		if pat != "" {
			destination.EncryptedPAT, err = s.encryptBackupPAT(destination, pat)
			if err != nil {
				return nil, nil, err
			}
		}
	}
	return map[string]any{"configured": true, "remote_url": remote, "branch": branch, "username": username, "pat_configured": destination.EncryptedPAT != ""}, []fileChange{jsonChange(filepath.Join(s.private, "backup-destination.json"), destination)}, nil
}
