package server

import (
	"context"
	"os/exec"
	"testing"

	"github.com/manishiitg/coding-agent-loop/workspace/security"
)

func TestConfineProviderSetupLeavesTheServerAccountAlone(t *testing.T) {
	cmd := exec.CommandContext(context.Background(), "/bin/echo", "hi")
	release, err := confineProviderSetup(cmd, "codex-cli", nil, false)
	if err != nil {
		t.Fatal(err)
	}
	release()
	if len(cmd.Args) != 2 || cmd.Args[0] != "/bin/echo" {
		t.Fatalf("the server account's terminal was rewritten: %v", cmd.Args)
	}
}

// Where the host cannot confine, a multi-user server refuses a personal account's terminal instead of
// running it open; a single-user install keeps working.
func TestConfineProviderSetupRefusesAnOpenTerminalOnAMultiUserHost(t *testing.T) {
	if _, available := security.CLILandlockRunner(); available {
		t.Skip("this host can confine; the refusal path needs a host without Landlock")
	}
	env := []string{"HOME=" + t.TempDir()}
	t.Setenv("MULTI_USER_MODE", "true")
	if _, err := confineProviderSetup(exec.CommandContext(context.Background(), "/bin/echo", "hi"), "pi-cli", env, true); err == nil {
		t.Fatal("a multi-user host started a personal account's terminal unconfined")
	}
	t.Setenv("MULTI_USER_MODE", "")
	if _, err := confineProviderSetup(exec.CommandContext(context.Background(), "/bin/echo", "hi"), "pi-cli", env, true); err != nil {
		t.Fatalf("single-user install broke: %v", err)
	}
}
