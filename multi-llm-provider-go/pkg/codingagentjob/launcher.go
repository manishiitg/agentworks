package codingagentjob

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
)

type ProcessLauncher struct {
	Executable string
	StatePath  string
	LogDir     string
}

func (l ProcessLauncher) Launch(jobID string, delegationDepth int) (int, error) {
	executable := l.Executable
	if executable == "" {
		var err error
		executable, err = os.Executable()
		if err != nil {
			return 0, fmt.Errorf("resolve MCP executable: %w", err)
		}
	}
	if l.StatePath == "" {
		return 0, fmt.Errorf("worker state path is required")
	}
	logDir := l.LogDir
	if logDir == "" {
		logDir = filepath.Join(filepath.Dir(l.StatePath), "logs")
	}
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		return 0, fmt.Errorf("create worker log directory: %w", err)
	}
	logPath := filepath.Join(logDir, jobID+".log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return 0, fmt.Errorf("open worker log: %w", err)
	}
	defer logFile.Close()

	// The worker must outlive the MCP request that launched it.
	command := exec.CommandContext(context.Background(), executable, "worker", "--job-id", jobID, "--state", l.StatePath)
	command.Stdin = nil
	command.Stdout = logFile
	command.Stderr = logFile
	command.Env = append(os.Environ(), EnvDelegationDepth+"="+strconv.Itoa(delegationDepth))
	configureDetachedProcess(command)
	if err := command.Start(); err != nil {
		return 0, err
	}
	pid := command.Process.Pid
	// Reap the worker while this MCP server remains alive. If the MCP server
	// exits first, Setsid keeps the worker independent and the OS reparents it.
	go func() { _ = command.Wait() }()
	return pid, nil
}
