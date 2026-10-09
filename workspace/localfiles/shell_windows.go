//go:build windows

package localfiles

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Local commands on Windows run as the signed-in user with no operating-system sandbox (owner decision, 2026-10-09; macOS has
// Seatbelt and Linux Landlock). The folder rules (blocked and read-only paths) therefore bind the file tools only. Commands are
// written for a POSIX shell, so they run through Git Bash (Git for Windows).

// findGitBash returns Git for Windows' bash.exe. System32\bash.exe is WSL's launcher and is never used.
func findGitBash() string {
	var candidates []string
	for _, base := range []string{os.Getenv("ProgramFiles"), os.Getenv("ProgramW6432"), os.Getenv("ProgramFiles(x86)")} {
		if base != "" {
			candidates = append(candidates, filepath.Join(base, "Git", "bin", "bash.exe"))
		}
	}
	if local := os.Getenv("LOCALAPPDATA"); local != "" {
		candidates = append(candidates, filepath.Join(local, "Programs", "Git", "bin", "bash.exe"))
	}
	if git, err := exec.LookPath("git.exe"); err == nil { // ...\Git\cmd\git.exe -> ...\Git\bin\bash.exe
		candidates = append(candidates, filepath.Join(filepath.Dir(filepath.Dir(git)), "bin", "bash.exe"))
	}
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() && !strings.Contains(strings.ToLower(candidate), `\system32\`) {
			return candidate
		}
	}
	return ""
}

func windowsShellCommand(ctx context.Context, workDir, command string) (*exec.Cmd, func(), error) {
	bash := findGitBash()
	if bash == "" {
		return nil, nil, errors.New("commands need Git for Windows (https://git-scm.com/download/win): install it, then run agentworks start again; file tools work without it")
	}
	cmd := exec.CommandContext(ctx, bash, "-lc", command)
	cmd.Dir = workDir
	cmd.Env = os.Environ()
	return cmd, func() {}, nil
}

// jobs holds the Job Object of each running command: closing it ends the command and everything it started, the Windows
// counterpart of killing a process group.
var (
	jobsMu sync.Mutex
	jobs   = map[*exec.Cmd]windows.Handle{}
)

func configureShellProcess(cmd *exec.Cmd) func() {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_NEW_PROCESS_GROUP
	var once sync.Once
	kill := func() {
		once.Do(func() {
			jobsMu.Lock()
			job, ok := jobs[cmd]
			delete(jobs, cmd)
			jobsMu.Unlock()
			if ok {
				_ = windows.TerminateJobObject(job, 1)
				_ = windows.CloseHandle(job)
			} else if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
		})
	}
	cmd.Cancel = func() error { kill(); return nil }
	return kill
}

// runShellCommand starts the command, puts it in a Job Object that kills everything it starts when closed, and waits.
func runShellCommand(cmd *exec.Cmd) error {
	if err := cmd.Start(); err != nil {
		return err
	}
	if job, err := windows.CreateJobObject(nil, nil); err == nil {
		info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE}}
		_, setErr := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)))
		process, openErr := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
		if setErr == nil && openErr == nil && windows.AssignProcessToJobObject(job, process) == nil {
			jobsMu.Lock()
			jobs[cmd] = job
			jobsMu.Unlock()
		} else {
			_ = windows.CloseHandle(job)
		}
		if openErr == nil {
			_ = windows.CloseHandle(process)
		}
	}
	return cmd.Wait()
}
