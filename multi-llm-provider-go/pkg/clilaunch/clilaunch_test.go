package clilaunch

import (
	"context"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func TestConfineCmdLeavesACommandAloneWhenLandlockIsNotEnforced(t *testing.T) {
	cmd := exec.CommandContext(context.Background(), "/bin/echo", "hi")
	before := append([]string{}, cmd.Args...)
	cleanup, err := ConfineCmd(&llmtypes.CLISecurityPolicy{}, cmd, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cleanup()
	if len(cmd.Args) != len(before) || cmd.Args[0] != before[0] {
		t.Fatalf("an unenforced policy changed the command: %v", cmd.Args)
	}
}

func TestConfineCmdRefusesAnEnforcingPolicyWithoutAUsableLauncher(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Landlock is Linux only")
	}
	cmd := exec.CommandContext(context.Background(), "/bin/echo", "hi")
	policy := &llmtypes.CLISecurityPolicy{
		Mode:           llmtypes.CLISecurityModeIsolated,
		LandlockRunner: filepath.Join(t.TempDir(), "missing-runner"),
		PrivateHome:    t.TempDir(),
	}
	if _, err := ConfineCmd(policy, cmd, t.TempDir()); err == nil {
		t.Fatal("an enforcing policy with a missing launcher must not start the command unconfined")
	}
}
