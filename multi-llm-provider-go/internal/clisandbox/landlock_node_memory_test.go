//go:build linux

package clisandbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// next build failed in every sandboxed Crew: a node started inside the lock cannot read its /proc/self, so
// process.memoryUsage() threw EACCES. Every confined launch preloads the shim, after the NODE_OPTIONS it already has.
func TestConfinedLaunchPreloadsNodeMemoryShim(t *testing.T) {
	dir := t.TempDir()
	runner := filepath.Join(dir, "runner")
	if err := os.WriteFile(runner, nil, 0o700); err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(dir, "home")
	policy := &llmtypes.CLISecurityPolicy{Mode: llmtypes.CLISecurityModeIsolated, Provider: "muse-cli", LandlockRunner: runner, PrivateHome: home, CredentialHome: dir}
	args, cleanup, err := landlockArgs(policy, []string{"sh"}, dir, nil, nil, []string{"NODE_OPTIONS=--max-old-space-size=4096"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	shim := filepath.Join(canonical(home), ".agentworks-node-memory.js")
	want := "NODE_OPTIONS=--max-old-space-size=4096 --require=" + shim
	found := false
	for i, arg := range args {
		if arg == want {
			found = i < len(args)-1 && args[len(args)-1] == "sh"
		}
	}
	if !found {
		t.Fatalf("launch args %q lack %q before the command", args, want)
	}
	if body, err := os.ReadFile(shim); err != nil || !strings.Contains(string(body), "process.memoryUsage = memoryUsage") {
		t.Fatalf("shim not written: %v", err)
	}
}
