package knowledgebase

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// GitFolder is Brain's folder as a Git working folder: the Brain chat (and its terminal) run git there normally
// (PLAT-633). Credentials never touch the folder: its credential helper reads the token from BRAIN_GIT_TOKEN, which
// the server puts in that shell's environment from the platform secret named by PATSecret.
type GitFolder struct {
	Path      string
	Remote    string
	Branch    string
	Username  string
	PATSecret string
}

// GitTokenEnv is the environment variable the folder's credential helper reads.
const GitTokenEnv = "BRAIN_GIT_TOKEN"

func (s *Service) GitFolder() (GitFolder, error) {
	d, err := s.configuredBackupDestination()
	if err != nil {
		return GitFolder{Path: s.live}, err
	}
	branch := d.Branch
	if branch == "" {
		branch = s.cfg.BackupBranch
	}
	return GitFolder{Path: s.live, Remote: d.Remote, Branch: branch, Username: d.Username, PATSecret: d.PATSecret}, nil
}

// GitToken resolves the backup token through the server's secret resolver; empty when none is configured.
func (s *Service) GitToken(f GitFolder) string {
	if f.PATSecret == "" || s.cfg.SecretResolver == nil {
		return ""
	}
	token, _ := s.cfg.SecretResolver(f.PATSecret)
	return token
}

// EnsureGitRepository makes Brain's folder a Git repository whose origin is the configured remote. It is idempotent
// and never commits, fetches or pushes: those are the person's (or their chat's) to run.
func (s *Service) EnsureGitRepository(ctx context.Context) (GitFolder, error) {
	f, err := s.GitFolder()
	if err != nil {
		return f, err
	}
	run := func(args ...string) error {
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = s.live
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0")
		if out, err := cmd.CombinedOutput(); err != nil {
			return kbErr("GIT_FAILED", "git "+args[0]+" in "+s.live+": "+err.Error()+": "+strings.TrimSpace(string(out)))
		}
		return nil
	}
	if _, err := os.Stat(filepath.Join(s.live, ".git")); os.IsNotExist(err) {
		if err := run("init", "-q", "-b", f.Branch); err != nil {
			return f, err
		}
	}
	// Brain's own bookkeeping files are not content.
	exclude := filepath.Join(s.live, ".git", "info", "exclude")
	if b, _ := os.ReadFile(exclude); !strings.Contains(string(b), ".kb-registry.json") {
		if err := os.MkdirAll(filepath.Dir(exclude), 0o700); err != nil {
			return f, err
		}
		if err := os.WriteFile(exclude, append(b, []byte("\n.kb-registry.json\n")...), 0o600); err != nil {
			return f, err
		}
	}
	helper := `!f() { test "$1" = get || exit 0; printf 'username=%s\npassword=%s\n' "${BRAIN_GIT_USERNAME:-x-access-token}" "$` + GitTokenEnv + `"; }; f`
	if err := run("config", "credential.helper", helper); err != nil {
		return f, err
	}
	if f.Remote != "" {
		if run("remote", "get-url", "origin") != nil {
			err = run("remote", "add", "origin", f.Remote)
		} else {
			err = run("remote", "set-url", "origin", f.Remote)
		}
		if err != nil {
			return f, err
		}
	}
	return f, nil
}

// CanEditWholeBrain reports whether p is Owner of Brain's root: only they get Brain's raw folder, since a shell there
// bypasses every folder role.
func (s *Service) CanEditWholeBrain(p Principal) bool { return s.effectiveRaw(p, "") >= roleOwner }
