// Package shellfixture holds the shell executor mcpagent's own bridge e2e tests
// register so the bridge has something real to call.
//
// It lives outside package codeexec on purpose. As part of codeexec it read as
// platform infrastructure — exported, production-looking, sitting beside the
// live tool registry — and on 2026-08-04 its 100KB maxOutputBytes was audited as
// the platform's shell cap. It never was. A host that owns a workspace runs
// shell commands in that service, behind a folder guard and a kernel sandbox
// this package does not have; mcp-agent-builder-go posts to /api/execute and
// never reaches here. The live path was uncapped, and a coding CLI rejected
// results up to 130,046 characters twelve times in one morning.
//
// Nothing outside a test may import this package.
package shellfixture

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// maxOutputBytes caps stdout/stderr captured here (100KB). It bounds this
// fixture only — see the package comment.
const maxOutputBytes = 100 * 1024

// ExecuteShellCommand runs a shell command via sh -c and returns
// a formatted string with exit_code, stdout, and stderr.
//
// env specifies the environment for the child process. If nil, a minimal safe
// environment is used instead of inheriting the parent process environment.
// Callers should pass BuildSafeEnvironment() plus any required env vars when
// the command needs bridge or workflow-specific values.
// stdout and stderr are each capped at maxOutputBytes.
func ExecuteShellCommand(ctx context.Context, args map[string]interface{}, env []string) (string, error) {
	command, ok := args["command"].(string)
	if !ok {
		return "", fmt.Errorf("command must be a string")
	}

	workingDirectory, err := shellWorkingDirectory(args)
	if err != nil {
		return "", err
	}

	cmd := exec.CommandContext(ctx, "sh", "-c", command) //nolint:gosec // G204: intentional — this tool's purpose is to execute user-provided commands
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if workingDirectory != "" {
		cmd.Dir = workingDirectory
	}

	if env != nil {
		cmd.Env = env
	} else {
		cmd.Env = BuildSafeEnvironment()
	}

	err = cmd.Run()

	exitCode := 0
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			return "", fmt.Errorf("failed to execute command: %w", err)
		}
	}

	stdoutStr := truncateOutput(stdout.Bytes(), maxOutputBytes)
	stderrStr := truncateOutput(stderr.Bytes(), maxOutputBytes)

	return fmt.Sprintf("exit_code: %d\nstdout:\n%s\nstderr:\n%s", exitCode, stdoutStr, stderrStr), nil
}

// BuildSafeEnvironment creates a minimal environment for shell commands.
// It intentionally excludes the parent process environment so API keys and
// process-level secrets are not inherited by accident.
func BuildSafeEnvironment() []string {
	return []string{
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"HOME=/tmp",
		"USER=agent",
		"SHELL=/bin/sh",
		"LANG=C.UTF-8",
		"LC_ALL=C.UTF-8",
	}
}

func shellWorkingDirectory(args map[string]interface{}) (string, error) {
	raw, ok := args["working_directory"]
	if !ok || raw == nil {
		return "", nil
	}
	dir, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("working_directory must be a string")
	}
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return "", nil
	}
	cleaned := filepath.Clean(dir)
	info, err := os.Stat(cleaned)
	if err != nil {
		return "", fmt.Errorf("working_directory %q is not accessible: %w", dir, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("working_directory %q is not a directory", dir)
	}
	return cleaned, nil
}

// truncateOutput truncates output to maxBytes and appends a truncation notice.
func truncateOutput(data []byte, maxBytes int) string {
	if len(data) <= maxBytes {
		return string(data)
	}
	return string(data[:maxBytes]) + "\n... [truncated, output exceeded 100KB limit]"
}
