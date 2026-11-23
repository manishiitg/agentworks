package cursorcli

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// TestWriteCursorDenyBuiltinHooksLifecycle covers the unit-level invariants
// of the deny-builtin hooks installer: the .cursor/hooks.json + deny script
// are written with the right content, the script is executable, cleanup
// restores any prior hooks.json the operator had, and cleanup removes the
// dir tree we created.
func TestWriteCursorDenyBuiltinHooksLifecycle(t *testing.T) {
	tmp := t.TempDir()
	cursorDir := filepath.Join(tmp, ".cursor")

	cleanup, err := writeCursorDenyBuiltinHooks(cursorDir)
	if err != nil {
		t.Fatalf("writeCursorDenyBuiltinHooks: %v", err)
	}

	// hooks.json content + structure
	hooksPath := filepath.Join(cursorDir, "hooks.json")
	raw, err := os.ReadFile(hooksPath)
	if err != nil {
		t.Fatalf("expected hooks.json at %s: %v", hooksPath, err)
	}
	var parsed struct {
		Version int                                 `json:"version"`
		Hooks   map[string][]map[string]interface{} `json:"hooks"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("hooks.json must be valid JSON: %v\ncontent:\n%s", err, raw)
	}
	if parsed.Version != 1 {
		t.Errorf("hooks.json version = %d, want 1", parsed.Version)
	}
	for _, ev := range []string{"preToolUse", "beforeShellExecution", "beforeReadFile"} {
		hooks, ok := parsed.Hooks[ev]
		if !ok || len(hooks) == 0 {
			t.Errorf("hooks.json missing %q event entry", ev)
			continue
		}
		cmd, _ := hooks[0]["command"].(string)
		if !strings.Contains(cmd, "mlp-deny-builtin.sh") {
			t.Errorf("hook %q command should reference mlp-deny-builtin.sh, got %q", ev, cmd)
		}
	}
	matcher, _ := parsed.Hooks["preToolUse"][0]["matcher"].(string)
	for _, tool := range []string{"Read", "ListDir", "Glob", "Grep", "Search", "Task", "Agent", "Subagent", "BackgroundAgent"} {
		if !strings.Contains(matcher, tool) {
			t.Fatalf("preToolUse matcher = %q, want built-in tool %s covered", matcher, tool)
		}
	}

	// deny script content + executability
	scriptPath := filepath.Join(cursorDir, "hooks", "mlp-deny-builtin.sh")
	scriptRaw, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatalf("expected deny script at %s: %v", scriptPath, err)
	}
	if !strings.Contains(string(scriptRaw), `"permission":"deny"`) {
		t.Errorf("deny script should emit permission=deny JSON; got:\n%s", scriptRaw)
	}
	if !strings.Contains(string(scriptRaw), "api-bridge") {
		t.Errorf("deny script user_message should point at api-bridge; got:\n%s", scriptRaw)
	}
	if !strings.Contains(string(scriptRaw), "delegation") || !strings.Contains(string(scriptRaw), "subagents") {
		t.Errorf("deny script should name delegation/subagent blocking; got:\n%s", scriptRaw)
	}
	info, err := os.Stat(scriptPath)
	if err != nil {
		t.Fatalf("stat deny script: %v", err)
	}
	if mode := info.Mode().Perm(); mode&0o100 == 0 {
		t.Errorf("deny script must be owner-executable; mode=%o", mode)
	}
	cmd := exec.CommandContext(context.Background(), "bash", scriptPath)
	cmd.Stdin = strings.NewReader(`{"tool_name":"Read","tool_input":{"path":"/tmp/secret.txt"}}`)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("deny script execution error = %v output=%q", err, string(output))
	}
	var decision struct {
		Permission   string `json:"permission"`
		UserMessage  string `json:"user_message"`
		AgentMessage string `json:"agent_message"`
	}
	if err := json.Unmarshal(output, &decision); err != nil {
		t.Fatalf("deny script output must be valid JSON: %v output=%q", err, string(output))
	}
	if decision.Permission != "deny" || !strings.Contains(decision.AgentMessage, "api-bridge") {
		t.Fatalf("deny script decision = %#v, want permission deny with bridge guidance", decision)
	}
	logPath := filepath.Join(cursorDir, "hooks", "mlp-deny-builtin-denials.jsonl")
	logBody, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("deny script should log hook stdin to %s: %v", logPath, err)
	}
	if !json.Valid([]byte(strings.TrimSpace(string(logBody)))) || !strings.Contains(string(logBody), "Read") {
		t.Fatalf("deny script log = %q, want valid JSON line containing Read", string(logBody))
	}

	// Cleanup removes what we wrote and leaves the temp dir empty.
	cleanup()
	if _, err := os.Stat(hooksPath); !os.IsNotExist(err) {
		t.Errorf("cleanup should have removed hooks.json; stat err=%v", err)
	}
	if _, err := os.Stat(scriptPath); !os.IsNotExist(err) {
		t.Errorf("cleanup should have removed deny script; stat err=%v", err)
	}
	if entries, _ := os.ReadDir(tmp); len(entries) > 0 {
		t.Errorf("cleanup should have removed .cursor dir; remaining entries=%v", entries)
	}
}

// TestWriteCursorDenyBuiltinHooksRestoresPreExistingHooksJSON guards the
// promise the comment makes: if the operator already had .cursor/hooks.json
// in their workspace, cleanup must restore it byte-for-byte instead of
// removing it. Without this guard the adapter would silently destroy
// user-owned hook config every time the option was enabled.
func TestWriteCursorDenyBuiltinHooksRestoresPreExistingHooksJSON(t *testing.T) {
	tmp := t.TempDir()
	cursorDir := filepath.Join(tmp, ".cursor")
	if err := os.MkdirAll(cursorDir, 0o755); err != nil {
		t.Fatalf("mkdir cursor dir: %v", err)
	}
	preExisting := []byte(`{"version":1,"hooks":{"sessionStart":[{"command":"./user-hook.sh"}]}}`)
	hooksPath := filepath.Join(cursorDir, "hooks.json")
	if err := os.WriteFile(hooksPath, preExisting, 0o600); err != nil {
		t.Fatalf("seed pre-existing hooks.json: %v", err)
	}

	cleanup, err := writeCursorDenyBuiltinHooks(cursorDir)
	if err != nil {
		t.Fatalf("writeCursorDenyBuiltinHooks with pre-existing config: %v", err)
	}
	// Mid-session our deny config is active.
	active, _ := os.ReadFile(hooksPath)
	if strings.Contains(string(active), "user-hook.sh") {
		t.Fatal("mid-session, pre-existing user-hook content should not be visible — our deny config must be installed")
	}

	cleanup()
	restored, err := os.ReadFile(hooksPath)
	if err != nil {
		t.Fatalf("after cleanup, hooks.json should exist (restored from pre-existing): %v", err)
	}
	if string(restored) != string(preExisting) {
		t.Errorf("cleanup must restore pre-existing hooks.json byte-for-byte\n  want: %q\n  got:  %q", preExisting, restored)
	}
}

func TestPrepareCursorProjectFilesDenyBuiltinRestoresProjectPermissions(t *testing.T) {
	workDir := t.TempDir()
	cursorDir := filepath.Join(workDir, ".cursor")
	if err := os.MkdirAll(cursorDir, 0o755); err != nil {
		t.Fatal(err)
	}
	staleCLI := filepath.Join(cursorDir, "cli.json")
	const staleBody = `{"permissions":{"allow":[],"deny":["Shell(*)"]}}`
	if err := os.WriteFile(staleCLI, []byte(staleBody), 0o600); err != nil {
		t.Fatal(err)
	}

	opts := &llmtypes.CallOptions{}
	WithDenyBuiltinTools(true)(opts)

	cleanup, err := prepareCursorProjectFiles(workDir, "Original system prompt.", opts, "test-session-cursor")
	if err != nil {
		t.Fatalf("prepareCursorProjectFiles: %v", err)
	}
	defer cleanup()

	// PLAT-872-21: ACP may bypass hooks; native permissions enforce the same rule.
	if got, err := os.ReadFile(staleCLI); err != nil || !strings.Contains(string(got), `"Write(**)"`) || !strings.Contains(string(got), `"Read(**)"`) {
		t.Fatalf("native denial not installed: %s %v", got, err)
	}
	t.Cleanup(func() {
		cleanup()
		got, err := os.ReadFile(staleCLI)
		if err != nil || string(got) != staleBody {
			t.Fatalf("original permissions not restored: %s %v", got, err)
		}
	})

	if _, err := os.Stat(filepath.Join(workDir, ".cursor", "hooks.json")); err != nil {
		t.Fatalf("deny-builtin mode should install hooks.json: %v", err)
	}

	ruleBody, err := os.ReadFile(filepath.Join(workDir, ".cursor", "rules", "mlp-system.mdc"))
	if err != nil {
		t.Fatalf("read generated system rule: %v", err)
	}
	for _, want := range []string{
		"Original system prompt.",
		"Do not start Cursor subagents",
		"Complete the task in this same Cursor session",
		"delegation tools are intentionally denied",
	} {
		if !strings.Contains(string(ruleBody), want) {
			t.Fatalf("mlp-system.mdc = %s, want %q", string(ruleBody), want)
		}
	}
}

func TestPrepareCursorProjectFilesCreatesRealGitRootForNestedWorkspace(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not available: %v", err)
	}
	parent := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(ctx, "git", "-C", parent, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("init parent git repo: %v output=%s", err, string(out))
	}
	workDir := filepath.Join(parent, "workspace-docs", "_users", "default", "Chats")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatal(err)
	}

	before, err := exec.CommandContext(ctx, "git", "-C", workDir, "rev-parse", "--show-toplevel").CombinedOutput()
	if err != nil {
		t.Fatalf("expected nested dir to see parent git before prepare: %v output=%s", err, string(before))
	}
	if cursorTestRealPath(strings.TrimSpace(string(before))) != cursorTestRealPath(parent) {
		t.Fatalf("before prepare git root = %q, want parent %q", strings.TrimSpace(string(before)), parent)
	}

	opts := &llmtypes.CallOptions{}
	WithDenyBuiltinTools(true)(opts)
	cleanup, err := prepareCursorProjectFiles(workDir, "Original system prompt.", opts, "test-session-nested-git")
	if err != nil {
		t.Fatalf("prepareCursorProjectFiles: %v", err)
	}

	after, err := exec.CommandContext(ctx, "git", "-C", workDir, "rev-parse", "--show-toplevel").CombinedOutput()
	if err != nil {
		t.Fatalf("expected prepared dir to be its own git root: %v output=%s", err, string(after))
	}
	if cursorTestRealPath(strings.TrimSpace(string(after))) != cursorTestRealPath(workDir) {
		t.Fatalf("after prepare git root = %q, want workflow dir %q", strings.TrimSpace(string(after)), workDir)
	}

	cleanup()
	if _, err := os.Stat(filepath.Join(workDir, ".git")); !os.IsNotExist(err) {
		t.Fatalf("cleanup should remove temporary git marker, stat err=%v", err)
	}
}

func cursorTestRealPath(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved
	}
	return abs
}
