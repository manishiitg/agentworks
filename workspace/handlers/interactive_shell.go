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
	"github.com/manishiitg/coding-agent-loop/workspace/utils"
	"github.com/spf13/viper"
)

// Interactive shells (Code's plain shell). The workspace service starts a
// small tmux server inside the same sandbox /api/execute uses: Folder Guard
// via Landlock, the private /tmp, the sanitized environment and the
// allow-listed extra env. The service itself creates the namespaces, since
// it is the only binary the host's AppArmor userns exception covers; a tmux
// pane could not. The shell and everything it starts inherit the sandbox.
// The tmux socket lives in the shell's own folder, the one /tmp path the
// shell is granted; the agent server (outside the sandbox) attaches with
// `tmux -S <socket> -CC attach -t <session>`. Other sandboxed commands never
// see it: their /tmp is private.
//
// Only the agent server calls these routes (workspace token); it authorizes
// the user for the project first and passes the project's Folder Guard.

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

func interactiveShellSocket(id string) (dir, socket string) {
	dir = filepath.Join(interactiveShellRoot, id)
	return dir, filepath.Join(dir, "tmux.sock")
}

func interactiveShellRunning(socket string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, "tmux", "-S", socket, "has-session", "-t", interactiveShellSession).Run() == nil
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
	dir, socket := interactiveShellSocket(req.ShellID)
	if interactiveShellRunning(socket) {
		c.JSON(http.StatusOK, models.APIResponse[InteractiveShellHandle]{Success: true, Data: InteractiveShellHandle{ShellID: req.ShellID, Socket: socket, Session: interactiveShellSession, Running: true}})
		return
	}
	_ = os.RemoveAll(dir)
	if err := os.MkdirAll(filepath.Join(dir, "tmp"), 0o700); err != nil {
		shellError(c, http.StatusInternalServerError, "Cannot prepare the shell folder")
		return
	}
	_ = os.Chmod(interactiveShellRoot, 0o700)

	for _, wp := range req.FolderGuard.WritePaths {
		physical := wp
		if !filepath.IsAbs(physical) {
			physical = filepath.Join(docsDir, physical)
		}
		_ = os.MkdirAll(physical, 0o755)
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
	command := fmt.Sprintf("TMPDIR=%s TERM=xterm-256color exec tmux -f /dev/null -S %s new-session -d -s %s -x %d -y %d %s -l",
		shellQuote(filepath.Join(dir, "tmp")), shellQuote(socket), interactiveShellSession, cols, rows, shell)
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
	for k, v := range req.ExtraEnv {
		if isAllowedShellExtraEnvKey(k) {
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
	c.JSON(http.StatusOK, models.APIResponse[InteractiveShellHandle]{Success: true, Data: InteractiveShellHandle{ShellID: req.ShellID, Socket: socket, Session: interactiveShellSession, Running: true}})
}

// StopInteractiveShell ends the shell and everything it started.
func StopInteractiveShell(c *gin.Context) {
	var req interactiveShellRef
	if err := c.ShouldBindJSON(&req); err != nil || !interactiveShellID.MatchString(req.ShellID) {
		shellError(c, http.StatusBadRequest, "valid shell_id is required")
		return
	}
	dir, socket := interactiveShellSocket(req.ShellID)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = exec.CommandContext(ctx, "tmux", "-S", socket, "kill-server").Run()
	_ = os.RemoveAll(dir)
	c.JSON(http.StatusOK, models.APIResponse[InteractiveShellHandle]{Success: true, Data: InteractiveShellHandle{ShellID: req.ShellID, Socket: socket, Session: interactiveShellSession, Running: false}})
}

// ResizeInteractiveShell sets the shell window size.
func ResizeInteractiveShell(c *gin.Context) {
	var req interactiveShellRef
	if err := c.ShouldBindJSON(&req); err != nil || !interactiveShellID.MatchString(req.ShellID) {
		shellError(c, http.StatusBadRequest, "valid shell_id is required")
		return
	}
	_, socket := interactiveShellSocket(req.ShellID)
	if !interactiveShellRunning(socket) {
		shellError(c, http.StatusNotFound, "The shell is not running")
		return
	}
	cols, rows := clampShellSize(req.Cols, req.Rows)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := exec.CommandContext(ctx, "tmux", "-S", socket, "resize-window", "-t", interactiveShellSession, "-x", strconv.Itoa(cols), "-y", strconv.Itoa(rows)).Run(); err != nil {
		shellError(c, http.StatusInternalServerError, "Resize failed")
		return
	}
	c.JSON(http.StatusOK, models.APIResponse[InteractiveShellHandle]{Success: true, Data: InteractiveShellHandle{ShellID: req.ShellID, Socket: socket, Session: interactiveShellSession, Running: true}})
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
