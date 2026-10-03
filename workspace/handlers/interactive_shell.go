package handlers

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/manishiitg/coding-agent-loop/workspace/models"
	"github.com/manishiitg/coding-agent-loop/workspace/security"
	"github.com/manishiitg/coding-agent-loop/workspace/slots"
	"github.com/manishiitg/coding-agent-loop/workspace/utils"
	"github.com/spf13/viper"
)

// Interactive shells (Code's terminal). The workspace service starts a small tmux server inside the same sandbox
// /api/execute uses: Folder Guard via Landlock, the private /tmp, a private /dev/pts, the sanitized environment and the
// allow-listed extra env. Where slots are on, all of it runs as the caller's own Linux account (slots.WrapCommandFile:
// the request travels in a file so the terminal stays the command's stdin), and a person without a slot gets no
// shell. The service itself creates the namespaces, since it is the only binary the host's AppArmor userns exception
// covers; a tmux pane could not. The shell and everything it starts inherit the sandbox.
//
// The tmux socket lives in the shell's own folder: in the slot's run folder (group-shared with the service) where slots
// are on, else a shared shells folder. Nothing outside the sandbox ever attaches to it: an attached tmux client runs
// whatever the server tells it to (`detach-client -E`), so the attach client runs in the shell's own sandbox too
// (interactive_shell_attach.go).
//
// Only the agent server calls these routes (workspace token); it authorizes the user for the project first, passes
// the project's Folder Guard and stamps the user (X-User-ID).

const interactiveShellRoot = "/tmp/.agentworks-shells"
const interactiveShellSession = "shell"

var interactiveShellID = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,47}$`)

type InteractiveShellStartRequest struct {
	ShellID          string                    `json:"shell_id"`
	WorkingDirectory string                    `json:"working_directory"`
	FolderGuard      *models.FolderGuardConfig `json:"folder_guard"`
	ExtraEnv         map[string]string         `json:"extra_env,omitempty"`
	Cols             int                       `json:"cols,omitempty"`
	Rows             int                       `json:"rows,omitempty"`
}

type InteractiveShellHandle struct {
	ShellID string `json:"shell_id"`
	Socket  string `json:"socket"`
	Session string `json:"session"`
	Running bool   `json:"running"`
}

type interactiveShellRef struct {
	ShellID string `json:"shell_id"`
	Cols    int    `json:"cols,omitempty"`
	Rows    int    `json:"rows,omitempty"`
}

// interactiveShellPaths returns the shell's folder and tmux socket. Where slots are on, the shell runs as the
// caller's slot account and its tmux files live in that slot's own run folder (group-shared with the service, so the
// service can reach the socket and clean up); elsewhere they are in the shared shells folder.
func interactiveShellPaths(id, slot string) (dir, socket string, err error) {
	if slot != "" {
		run, runErr := slots.RunDirFor(slot)
		if runErr != nil {
			return "", "", runErr
		}
		dir = filepath.Join(run, "shells", id)
	} else {
		dir = filepath.Join(interactiveShellRoot, id)
	}
	return dir, filepath.Join(dir, "tmux.sock"), nil
}

// interactiveShellSlot decides which account the caller's shell runs as. Where slots are on, a person without a
// slot gets no shell: it would otherwise run as the service account.
func interactiveShellSlot(c *gin.Context) (slot string, status int, message string) {
	slot, on, err := slots.For(getUserID(c))
	if on && err == nil && slot != "" {
		return slot, 0, ""
	}
	// Slots are available on this host (even only for some people): a person without one gets no shell, never one
	// as the service account.
	if slots.Enabled() || err != nil {
		return "", http.StatusForbidden, "the terminal needs a personal account on this server"
	}
	return "", 0, ""
}

// realTmux is the tmux binary itself: the service's PATH starts with the slot front-end shim, which must not see
// these -S calls.
func realTmux() string {
	if _, err := os.Stat("/usr/bin/tmux"); err == nil {
		return "/usr/bin/tmux"
	}
	return "tmux"
}

func interactiveShellRunning(socket string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, realTmux(), "-S", socket, "has-session", "-t", interactiveShellSession).Run() == nil
}

func clampShellSize(cols, rows int) (int, int) {
	if cols < 20 || cols > 500 {
		cols = 160
	}
	if rows < 5 || rows > 200 {
		rows = 48
	}
	return cols, rows
}

func shellError(c *gin.Context, status int, message string) {
	c.JSON(status, models.APIResponse[any]{Success: false, Message: message, Error: message})
}

// prepareInteractiveShellDir creates the shell's folder. For a slot it is group-writable and setgid (the run
// folder's group is the slot's), so the slot's tmux server can create its socket and the service can still remove it.
func prepareInteractiveShellDir(dir, slot string) error {
	if err := os.MkdirAll(filepath.Join(dir, "tmp"), 0o700); err != nil {
		return err
	}
	if slot == "" {
		return os.Chmod(interactiveShellRoot, 0o700)
	}
	for _, d := range []string{filepath.Dir(dir), dir, filepath.Join(dir, "tmp")} {
		if err := os.Chmod(d, 0o770|os.ModeSetgid); err != nil {
			return err
		}
	}
	return nil
}

// StartInteractiveShell starts (or returns the running) sandboxed shell.
func StartInteractiveShell(c *gin.Context) {
	var req InteractiveShellStartRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		shellError(c, http.StatusBadRequest, "Invalid request body")
		return
	}
	if !interactiveShellID.MatchString(req.ShellID) {
		shellError(c, http.StatusBadRequest, "shell_id must be 1-48 lowercase letters, digits or dashes")
		return
	}
	// A shell never runs without a Folder Guard: it would otherwise be an
	// unsandboxed login on the server.
	if req.FolderGuard == nil || !req.FolderGuard.Enabled || len(req.FolderGuard.WritePaths) == 0 {
		shellError(c, http.StatusBadRequest, "folder_guard with write paths is required")
		return
	}
	slot, status, message := interactiveShellSlot(c)
	if status != 0 {
		shellError(c, status, message)
		return
	}
	docsDir := viper.GetString("docs-dir")
	workingDir := docsDir
	if req.WorkingDirectory != "" {
		full := filepath.Join(docsDir, utils.SanitizeInputPath(req.WorkingDirectory, docsDir))
		if !utils.IsValidFilePath(full, docsDir) {
			shellError(c, http.StatusBadRequest, "Working directory must be within the workspace")
			return
		}
		if info, err := os.Stat(full); err != nil || !info.IsDir() {
			shellError(c, http.StatusBadRequest, "Working directory does not exist")
			return
		}
		workingDir = full
	}
	dir, socket, err := interactiveShellPaths(req.ShellID, slot)
	if err != nil {
		shellError(c, http.StatusInternalServerError, "Cannot find the shell folder: "+err.Error())
		return
	}
	extraEnv := map[string]string{}
	for k, v := range req.ExtraEnv {
		if isAllowedShellExtraEnvKey(k) {
			extraEnv[k] = v
		}
	}
	isolator := &security.Isolator{
		ReadPaths:         req.FolderGuard.ReadPaths,
		WritePaths:        append(append([]string{}, req.FolderGuard.WritePaths...), dir),
		BlockedPaths:      req.FolderGuard.BlockedPaths,
		BlockedWritePaths: req.FolderGuard.BlockedWritePaths,
		WorkDir:           workingDir,
		BaseDir:           docsDir,
		StrictAllowlist:   req.FolderGuard.StrictAllowlist,
		AllowNetwork:      !req.FolderGuard.DenyNetwork,
		BrowserSession:    req.FolderGuard.BrowserSession,
		AllowPTY:          true,
		Slot:              slot,
		Interactive:       true,
		ExtraEnv:          extraEnv,
	}
	// Attaches run in this same sandbox (interactive_shell_attach.go). The
	// agent server calls start before every attach, so a restarted service
	// learns the sandbox of a shell that outlived it.
	rememberInteractiveShell(req.ShellID, *isolator)
	handle := InteractiveShellHandle{ShellID: req.ShellID, Socket: socket, Session: interactiveShellSession, Running: true}
	if interactiveShellRunning(socket) {
		c.JSON(http.StatusOK, models.APIResponse[InteractiveShellHandle]{Success: true, Data: handle})
		return
	}
	_ = os.RemoveAll(dir)
	if err := prepareInteractiveShellDir(dir, slot); err != nil {
		shellError(c, http.StatusInternalServerError, "Cannot prepare the shell folder")
		return
	}

	for _, wp := range req.FolderGuard.WritePaths {
		physical := wp
		if !filepath.IsAbs(physical) {
			physical = filepath.Join(docsDir, physical)
		}
		_ = os.MkdirAll(physical, 0o755)
	}
	cols, rows := clampShellSize(req.Cols, req.Rows)
	shell := "/bin/bash"
	if _, err := os.Stat(shell); err != nil {
		shell = "/bin/sh"
	}
	// tmux daemonizes: this command returns once the server is up, and the
	// server keeps the sandbox (and its private /tmp) alive for the shell.
	// TMPDIR points at the shell's own folder: the per-command scratch is
	// removed as soon as this start command returns.
	environment := fmt.Sprintf("TMPDIR=%s TERM=xterm-256color", shellQuote(filepath.Join(dir, "tmp")))
	tmuxStart := fmt.Sprintf("tmux -f /dev/null -S %s new-session -d -s %s -x %d -y %d %s -l",
		shellQuote(socket), interactiveShellSession, cols, rows, shell)
	command := environment + " exec " + tmuxStart
	if slot != "" {
		// tmux makes its socket owner-only. The service reaches it (has-session, resize, stop) through the slot's group,
		// like the slot's own tmux socket, so the socket is opened to the group once the server is up.
		command = environment + " " + tmuxStart + fmt.Sprintf(" && chmod 0660 %s", shellQuote(socket))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd, cleanup, err := isolator.ExecuteIsolated(ctx, command, nil)
	if err != nil {
		shellError(c, http.StatusInternalServerError, "Failed to set up the sandbox: "+err.Error())
		return
	}
	if cleanup != nil {
		defer cleanup()
	}
	if slot == "" {
		// Not run as a slot: the environment is the command's own, so the extra values go on it directly.
		for k, v := range extraEnv {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		shellError(c, http.StatusInternalServerError, "The shell did not start: "+strings.TrimSpace(string(out)))
		return
	}
	if !interactiveShellRunning(socket) {
		shellError(c, http.StatusInternalServerError, "The shell exited right after starting")
		return
	}
	c.JSON(http.StatusOK, models.APIResponse[InteractiveShellHandle]{Success: true, Data: handle})
}

// StopInteractiveShell ends the shell and everything it started.
func StopInteractiveShell(c *gin.Context) {
	var req interactiveShellRef
	if err := c.ShouldBindJSON(&req); err != nil || !interactiveShellID.MatchString(req.ShellID) {
		shellError(c, http.StatusBadRequest, "valid shell_id is required")
		return
	}
	slot, status, message := interactiveShellSlot(c)
	if status != 0 {
		shellError(c, status, message)
		return
	}
	dir, socket, err := interactiveShellPaths(req.ShellID, slot)
	if err != nil {
		shellError(c, http.StatusInternalServerError, "Cannot find the shell folder")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = exec.CommandContext(ctx, realTmux(), "-S", socket, "kill-server").Run()
	_ = os.RemoveAll(dir)
	forgetInteractiveShell(req.ShellID)
	c.JSON(http.StatusOK, models.APIResponse[InteractiveShellHandle]{Success: true, Data: InteractiveShellHandle{ShellID: req.ShellID, Socket: socket, Session: interactiveShellSession}})
}

// ResizeInteractiveShell sets the shell window size.
func ResizeInteractiveShell(c *gin.Context) {
	var req interactiveShellRef
	if err := c.ShouldBindJSON(&req); err != nil || !interactiveShellID.MatchString(req.ShellID) {
		shellError(c, http.StatusBadRequest, "valid shell_id is required")
		return
	}
	slot, status, message := interactiveShellSlot(c)
	if status != 0 {
		shellError(c, status, message)
		return
	}
	_, socket, err := interactiveShellPaths(req.ShellID, slot)
	if err != nil || !interactiveShellRunning(socket) {
		shellError(c, http.StatusNotFound, "The shell is not running")
		return
	}
	cols, rows := clampShellSize(req.Cols, req.Rows)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := exec.CommandContext(ctx, realTmux(), "-S", socket, "resize-window", "-t", interactiveShellSession, "-x", strconv.Itoa(cols), "-y", strconv.Itoa(rows)).Run(); err != nil {
		shellError(c, http.StatusInternalServerError, "Resize failed")
		return
	}
	c.JSON(http.StatusOK, models.APIResponse[InteractiveShellHandle]{Success: true, Data: InteractiveShellHandle{ShellID: req.ShellID, Socket: socket, Session: interactiveShellSession, Running: true}})
}

// interactiveShellSweepRequest names the shells to keep; every other shell with the prefix is stopped.
type interactiveShellSweepRequest struct {
	Prefix string   `json:"prefix"`
	Keep   []string `json:"keep"`
}

// interactiveShellFolders lists every shell folder on this host: the shared shells folder and, where slots are on, each
// slot's own.
func interactiveShellFolders() []string {
	var folders []string
	roots := []string{interactiveShellRoot}
	if cfg, err := slots.LoadExecConfig(slots.ConfigPath()); err == nil && cfg.SlotRunRoot != "" {
		if slotDirs, err := filepath.Glob(filepath.Join(cfg.SlotRunRoot, "*", "shells")); err == nil {
			roots = append(roots, slotDirs...)
		}
	}
	for _, root := range roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() {
				folders = append(folders, filepath.Join(root, entry.Name()))
			}
		}
	}
	return folders
}

// SweepInteractiveShells stops shells a previous agent server left running: at its start nothing tracks them. Only
// the agent server calls it, with the ids it does track.
func SweepInteractiveShells(c *gin.Context) {
	var req interactiveShellSweepRequest
	if err := c.ShouldBindJSON(&req); err != nil || !regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,19}$`).MatchString(req.Prefix) {
		shellError(c, http.StatusBadRequest, "a prefix of 1-20 lowercase letters, digits or dashes is required")
		return
	}
	keep := map[string]bool{}
	for _, id := range req.Keep {
		keep[id] = true
	}
	stopped := 0
	for _, folder := range interactiveShellFolders() {
		id := filepath.Base(folder)
		if !strings.HasPrefix(id, req.Prefix) || !interactiveShellID.MatchString(id) || keep[id] {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		_ = exec.CommandContext(ctx, realTmux(), "-S", filepath.Join(folder, "tmux.sock"), "kill-server").Run()
		cancel()
		_ = os.RemoveAll(folder)
		forgetInteractiveShell(id)
		stopped++
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "stopped": stopped})
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
