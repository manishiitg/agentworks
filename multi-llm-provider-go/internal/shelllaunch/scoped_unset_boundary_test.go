package shelllaunch

import (
	"os"
	"strings"
	"testing"
)

// The unset has to survive the login shell. launchScriptWithFinalEnv runs the
// unsets INSIDE the interactive shell invocation rather than before it, so a
// shell profile cannot restore an ambient credential the caller removed. This
// asserts the generated script rather than the plan that fed it -- the script
// is what the tmux pane actually executes.
func TestFinalEnvScriptUnsetsAmbientCredentialsAtTheBoundary(t *testing.T) {
	t.Setenv("MLP_SHELLLAUNCH_DIRECT", "")

	command, cleanup, err := CommandWithFinalEnv(
		[]string{"/bin/echo", "hi"},
		t.TempDir(),
		[]string{"SECRET_MINE=declared"},
		[]string{"SECRET_OTHER_TENANT", "MCP_API_TOKEN"},
	)
	if err != nil {
		t.Fatalf("CommandWithFinalEnv: %v", err)
	}
	defer cleanup()

	path := scriptPathFromCommand(t, command)
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read launch script: %v", err)
	}
	script := string(body)

	for _, want := range []string{"unset SECRET_OTHER_TENANT", "unset MCP_API_TOKEN"} {
		if !strings.Contains(script, want) {
			t.Fatalf("launch script never removes the ambient credential (%q):\n%s", want, script)
		}
	}
	// The declared value must not be inlined into the tmux/parent command line.
	if strings.Contains(command, "declared") {
		t.Fatalf("declared secret leaked into the command line: %s", command)
	}
	if !strings.Contains(script, "SECRET_MINE") {
		t.Fatalf("declared secret never reaches the child:\n%s", script)
	}
}

func scriptPathFromCommand(t *testing.T, command string) string {
	t.Helper()
	fields := strings.Fields(command)
	if len(fields) == 0 {
		t.Fatalf("empty command")
	}
	path := strings.Trim(fields[len(fields)-1], `'"`)
	if !strings.Contains(path, "mlp-coding-agent-launch-") {
		t.Fatalf("expected a generated launch script, got %q", command)
	}
	return path
}
