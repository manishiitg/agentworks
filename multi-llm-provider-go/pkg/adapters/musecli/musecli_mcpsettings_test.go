package musecli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// redirectMuseConfigHome points XDG_CONFIG_HOME at a temp dir for the test.
func redirectMuseConfigHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	// Fail loudly if the resolved path escapes the temp dir.
	path, err := museSettingsPath()
	if err != nil {
		t.Fatalf("settings path: %v", err)
	}
	want := filepath.Join(dir, "muse", "settings.json")
	if path != want {
		t.Fatalf("settings path = %q, want %q (config home redirect not honored)", path, want)
	}
	return path
}

func readMCPServerNames(t *testing.T, path string) map[string]bool {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	var settings struct {
		Servers map[string]json.RawMessage `json:"mcpServers"`
	}
	if err := json.Unmarshal(raw, &settings); err != nil {
		t.Fatalf("parse settings: %v", err)
	}
	out := map[string]bool{}
	for name := range settings.Servers {
		out[name] = true
	}
	return out
}

func TestMuseApplyMCPConfigMergesAndRestores(t *testing.T) {
	path := redirectMuseConfigHome(t)

	// Pre-existing settings with one server and an unrelated key.
	before := `{"schema_version": 1, "theme": "dark", "mcpServers": {"keep": {"url": "http://127.0.0.1:9/x"}}}`
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(before), 0o600); err != nil {
		t.Fatal(err)
	}

	restore, err := museApplyMCPConfig(`{"mcpServers": {"api-bridge": {"url": "http://127.0.0.1:9/bridge"}, "keep": {"url": "http://127.0.0.1:9/override"}}}`, nil)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if restore == nil {
		t.Fatal("expected a restore func for a non-empty config")
	}
	names := readMCPServerNames(t, path)
	if !names["api-bridge"] || !names["keep"] {
		t.Fatalf("merged servers = %v, want api-bridge + keep", names)
	}

	restore()

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read after restore: %v", err)
	}
	var after struct {
		Theme   string                     `json:"theme"`
		Servers map[string]json.RawMessage `json:"mcpServers"`
	}
	if err := json.Unmarshal(raw, &after); err != nil {
		t.Fatalf("parse after restore: %v", err)
	}
	if after.Theme != "dark" {
		t.Fatalf("unrelated key lost: %s", raw)
	}
	if len(after.Servers) != 1 {
		t.Fatalf("servers after restore = %v, want only keep", after.Servers)
	}
	var kept struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(after.Servers["keep"], &kept); err != nil || kept.URL != "http://127.0.0.1:9/x" {
		t.Fatalf("pre-existing server not reinstated byte-fair: %s", after.Servers["keep"])
	}
}

func TestMuseApplyMCPConfigCreatesAndRemovesFile(t *testing.T) {
	path := redirectMuseConfigHome(t)

	restore, err := museApplyMCPConfig(`{"mcpServers": {"api-bridge": {"url": "http://127.0.0.1:9/bridge"}}}`, nil)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if names := readMCPServerNames(t, path); !names["api-bridge"] {
		t.Fatalf("servers = %v, want api-bridge", names)
	}
	restore()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("created settings.json not removed by restore")
	}
}

func TestMusePrepareIsolatedConfigKeepsConcurrentMountsSeparate(t *testing.T) {
	sharedPath := redirectMuseConfigHome(t)
	sharedDir := filepath.Dir(sharedPath)
	if err := os.MkdirAll(sharedDir, 0o700); err != nil {
		t.Fatal(err)
	}
	shared := `{"schema_version":1,"theme":"dark","mcpServers":{"stale":{"url":"http://127.0.0.1:1/mcp"}},"hooks":{"PreToolUse":[{"matcher":"*"}]}}`
	if err := os.WriteFile(sharedPath, []byte(shared), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sharedDir, "auth.json"), []byte(`{"schema_version":2}`), 0o600); err != nil {
		t.Fatal(err)
	}

	homeA, cleanupA, err := musePrepareIsolatedConfig(`{"mcpServers":{"bridge-a":{"url":"http://127.0.0.1:2/mcp"}}}`, []string{"web_search"})
	if err != nil {
		t.Fatalf("prepare A: %v", err)
	}
	defer cleanupA()
	homeB, cleanupB, err := musePrepareIsolatedConfig(`{"mcpServers":{"bridge-b":{"url":"http://127.0.0.1:3/mcp"}}}`, []string{"web_search"})
	if err != nil {
		t.Fatalf("prepare B: %v", err)
	}
	defer cleanupB()
	if homeA == homeB {
		t.Fatal("concurrent Muse launches shared an isolated config root")
	}

	namesA := readMCPServerNames(t, filepath.Join(homeA, "muse", "settings.json"))
	namesB := readMCPServerNames(t, filepath.Join(homeB, "muse", "settings.json"))
	if !namesA["bridge-a"] || len(namesA) != 1 {
		t.Fatalf("session A servers = %v, want bridge-a only", namesA)
	}
	if !namesB["bridge-b"] || len(namesB) != 1 {
		t.Fatalf("session B servers = %v, want bridge-b only", namesB)
	}
	if raw, err := os.ReadFile(sharedPath); err != nil || string(raw) != shared {
		t.Fatalf("shared settings changed: err=%v content=%q", err, raw)
	}
	for _, home := range []string{homeA, homeB} {
		if raw, err := os.ReadFile(filepath.Join(home, "muse", "auth.json")); err != nil || string(raw) != `{"schema_version":2}` {
			t.Fatalf("isolated auth copy invalid for %s: err=%v content=%q", home, err, raw)
		}
	}
}

func TestMusePrepareIsolatedConfigKeepsSharedSettingsUntouched(t *testing.T) {
	sharedPath := redirectMuseConfigHome(t)
	if err := os.MkdirAll(filepath.Dir(sharedPath), 0o700); err != nil {
		t.Fatal(err)
	}
	shared := `{
  "schema_version": 1,
  "theme": "dark",
  "mcpServers": {
    "api-bridge": {"command":"/opt/agentworks/mcpbridge","env":{"MCP_API_URL":"http://127.0.0.1:1","MCP_SESSION_ID":"old","MCP_TOOLS":"[]"}},
    "user-server": {"url":"https://example.test/mcp"}
  },
  "hooks": {
    "PreToolUse": [
      {"matcher":"*","hooks":[{"type":"command","command":"node '/tmp/muse-cli-hooks/native-tool-policy-deadbeef.js'"}]},
      {"matcher":"user","hooks":[{"type":"command","command":"check-user-policy"}]}
    ],
    "PostToolUse": [{"matcher":"user","hooks":[]}]
  }
}`
	if err := os.WriteFile(sharedPath, []byte(shared), 0o600); err != nil {
		t.Fatal(err)
	}

	home, cleanup, err := musePrepareIsolatedConfig(`{"mcpServers":{"current":{"url":"http://127.0.0.1:2/mcp"}}}`, nil)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	defer cleanup()

	raw, err := os.ReadFile(sharedPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != shared {
		t.Fatal("private launch changed the person's shared settings")
	}
	isolatedNames := readMCPServerNames(t, filepath.Join(home, "muse", "settings.json"))
	if len(isolatedNames) != 1 || !isolatedNames["current"] {
		t.Fatalf("isolated servers = %v, want current only", isolatedNames)
	}
}

func TestMuseApplyMCPConfigRejectsBadInput(t *testing.T) {
	redirectMuseConfigHome(t)
	for _, bad := range []string{
		`not json`,
		`{"mcpServers": {"": {"url": "http://x"}}}`,
	} {
		if _, err := museApplyMCPConfig(bad, nil); err == nil {
			t.Fatalf("expected error for %q", bad)
		}
	}
	// Empty server map still applies the voice-off overlay, so it is no
	// longer a true no-op: a real restore func always comes back.
	restore, err := museApplyMCPConfig(`{"mcpServers": {}}`, nil)
	if err != nil {
		t.Fatalf("empty config: %v", err)
	}
	if restore == nil {
		t.Fatal("expected a restore func even for an empty MCP config (voice-off overlay always applies)")
	}
	restore()
}

// TestMuseApplyMCPConfigForcesVoiceOff pins that voice input is disabled on
// every launch regardless of whether an MCP config is mounted: muse has no
// CLI flag for it (verified against `muse --help`), only the
// settings.json "tui":{"voice_enabled":...} field, so this is the only
// place that can guarantee it off. Other "tui" fields and other top-level
// settings survive the round trip untouched.
func TestMuseApplyMCPConfigForcesVoiceOff(t *testing.T) {
	path := redirectMuseConfigHome(t)
	before := `{"schema_version": 1, "theme": "dark", "tui": {"voice_enabled": true, "some_other_flag": true}}`
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(before), 0o600); err != nil {
		t.Fatal(err)
	}

	restore, err := museApplyMCPConfig("", nil) // no MCP config mounted this run
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if restore == nil {
		t.Fatal("expected a restore func: the voice-off overlay always writes")
	}

	var during struct {
		TUI struct {
			VoiceEnabled  bool `json:"voice_enabled"`
			SomeOtherFlag bool `json:"some_other_flag"`
		} `json:"tui"`
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read during: %v", err)
	}
	if err := json.Unmarshal(raw, &during); err != nil {
		t.Fatalf("parse during: %v", err)
	}
	if during.TUI.VoiceEnabled {
		t.Fatal("voice_enabled must be forced false while a turn is mounted")
	}
	if !during.TUI.SomeOtherFlag {
		t.Fatal("unrelated tui field lost while forcing voice off")
	}

	restore()

	var after struct {
		Theme string `json:"theme"`
		TUI   struct {
			VoiceEnabled bool `json:"voice_enabled"`
		} `json:"tui"`
	}
	raw, err = os.ReadFile(path)
	if err != nil {
		t.Fatalf("read after restore: %v", err)
	}
	if err := json.Unmarshal(raw, &after); err != nil {
		t.Fatalf("parse after restore: %v", err)
	}
	if after.Theme != "dark" {
		t.Fatalf("unrelated top-level key lost: %s", raw)
	}
	if !after.TUI.VoiceEnabled {
		t.Fatal("original voice_enabled=true not reinstated by restore")
	}
}

func TestMuseApplyMCPConfigInstallsToolPolicyAndRestores(t *testing.T) {
	path := redirectMuseConfigHome(t)
	before := `{"schema_version":1,"run":{"parallel_tool_calls":true,"toolset":["bash"]},"hooks":{"PreToolUse":[{"matcher":"existing","hooks":[]}]},"theme":"dark"}`
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(before), 0o600); err != nil {
		t.Fatal(err)
	}

	want := []string{"web_search", "web_search"}
	restore, err := museApplyMCPConfig("", want)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	var during struct {
		Run struct {
			Toolset                []string `json:"toolset"`
			Parallel               bool     `json:"parallel_tool_calls"`
			SubagentDelegationMode string   `json:"subagent_delegation_mode"`
			WorkflowTriggerMode    string   `json:"workflow_trigger_mode"`
		} `json:"run"`
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Type    string `json:"type"`
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &during); err != nil {
		t.Fatal(err)
	}
	if !during.Run.Parallel {
		t.Fatal("unrelated run setting was lost")
	}
	if during.Run.Toolset != nil {
		t.Fatal("inherited named toolset would hide the mounted MCP tools")
	}
	if during.Run.SubagentDelegationMode != "off" {
		t.Fatalf("subagent_delegation_mode = %q, want off", during.Run.SubagentDelegationMode)
	}
	if during.Run.WorkflowTriggerMode != "off" {
		t.Fatalf("workflow_trigger_mode = %q, want off", during.Run.WorkflowTriggerMode)
	}
	pre := during.Hooks["PreToolUse"]
	if len(pre) != 2 || pre[0].Matcher != "existing" || pre[1].Matcher != "*" || len(pre[1].Hooks) != 1 || pre[1].Hooks[0].Type != "command" {
		t.Fatalf("PreToolUse policy hook = %#v", pre)
	}
	if !strings.Contains(pre[1].Hooks[0].Command, "native-tool-policy-") {
		t.Fatalf("PreToolUse command = %q", pre[1].Hooks[0].Command)
	}

	restore()
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != before {
		t.Fatalf("settings not restored byte-exact:\nbefore=%s\nafter=%s", before, after)
	}
}

func TestMuseToolPolicyHookAllowsOnlyWebAndMCP(t *testing.T) {
	path, err := museWriteToolPolicyHook([]string{"web_search"}, "")
	if err != nil {
		t.Fatalf("write hook: %v", err)
	}
	for _, tc := range []struct {
		name    string
		allowed bool
	}{
		{name: "web_search", allowed: true},
		{name: "tool_search", allowed: true},
		{name: "submit_reminder_decision", allowed: true},
		{name: "mcp__api_bridge__execute_shell_command", allowed: true},
		{name: "bash", allowed: false},
		{name: "request_user_input", allowed: false},
		{name: "write_file", allowed: false},
		{name: "subagent_spawn", allowed: false},
	} {
		cmd := exec.CommandContext(t.Context(), "node", path)
		cmd.Stdin = strings.NewReader(`{"tool_name":"` + tc.name + `","tool_input":{}}`)
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("hook %s: %v", tc.name, err)
		}
		denied := strings.Contains(string(out), `"permissionDecision":"deny"`)
		if denied == tc.allowed {
			t.Fatalf("hook %s output = %q, allowed=%v", tc.name, out, tc.allowed)
		}
	}
	cmd := exec.CommandContext(t.Context(), "node", path)
	cmd.Stdin = strings.NewReader(`{"tool_name":"bash","tool_input":{"text":"\\\"tool_name\\\":\\\"mcp__escape__attempt\\\""}}`)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("hook nested tool_name: %v", err)
	}
	if !strings.Contains(string(out), `"permissionDecision":"deny"`) {
		t.Fatalf("nested tool_name text bypassed policy: %q", out)
	}
}

func TestMuseApplyMCPConfigRejectsInvalidToolAllowlist(t *testing.T) {
	redirectMuseConfigHome(t)
	if _, err := museApplyMCPConfig("", []string{"web_search", "  "}); err == nil {
		t.Fatal("blank tool name must fail closed")
	}
}

func TestMuseWithMCPConfigOptionRoundTrip(t *testing.T) {
	opts := &llmtypes.CallOptions{}
	WithMCPConfig(`{"mcpServers": {"a": {}}}`)(opts)
	if got := museMCPConfigFromOptions(opts); got != `{"mcpServers": {"a": {}}}` {
		t.Fatalf("option round trip = %q", got)
	}
	if got := museMCPConfigFromOptions(nil); got != "" {
		t.Fatalf("nil options = %q, want empty", got)
	}
}

func TestMuseWithToolAllowlistOptionRoundTrip(t *testing.T) {
	opts := &llmtypes.CallOptions{}
	if _, ok := museToolAllowlistFromOptions(opts); ok {
		t.Fatal("unset allowlist must stay distinguishable from an empty allowlist")
	}
	input := []string{"web_search"}
	WithToolAllowlist(input)(opts)
	input[0] = "mutated"
	got, ok := museToolAllowlistFromOptions(opts)
	if !ok || len(got) != 1 || got[0] != "web_search" {
		t.Fatalf("allowlist = %v, set=%v", got, ok)
	}
}

func TestMuseToolPolicyRequiresNodeBeforeWritingSettings(t *testing.T) {
	path := redirectMuseConfigHome(t)
	t.Setenv("PATH", t.TempDir())
	if _, err := museApplyMCPConfig("", []string{"web_search"}); err == nil || !strings.Contains(err.Error(), "requires Node.js") {
		t.Fatalf("expected missing Node error, got %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("settings written before policy prerequisite passed: %v", err)
	}
}

func TestMuseExplicitEmptyAllowlistStillInstallsPolicy(t *testing.T) {
	path := redirectMuseConfigHome(t)
	opts := &llmtypes.CallOptions{}
	WithToolAllowlist(nil)(opts)
	names, present := museToolAllowlistFromOptions(opts)
	if !present || names == nil || len(names) != 0 {
		t.Fatalf("explicit empty policy lost: %v, present=%v", names, present)
	}
	restore, err := museApplyMCPConfig("", names)
	if err != nil {
		t.Fatal(err)
	}
	defer restore()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "PreToolUse") {
		t.Fatal("explicit empty allowlist disabled the policy")
	}
}

func TestMuseToolPolicyDeniesMissingOrInvalidNames(t *testing.T) {
	path, err := museWriteToolPolicyHook([]string{"web_search"}, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, payload := range []string{`null`, `{}`, `[]`, `invalid json`, `{"tool_name":123}`, `{"tool_name":"unknown_tool"}`} {
		cmd := exec.CommandContext(t.Context(), "node", path)
		cmd.Stdin = strings.NewReader(payload)
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("hook error for %q: %v", payload, err)
		}
		if !strings.Contains(string(out), `"permissionDecision":"deny"`) {
			t.Fatalf("missing denial for %q: %s", payload, out)
		}
	}
}

func TestMuseNativeSubagentsEnableDelegationAndRemainCallable(t *testing.T) {
	path := redirectMuseConfigHome(t)
	restore, err := museApplyMCPConfig("", []string{"subagent_spawn", "subagent_wait"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(restore)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var settings struct {
		Run struct {
			Delegation string `json:"subagent_delegation_mode"`
		} `json:"run"`
	}
	if err := json.Unmarshal(raw, &settings); err != nil {
		t.Fatal(err)
	}
	if settings.Run.Delegation != "auto" {
		t.Fatalf("native subagents must be visible, got %q", settings.Run.Delegation)
	}
	hook, err := museWriteToolPolicyHook([]string{"subagent_spawn", "subagent_wait"}, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(hook) })
	for _, name := range []string{"subagent_spawn", "subagent_wait"} {
		cmd := exec.CommandContext(t.Context(), "node", hook)
		cmd.Stdin = strings.NewReader(`{"tool_name":"` + name + `","tool_input":{}}`)
		out, err := cmd.Output()
		if err != nil || strings.Contains(string(out), `"permissionDecision":"deny"`) {
			t.Fatalf("allowlisted native tool %s blocked: %s (%v)", name, out, err)
		}
	}
}

func TestMuseBridgeCallLimitIsAddedOnlyToTheBridgeEntry(t *testing.T) {
	bridge := json.RawMessage(`{"command":"/x/mcpbridge","env":{"MCP_API_URL":"http://a","MCP_TOOLS":"[]"}}`)
	var got struct {
		Env map[string]string `json:"env"`
	}
	if err := json.Unmarshal(museBridgeCallLimit(bridge), &got); err != nil {
		t.Fatal(err)
	}
	if got.Env["MCP_BRIDGE_MAX_CALL_SECONDS"] != "270" || got.Env["MCP_API_URL"] != "http://a" || got.Env["MCP_TOOLS"] != "[]" {
		t.Fatalf("env = %v", got.Env)
	}
	preset := json.RawMessage(`{"command":"/x/mcpbridge","env":{"MCP_API_URL":"http://a","MCP_BRIDGE_MAX_CALL_SECONDS":"60"}}`)
	if string(museBridgeCallLimit(preset)) != string(preset) {
		t.Fatal("an explicit limit must be kept")
	}
	for _, other := range []string{`{"url":"http://remote/mcp"}`, `{"command":"npx","env":{"TOKEN":"x"}}`, `"not an object"`} {
		if string(museBridgeCallLimit(json.RawMessage(other))) != other {
			t.Fatalf("%s must be unchanged", other)
		}
	}
}

func TestMuseStripResumeNoteReturnsTheOriginalMessage(t *testing.T) {
	bridge := `{"mcpServers":{"api-bridge":{"command":"x","env":{"MCP_API_URL":"http://127.0.0.1:1"}}}}`
	sent := museWithResumeNote("Run finished: 3 jobs.", true, bridge)
	if !strings.HasPrefix(sent, "[AgentWorks note:") {
		t.Fatalf("resumed run with the bridge should carry the note, got %q", sent)
	}
	if got := museStripResumeNote(sent); got != "Run finished: 3 jobs." {
		t.Fatalf("note leaked into the shown message: %q", got)
	}
	if got := museStripResumeNote("plain message"); got != "plain message" {
		t.Fatalf("a message without the note changed: %q", got)
	}
}
