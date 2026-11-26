package shelllaunch

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestManagedCLISelectionSurvivesShellPATHReset(t *testing.T) {
	root := t.TempDir()
	managed := filepath.Join(root, "managed bin")
	if err := os.MkdirAll(managed, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"codex", "claude", "pi", "cursor-agent", "agent"} {
		if err := os.WriteFile(filepath.Join(managed, name), []byte("#!/bin/sh\nprintf '%s' \"managed:$AGENTWORKS_CLI_SESSION_KEY\"\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	// A login-shell stand-in resets PATH just as real shell init files can.
	shell := filepath.Join(root, "bash")
	if err := os.WriteFile(shell, []byte("#!/bin/sh\nexport PATH=/usr/bin:/bin\nshift\nexec /bin/sh -c \"$@\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvShellPath, shell)
	t.Setenv(EnvShellMode, "")
	t.Setenv("AGENTWORKS_MANAGED_CLI_BIN", managed)
	for _, name := range []string{"codex", "claude", "pi", "cursor-agent", "agent"} {
		for _, mode := range []string{"command", "env", "final", "direct"} {
			t.Run(name+"/"+mode, func(t *testing.T) {
				var command string
				var cleanup func()
				var err error
				switch mode {
				case "command":
					command = Command([]string{name}, root)
				case "direct":
					command = DirectCommand([]string{name}, root)
				case "env":
					command, cleanup, err = CommandWithEnv([]string{name}, root, []string{"AGENTWORKS_CLI_SESSION_KEY=from-call"})
				case "final":
					command, cleanup, err = CommandWithFinalEnv([]string{name}, root, []string{"AGENTWORKS_CLI_SESSION_KEY=from-call"}, nil)
				}
				if err != nil {
					t.Fatal(err)
				}
				if cleanup != nil {
					defer cleanup()
				}
				out, err := exec.CommandContext(context.Background(), "/bin/sh", "-c", command).CombinedOutput()
				want := "managed:"
				if mode == "env" || mode == "final" {
					want += "from-call"
				}
				if err != nil || string(out) != want {
					t.Fatalf("%s: %v (%s)", out, err, command)
				}
			})
		}
	}
	args := []string{"codex", "--version"}
	_ = managedCLIArgs(args)
	if args[0] != "codex" {
		t.Fatal("mutated caller args")
	}
	if got := managedCLIArgs([]string{"/explicit/codex"}); got[0] != "/explicit/codex" {
		t.Fatal(got)
	}
	t.Setenv("AGENTWORKS_MANAGED_CLI_BIN", "")
	if strings.Contains(Command([]string{"codex"}, root), managed) {
		t.Fatal("managed routing active without opt-in")
	}
}
