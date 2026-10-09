package localfiles

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/manishiitg/coding-agent-loop/workspace/security"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

type shellRecord struct {
	Fingerprint string       `json:"fingerprint"`
	Result      *ShellResult `json:"result,omitempty"`
}

// Local folders support shell commands: arbitrary programs can
// edit project files (including git metadata and databases). The OS sandbox
// carries explicit folder exclusions; it is never replaced by command parsing.
func (e *Executor) shell(ctx context.Context, g Grant, r Request) (*ShellResult, error) {
	if !g.Shell {
		return nil, &wf.FileError{Status: 403, Message: "update and reconnect the CLI to enable shell commands"}
	}
	if err := r.ValidateShell(); err != nil {
		return nil, &wf.FileError{Status: 400, Message: err.Error()}
	}
	p, err := wf.CleanRelative(r.Path)
	if err != nil {
		return nil, &wf.FileError{Status: 400, Message: "invalid shell working directory"}
	}
	if wf.Private(p) || !g.Guard.Allows(p, g.Writable) {
		return nil, &wf.FileError{Status: 403, Message: "shell working directory is outside folder grants"}
	}
	dir, err := wf.OpenDirectory(e.roots[g.ID], p, false)
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	// Resolve the root's current spelling, and check that the pathname used by
	// subprocess creation still names the directory held open by os.Root.
	root, err := filepath.EvalSymlinks(g.Root)
	if err != nil {
		return nil, err
	}
	workDir := filepath.Join(root, filepath.FromSlash(p))
	held, err := dir.Stat(".")
	if err != nil {
		return nil, err
	}
	actual, err := os.Stat(workDir)
	if err != nil || !os.SameFile(held, actual) {
		return nil, &wf.FileError{Status: 409, Message: "local folder moved; reconnect before executing commands"}
	}
	// Windows has no command sandbox (owner decision, 2026-10-09): commands run as the user through Git Bash, and the folder rules
	// bind the file tools only.
	capability := security.CurrentSandboxCapability()
	if runtime.GOOS != "windows" && (!capability.Available || runtime.GOOS == "linux" && capability.Backend != "landlock") {
		return nil, &wf.FileError{Status: 503, Message: "SANDBOX_UNAVAILABLE: local commands require sandbox-exec on macOS or the Landlock launcher on Linux"}
	}
	operationCtx, cancel := context.WithTimeout(ctx, time.Duration(r.ShellTimeout())*time.Second)
	defer cancel()
	unlock, err := wf.LockWorkspace(operationCtx, root)
	if err != nil {
		return nil, err
	}
	defer unlock()
	journal, err := os.OpenRoot(g.State)
	if err != nil {
		return nil, err
	}
	defer journal.Close()
	if err = journal.Mkdir("shell-runs", 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, err
	}
	journalDir, err := wf.OpenDirectory(journal, "shell-runs", false)
	if err != nil {
		return nil, err
	}
	defer journalDir.Close()
	var downloads *Grant
	if g.Downloads {
		companion, ok := e.grants["downloads"]
		if !ok {
			return nil, &wf.FileError{Status: 403, Message: "Downloads is not shared by this CLI"}
		}
		downloads = &companion
	}
	payload, _ := json.Marshal([]any{root, g.Resource, g.PrivatePaths, downloads, p, r.Command, r.ShellTimeout(), r.Identity.UserID})
	fingerprint := sha256.Sum256(payload)
	id := sha256.Sum256([]byte(r.RequestID))
	name := hex.EncodeToString(id[:]) + ".json"
	record := shellRecord{Fingerprint: hex.EncodeToString(fingerprint[:])}
	existing, err := journalDir.ReadFile(name)
	if err == nil {
		var saved shellRecord
		if json.Unmarshal(existing, &saved) != nil {
			return nil, &wf.FileError{Status: 409, Message: "shell receipt is unreadable; do not retry automatically"}
		}
		if saved.Fingerprint != record.Fingerprint {
			return nil, &wf.FileError{Status: 409, Message: "shell request_id was used with different arguments"}
		}
		if saved.Result == nil {
			return nil, &wf.FileError{Status: 409, Code: "shell_outcome_unknown", Message: "shell outcome is unknown; inspect local state before issuing another command"}
		}
		return saved.Result, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	var cmd *exec.Cmd
	var cleanup func()
	if runtime.GOOS == "windows" {
		var windowsErr error
		cmd, cleanup, windowsErr = windowsShellCommand(operationCtx, workDir, r.Command)
		if windowsErr != nil {
			return nil, &wf.FileError{Status: 503, Message: windowsErr.Error()}
		}
		defer cleanup()
	} else {
		iso := &security.Isolator{BaseDir: root, WorkDir: workDir, StrictAllowlist: true, AllowNetwork: true, PrivateScratch: true}
		commandGrants := []Grant{g}
		if downloads != nil {
			commandGrants = append(commandGrants, *downloads)
		}
		for _, grant := range commandGrants {
			grantRoot, err := filepath.EvalSymlinks(grant.Root)
			if err != nil {
				return nil, err
			}
			held, err := e.roots[grant.ID].Stat(".")
			if err != nil {
				return nil, err
			}
			actual, err := os.Stat(grantRoot)
			if err != nil || !os.SameFile(held, actual) {
				return nil, &wf.FileError{Status: 409, Message: "local folder moved; reconnect before executing commands"}
			}
			for _, path := range append(append([]string{}, grant.Guard.ReadPaths...), grant.Guard.ReadOnlyPaths...) {
				iso.ReadPaths = append(iso.ReadPaths, filepath.Join(grantRoot, filepath.FromSlash(path)))
			}
			if !grant.Writable {
				// A writable companion must not override a read-only project nested within it.
				iso.BlockedWritePaths = append(iso.BlockedWritePaths, grantRoot)
			}
			for _, path := range grant.Guard.WritePaths {
				iso.WritePaths = append(iso.WritePaths, filepath.Join(grantRoot, filepath.FromSlash(path)))
			}
			for _, path := range grant.Guard.BlockedPaths {
				iso.BlockedPaths = append(iso.BlockedPaths, filepath.Join(grantRoot, filepath.FromSlash(path)))
			}
			for _, path := range append(append([]string{}, grant.Guard.ReadOnlyPaths...), grant.Guard.BlockedWritePaths...) {
				iso.BlockedWritePaths = append(iso.BlockedWritePaths, filepath.Join(grantRoot, filepath.FromSlash(path)))
			}
			if runtime.GOOS == "linux" {
				exclusions := append(append([]string{}, grant.Guard.BlockedPaths...), grant.Guard.ReadOnlyPaths...)
				exclusions = append(exclusions, grant.Guard.BlockedWritePaths...)
				for _, path := range exclusions {
					if _, err := os.Lstat(filepath.Join(grantRoot, filepath.FromSlash(path))); err != nil {
						return nil, &wf.FileError{Status: 503, Message: "SANDBOX_UNAVAILABLE: Linux shell exclusions must exist before commands run"}
					}
				}
			}
		}
		for _, grant := range e.grants {
			if grant.State != "" {
				iso.BlockedPaths = append(iso.BlockedPaths, grant.State)
			}
			iso.BlockedPaths = append(iso.BlockedPaths, grant.PrivatePaths...)
			if lockState, err := wf.DefaultStateDir(grant.Root); err == nil {
				iso.BlockedPaths = append(iso.BlockedPaths, lockState)
			}
		}
		var isoErr error
		if runtime.GOOS == "darwin" {
			// On a person's own Mac commands run under the light sandbox: their normal environment and home folder, writes limited
			// to the shared folders, temp and home, secrets unreadable, the folder rules enforced (owner decision, 2026-10-09).
			cmd, cleanup, isoErr = security.ExecuteLightLocal(operationCtx, workDir, security.LightLocalPolicy{WritePaths: iso.WritePaths, BlockedPaths: iso.BlockedPaths, BlockedWritePaths: iso.BlockedWritePaths}, r.Command)
		} else {
			cmd, cleanup, isoErr = iso.ExecuteIsolated(operationCtx, r.Command, nil)
		}
		if isoErr != nil {
			return nil, &wf.FileError{Status: 503, Message: isoErr.Error()}
		}
		defer cleanup()
	}
	// Keep only local tool lookup, locale and sandbox cache paths. Login tokens,
	// server/provider secrets and arbitrary CLI environment never reach commands.
	if runtime.GOOS != "darwin" {
		cmd.Env = shellEnvironment(cmd.Env)
	}
	if downloads != nil {
		cmd.Env = append(cmd.Env, "AGENTWORKS_DOWNLOADS="+downloads.Root)
	}
	var stdout, stderr boundedOutput
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	kill := configureShellProcess(cmd)
	var killOnce sync.Once
	stop := func() { killOnce.Do(kill) }
	defer stop()
	prepared, _ := json.Marshal(record)
	file, err := journalDir.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, err
	}
	_, err = file.Write(prepared)
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil {
		err = syncShellJournal(journalDir)
	}
	if err != nil {
		return nil, err
	}
	err = runShellCommand(cmd)
	result := &ShellResult{RequestID: r.RequestID, Identity: r.Identity, Stdout: stdout.String(), Stderr: stderr.String(), Truncated: stdout.truncated || stderr.truncated, TimedOut: errors.Is(operationCtx.Err(), context.DeadlineExceeded)}
	if err != nil {
		result.ExitCode = -1
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			result.ExitCode = exit.ExitCode()
		} else {
			result.Stderr += "\n" + err.Error()
		}
	}
	// Kill descendants even after a successful parent exit; a background task
	// must not outlive this bounded command or the authenticated connection.
	stop()
	record.Result = result
	completed, _ := json.Marshal(record)
	tmp := name + ".tmp"
	file, err = journalDir.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return nil, err
	}
	_, err = file.Write(completed)
	if err == nil {
		err = file.Sync()
	}
	closeErr = file.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil {
		err = journalDir.Rename(tmp, name)
	}
	if err == nil {
		err = syncShellJournal(journalDir)
	}
	if err != nil {
		return nil, &wf.FileError{Status: 409, Code: "shell_outcome_unknown", Message: "command ran but its result could not be saved; inspect local state before retrying"}
	}
	return result, nil
}

// What Windows programs need to start (system folders, the user's profile, program locations); no tokens or secrets.
var windowsShellEnv = map[string]bool{"SYSTEMROOT": true, "WINDIR": true, "SYSTEMDRIVE": true, "COMSPEC": true, "PATHEXT": true, "USERPROFILE": true, "HOMEDRIVE": true,
	"HOMEPATH": true, "APPDATA": true, "LOCALAPPDATA": true, "PROGRAMDATA": true, "PROGRAMFILES": true, "PROGRAMFILES(X86)": true, "PROGRAMW6432": true,
	"COMMONPROGRAMFILES": true, "COMMONPROGRAMFILES(X86)": true, "COMMONPROGRAMW6432": true, "ALLUSERSPROFILE": true, "USERNAME": true, "USERDOMAIN": true,
	"COMPUTERNAME": true, "OS": true, "PROCESSOR_ARCHITECTURE": true, "NUMBER_OF_PROCESSORS": true}

func shellEnvironment(env []string) []string {
	allowed := map[string]bool{"PATH": true, "HOME": true, "TMPDIR": true, "TMP": true, "TEMP": true, "LANG": true, "LC_ALL": true, "TZ": true, "XDG_CACHE_HOME": true, "XDG_CONFIG_HOME": true, "XDG_DATA_HOME": true, "GOCACHE": true, "GOMODCACHE": true, "GOPATH": true, "NPM_CONFIG_CACHE": true, "npm_config_cache": true, "PIP_CACHE_DIR": true, "PYTHONUNBUFFERED": true, "CARGO_HOME": true, "RUSTUP_HOME": true}
	result := []string{}
	for _, value := range env {
		key, _, _ := strings.Cut(value, "=")
		if allowed[key] || runtime.GOOS == "windows" && windowsShellEnv[strings.ToUpper(key)] {
			result = append(result, value)
		}
	}
	// PATH is local machine configuration, not a value supplied by the server.
	path := os.Getenv("PATH")
	if path != "" {
		result = append(result, "PATH="+path)
	}
	return result
}

type boundedOutput struct {
	buffer    bytes.Buffer
	truncated bool
}

func (b *boundedOutput) Write(p []byte) (int, error) {
	n := len(p)
	remaining := MaxShellOutputBytes - b.buffer.Len()
	if remaining > 0 {
		_, _ = b.buffer.Write(p[:min(n, remaining)])
	}
	if n > remaining {
		b.truncated = true
	}
	return n, nil // Continue draining the process, even after the capture limit.
}

var _ io.Writer = (*boundedOutput)(nil)

func syncShellJournal(root *os.Root) error {
	// Windows cannot flush a directory handle (Access is denied); each journal file is already synced on its own.
	if runtime.GOOS == "windows" {
		return nil
	}
	dir, err := root.Open(".")
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func (b *boundedOutput) String() string { return b.buffer.String() }
