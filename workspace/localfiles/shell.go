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
	// Commands run as the person, in their own login shell and environment, on every system (owner decision, 2026-10-22, replacing
	// the 2026-10-09 sandbox on Mac and Linux): the folder rules bind the file tools, and a command starts in the granted folder.
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
		commandGrants := []Grant{g}
		if r.CrewID != "" {
			commandGrants = nil
			for _, grant := range e.grants {
				commandGrants = append(commandGrants, grant)
			}
		} else if downloads != nil {
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
		}
		cmd = userTerminalCommand(operationCtx, workDir, r.Command)
		cleanup = func() {}
	}
	// The person's own environment (their tools, profiles and cloud settings), without the CLI's own variables: its login and
	// server settings are not the person's terminal.
	cmd.Env = userEnvironment(cmd.Env)
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

// userEnvironment is the environment a command gets: everything the CLI was started with, except the CLI's own AGENTWORKS_*
// variables (its login and server settings).
func userEnvironment(env []string) []string {
	result := make([]string, 0, len(env))
	for _, value := range env {
		if key, _, _ := strings.Cut(value, "="); strings.HasPrefix(strings.ToUpper(key), "AGENTWORKS_") {
			continue
		}
		result = append(result, value)
	}
	return result
}

// userTerminalCommand runs command the way the person's own terminal would: through their login shell (so their profile, PATH and
// tools such as cloud CLIs are there), starting in workDir, with no sandbox. zsh also reads its interactive startup file, where
// many people set their PATH.
func userTerminalCommand(ctx context.Context, workDir, command string) *exec.Cmd {
	shell := strings.TrimSpace(os.Getenv("SHELL"))
	if _, err := os.Stat(shell); shell == "" || err != nil {
		shell = "/bin/sh"
		for _, candidate := range []string{"/bin/zsh", "/bin/bash"} {
			if _, err := os.Stat(candidate); err == nil {
				shell = candidate
				break
			}
		}
	}
	args := []string{"-c", command}
	switch filepath.Base(shell) {
	case "zsh", "fish":
		args = []string{"-l", "-i", "-c", command}
	case "bash":
		args = []string{"-l", "-c", command}
	}
	cmd := exec.CommandContext(ctx, shell, args...)
	cmd.Dir = workDir
	cmd.Env = os.Environ()
	if os.Getenv("TERM") == "" {
		cmd.Env = append(cmd.Env, "TERM=dumb")
	}
	return cmd
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
