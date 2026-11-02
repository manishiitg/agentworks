//go:build windows

package testing

import (
	"os/exec"
	"strconv"
)

func setPreflightProcessGroup(*exec.Cmd) {}

// killPreflightProcessGroup ends the process and its children (no process groups on Windows).
func killPreflightProcessGroup(pid int) error {
	return exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(pid)).Run()
}
