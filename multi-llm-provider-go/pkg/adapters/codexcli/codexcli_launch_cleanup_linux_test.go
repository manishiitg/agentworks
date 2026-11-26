//go:build linux

package codexcli

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/internal/clisandbox"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func TestCodexDetachedLaunchKeepsPolicyUntilPaneStarts(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TMPDIR", root)
	t.Setenv("HOME", root)
	t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TMUX_TEST_LAUNCH", filepath.Join(root, "launch.sh"))
	// tmux acknowledges the detached session before its pane executes. Keep
	// the captured command unexecuted until startCodexTmuxSession returns.
	fakeTmux := `#!/bin/sh
case " $* " in *" new-session "*)
  for arg do last="$arg"; done
  printf '%s\n' "$last" > "$TMUX_TEST_LAUNCH"
esac
exit 0
`
	fakeRunner := `#!/bin/sh
test -r "$2" || { echo "missing launch policy"; exit 125; }
rm "$2"
shift 3
exec "$@"
`
	for name, source := range map[string]string{"tmux": fakeTmux, "runner": fakeRunner} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(source), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	policy := &llmtypes.CLISecurityPolicy{Mode: llmtypes.CLISecurityModeIsolated, Provider: "codex-cli", LandlockRunner: filepath.Join(root, "runner"), PrivateHome: filepath.Join(root, "home"), CredentialHome: root}
	cleanup, err := startCodexTmuxSession(context.Background(), "delayed-pane", []string{"/bin/sh", "-c", "printf launch-ok"}, root, policy, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	command, err := os.ReadFile(filepath.Join(root, "launch.sh"))
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("/bin/sh", "-c", string(command)).Output()
	if err != nil || strings.TrimSpace(string(out)) != "launch-ok" {
		t.Fatalf("detached pane could not consume its policy after startup returned: %s (%v)", out, err)
	}
}

func TestCodexFailedDetachedLaunchRemovesPolicy(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TMPDIR", root)
	t.Setenv("HOME", root)
	t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
	for name, source := range map[string]string{"tmux": "#!/bin/sh\nexit 1\n", "runner": "#!/bin/sh\nexit 0\n"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(source), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	policy := &llmtypes.CLISecurityPolicy{Mode: llmtypes.CLISecurityModeIsolated, Provider: "codex-cli", LandlockRunner: filepath.Join(root, "runner"), PrivateHome: filepath.Join(root, "home"), CredentialHome: root}
	_, err := startCodexTmuxSession(context.Background(), "failed-pane", []string{"codex"}, root, policy, nil, nil, nil, nil)
	if err == nil {
		t.Fatal("failed launch returned success")
	}
	files, err := filepath.Glob(filepath.Join(root, "agentworks-cli-landlock-*.json"))
	if err != nil || len(files) != 0 {
		t.Fatalf("failed launch leaked policy files: %v (%v)", files, err)
	}
}

// PLAT-872-57: a real app-server must discover an MCP executable supplied
// after launch in thread config, outside its workspace and private home.
// This uses installed Codex, the production bridge and Landlock, without
// model inference, credentials, or a running platform server.
func TestCodexAppServerConfinedBridgeDiscoveryLive(t *testing.T) {
	bridgeSource := os.Getenv("CODING_TEST_MCPBRIDGE")
	runner := os.Getenv("CODING_TEST_LANDLOCK_RUNNER")
	if bridgeSource == "" || runner == "" {
		t.Skip("set CODING_TEST_MCPBRIDGE and CODING_TEST_LANDLOCK_RUNNER")
	}
	if _, err := exec.LookPath("codex"); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	cwd := filepath.Join(root, "workspace")
	outside := filepath.Join(root, "release")
	for _, dir := range []string{cwd, outside, filepath.Join(root, "home", ".codex")} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(bridgeSource)
	if err != nil {
		t.Fatal(err)
	}
	bridge := filepath.Join(outside, "mcpbridge")
	if err = os.WriteFile(bridge, data, 0700); err != nil {
		t.Fatal(err)
	}
	// Exercise the release symlink used by the deployed bridge configuration.
	link := filepath.Join(root, "current-bridge")
	if err = os.Symlink(bridge, link); err != nil {
		t.Fatal(err)
	}
	toolsFile := filepath.Join(cwd, "tools.json")
	if err = os.WriteFile(toolsFile, []byte(`[{"name":"schedules","description":"List fixture schedules","input_schema":{"type":"object"},"type":"virtual"}]`), 0600); err != nil {
		t.Fatal(err)
	}
	servers, err := json.Marshal(map[string]any{"api-bridge": map[string]any{"command": link, "env": map[string]string{"MCP_API_URL": "http://127.0.0.1:1", "MCP_API_TOKEN": "fixture-not-a-credential", "MCP_TOOLS_FILE": toolsFile}}})
	if err != nil {
		t.Fatal(err)
	}
	opts := &llmtypes.CallOptions{CLISecurity: &llmtypes.CLISecurityPolicy{Mode: llmtypes.CLISecurityModeIsolated, Provider: "codex-cli", PrivateHome: filepath.Join(root, "home"), CredentialHome: filepath.Join(root, "empty-account"), LandlockRunner: runner}}
	WithMCPServers(string(servers))(opts)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	rpc, err := NewCodexCLIAdapter("", "", &MockLogger{}).launchAppServer(ctx, opts, cwd)
	if err != nil {
		t.Fatal(err)
	}
	defer rpc.close()
	config, err := codexAppThreadConfig(opts, "never")
	if err != nil {
		t.Fatal(err)
	}
	var started struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	if err = rpc.call(ctx, "thread/start", map[string]any{"cwd": cwd, "sandbox": "danger-full-access", "approvalPolicy": "never", "config": config}, &started); err != nil {
		t.Fatal(err)
	}
	var inventory struct {
		Data []struct {
			Name          string                     `json:"name"`
			RuntimeStatus string                     `json:"runtimeStatus"`
			Tools         map[string]json.RawMessage `json:"tools"`
			ToolsError    string                     `json:"toolsError"`
		} `json:"data"`
	}
	for {
		if err = rpc.call(ctx, "mcpServerStatus/list", map[string]any{"threadId": started.Thread.ID, "serverName": "api-bridge"}, &inventory); err != nil {
			t.Fatal(err)
		}
		ready := false
		for _, server := range inventory.Data {
			if server.Name != "api-bridge" {
				continue
			}
			if server.RuntimeStatus == "failed" || server.ToolsError != "" {
				t.Fatalf("bridge discovery failed: status=%s error=%s", server.RuntimeStatus, server.ToolsError)
			}
			for name := range server.Tools {
				if strings.Contains(name, "schedules") {
					t.Logf("real confined MCP catalog: %s", name)
					ready = true
				}
			}
		}
		if ready {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatalf("bridge absent from native MCP catalog: %+v", inventory)
		case <-time.After(100 * time.Millisecond):
		}
	}
	// The grant is the executable file, not the containing release directory.
	sibling := filepath.Join(outside, "unrelated.txt")
	if err = os.WriteFile(sibling, []byte("not granted"), 0600); err != nil {
		t.Fatal(err)
	}
	reads, err := strictCodexMCPRuntimePaths(opts)
	if err != nil {
		t.Fatal(err)
	}
	probe := exec.CommandContext(ctx, "/bin/sh", "-c", `cat "$BRIDGE_EXECUTABLE" >/dev/null && ! cat "$UNRELATED_FILE" >/dev/null 2>&1`)
	probe.Env = []string{"PATH=/usr/bin:/bin", "BRIDGE_EXECUTABLE=" + bridge, "UNRELATED_FILE=" + sibling}
	cleanup, err := clisandbox.LandlockCmd(opts.CLISecurity, probe, cwd, reads, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if output, err := probe.CombinedOutput(); err != nil {
		t.Fatalf("executable-only boundary failed: %s (%v)", output, err)
	}
	t.Log("bridge executable readable; adjacent unrelated file remains denied")
}
