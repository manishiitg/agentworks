//go:build linux

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/manishiitg/coding-agent-loop/workspace/security"
	"golang.org/x/sys/unix"
)

// The CLI doubles as its sandbox launcher, so installing agentworks is enough.
// This argument shape is the internal launcher protocol, not a Cobra command.
func localShellLauncher(args []string) (bool, int) {
	if len(args) < 4 || args[0] != "--config" || args[2] != "--" {
		return false, 0
	}
	if args[1] != "/proc/self/fd/3" && (!strings.HasPrefix(filepath.Base(args[1]), "agentworks-landlock-") || !strings.HasSuffix(args[1], ".json")) {
		return false, 0
	}
	file, err := os.Open(args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, "SANDBOX_UNAVAILABLE: read local shell policy")
		return true, 125
	}
	var policy security.LandlockPolicy
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	err = decoder.Decode(&policy)
	_ = file.Close()
	if args[1] == "/proc/self/fd/3" {
		_ = unix.Close(3)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "SANDBOX_UNAVAILABLE: decode local shell policy")
		return true, 125
	}
	_ = os.Remove(args[1])
	if err = security.RunLandlockLauncher(policy, args[3:]); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() >= 0 {
			return true, exit.ExitCode()
		}
		fmt.Fprintln(os.Stderr, err)
		return true, 125
	}
	return true, 0
}
func configureLocalShellSandbox() error {
	if os.Getenv("AGENTWORKS_LANDLOCK_RUNNER") != "" {
		return nil
	}
	path, err := os.Executable()
	if err != nil {
		return err
	}
	return os.Setenv("AGENTWORKS_LANDLOCK_RUNNER", path)
}
