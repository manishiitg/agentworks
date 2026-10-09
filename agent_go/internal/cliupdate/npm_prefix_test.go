package cliupdate

import (
	"os"
	"path/filepath"
	"testing"
)

// Regression: "codex update" installed into Node's own prefix, shadowing the managed CLI on PATH and failing
// the next deploy. A self-update must stay in the prefix the CLI came from.
func TestNPMPrefixEnvKeepsSelfUpdateInTheCLIPrefix(t *testing.T) {
	prefix := t.TempDir()
	if err := os.MkdirAll(filepath.Join(prefix, "lib", "node_modules"), 0o755); err != nil {
		t.Fatal(err)
	}
	got := npmPrefixEnv(filepath.Join(prefix, "bin", "codex"))
	if len(got) != 1 || got[0] != "npm_config_prefix="+prefix {
		t.Fatalf("npmPrefixEnv = %v", got)
	}
	if got := npmPrefixEnv(filepath.Join(t.TempDir(), "bin", "claude")); got != nil {
		t.Fatalf("a non-npm install got a prefix: %v", got)
	}
}
