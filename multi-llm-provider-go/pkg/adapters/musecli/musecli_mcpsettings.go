package musecli

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/manishiitg/multi-llm-provider-go/internal/slotfs"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/nativeshell"
)

// Muse has no --mcp-config CLI flag (verified against `muse exec --help`):
// MCP servers come from the user-level settings file
// $XDG_CONFIG_HOME/muse/settings.json, {"schema_version": 1,
// "mcpServers": {"<name>": {"url": "<streamable-http>"}}}. There is no
// workspace-level equivalent. Each launch gets an isolated XDG_CONFIG_HOME
// with its exact bridge and policy; shared user settings remain untouched.

// museSettingsPath resolves the user-level muse settings file the way the
// CLI does: $XDG_CONFIG_HOME wins when set, otherwise ~/.config (NOT
// os.UserConfigDir — on darwin that is ~/Library/Application Support, which
// the CLI does not read; proven live 2026-09-10 when a merge written there
// never reached the session). Tests redirect via XDG.
func museSettingsPath() (string, error) {
	if dir := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); dir != "" {
		return filepath.Join(dir, "muse", "settings.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir for muse settings: %w", err)
	}
	return filepath.Join(home, ".config", "muse", "settings.json"), nil
}

// museFullNativeTools is the deny-by-default policy of Full CLI mode: the native tools a chat may use next to
// the bridge. Everything else Muse ships (its own cron, goals, memory, messaging of other sessions, reminders,
// named workflows) and anything a later Muse update adds is refused until it is added here on purpose; the
// platform has its own scheduling, Goals, knowledge and sessions. MCP tools (mcp__*) are always allowed.
var museFullNativeTools = []string{
	"read_file", "search", "write_file", "edit_file", "bash", "bash_input", "monitor",
	"web_fetch", "web_search", "read_skill", "write_todos",
	"subagent_spawn", "subagent_status", "subagent_send_message", "subagent_wait", "subagent_read_result", "subagent_cancel",
	"work_status", "work_list", "work_stop", "request_user_input",
}

// museShellTools are Muse's command-running native tools: bash, its stdin companion and monitor (which runs a
// command and streams its output). Full mode drops them unless nativeshell.Enabled() (PLAT-491): the CLI's own
// shell runs as the app account with the platform environment; the bridge shell runs as the user's slot.
var museShellTools = []string{"bash", "bash_input", "monitor"}

const museShellOffReason = "Muse's own shell is turned off in AgentWorks chats. Run commands with mcp__api_bridge__execute_shell_command instead: it runs as your user account with your files and credentials. Native file tools (read_file, write_file, edit_file, search) still work."

// museFullAllowedTools is the native allowlist of a Full-mode launch: museFullNativeTools, minus the shell tools
// unless the escape hatch is on. The result is baked into the generated hook at launch, so the hook never reads
// its own (scrubbed) environment.
func museFullAllowedTools() []string {
	if nativeshell.Enabled() {
		return museFullNativeTools
	}
	out := make([]string, 0, len(museFullNativeTools))
	for _, name := range museFullNativeTools {
		if !slices.Contains(museShellTools, name) {
			out = append(out, name)
		}
	}
	return out
}

const museFullPolicyReason = "This Muse tool is not available in AgentWorks chats. Scheduling, goals, memory and other sessions are handled by the platform: use its schedules, Goals and knowledge tools, or ask the user."

// museApplyMCPConfig merges the "mcpServers" entries of configJSON (if any),
// optionally installs a deny-by-default PreToolUse policy for the supplied
// native-tool allowlist, enables native delegation only when subagent_spawn
// is allowlisted, disables workflow triggering, and forces tui.voice_enabled to false. It returns a restore
// function that puts the previous settings back byte-exact. Voice input has no
// CLI flag or launch argument (verified against `muse --help`/`muse exec
// --help`) -- settings.json's
// "tui":{"voice_enabled":...} is the only control, so every launch through
// this integration overlays it off regardless of whether an MCP config is
// also mounted: AgentWorks turns are driven by prompt injection, and a
// stray Alt+V (or the CLI's own voice-input hint rendering) has no
// legitimate role in an automated session. Anything already present under a
// colliding server name, or any other existing "tui" field, is preserved
// across the run and reinstated by restore. All file errors abort the
// launch: a half-mounted overlay is worse than no run.
func museApplyMCPConfig(configJSON string, toolAllowlist []string) (func(), error) {
	path, err := museSettingsPath()
	if err != nil {
		return nil, err
	}
	return museApplyMCPConfigAtPath(path, configJSON, toolAllowlist)
}

func museApplyMCPConfigAtPath(path, configJSON string, toolAllowlist []string) (func(), error) {
	var doc struct {
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
	}
	configJSON = strings.TrimSpace(configJSON)
	if configJSON != "" {
		if err := json.Unmarshal([]byte(configJSON), &doc); err != nil {
			return nil, fmt.Errorf("muse MCP config is not valid JSON: %w", err)
		}
	}
	previous := map[string]json.RawMessage{}
	var previousRaw []byte
	var settings map[string]json.RawMessage
	if raw, readErr := os.ReadFile(path); readErr == nil {
		previousRaw = append([]byte(nil), raw...)
		if err := json.Unmarshal(raw, &settings); err != nil {
			return nil, fmt.Errorf("existing muse settings.json is not a JSON object: %w", err)
		}
		if settings == nil {
			settings = map[string]json.RawMessage{}
		}
		if rawServers, ok := settings["mcpServers"]; ok {
			if err := json.Unmarshal(rawServers, &previous); err != nil {
				return nil, fmt.Errorf("existing muse settings.json mcpServers is not an object: %w", err)
			}
		}
	} else if !os.IsNotExist(readErr) {
		return nil, fmt.Errorf("read muse settings.json: %w", readErr)
	} else {
		settings = map[string]json.RawMessage{}
	}
	merged := make(map[string]json.RawMessage, len(previous)+len(doc.MCPServers))
	for name, entry := range previous {
		merged[name] = entry
	}
	for name, entry := range doc.MCPServers {
		name = strings.TrimSpace(name)
		if name == "" {
			return nil, fmt.Errorf("muse MCP config has an empty server name")
		}
		merged[name] = museBridgeCallLimit(entry)
	}
	if len(merged) > 0 {
		mergedRaw, err := json.Marshal(merged)
		if err != nil {
			return nil, fmt.Errorf("marshal merged muse mcpServers: %w", err)
		}
		settings["mcpServers"] = mergedRaw
	}
	if _, ok := settings["schema_version"]; !ok {
		settings["schema_version"] = json.RawMessage("1")
	}
	tui := map[string]json.RawMessage{}
	if rawTUI, ok := settings["tui"]; ok {
		if err := json.Unmarshal(rawTUI, &tui); err != nil {
			return nil, fmt.Errorf("existing muse settings.json tui is not an object: %w", err)
		}
	}
	if tui == nil {
		tui = map[string]json.RawMessage{}
	}
	tui["voice_enabled"] = json.RawMessage("false")
	tuiRaw, err := json.Marshal(tui)
	if err != nil {
		return nil, fmt.Errorf("marshal muse settings.json tui: %w", err)
	}
	settings["tui"] = tuiRaw
	// Full CLI mode passes no allowlist (the launch keeps --yolo); with the bridge mounted it still gets the
	// default-refuse policy, through the same hook, without the bridge-only launch switches.
	denyReason, shellReason := "", ""
	if toolAllowlist == nil && museHasBridgeServer(doc.MCPServers) {
		toolAllowlist = museFullAllowedTools()
		denyReason = museFullPolicyReason
		if !nativeshell.Enabled() {
			shellReason = museShellOffReason
		}
	}
	policyHookInstalled := false
	if toolAllowlist != nil {
		if _, err := exec.LookPath("node"); err != nil {
			return nil, fmt.Errorf("Muse native-tool policy requires Node.js: %w", err)
		}
		run := map[string]json.RawMessage{}
		if rawRun, ok := settings["run"]; ok {
			if err := json.Unmarshal(rawRun, &run); err != nil {
				return nil, fmt.Errorf("existing muse settings.json run is not an object: %w", err)
			}
		}
		if run == nil {
			run = map[string]json.RawMessage{}
		}
		cleaned, err := museCleanToolAllowlist(toolAllowlist)
		if err != nil {
			return nil, err
		}
		// run.toolset cannot express this policy: once it is named, Muse also
		// removes mounted MCP tools, while MCP identifiers themselves are rejected
		// as unknown names. Keep the full discovery surface and enforce the exact
		// native policy at execution time with a PreToolUse hook instead.
		delete(run, "toolset")
		// Native subagents are opt-in: allowlisting subagent_spawn turns
		// delegation on ("auto"); otherwise the tools stay hidden ("off").
		// Children run under this same settings.json hook and launch flags.
		delegation := `"off"`
		if slices.Contains(cleaned, "subagent_spawn") {
			delegation = `"auto"`
		}
		run["subagent_delegation_mode"] = json.RawMessage(delegation)
		run["workflow_trigger_mode"] = json.RawMessage(`"off"`)
		runRaw, err := json.Marshal(run)
		if err != nil {
			return nil, fmt.Errorf("marshal muse settings.json run: %w", err)
		}
		settings["run"] = runRaw

		hookPath, err := museWriteToolPolicyHookShellRedirect(cleaned, denyReason, shellReason, museHasBridgeServer(doc.MCPServers))
		if err != nil {
			return nil, err
		}
		if err := museAppendPreToolUseHook(settings, hookPath, museBridgeHostPorts(doc.MCPServers)...); err != nil {
			return nil, err
		}
		policyHookInstalled = true
	}
	// The bridge's credentials exist only in the bridge's own shell. Muse's native
	// shell has none, so a platform call made there can only fail; the hook turns
	// that failure into the instruction the agent needs (Full CLI mode has no
	// allowlist hook, so install a redirect-only hook if no policy hook was installed).
	if museHasBridgeServer(doc.MCPServers) && !policyHookInstalled {
		if _, err := exec.LookPath("node"); err == nil {
			redirectPath, err := museWriteShellRedirectHook()
			if err != nil {
				return nil, err
			}
			if err := museAppendPreToolUseHook(settings, redirectPath, museBridgeHostPorts(doc.MCPServers)...); err != nil {
				return nil, err
			}
		}
	}
	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal muse settings.json: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create muse config dir: %w", err)
	}
	if err := os.WriteFile(path, append(out, '\n'), 0o600); err != nil {
		return nil, fmt.Errorf("write muse settings.json: %w", err)
	}
	// Restore is byte-exact: the pre-run snapshot goes back verbatim (or the
	// file is removed when we created it), so key order, formatting, and
	// trailing bytes survive the round trip. Deliberately unconditional — a
	// leaked mount impersonates user intent to every later CLI run, which is
	// worse than clobbering a concurrent external edit in this window.
	return func() {
		if previousRaw == nil {
			_ = os.Remove(path)
			return
		}
		_ = os.WriteFile(path, previousRaw, 0o600)
	}, nil
}

// musePrepareIsolatedConfig gives one Muse process/session its own config
// root. Muse has no --mcp-config flag, so writing the shared user settings was
// previously the only way to mount a bridge. That breaks as soon as two runs
// overlap: each snapshots the other's temporary overlay and out-of-order
// restores can leave a dead required MCP server in ~/.config/muse forever.
//
// The isolated root copies only the login/trust material needed by the CLI and
// non-ephemeral user preferences. MCP servers and PreToolUse hooks are rebuilt
// exactly for this AgentWorks launch, so a prior crashed run cannot leak tools
// or a localhost test stub into the next one.
func musePrepareIsolatedConfig(configJSON string, toolAllowlist []string, accountSettings ...string) (string, func(), error) {
	sourceSettings, err := museSettingsPath()
	if err != nil {
		return "", nil, err
	}
	if len(accountSettings) > 0 && accountSettings[0] != "" {
		sourceSettings = accountSettings[0]
	}
	root, err := os.MkdirTemp("", "agentworks-muse-config-*")
	if err != nil {
		return "", nil, fmt.Errorf("create isolated muse config: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(root) }
	targetDir := filepath.Join(root, "muse")
	if err := os.MkdirAll(targetDir, 0o700); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("create isolated muse config directory: %w", err)
	}

	sourceDir := filepath.Dir(sourceSettings)
	for _, name := range []string{"auth.json", "trust.json"} {
		if err := copyMuseConfigFile(filepath.Join(sourceDir, name), filepath.Join(targetDir, name)); err != nil {
			cleanup()
			return "", nil, err
		}
	}

	targetSettings := filepath.Join(targetDir, "settings.json")
	if raw, readErr := os.ReadFile(sourceSettings); readErr == nil {
		var settings map[string]json.RawMessage
		if err := json.Unmarshal(raw, &settings); err != nil {
			cleanup()
			return "", nil, fmt.Errorf("existing muse settings.json is not a JSON object: %w", err)
		}
		if settings == nil {
			settings = map[string]json.RawMessage{}
		}
		delete(settings, "mcpServers")
		if rawHooks, ok := settings["hooks"]; ok {
			var hooks map[string]json.RawMessage
			if err := json.Unmarshal(rawHooks, &hooks); err != nil {
				cleanup()
				return "", nil, fmt.Errorf("existing muse settings.json hooks is not an object: %w", err)
			}
			delete(hooks, "PreToolUse")
			if len(hooks) == 0 {
				delete(settings, "hooks")
			} else {
				hooksRaw, _ := json.Marshal(hooks)
				settings["hooks"] = hooksRaw
			}
		}
		seed, err := json.MarshalIndent(settings, "", "  ")
		if err != nil {
			cleanup()
			return "", nil, fmt.Errorf("prepare isolated muse settings: %w", err)
		}
		if err := os.WriteFile(targetSettings, append(seed, '\n'), 0o600); err != nil {
			cleanup()
			return "", nil, fmt.Errorf("write isolated muse settings: %w", err)
		}
	} else if !os.IsNotExist(readErr) {
		cleanup()
		return "", nil, fmt.Errorf("read muse settings.json: %w", readErr)
	}

	if _, err := museApplyMCPConfigAtPath(targetSettings, configJSON, toolAllowlist); err != nil {
		cleanup()
		return "", nil, err
	}
	return root, cleanup, nil
}

// museHookDir is this OS user's own hook folder. One shared /tmp folder broke
// on hosts with several service accounts: the first account to run Muse owned
// it and every other account failed with "permission denied" -- and another
// account could have created it first and controlled the hook it runs. The
// folder is per user, owner-only, and refused when it is not ours.
func museHookDir() (string, error) {
	dir := filepath.Join(os.TempDir(), fmt.Sprintf("muse-cli-hooks-%d", os.Getuid()))
	// The hook is a fixed policy script, not a secret. With per-user accounts every user's Muse runs it,
	// so the folder is readable by all of them; only this account can write it.
	mode := os.FileMode(0o700)
	if slotfs.On() {
		mode = 0o755
	}
	if err := os.MkdirAll(dir, mode); err != nil {
		return "", fmt.Errorf("create muse hook dir: %w", err)
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return "", fmt.Errorf("check muse hook dir: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || !ownedByCurrentUser(info) {
		return "", fmt.Errorf("muse hook dir %s is not a directory owned by this user", dir)
	}
	if (mode == 0o700 && info.Mode().Perm()&0o077 != 0) || (mode != 0o700 && info.Mode().Perm() != mode) {
		if err := os.Chmod(dir, mode); err != nil {
			return "", fmt.Errorf("secure muse hook dir: %w", err)
		}
	}
	return dir, nil
}

func copyMuseConfigFile(source, target string) error {
	in, err := os.Open(source)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read muse config credential: %w", err)
	}
	defer in.Close()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("create isolated muse config credential: %w", err)
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return fmt.Errorf("copy muse config credential: %w", err)
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("close isolated muse config credential: %w", err)
	}
	return nil
}

func museEnvironmentWithConfigHome(configHome string) []string {
	environment := make([]string, 0, len(os.Environ())+1)
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if key != "XDG_CONFIG_HOME" {
			environment = append(environment, entry)
		}
	}
	return append(environment, "XDG_CONFIG_HOME="+configHome)
}

var museToolNamePattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

func museCleanToolAllowlist(toolAllowlist []string) ([]string, error) {
	cleaned := make([]string, 0, len(toolAllowlist))
	seen := make(map[string]bool, len(toolAllowlist))
	for _, name := range toolAllowlist {
		name = strings.TrimSpace(name)
		if name == "" {
			return nil, fmt.Errorf("muse tool allowlist has an empty tool name")
		}
		if !museToolNamePattern.MatchString(name) {
			return nil, fmt.Errorf("muse tool allowlist has invalid tool name %q", name)
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		cleaned = append(cleaned, name)
	}
	return cleaned, nil
}

// museWriteToolPolicyHook writes a content-addressed JavaScript hook. The
// AgentWorks server always installs Node with its coding-agent CLIs, and a real
// JSON parser is important here: text extraction could be bypassed by a nested
// tool_name-like string in model-controlled arguments. Muse's
// named run.toolset suppresses MCP tools as well as native tools, so the hook
// is the supported way to keep MCP routing available while denying native
// execution. MCP tools, Muse's MCP discovery gateway, and the runtime-only
// reminder verdict sink are always admitted; the latter cannot access user
// data or the host, and denying it makes Muse's observer retry indefinitely.
// The caller controls the exact native work-tool allowlist (web_search in
// mcpagent).
func museWriteToolPolicyHook(nativeAllowed []string, denyReason string) (string, error) {
	return museWriteToolPolicyHookShell(nativeAllowed, denyReason, "")
}

// museWriteToolPolicyHookShell is museWriteToolPolicyHook with a separate refusal for the shell tools, so a
// refused bash names the tool to use instead of the generic reason.
func museWriteToolPolicyHookShell(nativeAllowed []string, denyReason, shellReason string) (string, error) {
	return museWriteToolPolicyHookShellRedirect(nativeAllowed, denyReason, shellReason, false)
}

// Share one Node process and one payload read when both checks are installed.
func museWriteToolPolicyHookShellRedirect(nativeAllowed []string, denyReason, shellReason string, redirect bool) (string, error) {
	allowed := append([]string(nil), nativeAllowed...)
	allowed = append(allowed, "tool_search", "submit_reminder_decision")
	allowedJSON, err := json.Marshal(allowed)
	if err != nil {
		return "", fmt.Errorf("marshal muse hook allowlist: %w", err)
	}
	reason := denyReason
	if reason == "" {
		reason = "Muse internal tools are disabled for this session; use web search or an AgentWorks MCP tool."
	}
	reasonBytes, err := json.Marshal(reason)
	if err != nil {
		return "", fmt.Errorf("marshal muse hook reason: %w", err)
	}
	reasonJSON := string(reasonBytes)
	shellBranch := ""
	if shellReason != "" {
		shellBytes, err := json.Marshal(shellReason)
		if err != nil {
			return "", fmt.Errorf("marshal muse hook shell reason: %w", err)
		}
		toolsBytes, _ := json.Marshal(museShellTools)
		shellBranch = "const reason = new Set(" + string(toolsBytes) + ").has(name) ? " + string(shellBytes) + " : " + reasonJSON + ";\n"
		reasonJSON = "reason"
	}
	body := museHookPayloadScript +
		"const allowed = new Set(" + string(allowedJSON) + ");\n" +
		"if (!allowed.has(name) && !name.startsWith('mcp__')) {\n" +
		shellBranch +
		"process.stdout.write(JSON.stringify({hookSpecificOutput:{hookEventName:'PreToolUse',permissionDecision:'deny',permissionDecisionReason:" + reasonJSON + "}}) + '\\n');\n" +
		"process.exit(0);\n}\n"
	if redirect {
		body += museShellRedirectBody
	}

	digest := sha256.Sum256([]byte(body))
	dir, err := museHookDir()
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, fmt.Sprintf("native-tool-policy-%x.js", digest[:8]))
	// Publish atomically: another launch may be executing this same hook.
	// Truncating it in place would briefly turn the policy into an empty script.
	tmp, err := os.CreateTemp(dir, ".native-tool-policy-*")
	if err != nil {
		return "", fmt.Errorf("create muse native-tool policy hook: %w", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(body); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("write muse native-tool policy hook: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("close muse native-tool policy hook: %w", err)
	}
	if slotfs.On() {
		if err := os.Chmod(tmp.Name(), 0o644); err != nil {
			return "", fmt.Errorf("share muse native-tool policy hook: %w", err)
		}
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return "", fmt.Errorf("publish muse native-tool policy hook: %w", err)
	}
	return path, nil
}

// museBridgeCallSeconds is how long the bridge may spend on one call. Muse ends any tool call after 300 s and then
// drops that MCP connection for the rest of the process (every later call fails with "MCP stdio connection is
// closed"), so the bridge must answer first: a slow call then ends as an ordinary tool error.
const museBridgeCallSeconds = "270"

// museBridgeCallLimit adds MCP_BRIDGE_MAX_CALL_SECONDS to an MCP server entry's env unless it already sets one.
// Entries that are not objects, or have no command (url servers), are returned unchanged.
func museBridgeCallLimit(entry json.RawMessage) json.RawMessage {
	var server map[string]json.RawMessage
	if err := json.Unmarshal(entry, &server); err != nil {
		return entry
	}
	if _, hasCommand := server["command"]; !hasCommand {
		return entry
	}
	env := map[string]string{}
	if raw, ok := server["env"]; ok {
		if err := json.Unmarshal(raw, &env); err != nil {
			return entry
		}
	}
	if _, set := env["MCP_BRIDGE_MAX_CALL_SECONDS"]; set {
		return entry
	}
	if _, isBridge := env["MCP_API_URL"]; !isBridge {
		return entry
	}
	env["MCP_BRIDGE_MAX_CALL_SECONDS"] = museBridgeCallSeconds
	rawEnv, err := json.Marshal(env)
	if err != nil {
		return entry
	}
	server["env"] = rawEnv
	out, err := json.Marshal(server)
	if err != nil {
		return entry
	}
	return out
}

// museAppendPreToolUseHook adds a node command hook for every tool to settings.hooks.PreToolUse,
// keeping whatever the person already configured there.
func museAppendPreToolUseHook(settings map[string]json.RawMessage, hookPath string, args ...string) error {
	hooks := map[string]json.RawMessage{}
	if rawHooks, ok := settings["hooks"]; ok {
		if err := json.Unmarshal(rawHooks, &hooks); err != nil {
			return fmt.Errorf("existing muse settings.json hooks is not an object: %w", err)
		}
	}
	if hooks == nil {
		hooks = map[string]json.RawMessage{}
	}
	var preToolUse []json.RawMessage
	if rawPreToolUse, ok := hooks["PreToolUse"]; ok {
		if err := json.Unmarshal(rawPreToolUse, &preToolUse); err != nil {
			return fmt.Errorf("existing muse settings.json hooks.PreToolUse is not an array: %w", err)
		}
	}
	command := shellQuoteMuseArg(museHookNode()) + " '" + strings.ReplaceAll(hookPath, "'", "'\\''") + "'"
	for _, arg := range args {
		command += " '" + strings.ReplaceAll(arg, "'", "'\\''") + "'"
	}
	hook, err := json.Marshal(map[string]interface{}{
		"matcher": "*",
		"hooks": []map[string]interface{}{
			{
				"type":    "command",
				"command": command,
				"timeout": 5,
			},
		},
	})
	if err != nil {
		return fmt.Errorf("marshal muse PreToolUse hook: %w", err)
	}
	preToolUseRaw, err := json.Marshal(append(preToolUse, hook))
	if err != nil {
		return fmt.Errorf("marshal muse settings.json hooks.PreToolUse: %w", err)
	}
	hooks["PreToolUse"] = preToolUseRaw
	hooksRaw, err := json.Marshal(hooks)
	if err != nil {
		return fmt.Errorf("marshal muse settings.json hooks: %w", err)
	}
	settings["hooks"] = hooksRaw
	return nil
}

// museBridgeHostPorts lists the platform's own host:port values the mounted bridge talks to. Muse starts
// hooks with a scrubbed environment (proven live: no MCP_* variable reaches them), so the hosts travel as
// command-line arguments. A host and port are not secrets; no token is ever passed.
func museBridgeHostPorts(servers map[string]json.RawMessage) []string {
	var out []string
	for _, entry := range servers {
		var server struct {
			Env map[string]string `json:"env"`
		}
		if json.Unmarshal(entry, &server) != nil {
			continue
		}
		if _, ok := server.Env["MCP_API_URL"]; !ok {
			continue
		}
		for _, key := range []string{"MCP_API_URL", "MCP_BRIDGE_API_URL", "MCP_AGENT_SERVER_URL"} {
			if u, err := url.Parse(strings.TrimSpace(server.Env[key])); err == nil && u.Host != "" && !slices.Contains(out, u.Host) {
				out = append(out, u.Host)
			}
		}
	}
	slices.Sort(out)
	return out
}

// museHasBridgeServer reports whether the mounted servers include the AgentWorks bridge (the entry
// whose env carries MCP_API_URL, as museBridgeCallLimit recognises it).
func museHasBridgeServer(servers map[string]json.RawMessage) bool {
	for _, entry := range servers {
		var server struct {
			Env map[string]string `json:"env"`
		}
		if json.Unmarshal(entry, &server) == nil {
			if _, ok := server.Env["MCP_API_URL"]; ok {
				return true
			}
		}
	}
	return false
}

// museResumeBridgeNote is delivered once with the first message of a resumed native conversation when the
// bridge is mounted: the resumed history may say there is no bridge, which is out of date.
const museResumeBridgeNote = "[AgentWorks note: the platform bridge is mounted for this run. Any earlier statement in this conversation that there is no bridge is out of date. Platform tools are the mcp__api_bridge__* tools in your tool list; call platform APIs through mcp__api_bridge__execute_shell_command, never in native bash.]"

// museMCPJSONHasBridge reports whether an MCP config document mounts the AgentWorks bridge.
func museMCPJSONHasBridge(mcpJSON string) bool {
	var doc struct {
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
	}
	return strings.TrimSpace(mcpJSON) != "" && json.Unmarshal([]byte(mcpJSON), &doc) == nil && museHasBridgeServer(doc.MCPServers)
}

// museWithResumeNote puts the note ahead of the user's text for a resumed run with the bridge mounted.
func museWithResumeNote(human string, resumed bool, mcpJSON string) string {
	if !resumed || !museMCPJSONHasBridge(mcpJSON) {
		return human
	}
	return museResumeBridgeNote + "\n\n" + human
}

// museStripResumeNote removes the note museWithResumeNote prepended, so a user message read back from the Muse
// transcript is what the person (or an automatic notification) actually sent. The note is for the model only.
func museStripResumeNote(text string) string {
	return strings.TrimPrefix(text, museResumeBridgeNote+"\n\n")
}

// museShellRedirectScript refuses a native bash/monitor/web_fetch call that targets the platform API and names the
// tool that can make it, and refuses cron_create outright (scheduling belongs to the platform). It
// blocks nothing else, reveals no secret and grants nothing.
const museHookPayloadScript = `const fs = require('fs');
let payload = {};
try { payload = JSON.parse(fs.readFileSync(0, 'utf8') || '{}'); } catch (_) {}
const name = payload && typeof payload.tool_name === 'string' ? payload.tool_name : '';
`

const museShellRedirectScript = museHookPayloadScript + museShellRedirectBody

const museShellRedirectBody = `const input = payload && payload.tool_input;
function deny(reason) {
  process.stdout.write(JSON.stringify({hookSpecificOutput:{hookEventName:'PreToolUse',permissionDecision:'deny',permissionDecisionReason:reason}}) + '\n');
  process.exit(0);
}
const platformRoute = /\/tools\/(custom|virtual|mcp)\//;
if (name === 'cron_create') {
  deny("Scheduling is done through the platform's Schedules, not with cron_create: a Muse cron runs unattended outside the platform's schedule controls and only while this session lives. Use the platform's schedule tools or ask the user.");
}
const bridgeReason = 'Refused here: native tools have no platform credentials and no route to the platform. The variables MCP_AUTH, MCP_CUSTOM, MCP_MCP and MCP_API_TOKEN exist only inside the bridge shell, so their absence in this shell is expected and does NOT mean the bridge is missing. The tools in your tool list named mcp__api_bridge__* are the working bridge. Run platform calls through mcp__api_bridge__execute_shell_command (its shell has the credentials). To find a platform tool use mcp__api_bridge__search_tools, then read its schema and route with mcp__api_bridge__get_api_spec.';
const credVar = /\bMCP_(CUSTOM|AUTH|MCP|API_TOKEN)\b/;
const envRead = /(^|[;&|(\x60]|\$\()\s*env\s*($|[;&|)\x60])|\bprintenv\b|\$\{!|\[\[?\s+-[nz]\b|\btest\s+-[nz]\b|\bcompgen\s+-[ev]\b|\b(declare|typeset)\s+-[pxg]+\b|\bexport\s+-p\b|\bos\.environ\b|\bos\.getenv\b|\bprocess\.env\b|\/proc\/[^\s]*\/environ|\b(echo|printf)\b[^\n]*\$/;
const hostPorts = process.argv.slice(2).filter(function (a) { return a.length > 0; });
for (const key of ['MCP_API_URL', 'MCP_BRIDGE_API_URL', 'MCP_AGENT_SERVER_URL']) {
  const raw = process.env[key];
  if (!raw) continue;
  try { const h = new URL(raw).host; if (h) hostPorts.push(h); } catch (_) {}
}
function targetsPlatformHost(text) {
  return hostPorts.some(function (h) { return text.indexOf(h) !== -1; });
}
function probesCredentials(command) {
  return credVar.test(command) && envRead.test(command);
}
if (name === 'bash' || name === 'bash_input' || name === 'monitor') {
  const command = input && typeof input.command === 'string' ? input.command : JSON.stringify(input || {});
  if (/\$\{?MCP_(CUSTOM|AUTH|MCP|API_TOKEN)\b/.test(command) || platformRoute.test(command) || targetsPlatformHost(command) || probesCredentials(command)) deny(bridgeReason);
} else if (name === 'web_fetch') {
  const url = input && typeof input.url === 'string' ? input.url : JSON.stringify(input || {});
  let hit = platformRoute.test(url) || targetsPlatformHost(url);
  if (hit) deny(bridgeReason);
}
`

func museWriteShellRedirectHook() (string, error) {
	digest := sha256.Sum256([]byte(museShellRedirectScript))
	dir, err := museHookDir()
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, fmt.Sprintf("shell-redirect-%x.js", digest[:8]))
	tmp, err := os.CreateTemp(dir, ".shell-redirect-*")
	if err != nil {
		return "", fmt.Errorf("create muse shell redirect hook: %w", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(museShellRedirectScript); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("write muse shell redirect hook: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("close muse shell redirect hook: %w", err)
	}
	if slotfs.On() {
		if err := os.Chmod(tmp.Name(), 0o644); err != nil {
			return "", fmt.Errorf("share muse shell redirect hook: %w", err)
		}
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return "", fmt.Errorf("publish muse shell redirect hook: %w", err)
	}
	return path, nil
}

// museHookNode is the node that runs Muse's PreToolUse hooks: an absolute path
// any account can execute. On a slot host Muse runs as the person's own Linux
// account, and the PATH node there was the service account's nvm install,
// which slots cannot enter, so every hook failed with "Permission denied" and
// the tool policy never ran (Excellence, 2026-10-07). "node" is the fallback
// when no such path exists.
func museHookNode() string {
	candidates := []string{}
	if found, err := exec.LookPath("node"); err == nil {
		if abs, err := filepath.Abs(found); err == nil {
			candidates = append(candidates, abs)
		}
	}
	candidates = append(candidates, "/usr/local/bin/node", "/usr/bin/node")
	for _, candidate := range candidates {
		if everyoneCanExecute(candidate) {
			return candidate
		}
	}
	return "node"
}

// everyoneCanExecute reports whether a file is executable by any account:
// the file has o+x and every folder above it has o+x.
func everyoneCanExecute(file string) bool {
	info, err := os.Stat(file)
	if err != nil || info.IsDir() || info.Mode().Perm()&0o001 == 0 {
		return false
	}
	for dir := filepath.Dir(file); ; dir = filepath.Dir(dir) {
		dirInfo, err := os.Stat(dir)
		if err != nil || dirInfo.Mode().Perm()&0o001 == 0 {
			return false
		}
		if parent := filepath.Dir(dir); parent == dir {
			return true
		}
	}
}

func shellQuoteMuseArg(value string) string {
	if value == "node" {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
