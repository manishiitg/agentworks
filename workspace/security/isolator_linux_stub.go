//go:build !linux

package security

import (
	"context"
	"fmt"
	"os/exec"
)

func (iso *Isolator) executeIsolatedLinuxPlatform(context.Context, string, []string) (*exec.Cmd, func(), error) {
	return nil, nil, fmt.Errorf("SANDBOX_UNAVAILABLE: Linux sandbox requested on a non-Linux host")
}

func CurrentSandboxCapability() SandboxCapability {
	path, err := exec.LookPath("sandbox-exec")
	if err != nil {
		return SandboxCapability{Available: false, Detail: "sandbox-exec unavailable"}
	}
	return SandboxCapability{Available: true, Backend: path}
}

// CLILandlockRunner: Landlock is Linux only.
func CLILandlockRunner() (string, bool) { return "", false }
