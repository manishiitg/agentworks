package clisandbox

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/internal/shelllaunch"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// Codex sat outside the isolation guarantee: compatibility mode -- the default
// -- handed the child a plain command with full ambient inheritance. These run
// the REAL prepared command and read the environment the child actually got,
// rather than asserting on the command string.
func TestCodexCompatibilityLaunchAppliesTheDeclaredScope(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "env.txt")

	t.Setenv("SECRET_OTHER_TENANT", "leaked")
	t.Setenv("MCP_API_TOKEN", "another-sessions-token")
	t.Setenv("MCP_API_URL", "http://bridge.local")

	scrub := &shelllaunch.ScopeScrub{
		Prefixes: llmtypes.ScopedCredentialPrefixes(),
		Names:    llmtypes.ScopedCredentialNames(),
		Keep:     []string{"SECRET_MINE"},
	}
	command, cleanup, err := PrepareCodexCommandScoped(
		nil, // compatibility mode
		[]string{"sh", "-c", "env > " + out},
		dir,
		nil,
		[]string{"SECRET_MINE=declared"},
		nil,
		scrub,
	)
	if err != nil {
		t.Fatalf("PrepareCodexCommandScoped: %v", err)
	}
	defer cleanup()

	env := runAndReadEnv(t, command, out)

	if !strings.Contains(env, "SECRET_MINE=declared") {
		t.Fatalf("declared credential never reached the Codex child:\n%s", env)
	}
	for _, leaked := range []string{"SECRET_OTHER_TENANT", "MCP_API_TOKEN"} {
		if strings.Contains(env, leaked) {
			t.Fatalf("ambient %s reached the Codex child:\n%s", leaked, env)
		}
	}
	// An address grants nothing and dropping it silently breaks the bridge.
	if !strings.Contains(env, "MCP_API_URL=http://bridge.local") {
		t.Fatalf("address route was scrubbed from the Codex child:\n%s", env)
	}
}

// The sandboxed modes already start from env -i, so the risk there is the
// opposite one: the declared scope never being injected at all.
func TestCodexSandboxedLaunchInjectsTheDeclaredScope(t *testing.T) {
	if _, err := exec.LookPath("sandbox-exec"); err != nil {
		t.Skip("sandbox-exec is required")
	}
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh is required")
	}
	dir := t.TempDir()
	out := filepath.Join(dir, "env.txt")

	command, cleanup, err := PrepareCodexCommandScoped(
		&llmtypes.CLISecurityPolicy{Mode: llmtypes.CLISecurityModeVerified, Provider: "codex-cli"},
		[]string{"sh", "-c", "env > " + out},
		dir,
		nil,
		[]string{"SECRET_MINE=declared"},
		nil,
		nil,
	)
	if err != nil {
		t.Skipf("sandboxed Codex launch unavailable here: %v", err)
	}
	defer cleanup()

	env := runAndReadEnv(t, command, out)
	if !strings.Contains(env, "SECRET_MINE=declared") {
		t.Fatalf("declared credential never reached the sandboxed Codex child:\n%s", env)
	}
}

func runAndReadEnv(t *testing.T, command, outPath string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(ctx, "/bin/sh", "-c", command).CombinedOutput(); err != nil {
		t.Fatalf("run prepared command: %v: %s", err, out)
	}
	body, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("child never wrote its environment: %v", err)
	}
	return string(body)
}
