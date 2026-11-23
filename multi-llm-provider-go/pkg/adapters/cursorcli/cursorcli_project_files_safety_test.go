package cursorcli

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func cursorSafetyOpts() *llmtypes.CallOptions {
	opts := &llmtypes.CallOptions{}
	WithMCPConfig(`{"mcpServers":{"api-bridge":{"command":"node","args":["server.js"]}}}`)(opts)
	WithDenyBuiltinTools(true)(opts)
	return opts
}

// A project's own .cursor files (MCP servers, CLI permissions, hooks, rules) and
// its real git repository survive a session, and overlapping sessions in one
// folder keep each other's generated files until the last one ends.
func TestCursorSessionsKeepProjectsOwnFilesAndShareGeneratedOnes(t *testing.T) {
	dir := t.TempDir()
	cursorDir := filepath.Join(dir, ".cursor")
	own := map[string]string{
		"mcp.json":           `{"mcpServers":{"github":{"command":"docker"}}}`,
		"cli.json":           `{"permissions":{"allow":["Shell(ls)"],"deny":[]}}`,
		"hooks.json":         `{"version":1,"hooks":{}}`,
		"rules/team.mdc":     "team rule",
		"hooks/own-hook.sh":  "#!/bin/sh\n",
		"commands/deploy.md": "deploy",
	}
	for name, body := range own {
		path := filepath.Join(cursorDir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	first, err := prepareCursorProjectFiles(dir, "prompt A", cursorSafetyOpts(), "sess-a")
	if err != nil {
		t.Fatal(err)
	}
	second, err := prepareCursorProjectFiles(dir, "prompt A", cursorSafetyOpts(), "sess-b")
	if err != nil {
		t.Fatal(err)
	}
	first() // the first session ends while the second is still running
	first()
	if _, err := os.Stat(filepath.Join(cursorDir, "rules", "mlp-system.mdc")); err != nil {
		t.Fatalf("the running session's rules were removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cursorDir, "hooks", "mlp-deny-builtin.sh")); err != nil {
		t.Fatalf("the running session's hook script was removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		t.Fatalf("the running session's git marker was removed: %v", err)
	}
	second()

	for name, want := range own {
		got, err := os.ReadFile(filepath.Join(cursorDir, name))
		if err != nil || string(got) != want {
			t.Fatalf("project's own .cursor/%s = %q err=%v, want it unchanged", name, got, err)
		}
	}
	for _, generated := range []string{filepath.Join(cursorDir, "rules", "mlp-system.mdc"), filepath.Join(cursorDir, "hooks", "mlp-deny-builtin.sh"), filepath.Join(dir, ".git")} {
		if _, err := os.Stat(generated); !os.IsNotExist(err) {
			t.Fatalf("%s left behind: %v", generated, err)
		}
	}
}

// A folder that already is a git repository keeps its .git, whatever a session does.
func TestCursorSessionNeverTouchesARealGitRepo(t *testing.T) {
	dir := t.TempDir()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	if out, err := exec.CommandContext(context.Background(), "git", "-C", dir, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	cleanup, err := prepareCursorProjectFiles(dir, "prompt", cursorSafetyOpts(), "sess-git")
	if err != nil {
		t.Fatal(err)
	}
	cleanup()
	if _, err := os.Stat(filepath.Join(dir, ".git", "HEAD")); err != nil {
		t.Fatalf("the project's .git was damaged: %v", err)
	}
}

// PLAT-872-21: ACP ignores TUI hooks, so a caller config cannot bypass native-tool policy.
func TestCursorCallerConfigStillDeniesNativeTools(t *testing.T) {
	dir := t.TempDir()
	opts := cursorSafetyOpts()
	WithProjectConfig(`{"permissions":{"allow":["Shell(*)"]},"theme":"dark"}`)(opts)
	cleanup, err := prepareCursorProjectFiles(dir, "", opts, t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	raw, err := os.ReadFile(filepath.Join(dir, ".cursor", "cli.json"))
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		Permissions struct{ Deny []string }
		Theme       string
	}
	if err = json.Unmarshal(raw, &config); err != nil {
		t.Fatal(err)
	}
	if len(config.Permissions.Deny) != 3 || config.Theme != "dark" {
		t.Fatalf("policy lost: %s", raw)
	}
}
