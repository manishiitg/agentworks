package handlers

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/manishiitg/coding-agent-loop/workspace/models"
	"github.com/manishiitg/coding-agent-loop/workspace/security"
	"github.com/manishiitg/coding-agent-loop/workspace/utils"

	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"
)

// ExecuteShellCommand handles POST /api/execute
func ExecuteShellCommand(c *gin.Context) {
	var req models.ExecuteShellRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.APIResponse[any]{
			Success: false,
			Message: "Invalid request body",
			Error:   err.Error(),
		})
		return
	}

	// Validate command is not empty
	if strings.TrimSpace(req.Command) == "" {
		c.JSON(http.StatusBadRequest, models.APIResponse[any]{
			Success: false,
			Message: "Command is required",
			Error:   "Command cannot be empty",
		})
		return
	}

	// Get docs directory
	docsDir := viper.GetString("docs-dir")

	// Log user ID from request header for debugging
	rawUserIDHeader := c.GetHeader("X-User-ID")
	resolvedUserID := getUserID(c)
	commandLength, commandFingerprint := shellCommandLogIdentity(req.Command)
	workspaceDebugLogf("[USER_ID_DEBUGGING] Shell handler: X-User-ID header=%q, resolved=%q, command_length=%d, command_sha256=%s, working_dir=%q",
		rawUserIDHeader, resolvedUserID, commandLength, commandFingerprint, req.WorkingDirectory)

	// Determine working directory
	workingDir := docsDir
	if req.WorkingDirectory != "" {
		// Sanitize and validate working directory
		sanitizedDir := utils.SanitizeInputPath(req.WorkingDirectory, docsDir)

		// Build full path
		fullWorkingDir := filepath.Join(docsDir, sanitizedDir)

		// Validate path is within docs-dir boundary
		if !utils.IsValidFilePath(fullWorkingDir, docsDir) {
			c.JSON(http.StatusBadRequest, models.APIResponse[any]{
				Success: false,
				Message: "Invalid working directory",
				Error:   "Working directory must be within the workspace boundary and cannot contain directory traversal",
			})
			return
		}

		// Check if directory exists (for FolderGuard mode, the logical path may exist
		// as a mount point artifact from previous isolator runs)
		if info, err := os.Stat(fullWorkingDir); os.IsNotExist(err) || !info.IsDir() {
			c.JSON(http.StatusBadRequest, models.APIResponse[any]{
				Success: false,
				Message: "Working directory does not exist",
				Error:   fmt.Sprintf("Directory does not exist: %s", req.WorkingDirectory),
			})
			return
		}

		workingDir = fullWorkingDir
	}

	// Validate and set timeout
	// Default: 60s for normal commands. 0 = 30 minutes (for long-running operations
	// like sub-agent calls via curl).
	timeoutSeconds := 60
	if req.Timeout == 0 {
		timeoutSeconds = 3600 // 1 hour
	} else if req.Timeout > 0 {
		timeoutSeconds = req.Timeout
	}

	// Create context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSeconds)*time.Second)
	defer cancel()

	// Read-only scripted steps cannot safely open the live db.sqlite through a
	// sandbox that denies creation of its WAL/SHM sidecars. Materialize a
	// transactionally consistent standalone copy while still in the trusted
	// workspace service, then expose only that copy to the child process.
	if req.DBReadSnapshot {
		snapshotPath, snapshotCleanup, snapshotErr := createReadonlyDBSnapshot(ctx, docsDir, req.ExtraEnv, req.FolderGuard)
		if snapshotErr != nil {
			c.JSON(http.StatusBadRequest, models.APIResponse[any]{
				Success: false,
				Message: "Failed to prepare read-only workflow database snapshot",
				Error:   snapshotErr.Error(),
			})
			return
		}
		defer snapshotCleanup()
		req.ExtraEnv["DB_PATH"] = snapshotPath
	}

	// Build command with folder guard isolation
	var cmd *exec.Cmd
	var fullCommand string
	var cleanup func()

	// Check if folder guard is enabled
	if req.FolderGuard != nil && req.FolderGuard.Enabled {
		// Pre-create write path directories in the real filesystem before isolation.
		// The mount script relies on these existing so it can bind-mount them as writable.
		for _, wp := range req.FolderGuard.WritePaths {
			physicalPath := wp
			// Resolve relative to docsDir if not already absolute
			if !filepath.IsAbs(physicalPath) {
				physicalPath = filepath.Join(docsDir, physicalPath)
			}
			if mkErr := os.MkdirAll(physicalPath, 0755); mkErr != nil {
				log.Printf("[SHELL ISOLATOR] Warning: failed to pre-create write path %s: %v", physicalPath, mkErr)
			}
		}

		// Use isolated execution with filesystem restrictions
		isolator := &security.Isolator{
			ReadPaths:         req.FolderGuard.ReadPaths,
			WritePaths:        req.FolderGuard.WritePaths,
			BlockedPaths:      req.FolderGuard.BlockedPaths,
			BlockedWritePaths: req.FolderGuard.BlockedWritePaths,
			WorkDir:           workingDir,
			BaseDir:           docsDir,
			// An agent profile's runtime.sandbox policy: deny-by-default and,
			// optionally, no outbound network (macOS seatbelt enforces both;
			// the Linux path applies the folder rules only).
			StrictAllowlist: req.FolderGuard.StrictAllowlist,
			AllowNetwork:    !req.FolderGuard.DenyNetwork,
		}

		// Debug: log isolator configuration for troubleshooting mount namespace issues
		workspaceDebugPrintf("[SHELL ISOLATOR] WorkDir=%s ReadPaths=%v WritePaths=%v BlockedWritePaths=%v\n",
			workingDir, req.FolderGuard.ReadPaths, req.FolderGuard.WritePaths, req.FolderGuard.BlockedWritePaths)

		fullCommand = stripShellPrefix(req.Command)

		var err error
		// Pass the command directly - generateMountScript already wraps with "exec sh -c '...'"
		// Do NOT double-wrap with "sh -c" here, as that causes shell argument parsing issues
		cmd, cleanup, err = isolator.ExecuteIsolated(ctx, fullCommand, nil)
		if err != nil {
			c.JSON(http.StatusInternalServerError, models.APIResponse[any]{
				Success: false,
				Message: "Failed to setup isolated execution",
				Error:   err.Error(),
			})
			return
		}
		if cleanup != nil {
			defer cleanup() // Clean up script file after execution
		}

	} else {
		// Non-isolated execution
		fullCommand = stripShellPrefix(req.Command)

		cmd = exec.CommandContext(ctx, "sh", "-c", fullCommand)
		cmd.Dir = workingDir

		// CRITICAL: Always sanitize environment, even without folder guard
		cmd.Env = security.BuildSafeEnvironment()
	}

	configureShellCommandProcessGroup(cmd)

	// Check browser session limits for agent-browser commands (both direct and via code exec)
	if browserLimitMsg := CheckBrowserSessionLimit(req.Command, req.ExtraEnv); browserLimitMsg != "" {
		c.JSON(http.StatusOK, models.APIResponse[models.ExecuteShellResponse]{
			Success: true,
			Message: "Command executed successfully",
			Data: models.ExecuteShellResponse{
				Stdout:          browserLimitMsg,
				Stderr:          "",
				ExitCode:        1,
				ExecutionTimeMs: 0,
				Command:         fullCommand,
			},
		})
		return
	}

	// Inject whitelisted extra env vars
	// MCP_*    — internal API URLs and tokens
	// SECRET_* — user-provided credentials and secrets
	// VAR_*    — workflow variables (non-secret config values like user IDs, sheet IDs)
	// STEP_*   — per-step execution paths and trusted route delegation context
	// DB_PATH  — absolute path to the workflow's SQLite database
	// SCRIPT_* — script control flags (SCRIPT_VERBOSE)
	// This applies to both isolated and non-isolated execution paths
	extraEnvCount := 0
	for k, v := range req.ExtraEnv {
		if isAllowedShellExtraEnvKey(k) {
			if k == "PYTHONPATH" {
				// Keep the platform helper importable alongside workflow helpers.
				for _, entry := range cmd.Env {
					if strings.HasPrefix(entry, "TMPDIR=") {
						v = strings.TrimPrefix(entry, "TMPDIR=") + string(os.PathListSeparator) + v
					}
				}
			}
			cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
			extraEnvCount++
		}
	}
	workspaceDebugLogf("[SHELL_ENV_DEBUG] ExtraEnv received: %d keys total, %d allowed (runtime prefixes plus DB_PATH/PYTHONDONTWRITEBYTECODE)", len(req.ExtraEnv), extraEnvCount)
	if len(req.ExtraEnv) > 0 {
		keys := make([]string, 0, len(req.ExtraEnv))
		for k := range req.ExtraEnv {
			keys = append(keys, k)
		}
		workspaceDebugLogf("[SHELL_ENV_DEBUG] ExtraEnv keys: %v", keys)
	}

	// Capture stdout and stderr separately
	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	if req.ArtifactTransfer != nil {
		if req.FolderGuard == nil || !req.FolderGuard.Enabled {
			c.JSON(http.StatusBadRequest, models.APIResponse[models.ExecuteShellResponse]{
				Success: false,
				Message: "Browser artifact transfer requires folder guard",
				Error:   "artifact destination cannot be authorized without an enabled folder guard",
			})
			return
		}
		if prepareErr := security.PrepareBrowserArtifactStaging(req.ArtifactTransfer.SourcePath); prepareErr != nil {
			c.JSON(http.StatusBadRequest, models.APIResponse[models.ExecuteShellResponse]{
				Success: false,
				Message: "Browser artifact staging failed",
				Error:   prepareErr.Error(),
			})
			return
		}
	}
	if len(req.UploadTransfers) > 0 {
		if req.FolderGuard == nil || !req.FolderGuard.Enabled {
			c.JSON(http.StatusBadRequest, models.APIResponse[models.ExecuteShellResponse]{
				Success: false,
				Message: "Browser upload staging requires folder guard",
				Error:   "upload sources cannot be authorized without an enabled folder guard",
			})
			return
		}
		for _, transfer := range req.UploadTransfers {
			if stageErr := security.StageBrowserUpload(
				transfer.SourcePath,
				transfer.StagedPath,
				docsDir,
				workingDir,
				req.FolderGuard.ReadPaths,
				req.FolderGuard.WritePaths,
				req.FolderGuard.BlockedPaths,
			); stageErr != nil {
				for _, cleanup := range req.UploadTransfers {
					security.CleanupBrowserUploadStaging(cleanup.StagedPath)
				}
				c.JSON(http.StatusBadRequest, models.APIResponse[models.ExecuteShellResponse]{
					Success: false,
					Message: "Browser upload staging failed",
					Error:   stageErr.Error(),
				})
				return
			}
		}
		defer func() {
			for _, transfer := range req.UploadTransfers {
				security.CleanupBrowserUploadStaging(transfer.StagedPath)
			}
		}()
	}

	// Record start time
	startTime := time.Now()

	// Execute command
	if err := cmd.Start(); err != nil {
		executionTime := time.Since(startTime)
		c.JSON(http.StatusInternalServerError, models.APIResponse[models.ExecuteShellResponse]{
			Success: false,
			Message: "Failed to start command",
			Error:   err.Error(),
			Data: models.ExecuteShellResponse{
				Stdout:          stdoutBuf.String(),
				Stderr:          err.Error(),
				ExitCode:        -1,
				ExecutionTimeMs: int(executionTime.Milliseconds()),
				Command:         fullCommand,
			},
		})
		return
	}
	processRecord := registerShellProcess(cmd, ownerFromShellRequest(req.ExtraEnv, workingDir, fullCommand), fullCommand, workingDir, timeoutSeconds)
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			killShellCommandProcessGroup(cmd)
		case <-done:
		}
	}()
	err := cmd.Wait()
	close(done)
	executionTime := time.Since(startTime)

	// Get exit code
	exitCode := 0
	status := "completed"
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			timeoutExitCode := -1
			finishShellProcess(processRecord.PID, "timeout", &timeoutExitCode)
			// Build stderr with timeout info prepended so the LLM sees it clearly
			timeoutMsg := fmt.Sprintf("TIMEOUT: Command killed after %d seconds\n", timeoutSeconds)
			capturedStderr := stderrBuf.String()
			if capturedStderr != "" {
				timeoutMsg += capturedStderr
			}
			c.JSON(http.StatusRequestTimeout, models.APIResponse[models.ExecuteShellResponse]{
				Success: false,
				Message: "Command execution timed out",
				Error:   fmt.Sprintf("Command exceeded timeout of %d seconds", timeoutSeconds),
				Data: models.ExecuteShellResponse{
					Stdout:          stdoutBuf.String(),
					Stderr:          timeoutMsg,
					ExitCode:        -1,
					ExecutionTimeMs: int(executionTime.Milliseconds()),
					Command:         fullCommand,
				},
			})
			return
		}
		if exitError, ok := err.(*exec.ExitError); ok {
			exitCode = exitError.ExitCode()
			status = "failed"
		} else {
			// Other execution errors (e.g., command not found)
			errorExitCode := -1
			finishShellProcess(processRecord.PID, "failed", &errorExitCode)
			errorStderr := stderrBuf.String()
			if errorStderr == "" {
				errorStderr = err.Error()
			}
			c.JSON(http.StatusInternalServerError, models.APIResponse[models.ExecuteShellResponse]{
				Success: false,
				Message: "Failed to execute command",
				Error:   err.Error(),
				Data: models.ExecuteShellResponse{
					Stdout:          stdoutBuf.String(),
					Stderr:          errorStderr,
					ExitCode:        -1,
					ExecutionTimeMs: int(executionTime.Milliseconds()),
					Command:         fullCommand,
				},
			})
			return
		}
	}
	finishShellProcess(processRecord.PID, status, &exitCode)

	// Browser daemons are intentionally persistent and may have inherited a
	// different workflow step's sandbox. Finalize their staged artifacts in this
	// trusted process only after the command succeeds and the current request's
	// write guard authorizes the destination.
	if exitCode == 0 && req.ArtifactTransfer != nil && req.ArtifactTransfer.Finalize {
		transfer := req.ArtifactTransfer
		if transferErr := security.FinalizeBrowserArtifact(
			transfer.SourcePath,
			transfer.DestinationPath,
			transfer.Kind,
			docsDir,
			req.FolderGuard.WritePaths,
			req.FolderGuard.BlockedPaths,
			req.FolderGuard.BlockedWritePaths,
		); transferErr != nil {
			c.JSON(http.StatusBadRequest, models.APIResponse[models.ExecuteShellResponse]{
				Success: false,
				Message: "Browser artifact transfer failed",
				Error:   transferErr.Error(),
				Data: models.ExecuteShellResponse{
					Stdout:          stdoutBuf.String(),
					Stderr:          stderrBuf.String(),
					ExitCode:        exitCode,
					ExecutionTimeMs: int(executionTime.Milliseconds()),
					Command:         fullCommand,
				},
			})
			return
		}
	}

	// Success response
	c.JSON(http.StatusOK, models.APIResponse[models.ExecuteShellResponse]{
		Success: true,
		Message: "Command executed successfully",
		Data: models.ExecuteShellResponse{
			Stdout:          stdoutBuf.String(),
			Stderr:          stderrBuf.String(),
			ExitCode:        exitCode,
			ExecutionTimeMs: int(executionTime.Milliseconds()),
			Command:         fullCommand,
		},
	})
}

func isAllowedShellExtraEnvKey(key string) bool {
	return strings.HasPrefix(key, "MCP_") ||
		strings.HasPrefix(key, "SECRET_") ||
		strings.HasPrefix(key, "WORKFLOW_FOLDER_") ||
		strings.HasPrefix(key, "WORK_FOLDER_") ||
		strings.HasPrefix(key, "VAR_") ||
		strings.HasPrefix(key, "STEP_") ||
		strings.HasPrefix(key, "SCRIPT_") ||
		strings.HasPrefix(key, "RUNLOOP_") ||
		strings.HasPrefix(key, "REPORT_") ||
		key == "DB_PATH" ||
		key == "WORKFLOW_CODE_ROOT" || key == "WORKFLOW_CODE_DEPS" || key == "PYTHONPATH" ||
		key == "PYTHONDONTWRITEBYTECODE"
}

// stripShellPrefix removes a leading "sh -c " wrapper from the command string.
// LLMs sometimes generate commands like "sh -c ls -la" which, when passed to
// exec.CommandContext(ctx, "sh", "-c", fullCommand), causes double sh -c wrapping
// and breaks argument parsing.
func stripShellPrefix(cmd string) string {
	trimmed := strings.TrimSpace(cmd)
	for _, prefix := range []string{"sh -c ", "/bin/sh -c ", "bash -c ", "/bin/bash -c "} {
		if strings.HasPrefix(trimmed, prefix) {
			return strings.TrimSpace(trimmed[len(prefix):])
		}
	}
	return cmd
}

func configureShellCommandProcessGroup(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

func killShellCommandProcessGroup(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	pid := cmd.Process.Pid
	if pid <= 0 {
		return
	}
	if pgid, err := syscall.Getpgid(pid); err == nil && pgid > 0 {
		if err := syscall.Kill(-pgid, syscall.SIGKILL); err == nil {
			return
		}
	}
	_ = cmd.Process.Kill()
}
