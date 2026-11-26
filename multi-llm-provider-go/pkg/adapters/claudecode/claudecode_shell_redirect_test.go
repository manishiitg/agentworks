package claudecode

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

const redirectBridgeConfig = `{"mcpServers":{"api-bridge":{"command":"/bin/true","env":{"MCP_API_URL":"http://127.0.0.1:1"}}}}`

func runClaudeShellRedirectHook(t *testing.T, toolName, payload string) string {
	t.Helper()
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed")
	}
	path, err := claudeWriteShellRedirectHook(toolName)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(context.Background(), "node", path)
	cmd.Stdin = strings.NewReader(payload)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("hook exited with an error: %v", err)
	}
	return string(out)
}

func TestClaudeBridgeShellToolName(t *testing.T) {
	if got := claudeBridgeShellToolName(redirectBridgeConfig); got != "mcp__api-bridge__execute_shell_command" {
		t.Errorf("tool name = %q", got)
	}
	if got := claudeBridgeShellToolName(`{"mcpServers":{"my bridge.x":{"env":{"MCP_API_URL":"u"}}}}`); got != "mcp__my_bridge_x__execute_shell_command" {
		t.Errorf("normalised tool name = %q", got)
	}
	for _, cfg := range []string{``, `not json`, `{"mcpServers":{"other":{"command":"x"}}}`} {
		if got := claudeBridgeShellToolName(cfg); got != "" {
			t.Errorf("config %q gave %q, want no bridge", cfg, got)
		}
	}
}

func TestClaudeShellRedirectHookRefusesOnlyPlatformCallsInNativeBash(t *testing.T) {
	const tool = "mcp__api-bridge__execute_shell_command"
	refused := []string{
		`{"tool_name":"Bash","tool_input":{"command":"curl -s -X POST \"$MCP_CUSTOM/get_contract_upgrades\" -H \"$MCP_AUTH\" --json '{}'"}}`,
		`{"tool_name":"Bash","tool_input":{"command":"curl -s \"${MCP_CUSTOM}/x\""}}`,
		`{"tool_name":"Bash","tool_input":{"command":"curl http://127.0.0.1:18743/tools/custom/get_contract_upgrades --json '{}'"}}`,
		`{"tool_name":"Bash","tool_input":{"command":"curl http://localhost:1/tools/virtual/get_api_spec"}}`,
		`{"tool_name":"Monitor","tool_input":{"command":"until curl -s $MCP_CUSTOM/x; do sleep 2; done","description":"d"}}`,
		`{"tool_name":"PowerShell","tool_input":{"command":"iwr http://127.0.0.1:1/tools/mcp/x"}}`,
		`{"tool_name":"WebFetch","tool_input":{"url":"http://127.0.0.1:18743/tools/custom/get_x","prompt":"p"}}`,
	}
	for _, payload := range refused {
		out := runClaudeShellRedirectHook(t, tool, payload)
		var decision struct {
			Hook struct {
				Event    string `json:"hookEventName"`
				Decision string `json:"permissionDecision"`
				Reason   string `json:"permissionDecisionReason"`
			} `json:"hookSpecificOutput"`
		}
		if err := json.Unmarshal([]byte(out), &decision); err != nil {
			t.Fatalf("payload %s: output %q is not a decision: %v", payload, out, err)
		}
		if decision.Hook.Event != "PreToolUse" || decision.Hook.Decision != "deny" {
			t.Errorf("payload %s: decision = %+v, want a PreToolUse deny", payload, decision.Hook)
		}
		if !strings.Contains(decision.Hook.Reason, tool) {
			t.Errorf("payload %s: reason %q does not name the bridge shell tool", payload, decision.Hook.Reason)
		}
	}
	allowed := []string{
		`{"tool_name":"Bash","tool_input":{"command":"ls -la && cat project/workflow.json"}}`,
		`{"tool_name":"Bash","tool_input":{"command":"curl -s https://example.com/api/tools-list"}}`,
		`{"tool_name":"Bash","tool_input":{"command":"echo $HOME"}}`,
		`{"tool_name":"mcp__api-bridge__execute_shell_command","tool_input":{"command":"curl \"$MCP_CUSTOM/get_contract_upgrades\" -H \"$MCP_AUTH\""}}`,
		`{"tool_name":"Monitor","tool_input":{"command":"tail -F app.log | grep ERROR"}}`,
		`{"tool_name":"WebFetch","tool_input":{"url":"https://example.com/docs","prompt":"p"}}`,
		`{"tool_name":"WebFetch","tool_input":{"url":"http://127.0.0.1:2/health","prompt":"p"}}`,
		`{"tool_name":"Read","tool_input":{"file_path":"/tools/custom/x"}}`,
		`not json at all`,
		``,
	}
	for _, payload := range allowed {
		if out := runClaudeShellRedirectHook(t, tool, payload); out != "" {
			t.Errorf("payload %q was refused: %s", payload, out)
		}
	}
}

func TestClaudeShellRedirectHookRefusesWebFetchToAPIHost(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed")
	}
	path, err := claudeWriteShellRedirectHook("mcp__api-bridge__execute_shell_command")
	if err != nil {
		t.Fatal(err)
	}
	run := func(payload string) string {
		cmd := exec.CommandContext(context.Background(), "node", path)
		cmd.Env = append(os.Environ(), "MCP_API_URL=http://127.0.0.1:18743")
		cmd.Stdin = strings.NewReader(payload)
		out, err := cmd.Output()
		if err != nil {
			t.Fatal(err)
		}
		return string(out)
	}
	if out := run(`{"tool_name":"WebFetch","tool_input":{"url":"http://127.0.0.1:18743/api/anything"}}`); !strings.Contains(out, `"deny"`) {
		t.Errorf("API host fetch not refused: %q", out)
	}
	if out := run(`{"tool_name":"WebFetch","tool_input":{"url":"http://127.0.0.1:18744/api/anything"}}`); out != "" {
		t.Errorf("other port refused: %q", out)
	}
}

func TestClaudeAddShellRedirectHookKeepsPersonsHooks(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed")
	}
	settings := claudeLoadSettingsMap(`{"model":"x","hooks":{"PreToolUse":[{"matcher":"Mine","hooks":[{"type":"command","command":"true"}]}],"Stop":[{"hooks":[]}]}}`)
	added, err := claudeAddShellRedirectHook(settings, redirectBridgeConfig)
	if err != nil || !added {
		t.Fatalf("added=%v err=%v", added, err)
	}
	raw, _ := json.Marshal(settings)
	var got struct {
		Model string `json:"model"`
		Hooks struct {
			PreToolUse []struct {
				Matcher string `json:"matcher"`
				Hooks   []struct {
					Command string `json:"command"`
				} `json:"hooks"`
			} `json:"PreToolUse"`
			Stop []json.RawMessage `json:"Stop"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.Model != "x" || len(got.Hooks.Stop) != 1 || len(got.Hooks.PreToolUse) != 2 {
		t.Fatalf("settings not preserved: %s", raw)
	}
	if got.Hooks.PreToolUse[0].Matcher != "Mine" {
		t.Errorf("person's hook moved: %s", raw)
	}
	last := got.Hooks.PreToolUse[1]
	if last.Matcher != claudeShellRedirectMatcher || len(last.Hooks) != 1 || !strings.Contains(last.Hooks[0].Command, "shell-redirect-") {
		t.Errorf("redirect hook missing: %s", raw)
	}
}

func TestClaudeAddShellRedirectHookNeedsBridge(t *testing.T) {
	settings := map[string]any{"model": "x"}
	added, err := claudeAddShellRedirectHook(settings, `{"mcpServers":{"other":{"command":"x"}}}`)
	if err != nil || added {
		t.Fatalf("added=%v err=%v, want no hook without a bridge", added, err)
	}
	if _, ok := settings["hooks"]; ok {
		t.Errorf("settings gained hooks without a bridge: %v", settings)
	}
}

func TestClaudeAddShellRedirectHookRejectsMalformedHooks(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed")
	}
	if _, err := claudeAddShellRedirectHook(map[string]any{"hooks": "nope"}, redirectBridgeConfig); err == nil {
		t.Error("expected an error for a non-object hooks value")
	}
}

func TestClaudeLoadSettingsMapReadsFile(t *testing.T) {
	path := t.TempDir() + "/s.json"
	if err := os.WriteFile(path, []byte(`{"model":"y"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := claudeLoadSettingsMap(path); got["model"] != "y" {
		t.Errorf("got %v", got)
	}
	if got := claudeLoadSettingsMap(""); len(got) != 0 {
		t.Errorf("got %v", got)
	}
}

// The tmux launch carries the redirect in its --settings file, with or without
// the status-line session, and a launch without a bridge gets no redirect. Every
// launch's settings keep the caller's keys and switch off Claude's own extras
// (owner, 2026-10-29: AgentWorks-launched Claude uses only AgentWorks skills).
func TestBuildClaudeArgsInstallsShellRedirectOnlyWithBridge(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed")
	}
	adapter := NewClaudeCodeInteractiveAdapter("claude-sonnet-4-6", &MockLogger{})
	settingsOf := func(args []string) string {
		for i, a := range args {
			if a == "--settings" && i+1 < len(args) {
				raw, err := os.ReadFile(args[i+1])
				if err != nil {
					t.Fatal(err)
				}
				return string(raw)
			}
		}
		return ""
	}
	for _, session := range []string{"", "sess1"} {
		opts := &llmtypes.CallOptions{}
		WithMCPConfig(redirectBridgeConfig)(opts)
		args, files, err := adapter.buildClaudeArgs(opts, session, "7aa21987-0003-4d71-b887-ad73e29d2faf", "p")
		if err != nil {
			t.Fatal(err)
		}
		if got := settingsOf(args); !strings.Contains(got, "shell-redirect-") {
			t.Errorf("session %q: settings %q lack the redirect hook", session, got)
		}
		removeFiles(files)
	}
	opts := &llmtypes.CallOptions{}
	WithMCPConfig(`{"mcpServers":{"other":{"command":"x"}}}`)(opts)
	WithClaudeCodeSettings(`{"model":"keep-me","enabledPlugins":{"own@market":true}}`)(opts)
	runtimeDir := t.TempDir()
	llmtypes.WithCLIRuntimeDir(runtimeDir)(opts)
	args, files, err := adapter.buildClaudeArgs(opts, "", "7aa21987-0003-4d71-b887-ad73e29d2faf", "p")
	if err != nil {
		t.Fatal(err)
	}
	defer removeFiles(files)
	got := settingsOf(args)
	// The merged file stays in the session's private runtime dir, owner-only,
	// and is one of the launch files removed when the CLI exits.
	for i, a := range args {
		if a != "--settings" || i+1 >= len(args) {
			continue
		}
		info, err := os.Stat(args[i+1])
		if err != nil || filepath.Dir(args[i+1]) != runtimeDir || info.Mode().Perm() != 0o600 || !slices.Contains(files, args[i+1]) {
			t.Errorf("settings file %s: dir/mode/cleanup wrong (%v, %v)", args[i+1], info, err)
		}
	}
	if strings.Contains(got, "shell-redirect-") {
		t.Errorf("no bridge but the redirect hook was installed: %s", got)
	}
	var settings struct {
		Model          string            `json:"model"`
		EnabledPlugins map[string]bool   `json:"enabledPlugins"`
		SkillOverrides map[string]string `json:"skillOverrides"`
		SyncSkills     *bool             `json:"syncClaudeAiSkills"`
	}
	if err := json.Unmarshal([]byte(got), &settings); err != nil {
		t.Fatalf("settings %q: %v", got, err)
	}
	if settings.Model != "keep-me" || !settings.EnabledPlugins["own@market"] {
		t.Errorf("caller settings lost: %s", got)
	}
	if on, set := settings.EnabledPlugins["cc-plugin-plugin-authoring@builtin"]; !set || on ||
		settings.SkillOverrides["design"] != "off" || settings.SkillOverrides["doctor"] != "off" ||
		settings.SyncSkills == nil || *settings.SyncSkills {
		t.Errorf("Claude's own skills not switched off: %s", got)
	}
}

func TestClaudeShellRedirectHookRefusesHostAndCredentialProbes(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed")
	}
	const tool = "mcp__api-bridge__execute_shell_command"
	path, err := claudeWriteShellRedirectHook(tool)
	if err != nil {
		t.Fatal(err)
	}
	run := func(command string) string {
		payload, _ := json.Marshal(map[string]any{"tool_name": "Bash", "tool_input": map[string]any{"command": command}})
		cmd := exec.CommandContext(context.Background(), "node", path)
		cmd.Env = append(os.Environ(), "MCP_API_URL=http://127.0.0.1:18743", "MCP_AGENT_SERVER_URL=https://agent.example.test:8443")
		cmd.Stdin = strings.NewReader(string(payload))
		out, err := cmd.Output()
		if err != nil {
			t.Fatal(err)
		}
		return string(out)
	}
	refused := []string{
		`for v in MCP_AUTH MCP_CUSTOM MCP_MCP MCP_API_TOKEN AGENTWORKS_TOKEN; do if [ -n "${!v}" ]; then echo "$v set"; else echo "$v unset"; fi; done; agentworks tools list; curl -sS -m 3 -o /dev/null -w '%{http_code}' http://127.0.0.1:18743/`,
		`curl -sS http://127.0.0.1:18743/`,
		`curl -sS http://localhost:18743/health`,
		`curl https://agent.example.test:8443/x`,
		`printenv MCP_AUTH`,
		`env | grep MCP_CUSTOM`,
		`echo $MCP_API_TOKEN`,
		`test -n "$MCP_MCP" && echo yes`,
		`[ -z "${MCP_AUTH}" ] && echo empty`,
		`compgen -e | grep MCP_AUTH`,
		`declare -p MCP_CUSTOM`,
		`python3 -c "import os; print(os.environ.get('MCP_AUTH'))"`,
	}
	for _, c := range refused {
		out := run(c)
		var d struct {
			Hook struct {
				Decision string `json:"permissionDecision"`
				Reason   string `json:"permissionDecisionReason"`
			} `json:"hookSpecificOutput"`
		}
		if err := json.Unmarshal([]byte(out), &d); err != nil || d.Hook.Decision != "deny" {
			t.Errorf("command %q not refused: %q", c, out)
			continue
		}
		for _, want := range []string{tool, "mcp__api-bridge__search_tools", "mcp__api-bridge__get_api_spec", "MCP_API_TOKEN", "NOT mean the bridge is missing"} {
			if !strings.Contains(d.Hook.Reason, want) {
				t.Errorf("command %q: reason lacks %q: %s", c, want, d.Hook.Reason)
			}
		}
		if len(d.Hook.Reason) > 700 {
			t.Errorf("reason is %d chars", len(d.Hook.Reason))
		}
	}
	allowed := []string{
		`grep -rn MCP_AUTH code/`,
		`cat code/x/agentworks_db.py`,
		`python3 code/x/main.py`,
		`ls`,
		`git status`,
		`curl -sS https://example.com/`,
		`curl -sS http://127.0.0.1:18744/`,
		`curl -sS http://127.0.0.1:187430/`,
		`echo hello-ok && ls`,
		`printenv HOME`,
	}
	for _, c := range allowed {
		if out := run(c); out != "" {
			t.Errorf("command %q was refused: %s", c, out)
		}
	}
}
