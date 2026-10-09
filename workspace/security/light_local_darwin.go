//go:build darwin

package security

import (
	"context"
	"fmt"
	"os"
	"os/exec"
)

// ExecuteLightLocal runs command in a shell under the light sandbox (see light_local.go) in workDir.
func ExecuteLightLocal(ctx context.Context, workDir string, p LightLocalPolicy, command string) (*exec.Cmd, func(), error) {
	p.Home = lightHome(p)
	profile, err := os.CreateTemp("", "agentworks-light-*.sb")
	if err != nil {
		return nil, nil, fmt.Errorf("sandbox profile: %w", err)
	}
	cleanup := func() { _ = os.Remove(profile.Name()) }
	if _, err := profile.WriteString(lightLocalProfile(p)); err != nil {
		profile.Close()
		cleanup()
		return nil, nil, fmt.Errorf("sandbox profile: %w", err)
	}
	if err := profile.Close(); err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("sandbox profile: %w", err)
	}
	cmd := exec.CommandContext(ctx, "sandbox-exec", "-f", profile.Name(), "/bin/sh", "-c", command)
	cmd.Dir = workDir
	cmd.Env = lightLocalEnvironment(os.Environ())
	return cmd, cleanup, nil
}
