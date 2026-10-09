package handlers

import (
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
	"github.com/manishiitg/coding-agent-loop/workspace/slots"
	"github.com/spf13/viper"
)

// resolveGuardWritePath resolves one FolderGuard.WritePaths entry the way the
// isolator resolves it (relative entries join to docsDir, absolute entries
// used as-is) and requires the result to stay inside the workspace boundary,
// following the same containment check as the working directory. Absolute
// paths outside the boundary, .. escapes, and symlink redirects fail closed:
// the caller must reject the request, never MkdirAll the result.
func resolveGuardWritePath(wp, docsDir string) (string, error) {
	physicalPath := wp
	if !filepath.IsAbs(physicalPath) {
		physicalPath = filepath.Join(docsDir, physicalPath)
	}
	if !utils.IsValidFilePath(physicalPath, docsDir) {
		return "", fmt.Errorf("write path must be within the workspace boundary and cannot contain directory traversal")
	}
	return physicalPath, nil
}

// isExistingHostGrant reports whether a write path outside the workspace
// boundary, on a person's own machine, is an already-existing absolute directory that is safe to leave to
// the isolator: it is not created or resolved through anything, so the
// boundary check's concern (directories created anywhere as the service
// account) does not apply. A missing path, a relative path, any ".." segment
// and anything lexically inside the workspace (symlink redirects) still fail.
func isExistingHostGrant(wp, docsDir string) bool {
	// Only on a person's own Mac (the terminal's rule): on a server, Linux or
	// NATIVE_WORKSPACE alike, a folder named in a workflow must not reach outside
	// the workspace, whoever owns that workflow.
	if !filepath.IsAbs(wp) || !interactiveShellUnconfinedAllowed() {
		return false
	}
	for _, segment := range strings.Split(filepath.ToSlash(wp), "/") {
		if segment == ".." {
			return false
		}
	}
	clean := filepath.Clean(wp)
	if rel, err := filepath.Rel(docsDir, clean); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	info, err := os.Stat(clean)
	return err == nil && info.IsDir()
}

// Compatibility helper: existing local host roots are never created here.
func guardWritePathToCreate(wp, docsDir string) (string, error) {
	physicalPath, err := resolveGuardWritePath(wp, docsDir)
	if err != nil && os.Getenv("LOCAL_MODE") == "true" && os.Getenv("MULTI_USER_MODE") == "false" && os.Getenv("NATIVE_WORKSPACE") == "true" && isExistingHostGrant(wp, docsDir) {
		return "", nil
	}
	return physicalPath, err
}

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

	// Where slots are on, every command runs as the caller's own Linux account and never without
	// a folder guard: an unguarded command would run as the service account from the workspace root.
	userSlot, slotsOn, slotErr := slots.For(resolvedUserID)
	// A plain agent-browser call runs as the service account (the platform owns the browser daemon, its profile
	// folders and the live view); it keeps the folder guard. See isStandaloneBrowserCommand.
	if slotsOn && slotErr == nil && userSlot != "" && isStandaloneBrowserCommand(stripShellPrefix(req.Command)) {
		log.Printf("[SLOTS] browser command for %s runs as the service account, not %s", resolvedUserID, userSlot)
		userSlot = ""
	}
	// Brain's folder is app-owned (PLAT-633): a command working there runs as the service account, and only when the
	// server's folder guard grants it Brain/ (the Brain chat of someone who owns the whole Brain); Landlock still
	// confines it to that guard.
	if slotsOn && slotErr == nil && userSlot != "" && isBrainFolderCommand(docsDir, workingDir, req.FolderGuard) {
		log.Printf("[SLOTS] Brain folder command for %s runs as the service account, not %s", resolvedUserID, userSlot)
		userSlot = ""
	}
	if slotsOn {
		if slotErr != nil {
			c.JSON(http.StatusForbidden, models.APIResponse[any]{
				Success: false,
				Message: "No account slot for this user",
				Error:   slotErr.Error(),
			})
			return
		}
		if req.FolderGuard == nil || !req.FolderGuard.Enabled {
			c.JSON(http.StatusForbidden, models.APIResponse[any]{
				Success: false,
				Message: "Commands without a folder guard are not available on this server",
				Error:   "a folder guard is required",
			})
			return
		}
	}

	// Check if folder guard is enabled
	if req.FolderGuard != nil && req.FolderGuard.Enabled {
		// Pre-create write path directories in the real filesystem before isolation.
		// The mount script relies on these existing so it can bind-mount them as writable.
		// Only create workspace paths. The single-user local native launcher
		// may keep existing host-folder grants without a service-side MkdirAll.
		for _, wp := range req.FolderGuard.WritePaths {
			physicalPath, wpErr := resolveGuardWritePath(wp, docsDir)
			if wpErr != nil && isExistingHostGrant(wp, docsDir) {
				// A folder the person granted outside the workspace (Downloads,
				// a project folder): it already exists and is mounted as it is.
				// It is never created here.
				continue
			}
			if wpErr != nil {
				c.JSON(http.StatusBadRequest, models.APIResponse[any]{
					Success: false,
					Message: "Invalid folder guard write path",
					Error:   wpErr.Error(),
				})
				return
			}
			if physicalPath == "" {
				continue
			}
			if mkErr := prepareGuardWriteDirectory(physicalPath, docsDir, userSlot != ""); mkErr != nil {
				if userSlot != "" {
					c.JSON(http.StatusInternalServerError, models.APIResponse[any]{Success: false, Message: "Failed to prepare slot write directory", Error: mkErr.Error()})
					return
				}
				log.Printf("[SHELL ISOLATOR] Warning: failed to pre-create write path %s: %v", physicalPath, mkErr)
			}
		}

		// The per-call environment a slot's command needs is part of the request that runs it as that account.
		slotExtraEnv := map[string]string{}
		for k, v := range req.ExtraEnv {
			if isAllowedShellExtraEnvKey(k) {
				slotExtraEnv[k] = v
			}
		}

		// Use isolated execution with filesystem restrictions
		// A Code command run as its owner's slot gets the slot's own home: one home per person for Code, shared by the terminal
		// and the agent (installs and logins made once). Workflows and Crew keep their per-project home.
		userHome := ""
		if userSlot != "" && slots.IsCodeProjectDir(docsDir, workingDir) {
			userHome = slots.HomeOf(userSlot)
		}
		isolator := &security.Isolator{
			Slot:              userSlot,
			UserHome:          userHome,
			ExtraEnv:          slotExtraEnv,
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
			BrowserSession:  req.FolderGuard.BrowserSession,
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
	browserTransport := ""
	if req.FolderGuard != nil && req.FolderGuard.Enabled {
		browserTransport = req.FolderGuard.BrowserTransport
	}
	if browserLimitMsg := CheckBrowserSessionLimit(req.Command, req.ExtraEnv, browserTransport); browserLimitMsg != "" {
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

	if req.ArtifactTransfer != nil {
		if req.FolderGuard == nil || !req.FolderGuard.Enabled {
			c.JSON(http.StatusBadRequest, models.APIResponse[models.ExecuteShellResponse]{
				Success: false,
				Message: "Browser artifact transfer requires folder guard",
				Error:   "artifact destination cannot be authorized without an enabled folder guard",
			})
			return
		}
		if prepareErr := security.PrepareBrowserArtifactStaging(req.ArtifactTransfer.SourcePath, req.FolderGuard.BrowserSession); prepareErr != nil {
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

	// Capture stdout and stderr separately, through pipes this service makes itself (PLAT-645): Wait then returns
	// when the shell exits, not when the last background job holding the output ends. See shellOutputCapture.
	stdoutR, stdoutW, pipeErr := os.Pipe()
	var stderrR, stderrW *os.File
	if pipeErr == nil {
		if stderrR, stderrW, pipeErr = os.Pipe(); pipeErr != nil {
			_ = stdoutR.Close()
			_ = stdoutW.Close()
		}
	}
	if pipeErr != nil {
		c.JSON(http.StatusInternalServerError, models.APIResponse[models.ExecuteShellResponse]{
			Success: false,
			Message: "Failed to start command",
			Error:   pipeErr.Error(),
			Data:    models.ExecuteShellResponse{Stderr: pipeErr.Error(), ExitCode: -1, Command: fullCommand},
		})
		return
	}
	cmd.Stdout = stdoutW
	cmd.Stderr = stderrW

	// Record start time
	startTime := time.Now()

	// Execute command
	startErr := cmd.Start()
	// The children hold their own copies of the write ends; this process must not, or the readers never see EOF.
	_ = stdoutW.Close()
	_ = stderrW.Close()
	stdoutCap := newShellOutputCapture(stdoutR)
	stderrCap := newShellOutputCapture(stderrR)
	if err := startErr; err != nil {
		executionTime := time.Since(startTime)
		c.JSON(http.StatusInternalServerError, models.APIResponse[models.ExecuteShellResponse]{
			Success: false,
			Message: "Failed to start command",
			Error:   err.Error(),
			Data: models.ExecuteShellResponse{
				Stdout:          "",
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
	// A process the command left running in the background still holds its output: return now, tell the caller
	// what is still running, and stop keeping that output (PLAT-645). Confinement is unchanged: the processes keep
	// the sandbox, account and process group they were started in.
	backgroundNotice := ""
	if !waitShellOutputs(shellBackgroundGrace, stdoutCap, stderrCap) {
		stdoutCap.detach()
		stderrCap.detach()
		slotted := slots.IsWrapped(cmd)
		var pids []int
		if !slotted {
			pids = shellProcessGroupMembers(processRecord.PGID)
		}
		backgroundNotice = shellBackgroundNotice(pids, slotted)
		log.Printf("[SHELL] command_sha256=%s left background process(es) running (pgid=%d pids=%v slotted=%v); returning without waiting for them",
			commandFingerprint, processRecord.PGID, pids, slotted)
	}

	// Get exit code
	exitCode := 0
	status := "completed"
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			timeoutExitCode := -1
			finishShellProcess(processRecord.PID, "timeout", &timeoutExitCode)
			// Build stderr with timeout info prepended so the LLM sees it clearly
			timeoutMsg := fmt.Sprintf("TIMEOUT: Command killed after %d seconds\n", timeoutSeconds)
			capturedStderr := stderrCap.String()
			if capturedStderr != "" {
				timeoutMsg += capturedStderr
			}
			c.JSON(http.StatusRequestTimeout, models.APIResponse[models.ExecuteShellResponse]{
				Success: false,
				Message: "Command execution timed out",
				Error:   fmt.Sprintf("Command exceeded timeout of %d seconds", timeoutSeconds),
				Data: models.ExecuteShellResponse{
					Stdout:          stdoutCap.String(),
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
			errorStderr := stderrCap.String()
			if errorStderr == "" {
				errorStderr = err.Error()
			}
			c.JSON(http.StatusInternalServerError, models.APIResponse[models.ExecuteShellResponse]{
				Success: false,
				Message: "Failed to execute command",
				Error:   err.Error(),
				Data: models.ExecuteShellResponse{
					Stdout:          stdoutCap.String(),
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
	if exitCode == 0 && req.FolderGuard != nil && req.FolderGuard.BrowserSession != "" && commandClosesBrowser(req.Command) {
		removeSocketFolderAfterClose(req.FolderGuard.BrowserSession)
	}

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
			req.FolderGuard.BrowserSession,
		); transferErr != nil {
			c.JSON(http.StatusBadRequest, models.APIResponse[models.ExecuteShellResponse]{
				Success: false,
				Message: "Browser artifact transfer failed",
				Error:   transferErr.Error(),
				Data: models.ExecuteShellResponse{
					Stdout:          stdoutCap.String(),
					Stderr:          stderrCap.String(),
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
			Stdout:          stdoutCap.String(),
			Stderr:          stderrCap.String() + backgroundNotice,
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
		key == "PYTHONDONTWRITEBYTECODE" ||
		// Paths and flags the platform sets for a workflow's code steps (code_layout.go codeRuntimeEnv). Dropping them
		// here meant a step never learned where its trigger delivery was, so a webhook, MCP or Crew-started run always
		// looked like it had no input (PLAT-514), and knowledge-base and database access flags never arrived either.
		strings.HasPrefix(key, "WORKFLOW_TRIGGER_") ||
		strings.HasPrefix(key, "WORKFLOW_KB_") ||
		key == "WORKFLOW_DB_ACCESS" || key == "RUN_FOLDER" ||
		// Brain's chat runs git in Brain's folder (PLAT-633): the token its credential helper reads, and the person as
		// the commit author.
		strings.HasPrefix(key, "BRAIN_GIT_") ||
		key == "GIT_AUTHOR_NAME" || key == "GIT_AUTHOR_EMAIL" || key == "GIT_COMMITTER_NAME" || key == "GIT_COMMITTER_EMAIL"
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
	setProcessGroup(cmd)
}

func killShellCommandProcessGroup(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	pid := cmd.Process.Pid
	if pid <= 0 {
		return
	}
	if slots.IsWrapped(cmd) {
		// A slotted command's processes belong to another account, so a kill from here would stop only sudo and
		// leave the command running. A graceful signal is passed on by sudo to the slot program, which stops the
		// whole group and kills what is left after its grace period; the hard kill below is the last resort.
		_ = cmd.Process.Signal(syscall.SIGTERM)
		time.AfterFunc(slots.StopGrace*3, func() {
			if pgid, err := getProcessGroup(pid); err == nil && pgid > 0 {
				_ = signalProcessGroup(pgid, syscall.SIGKILL)
			}
			_ = cmd.Process.Kill()
		})
		return
	}
	if pgid, err := getProcessGroup(pid); err == nil && pgid > 0 {
		if err := signalProcessGroup(pgid, syscall.SIGKILL); err == nil {
			return
		}
	}
	_ = cmd.Process.Kill()
}
