//go:build windows

package handlers

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// Windows has no process groups to signal: a process is stopped by itself (the local CLI's shell tracks a Job Object instead).

var errNoProcessGroups = errors.New("process groups are not available on Windows")

func getProcessGroup(int) (int, error) { return 0, errNoProcessGroups }

func signalProcessGroup(int, syscall.Signal) error { return errNoProcessGroups }

func signalProcess(pid int, _ syscall.Signal) error {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return proc.Kill()
}

func processRunning(pid int) bool {
	if pid <= 0 {
		return false
	}
	handle, err := syscall.OpenProcess(syscall.PROCESS_QUERY_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer syscall.CloseHandle(handle)
	var code uint32
	if syscall.GetExitCodeProcess(handle, &code) != nil {
		return false
	}
	return code == 259 // STILL_ACTIVE
}

func setProcessGroup(*exec.Cmd) {}
