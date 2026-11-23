package shelllaunch

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The tmux server is long-lived and outlives backend restarts, so a pane can
// inherit variables that are NOT in the current Go process environment. Any
// scrubbing derived from os.Environ() therefore misses exactly the drifted
// keys it most needs to remove. This drives a REAL tmux server where the
// leaked credential exists ONLY in tmux's environment, which is the case the
// generated-script tests structurally cannot reach: they supply the key
// themselves.
func TestTmuxServerDriftedCredentialIsScrubbedAtTheBoundary(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is required for this boundary test")
	}
	t.Setenv("MLP_SHELLLAUNCH_DIRECT", "1")

	// The drifted key must not exist in this process, or the test would pass
	// for the wrong reason.
	const drifted = "SECRET_DRIFTED_FROM_TMUX"
	if _, present := os.LookupEnv(drifted); present {
		t.Fatalf("%s must not be set in the test process", drifted)
	}

	dir := t.TempDir()
	// macOS caps unix socket paths near 104 bytes and t.TempDir() is already
	// long, so the socket lives in a short directory of its own.
	sockDir, err := os.MkdirTemp("/tmp", "mlp")
	if err != nil {
		t.Fatalf("socket dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sockDir) })
	socket := filepath.Join(sockDir, "s")
	outPath := filepath.Join(dir, "env.txt")
	session := "mlp-drift-test"

	tmux := func(args ...string) ([]byte, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		return exec.CommandContext(ctx, "tmux", append([]string{"-S", socket}, args...)...).CombinedOutput()
	}
	t.Cleanup(func() { _, _ = tmux("kill-server") })

	// Seed the credential into the SERVER environment only.
	if out, err := tmux("new-session", "-d", "-s", "seed", "sh"); err != nil {
		t.Fatalf("start tmux server: %v: %s", err, out)
	}
	if out, err := tmux("setenv", "-g", drifted, "leaked-from-tmux"); err != nil {
		t.Fatalf("seed tmux server env: %v: %s", err, out)
	}

	command, cleanup, err2 := CommandWithScopedEnv(
		[]string{"sh", "-c", "env > " + outPath},
		dir,
		[]string{"SECRET_MINE=declared"},
		// The caller cannot enumerate the drifted key: it is not in its own
		// environment. This is the whole point -- the scrub below has to find
		// it by pattern at exec time.
		nil,
		&ScopeScrub{
			Prefixes: []string{"SECRET_", "VAR_"},
			Names:    []string{"MCP_API_TOKEN", "MCP_AUTH", "MCP_SESSION_ID"},
			Keep:     []string{"SECRET_MINE"},
		},
	)
	if err2 != nil {
		t.Fatalf("CommandWithScopedEnv: %v", err2)
	}
	defer cleanup()

	if out, err := tmux("new-session", "-d", "-s", session, command); err != nil {
		t.Fatalf("start pane: %v: %s", err, out)
	}

	deadline := time.Now().Add(20 * time.Second)
	var body []byte
	for time.Now().Before(deadline) {
		if body, err = os.ReadFile(outPath); err == nil && len(body) > 0 {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if len(body) == 0 {
		t.Fatalf("pane never wrote its environment to %s", outPath)
	}
	env := string(body)

	if !strings.Contains(env, "SECRET_MINE=declared") {
		t.Fatalf("declared value never reached the pane:\n%s", env)
	}
	if strings.Contains(env, drifted) {
		t.Fatalf("a credential that exists ONLY in the tmux server environment reached the child:\n%s", env)
	}
}
