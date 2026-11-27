//go:build windows

package upstream

import "os/exec"

func configureCommandProcessGroup(_ *exec.Cmd) {}

func killCommandProcessGroup(_ *exec.Cmd) {}
