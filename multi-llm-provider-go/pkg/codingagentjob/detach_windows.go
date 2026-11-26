//go:build windows

package codingagentjob

import "os/exec"

func configureDetachedProcess(command *exec.Cmd) {}
