package cursorcli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// PLAT-856-145: generated policy/instructions must remain private, with the
// account's pre-existing deny rules intact and no writes to the person's cwd.
func TestPrivateCursorRuntimePreservesProjectAndAccountPolicy(t *testing.T) {
	project, private, home := t.TempDir(), t.TempDir(), t.TempDir()
	originals := map[string]string{"AGENTS.md": "project instructions", ".cursor/cli.json": `{"permissions":{"allow":["Shell(*)"]}}`, ".cursor/rules/own.mdc": "project rule"}
	for name, data := range originals {
		p := filepath.Join(project, name)
		if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	account := filepath.Join(home, ".cursor")
	if err := os.MkdirAll(account, 0700); err != nil {
		t.Fatal(err)
	}
	originalConfig := `{"version":1,"permissions":{"deny":["Shell(private-command)"],"allow":[]}}`
	if err := os.WriteFile(filepath.Join(account, "cli-config.json"), []byte(originalConfig), 0600); err != nil {
		t.Fatal(err)
	}
	opts := &llmtypes.CallOptions{}
	llmtypes.WithCLIRuntimeDir(private)(opts)
	WithDenyBuiltinTools(true)(opts)
	cleanup, err := prepareCursorProjectFiles(llmtypes.CLIArtifactDirectory(opts, project), "private system instruction", opts, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	configDir, err := prepareCursorPrivateRuntime(opts, []string{"HOME=" + home})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(configDir, "cli-config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Permissions struct {
			Deny []string `json:"deny"`
		} `json:"permissions"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	want := append([]string{"Shell(private-command)"}, cursorNativeDenyPermissions(false, true)...)
	if !reflect.DeepEqual(cfg.Permissions.Deny, want) {
		t.Fatalf("private policy lost deny rules: %v", cfg.Permissions.Deny)
	}
	for name, data := range originals {
		got, err := os.ReadFile(filepath.Join(project, name))
		if err != nil || string(got) != data {
			t.Fatalf("project file changed: %s", name)
		}
	}
	got, err := os.ReadFile(filepath.Join(account, "cli-config.json"))
	if err != nil || string(got) != originalConfig {
		t.Fatal("changed account configuration")
	}
	if _, err := os.Stat(filepath.Join(private, ".cursor-plugin", "plugin.json")); err != nil {
		t.Fatal(err)
	}
	// PLAT-856-145 review: changing selected HOME must not inherit credentials,
	// history, or XDG configuration from the previous/ambient account.
	if _, err := os.Stat(filepath.Join(private, ".git")); !os.IsNotExist(err) {
		t.Fatal("private runtime was git-initialized")
	}
	if err := os.WriteFile(filepath.Join(account, "auth.json"), []byte("fixture-A"), 0600); err != nil {
		t.Fatal(err)
	}
	configA, err := prepareCursorPrivateRuntime(opts, []string{"HOME=" + home, "XDG_CONFIG_HOME=" + t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if raw, err := os.ReadFile(filepath.Join(configA, "auth.json")); err != nil || string(raw) != "fixture-A" {
		t.Fatal("selected account overridden", err)
	}
	// Parallel launches of the same selected account must never unlink its
	// authentication in between another launch's configuration and read.
	var launches sync.WaitGroup
	results := make(chan error, 24)
	for i := 0; i < 24; i++ {
		launches.Add(1)
		go func() {
			defer launches.Done()
			dir, err := prepareCursorPrivateRuntime(opts, []string{"HOME=" + home})
			if err == nil {
				var raw []byte
				raw, err = os.ReadFile(filepath.Join(dir, "auth.json"))
				if err == nil && string(raw) != "fixture-A" {
					err = fmt.Errorf("selected authentication changed during parallel launch")
				}
			}
			results <- err
		}()
	}
	launches.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}

	if err := os.MkdirAll(filepath.Join(configA, "acp-sessions"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configA, "acp-sessions", "record-A"), []byte("fixture history"), 0600); err != nil {
		t.Fatal(err)
	}
	homeB := t.TempDir()
	configB, err := prepareCursorPrivateRuntime(opts, []string{"HOME=" + homeB, "XDG_CONFIG_HOME=" + filepath.Dir(account)})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"auth.json", "acp-sessions/record-A"} {
		if _, err := os.Stat(filepath.Join(configB, name)); !os.IsNotExist(err) {
			t.Fatalf("old account visible in B: %s %v", name, err)
		}
	}
	// macOS temporary directories can use /var while the link resolves under
	// /private/var. Compare canonical directories, not those OS aliases.
	canonicalB, err := filepath.EvalSymlinks(configB)
	if err != nil {
		t.Fatal(err)
	}
	if current, err := filepath.EvalSymlinks(filepath.Join(private, "cursor-config")); err != nil || current != canonicalB {
		t.Fatal("transcript account not updated", err)
	}
	if _, err := os.Stat(filepath.Join(configA, "acp-sessions", "record-A")); err != nil {
		t.Fatal("old history deleted", err)
	}
	if err := os.Remove(filepath.Join(private, ".cursor", "cli.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareCursorPrivateRuntime(opts, []string{"HOME=" + homeB, "CURSOR_CONFIG_DIR=" + account}); err != nil {
		t.Fatalf("nonbridge runtime without generated permissions: %v", err)
	}

}

// PLAT-872-55 T1/T5: terminal launch must preserve account and deny-tool policy.
func TestCursorTerminalLaunchCannotBorrowServerKeyOrBypassDenyHooks(t *testing.T) {
	opts := &llmtypes.CallOptions{}
	WithWorkingDir(t.TempDir())(opts)
	llmtypes.WithCLIRuntimeDir(t.TempDir())(opts)
	llmtypes.WithProviderAccountEnvironment(map[string]string{"HOME": t.TempDir()})(opts)
	WithDenyBuiltinTools(true)(opts)
	WithForce()(opts)
	c := NewCursorCLIAdapter("server-test-key", "composer-2.5", &MockLogger{})
	args, env, _, cleanup, err := c.buildCursorInteractiveLaunch(opts, "", "owner")
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if strings.Contains(strings.Join(env, "\n"), "server-test-key") {
		t.Fatal("file login inherited server key")
	}
	for _, arg := range args {
		if arg == "--force" {
			t.Fatal("deny hooks bypassed by force")
		}
	}
	llmtypes.WithProviderAccountCredentials(map[string]string{"CURSOR_API_KEY": "account-test-key"})(opts)
	_, env, _, cleanup2, err := c.buildCursorInteractiveLaunch(opts, "", "owner")
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup2()
	if !strings.Contains(strings.Join(env, "\n"), "CURSOR_API_KEY=account-test-key") {
		t.Fatal("selected key missing")
	}
}

// PLAT-872-57: a Crew bound to a person's folder keeps its MCP config in the private runtime, not cwd.
// The sandbox must grant the MCP command named there, or AgentWorks tools fail to start.
func TestCursorSandboxGrantsMCPCommandFromPrivateRuntime(t *testing.T) {
	project, private := t.TempDir(), t.TempDir()
	command := filepath.Join(t.TempDir(), "mcpbridge")
	if err := os.WriteFile(command, []byte("#!/bin/sh\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(private, ".cursor"), 0700); err != nil {
		t.Fatal(err)
	}
	config := `{"mcpServers":{"agentworks":{"command":"` + command + `"}}}`
	if err := os.WriteFile(filepath.Join(private, ".cursor", "mcp.json"), []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	opts := &llmtypes.CallOptions{}
	llmtypes.WithCLIRuntimeDir(private)(opts)
	reads := cursorLandlockReads(opts, nil, project)
	for _, read := range reads {
		if read == command {
			return
		}
	}
	t.Fatalf("MCP command %s not granted; reads=%v", command, reads)
}
