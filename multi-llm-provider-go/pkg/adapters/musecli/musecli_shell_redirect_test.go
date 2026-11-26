package musecli

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func runShellRedirectHook(t *testing.T, payload string) string {
	t.Helper()
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed")
	}
	path, err := museWriteShellRedirectHook()
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

// The bridge's credentials exist only in the bridge's own shell. A native bash call that targets the
// platform can only fail with "missing or invalid Authorization header"; the hook says so up front and
// names the tool that can make the call. Everything else in bash is left alone.
func TestShellRedirectHookRefusesOnlyPlatformCallsInNativeBash(t *testing.T) {
	refused := []string{
		`{"tool_name":"bash","tool_input":{"command":"curl -s -X POST \"$MCP_CUSTOM/get_contract_upgrades\" -H \"$MCP_AUTH\" --json '{}'"}}`,
		`{"tool_name":"bash","tool_input":{"command":"curl -s \"${MCP_CUSTOM}/x\""}}`,
		`{"tool_name":"bash","tool_input":{"command":"curl http://127.0.0.1:18743/tools/custom/get_contract_upgrades --json '{}'"}}`,
		`{"tool_name":"bash_input","tool_input":{"command":"curl http://localhost:1/tools/virtual/get_api_spec"}}`,
		`{"tool_name":"monitor","tool_input":{"command":"while true; do curl -s \"$MCP_CUSTOM/poll\"; sleep 5; done"}}`,
		`{"tool_name":"web_fetch","tool_input":{"url":"http://127.0.0.1:1/tools/custom/get_contract_upgrades"}}`,
		`{"tool_name":"cron_create","tool_input":{"cron":"* * * * *","prompt":"echo hi"}}`,
	}
	for _, payload := range refused {
		out := runShellRedirectHook(t, payload)
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
		want := "mcp__api_bridge__execute_shell_command"
		if strings.Contains(payload, "cron_create") {
			want = "platform's Schedules"
		}
		if !strings.Contains(decision.Hook.Reason, want) {
			t.Errorf("payload %s: reason %q does not contain %q", payload, decision.Hook.Reason, want)
		}
	}
	allowed := []string{
		`{"tool_name":"bash","tool_input":{"command":"ls -la && cat project/workflow.json"}}`,
		`{"tool_name":"bash","tool_input":{"command":"curl -s https://example.com/api/tools-list"}}`,
		`{"tool_name":"bash","tool_input":{"command":"echo $HOME"}}`,
		`{"tool_name":"mcp__api_bridge__execute_shell_command","tool_input":{"command":"curl \"$MCP_CUSTOM/get_contract_upgrades\" -H \"$MCP_AUTH\""}}`,
		`{"tool_name":"monitor","tool_input":{"command":"tail -f build.log"}}`,
		`{"tool_name":"web_fetch","tool_input":{"url":"https://example.com/tools-list"}}`,
		`{"tool_name":"cron_list","tool_input":{}}`,
		`{"tool_name":"cron_delete","tool_input":{"id":"1"}}`,
		`{"tool_name":"read_file","tool_input":{"path":"/tools/custom/x"}}`,
		`not json at all`,
		``,
	}
	for _, payload := range allowed {
		if out := runShellRedirectHook(t, payload); out != "" {
			t.Errorf("payload %q was refused: %s", payload, out)
		}
	}
}

// web_fetch to the host:port of MCP_API_URL is refused too, when the hook process has it.
func TestShellRedirectHookRefusesWebFetchToTheAPIHost(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed")
	}
	path, err := museWriteShellRedirectHook()
	if err != nil {
		t.Fatal(err)
	}
	run := func(url string) string {
		cmd := exec.CommandContext(context.Background(), "node", path)
		cmd.Env = append(os.Environ(), "MCP_API_URL=http://127.0.0.1:18743")
		cmd.Stdin = strings.NewReader(`{"tool_name":"web_fetch","tool_input":{"url":"` + url + `"}}`)
		out, err := cmd.Output()
		if err != nil {
			t.Fatal(err)
		}
		return string(out)
	}
	if out := run("http://127.0.0.1:18743/api/anything"); !strings.Contains(out, `"deny"`) {
		t.Errorf("API host fetch not refused: %q", out)
	}
	if out := run("http://127.0.0.1:9999/api/anything"); out != "" {
		t.Errorf("other port refused: %q", out)
	}
}

// Full CLI mode has no native-tool allowlist, so the allowlist hook is absent; the redirect must still
// be installed whenever the bridge is mounted, next to whatever hooks the person already has.
func TestApplyMCPConfigInstallsShellRedirectWithoutAnAllowlist(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed")
	}
	path := redirectMuseConfigHome(t)
	if err := os.MkdirAll(strings.TrimSuffix(path, "/settings.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"schema_version":1,"hooks":{"PreToolUse":[{"matcher":"mine","hooks":[{"type":"command","command":"true"}]}]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	bridge := `{"mcpServers":{"api-bridge":{"command":"/bin/true","env":{"MCP_API_URL":"http://127.0.0.1:1"}}}}`
	restore, err := museApplyMCPConfigAtPath(path, bridge, nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var settings struct {
		Hooks struct {
			PreToolUse []struct {
				Matcher string `json:"matcher"`
				Hooks   []struct {
					Command string `json:"command"`
				} `json:"hooks"`
			} `json:"PreToolUse"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(raw, &settings); err != nil {
		t.Fatal(err)
	}
	var mine, redirect int
	for _, entry := range settings.Hooks.PreToolUse {
		if entry.Matcher == "mine" {
			mine++
		}
		for _, h := range entry.Hooks {
			if strings.Contains(h.Command, "shell-redirect-") || strings.Contains(h.Command, "native-tool-policy-") {
				redirect++
			}
		}
	}
	if mine != 1 || redirect != 1 {
		t.Errorf("PreToolUse has %d of the person's hooks and %d redirect hooks, want 1 and 1: %s", mine, redirect, raw)
	}
	restore()
	if back, _ := os.ReadFile(path); !strings.Contains(string(back), `"matcher":"mine"`) || strings.Contains(string(back), "shell-redirect-") {
		t.Errorf("restore did not put the person's settings back: %s", back)
	}

	// No bridge mounted: nothing is installed.
	restore2, err := museApplyMCPConfigAtPath(path, `{"mcpServers":{"other":{"command":"/bin/true"}}}`, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer restore2()
	if raw2, _ := os.ReadFile(path); strings.Contains(string(raw2), "shell-redirect-") {
		t.Errorf("redirect hook installed without a bridge: %s", raw2)
	}
}

func runHookWithEnv(t *testing.T, payload string, env ...string) string {
	t.Helper()
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed")
	}
	path, err := museWriteShellRedirectHook()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(context.Background(), "node", path)
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdin = strings.NewReader(payload)
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func bashPayload(t *testing.T, tool, command string) string {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"tool_name": tool, "tool_input": map[string]string{"command": command}})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// A real jobsearch chat probed for the credentials in native bash, found none and concluded the bridge
// was missing. Probing for them, or touching the platform host:port, is refused with the full reason.
func TestShellRedirectHookRefusesCredentialProbingAndPlatformHost(t *testing.T) {
	probe := `for v in MCP_AUTH MCP_CUSTOM MCP_MCP MCP_API_TOKEN AGENTWORKS_TOKEN; do if [ -n "${!v}" ]; then echo "$v set"; else echo "$v unset"; fi; done; agentworks tools list; curl -sS -m 3 -o /dev/null -w '%{http_code}' http://127.0.0.1:18743/`
	refused := []string{
		probe,
		`printenv MCP_AUTH`,
		`env | grep MCP_CUSTOM`,
		`[ -z "$X" ] || test -n x; echo MCP_MCP`,
		`test -n "${!name}" && echo MCP_API_TOKEN`,
		`compgen -e | grep MCP_AUTH`,
		`declare -p | grep MCP_MCP`,
		`python3 -c 'import os; print(os.environ.get("MCP_AUTH"))'`,
		`cat /proc/self/environ | tr '\0' '\n' | grep MCP_API_TOKEN`,
		`curl -s http://127.0.0.1:18743/health`,
		`agentworks tools list --server 127.0.0.1:18743`,
	}
	for _, tool := range []string{"bash", "bash_input", "monitor"} {
		for _, command := range refused {
			out := runHookWithEnv(t, bashPayload(t, tool, command), "MCP_API_URL=http://127.0.0.1:18743")
			if !strings.Contains(out, `"deny"`) {
				t.Errorf("%s %q was not refused: %q", tool, command, out)
				continue
			}
			for _, want := range []string{"MCP_AUTH", "only inside the bridge shell", "mcp__api_bridge__execute_shell_command", "mcp__api_bridge__search_tools", "mcp__api_bridge__get_api_spec", "working bridge"} {
				if !strings.Contains(out, want) {
					t.Errorf("%s %q: reason lacks %q: %s", tool, command, want, out)
				}
			}
		}
	}
	var decision struct {
		Hook struct {
			Reason string `json:"permissionDecisionReason"`
		} `json:"hookSpecificOutput"`
	}
	out := runHookWithEnv(t, bashPayload(t, "bash", probe))
	if err := json.Unmarshal([]byte(out), &decision); err != nil {
		t.Fatalf("probe without any URL env was not refused: %q", out)
	}
	if n := len(decision.Hook.Reason); n > 700 {
		t.Errorf("reason is %d chars, want <= 700", n)
	}
	allowed := []string{
		`grep -rn MCP_AUTH code/`,
		`cat code/x/agentworks_db.py`,
		`python3 code/x/main.py`,
		`ls`,
		`git status`,
		`curl -s https://example.com/api`,
		`curl -s http://127.0.0.1:9999/health`,
		`env FOO=1 make build`,
		`echo hello`,
		`grep -n "MCP_CUSTOM" code/x/agentworks_db.py | head`,
		`sed -n 1,20p README.md`,
	}
	for _, tool := range []string{"bash", "bash_input", "monitor"} {
		for _, command := range allowed {
			if out := runHookWithEnv(t, bashPayload(t, tool, command), "MCP_API_URL=http://127.0.0.1:18743"); out != "" {
				t.Errorf("%s %q was refused: %s", tool, command, out)
			}
		}
	}
	// Each host variable counts, and web_fetch uses the same reason.
	for _, key := range []string{"MCP_BRIDGE_API_URL", "MCP_AGENT_SERVER_URL"} {
		out := runHookWithEnv(t, `{"tool_name":"web_fetch","tool_input":{"url":"http://localhost:4321/x"}}`, key+"=http://localhost:4321")
		if !strings.Contains(out, "search_tools") {
			t.Errorf("%s: web_fetch to the platform host not refused with the full reason: %q", key, out)
		}
	}
}

// A resumed conversation can carry old history that says there is no bridge. The note rides with the
// message only for a resumed run with the bridge mounted, never for a fresh run or an unmounted one.
func TestResumeBridgeNote(t *testing.T) {
	bridge := `{"mcpServers":{"api-bridge":{"command":"/bin/true","env":{"MCP_API_URL":"http://127.0.0.1:1"}}}}`
	other := `{"mcpServers":{"other":{"command":"/bin/true"}}}`
	if got := museWithResumeNote("hello", true, bridge); got != museResumeBridgeNote+"\n\nhello" {
		t.Errorf("resumed+bridge: %q", got)
	}
	for name, got := range map[string]string{
		"fresh run":          museWithResumeNote("hello", false, bridge),
		"no bridge mounted":  museWithResumeNote("hello", true, other),
		"no mcp config":      museWithResumeNote("hello", true, ""),
		"malformed mcp json": museWithResumeNote("hello", true, "{"),
	} {
		if got != "hello" {
			t.Errorf("%s: prompt was altered: %q", name, got)
		}
	}
	if !strings.Contains(museResumeBridgeNote, "out of date") || !strings.Contains(museResumeBridgeNote, "mcp__api_bridge__execute_shell_command") {
		t.Errorf("note text changed: %q", museResumeBridgeNote)
	}
}
