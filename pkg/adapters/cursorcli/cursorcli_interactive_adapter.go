package cursorcli

import (
	"context"

	"encoding/json"

	"fmt"
	"github.com/manishiitg/multi-llm-provider-go/pkg/projectfile"

	"os"
	"os/exec"
	"path/filepath"

	"sort"
	"strconv"
	"strings"

	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"

	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/nativeshell"

	"github.com/manishiitg/multi-llm-provider-go/pkg/codingtimeout"
	"github.com/manishiitg/multi-llm-provider-go/pkg/pathidentity"
)

const (
	// Default to no provider-level turn timeout. Workflow/background callers own
	// their execution deadline; the adapter should not cancel a still-running tmux
	// coding agent before the outer workflow timeout.
	defaultCursorInteractiveTimeout     = 0
	defaultCursorInteractiveIdleTimeout = codingtimeout.DefaultPersistentSessionIdle
	defaultCursorInteractiveRetention   = 30 * time.Minute
	cursorInteractiveStableWindow       = 1200 * time.Millisecond
	cursorFinalAnswerRecoveryDelay      = 3 * time.Second
	cursorFinalAnswerRecoveryPrompt     = "FINAL_ANSWER_RECOVERY: The previous tool sequence returned without a final response. If the requested work is incomplete, continue it now and use tools as needed. Otherwise reply only with the requested final answer. Do not repeat side effects that already completed."
	cursorBootBannerPromptGrace         = 2 * time.Second
	cursorRestoredBootBannerPromptGrace = 8 * time.Second
	cursorRestoredSessionMinReadyAge    = 10 * time.Second
	// Cursor keeps the normal composer painted underneath its workspace-trust
	// overlay. Immediately after we choose "Trust this workspace", that composer
	// can therefore look ready while Cursor is still applying trust. Typing the
	// first workflow prompt in that window makes Cursor drop the turn and return
	// to an empty prompt. Hold startup through the transition, then require the
	// normal consecutive-ready debounce below before delivering user input.
	cursorWorkspaceTrustReadyGrace = 3 * time.Second
	// Hard cap on how long we wait for the CLI to show ANY activity after the
	// prompt is submitted. A live turn shows "Thinking…"/streaming within
	// seconds; if nothing appears within this window the input never reached the
	// pane (paste/Enter swallowed during launch), and without this cap the
	// response loop spins forever because every completion branch is gated
	// behind sawActivity and the call context has no deadline by default.
	defaultCursorInteractiveFirstActivityTimeout = 90 * time.Second
	// A byte-stable pane is not proof that Cursor finished: long reasoning and
	// tool calls can legitimately leave the TUI unchanged. Keep this recovery
	// policy opt-in instead of returning a partial response after a fixed delay.
	defaultCursorInteractiveStalePaneBackstop = 0

	EnvCursorInteractiveSessionPrefix               = "CURSOR_CLI_INTERACTIVE_SESSION_PREFIX"
	EnvCursorInteractiveTimeoutSeconds              = "CURSOR_CLI_INTERACTIVE_TIMEOUT_SECONDS"
	EnvCursorInteractiveIdleTimeoutSeconds          = "CURSOR_CLI_INTERACTIVE_IDLE_TIMEOUT_SECONDS"
	EnvCursorInteractivePromptWaitSeconds           = "CURSOR_CLI_INTERACTIVE_PROMPT_WAIT_SECONDS"
	EnvCursorInteractiveFirstActivityTimeoutSeconds = "CURSOR_CLI_INTERACTIVE_FIRST_ACTIVITY_TIMEOUT_SECONDS"
	EnvCursorInteractiveStalePaneBackstopSeconds    = "CURSOR_CLI_INTERACTIVE_STALE_PANE_BACKSTOP_SECONDS"
)

// cursorBridgeOnlyDeniedTools are the Cursor hook tool names denied in
// bridge-only mode. Names come from the cursor-agent bundle's toolName
// registry (2026.09.18): Delete, List, WriteShellStdin, ComputerUse,
// RecordScreen, generateImage and lowercase task were missing, so a native
// Delete fell through to Cursor's own approval prompt and stalled the turn
// (reproduced live 2026-09-24). Todos (updateTodos), plans (createPlan) and
// read-only diagnostics stay allowed.
var cursorBridgeOnlyDeniedTools = []string{
	"Shell",
	"WriteShellStdin",
	"Read",
	"List",
	"ListDir",
	"Glob",
	"Grep",
	"Search",
	"Edit",
	"Write",
	"Delete",
	"ComputerUse",
	"RecordScreen",
	"generateImage",
	"task",
	"Task",
	"Agent",
	"Subagent",
	"BackgroundAgent",
	"CloudAgent",
	"Delegate",
}

// cursorFullNativeAllowedTools run natively in Full CLI ("Native agent tools")
// mode, inside AgentWorks' confinement. Delete stays denied: Cursor's own
// Delete always asks "Delete this file?" and nothing pre-approves it, so the
// turn would stop (live 2026-10-03); the shell (rm) deletes instead. Every
// other name above stays denied too:
// computer use, screen recording, image generation, and every subagent,
// background, cloud or delegated agent (they run outside this session's
// confinement and do not reliably inherit the bridge).
var cursorFullNativeAllowedTools = map[string]bool{
	"Shell": true, "WriteShellStdin": true,
	"Read": true, "List": true, "ListDir": true, "Glob": true, "Grep": true, "Search": true,
	"Edit": true, "Write": true,
}

// cursorShellTools are Cursor's command-execution tools. In Full mode they are
// denied unless the native-shell escape hatch is on (PLAT-491): the bridge
// shell (execute_shell_command) runs as the user's slot, Cursor's own shell
// runs as the app account.
var cursorShellTools = map[string]bool{"Shell": true, "WriteShellStdin": true}

func cursorBridgeOnlyDeniedToolMatcher() string {
	return strings.Join(cursorBridgeOnlyDeniedTools, "|")
}

func cursorDeniedToolMatcher(fullNative bool) string {
	if !fullNative {
		return cursorBridgeOnlyDeniedToolMatcher()
	}
	denied := make([]string, 0, len(cursorBridgeOnlyDeniedTools))
	for _, tool := range cursorBridgeOnlyDeniedTools {
		if !cursorFullNativeAllowedTools[tool] || (cursorShellTools[tool] && !nativeshell.Enabled()) {
			denied = append(denied, tool)
		}
	}
	return strings.Join(denied, "|")
}

// cursorBridgeOnlySystemPrompt adds the session rules. flags[0]: Cursor's own tools are on (Full CLI);
// flags[1]: the SDK lane runs Task subagents inside this session (children keep its tool limits).
func cursorBridgeOnlySystemPrompt(systemPrompt string, denyBuiltin bool, flags ...bool) string {
	if !denyBuiltin {
		return systemPrompt
	}
	guidance := strings.TrimSpace(`Cursor bridge-only session rules:
- Do not start Cursor subagents, background agents, cloud agents, workers, delegated agents, or request a mode switch. Nested Cursor agents do not reliably inherit the api-bridge MCP config and can stall on interactive mode-switch prompts.
- Complete the task in this same Cursor session using the api-bridge MCP tools.
- Built-in filesystem, shell, edit, search, and delegation tools are intentionally denied by the orchestrator.`)
	if len(flags) > 0 && flags[0] {
		tools := "Your own Read, List, Glob, Grep, Search, Edit and Write tools are enabled; use them directly for files. Native Shell and the Delete tool are disabled; use a permitted api-bridge MCP shell for commands and file deletion."
		if nativeshell.Enabled() {
			tools = "Your own Shell, Read, List, Glob, Grep, Search, Edit and Write tools are enabled; use them directly for files and commands (delete files with the shell, e.g. rm; the Delete tool is denied)."
		}
		agents := "Do not start Cursor subagents, background agents, cloud agents, workers, delegated agents, or request a mode switch. Complete the task in this same session."
		if len(flags) > 1 && flags[1] {
			agents = "You may use your Task tool for a focused subtask; each subagent appears to the person as its own child chat. Run subagents in the foreground only: never background or cloud agents, and never request a mode switch."
		}
		guidance = strings.TrimSpace(`Cursor session rules:
- ` + tools + ` They run inside this session's sandbox: only the folders granted to this chat are reachable, and protected files (a workflow's planning/, its raw database, instruction files) are refused even inside them.
- Use the api-bridge MCP tools for platform actions, integrations, the workflow database and anything outside your folders.
- Computer use, screen recording and image tools are denied. ` + agents + `
- Do not use Cursor's own goal tools (CreateGoal, UpdateGoal). Goals and scheduling belong to the platform. Cursor's own AskQuestion is not connected to chat: to ask the person, use the request_clarification tool when it is offered, otherwise ask in plain text. A chat nobody is watching must never wait on a question.`)
	}
	if strings.TrimSpace(systemPrompt) == "" {
		return guidance
	}
	if strings.Contains(systemPrompt, guidance) {
		return systemPrompt
	}
	return strings.TrimRight(systemPrompt, "\n") + "\n\n" + guidance
}

func prepareCursorProjectFiles(workingDir, systemPrompt string, opts *llmtypes.CallOptions, ownerSessionID string) (func(), error) {
	cleanups := make([]func(), 0, 3)
	addCleanup := func(cleanup func()) {
		if cleanup != nil {
			cleanups = append(cleanups, cleanup)
		}
	}
	cleanupAll := func() {
		for i := len(cleanups) - 1; i >= 0; i-- {
			cleanups[i]()
		}
	}

	denyBuiltin := false
	if opts != nil && opts.Metadata != nil && opts.Metadata.Custom != nil {
		denyBuiltin, _ = opts.Metadata.Custom[MetadataKeyDenyBuiltinTools].(bool)
	}
	fullNative := cursorFullNativeFromOptions(opts)
	systemPrompt = cursorBridgeOnlySystemPrompt(systemPrompt, denyBuiltin, fullNative, fullNative && cursorMetaBool(opts, metadataKeySDKSubagents))

	// cursor-agent uses the git project root for .cursor/mcp.json discovery.
	// An empty .git/ directory is not enough for the interactive TUI: when the
	// workflow folder sits inside a parent repo, Cursor walks up to that parent
	// and loads the wrong .cursor/mcp.json. Create a tiny real git repo marker
	// in the workflow folder so Cursor treats this folder as the project root.
	// workspace-docs is gitignored upstream, and cleanup removes the marker on
	// session end.
	if !llmtypes.CLIHasPrivateRuntime(opts) {
		releaseGit, gitErr := acquireCursorGitMarker(workingDir)
		if gitErr != nil {
			cleanupAll()
			return nil, gitErr
		}
		addCleanup(releaseGit)
	}

	cursorDir := filepath.Join(workingDir, ".cursor")
	addCleanup(func() { _ = os.Remove(cursorDir) }) // Only remove when empty, after per-file cleanup.
	if strings.TrimSpace(systemPrompt) != "" {
		rulesDir := filepath.Join(cursorDir, "rules")
		if err := os.MkdirAll(rulesDir, 0o755); err != nil {
			cleanupAll()
			return nil, fmt.Errorf("failed to create Cursor rules dir: %w", err)
		}
		// Fixed filename shared by every Cursor session in this folder, so
		// sessions are counted: the file is removed only when the last one
		// ends, and a file that was there before is restored.
		rulePath := filepath.Join(rulesDir, "mlp-system.mdc")
		content := "---\nalwaysApply: true\n---\n\n" + systemPrompt
		token, err := projectfile.AcquireOwnedLease(rulePath, []byte(content))
		if err != nil {
			cleanupAll()
			return nil, err
		}
		addCleanup(func() { projectfile.ReleaseToken(token) })
	}

	if opts != nil && opts.Metadata != nil && opts.Metadata.Custom != nil {
		configJSON, callerSuppliedCLI := opts.Metadata.Custom[MetadataKeyProjectConfig].(string)
		callerSuppliedCLI = callerSuppliedCLI && strings.TrimSpace(configJSON) != ""
		if callerSuppliedCLI {
			// ACP enforces CLI permissions, not the old TUI hooks. Preserve the
			// caller's settings while applying the same native-tool restrictions.
			var config map[string]any
			if err := json.Unmarshal([]byte(configJSON), &config); err != nil || config == nil {
				cleanupAll()
				return nil, fmt.Errorf("cursor project config must be a JSON object")
			}
			permissions, _ := config["permissions"].(map[string]any)
			if permissions == nil {
				permissions = map[string]any{}
			}
			deny, _ := permissions["deny"].([]any)
			for _, rule := range cursorNativeDenyPermissions(fullNative, denyBuiltin) {
				deny = append(deny, rule)
			}
			permissions["deny"] = deny
			config["permissions"] = permissions
			encoded, _ := json.Marshal(config)
			configJSON = string(encoded)
			cleanup, err := writeCursorRestoredFile(filepath.Join(cursorDir, "cli.json"), []byte(configJSON))
			if err != nil {
				cleanupAll()
				return nil, err
			}
			addCleanup(cleanup)
		}
		// An older adapter generated a deny-only .cursor/cli.json that hides the
		// bridge tools. A cli.json in the project is never deleted here: when a
		// session has bridge tools its own allowlist replaces the file for the
		// session and the original is restored afterwards.
		if mcpJSON, ok := opts.Metadata.Custom[MetadataKeyMCPConfig].(string); ok && strings.TrimSpace(mcpJSON) != "" {
			normalizedMCPJSON, err := normalizeCursorMCPConfigForCLI(mcpJSON)
			if err != nil {
				cleanupAll()
				return nil, err
			}
			// The bridge token goes to a private file, not the workspace
			// (cursorcli_bridge_token.go). The ACP session's bridge may
			// restart, so the file stays for the session.
			normalizedMCPJSON, _, err = externalizeCursorBridgeTokens(normalizedMCPJSON, workingDir)
			if err != nil {
				cleanupAll()
				return nil, err
			}
			cleanup, err := writeCursorRestoredFile(filepath.Join(cursorDir, "mcp.json"), []byte(normalizedMCPJSON))
			if err != nil {
				cleanupAll()
				return nil, err
			}
			addCleanup(cleanup)
			if !callerSuppliedCLI {
				allowlistJSON, ok, err := cursorMCPAllowlistCLIConfig(normalizedMCPJSON, cursorFullNativeFromOptions(opts), denyBuiltin)
				if err != nil {
					cleanupAll()
					return nil, err
				}
				if ok {
					cleanup, err := writeCursorRestoredFile(filepath.Join(cursorDir, "cli.json"), []byte(allowlistJSON))
					if err != nil {
						cleanupAll()
						return nil, err
					}
					addCleanup(cleanup)
				}
			}
		}
		// Full CLI without a bridge config still needs Cursor's own tools
		// pre-approved, or it stops on its own permission prompt.
		if mcpJSON, _ := opts.Metadata.Custom[MetadataKeyMCPConfig].(string); (fullNative || denyBuiltin) && strings.TrimSpace(mcpJSON) == "" && !callerSuppliedCLI {
			nativeJSON, _, err := cursorMCPAllowlistCLIConfig(`{"mcpServers":{}}`, fullNative, denyBuiltin)
			if err != nil {
				cleanupAll()
				return nil, err
			}
			cleanup, err := writeCursorRestoredFile(filepath.Join(cursorDir, "cli.json"), []byte(nativeJSON))
			if err != nil {
				cleanupAll()
				return nil, err
			}
			addCleanup(cleanup)
		}
		if denyBuiltin {
			cleanup, err := writeCursorDenyBuiltinHooksWithBridge(cursorDir, fullNative, cursorBridgeServerNameFromOptions(opts))
			if err != nil {
				cleanupAll()
				return nil, err
			}
			addCleanup(cleanup)
		}
	}

	return cleanupAll, nil
}

func cursorWorkingDirIsGitRoot(workingDir string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", workingDir, "rev-parse", "--show-toplevel")
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	root := strings.TrimSpace(string(out))
	if root == "" {
		return false
	}
	return pathidentity.Same(root, workingDir)
}

func initCursorWorkspaceGitMarker(workingDir string) error {
	gitDir := filepath.Join(workingDir, ".git")
	if err := os.Mkdir(gitDir, 0700); err != nil {
		return fmt.Errorf("refuse to replace existing cursor git marker: %w", err)
	}
	if _, err := exec.LookPath("git"); err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "git", "init", "-q")
		cmd.Dir = workingDir
		if out, runErr := cmd.CombinedOutput(); runErr == nil {
			return nil
		} else {
			_ = out
		}
	}
	if err := os.MkdirAll(filepath.Join(gitDir, "refs", "heads"), 0o755); err != nil {
		return fmt.Errorf("create cursor git marker refs: %w", err)
	}
	if err := os.WriteFile(filepath.Join(gitDir, "HEAD"), []byte("ref: refs/heads/main\n"), 0o600); err != nil {
		return fmt.Errorf("write cursor git marker HEAD: %w", err)
	}
	config := "[core]\n\trepositoryformatversion = 0\n\tfilemode = true\n\tbare = false\n\tlogallrefupdates = true\n"
	if err := os.WriteFile(filepath.Join(gitDir, "config"), []byte(config), 0o600); err != nil {
		return fmt.Errorf("write cursor git marker config: %w", err)
	}
	return nil
}

func cursorMCPServerNames(mcpJSON string) ([]string, error) {
	normalized, err := normalizeCursorMCPConfigForCLI(mcpJSON)
	if err != nil {
		return nil, err
	}
	var parsed struct {
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(normalized), &parsed); err != nil {
		return nil, fmt.Errorf("cursor MCP config is not valid JSON: %w", err)
	}
	names := make([]string, 0, len(parsed.MCPServers))
	for name := range parsed.MCPServers {
		if strings.TrimSpace(name) != "" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names, nil
}

func normalizeCursorMCPConfigForCLI(mcpJSON string) (string, error) {
	var root map[string]interface{}
	if err := json.Unmarshal([]byte(mcpJSON), &root); err != nil {
		return "", fmt.Errorf("cursor MCP config is not valid JSON: %w", err)
	}
	serversRaw, _ := root["mcpServers"].(map[string]interface{})
	for _, serverRaw := range serversRaw {
		server, ok := serverRaw.(map[string]interface{})
		if !ok {
			continue
		}
		if _, hasType := server["type"]; hasType {
			continue
		}
		if _, hasCommand := server["command"]; hasCommand {
			server["type"] = "stdio"
		}
	}
	out, err := json.Marshal(root)
	if err != nil {
		return "", fmt.Errorf("normalize cursor MCP config: %w", err)
	}
	return string(out), nil
}

// cursorFullNativePermissions pre-approve Cursor's own shell, reads and writes
// in Full CLI mode. Cursor asks before every one of them otherwise (a hook
// "allow" does not answer its own permission prompt), and nobody is there to
// answer, so the turn stopped with no output.
var cursorFullNativePermissions = []string{"Shell(*)", "Read(**)", "Write(**)"}

// cursorFullNativeAllow is cursorFullNativePermissions without Shell(*) when
// the built-in shell is off.
func cursorFullNativeAllow() []string {
	if nativeshell.Enabled() {
		return cursorFullNativePermissions
	}
	return []string{"Read(**)", "Write(**)"}
}

func cursorNativeDenyPermissions(full, denyBuiltin bool) []string {
	if denyBuiltin && !full {
		return []string{"Shell(*)", "Write(**)", "Read(**)"}
	}
	if full && !nativeshell.Enabled() {
		return []string{"Shell(*)"}
	}
	return []string{}
}

func cursorMCPAllowlistCLIConfig(mcpJSON string, fullNative ...bool) (string, bool, error) {
	names, err := cursorMCPServerNames(mcpJSON)
	if err != nil {
		return "", false, err
	}
	full := len(fullNative) > 0 && fullNative[0]
	if len(names) == 0 && !full && !(len(fullNative) > 1 && fullNative[1]) {
		return "", false, nil
	}
	allow := make([]string, 0, len(names)+len(cursorFullNativePermissions))
	for _, name := range names {
		allow = append(allow, fmt.Sprintf("Mcp(%s:*)", name))
	}
	if full {
		allow = append(allow, cursorFullNativeAllow()...)
	}
	deny := cursorNativeDenyPermissions(full, len(fullNative) > 1 && fullNative[1])
	out, err := json.Marshal(map[string]interface{}{
		"permissions": map[string]interface{}{
			"allow": allow,
			"deny":  deny,
		},
	})
	if err != nil {
		return "", false, fmt.Errorf("build cursor MCP allowlist config: %w", err)
	}
	return string(out), true, nil
}

// writeCursorDenyBuiltinHooks installs a .cursor/hooks.json + deny script
// that blocks cursor's built-in filesystem, shell, edit, search, and
// delegation tools via cursor's hook system
// (https://cursor.com/docs/hooks). The model is forced to call the MCP
// bridge instead (api-bridge.execute_shell_command, api-bridge.read_file)
// when the orchestrator has injected the bridge mcp.json.
//
// Cleanup restores any pre-existing hooks.json the operator had in their
// workspace and removes our deny script + the hooks/ subdir if we created
// them. Order matters: write-then-restore composes cleanly with the rest
// of prepareCursorProjectFiles's cleanup stack.
// cursorBeforeReadFileHook denies native file reads in bridge-only mode; in
// Full CLI mode reads are allowed, so the hook is not installed.
func cursorBeforeReadFileHook(fullNative bool) string {
	if fullNative {
		return ""
	}
	return `,
    "beforeReadFile": [{"command": "./.cursor/hooks/mlp-deny-builtin.sh", "failClosed": true}]`
}

// cursorShellHookCommand answers Cursor's shell approval: denied in
// bridge-only mode; in Full CLI mode approved by a small allow script (the
// command runs inside AgentWorks' confinement), so the turn never waits on an
// approval prompt and --force (which turns every hook off) is never needed.
// Cursor runs hook commands as programs, not through a shell, so the answer is
// a script file like the deny one, never an inline shell command.
func cursorShellHookCommand(fullNative bool) string {
	if fullNative && nativeshell.Enabled() {
		return "./.cursor/hooks/mlp-allow-shell.sh"
	}
	return "./.cursor/hooks/mlp-deny-builtin.sh"
}

// cursorBridgeServerName returns the name of the mounted AgentWorks bridge
// server (the entry whose env carries MCP_API_URL), or "" when there is none.
func cursorBridgeServerName(mcpJSON string) string {
	var parsed struct {
		MCPServers map[string]struct {
			Env map[string]string `json:"env"`
		} `json:"mcpServers"`
	}
	if strings.TrimSpace(mcpJSON) == "" || json.Unmarshal([]byte(mcpJSON), &parsed) != nil {
		return ""
	}
	names := make([]string, 0, len(parsed.MCPServers))
	for name, server := range parsed.MCPServers {
		if _, ok := server.Env["MCP_API_URL"]; ok {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		return ""
	}
	return names[0]
}

// cursorShellAllowScript approves Cursor's own shell in Full CLI mode. When the
// bridge is mounted it first refuses a command that calls the platform API: the
// credentials exist only in the bridge's own shell, so that call would fail with
// "missing or invalid Authorization header" here. The refusal grants nothing and
// reveals nothing; every other command is approved as before.
func cursorShellAllowScript(bridgeName string) string {
	head := "#!/bin/bash\n# Installed by the multi-llm-provider-go cursor adapter in Full CLI mode:\n# approves Cursor's own shell commands (they run inside AgentWorks' confinement).\n"
	if bridgeName == "" {
		return head + "cat >/dev/null\nprintf '%s\\n' '{\"permission\":\"allow\"}'\nexit 0\n"
	}
	tool := bridgeName + "-execute_shell_command"
	msg := "This shell is not the platform shell: MCP_AUTH, MCP_CUSTOM, MCP_MCP and MCP_API_TOKEN exist only in the " + bridgeName + " bridge shell, so their absence here is expected and does not mean the bridge is missing. The " + bridgeName + " tools in your tool list are the working bridge. Run platform calls (and anything needing those variables or the platform address) through " + tool + ". Find a platform tool with " + bridgeName + "-search_tools, then read its schema and route with " + bridgeName + "-get_api_spec."
	return head + `input=$(cat)
cmd=$(printf '%s' "$input" | grep -oE '"(command|url)"[[:space:]]*:[[:space:]]*"([^"\\]|\\.)*"' | head -2)
hit=0
for u in "${MCP_API_URL:-}" "${MCP_BRIDGE_API_URL:-}" "${MCP_AGENT_SERVER_URL:-}"; do
  hostport=$(printf '%s' "$u" | sed -E 's#^[a-zA-Z]+://##; s#/.*$##')
  if [ -n "$hostport" ] && printf '%s' "$cmd" | grep -qF "$hostport"; then hit=1; fi
done
# Credential probing: names a platform variable AND reads the environment.
if printf '%s' "$cmd" | grep -qE 'MCP_(CUSTOM|AUTH|MCP|API_TOKEN)\b' && printf '%s' "$cmd" | grep -qE 'printenv|(^|[^[:alnum:]_./-])env([^[:alnum:]_./=-]|$)|(echo|printf)[^|;&]*\$|\$\{![A-Za-z_]|\[+[[:space:]]*-[nz][[:space:]]|test[[:space:]]+-[nz][[:space:]]|compgen[[:space:]]+-e|declare[[:space:]]+-p'; then hit=1; fi
if printf '%s' "$cmd" | grep -qE '\$\{?MCP_(CUSTOM|AUTH|MCP|API_TOKEN)\b|/tools/(custom|virtual|mcp)/'; then hit=1; fi
if [ "$hit" = 1 ]; then
  printf '%s\n' '{"permission":"deny","user_message":"` + msg + `","agent_message":"` + msg + `"}'
  exit 0
fi
printf '%s\n' '{"permission":"allow"}'
exit 0
`
}

// cursorPreToolRedirectEntry adds a second preToolUse entry (Full CLI + bridge
// only) that runs the same redirect script for Shell, WebFetch and WebSearch.
// Checked live (cursor-agent 2026.10.01): Cursor fires preToolUse for Shell
// (also inside a Task subagent) but does NOT fire any hook for WebFetch or
// WebSearch, so the web part is inert until Cursor does; the Shell part is
// redundant with beforeShellExecution and harmless.
func cursorPreToolRedirectEntry(fullNative bool, bridgeName string) string {
	if !fullNative || bridgeName == "" {
		return ""
	}
	return `, {"command": "./.cursor/hooks/mlp-allow-shell.sh", "matcher": "Shell|WebFetch|WebSearch", "failClosed": true}`
}

func writeCursorDenyBuiltinHooksWithBridge(cursorDir string, fullNative bool, bridgeName string) (func(), error) {
	hooksDir := filepath.Join(cursorDir, "hooks")
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		return nil, fmt.Errorf("failed to create cursor hooks dir: %w", err)
	}
	scriptPath := filepath.Join(hooksDir, "mlp-deny-builtin.sh")
	// Heredoc emits the deny JSON cursor expects on stdout per its hook
	// schema (https://cursor.com/docs/hooks). Exit code 0 + valid JSON
	// tells cursor to obey the permission verdict. Exit code 2 would
	// also deny, but emitting the verdict explicitly lets us include a
	// user_message that helps debug "why didn't cursor run my command".
	logPath := filepath.Join(hooksDir, "mlp-deny-builtin-denials.jsonl")
	script := `#!/bin/bash
# Installed by the multi-llm-provider-go cursor adapter when
# WithDenyBuiltinTools is enabled. Denies cursor's built-in filesystem,
# shell, edit, and delegation tools so the agent routes through the MCP bridge
# (api-bridge.*) instead.
input=$(cat)
printf '%s\n' "$input" >> ` + cursorShellQuote(logPath) + `
# beforeReadFile carries a top-level file_path (cursor.com/docs/hooks). The
# MCP bridge has no vision equivalent to native image reading, so denying
# beforeReadFile for an image doesn't redirect the agent to a working
# alternative -- it just leaves it retrying until the session times out.
# Allow reads of image files through; every other denial is unchanged.
file_path=$(printf '%s' "$input" | grep -o '"file_path"[[:space:]]*:[[:space:]]*"[^"]*"' | head -1 | sed -E 's/.*:[[:space:]]*"([^"]*)"/\1/')
case "$file_path" in
  *.png|*.jpg|*.jpeg|*.webp)
    cat <<'JSON'
{"permission":"allow"}
JSON
    exit 0
    ;;
esac
cat <<'JSON'
{"permission":"deny","user_message":"Built-in filesystem/shell/edit/search/delegation tools are disabled in this session by the orchestrator. Use the MCP bridge tools instead.","agent_message":"Built-in filesystem/shell/edit/search/delegation tools are DENIED. Do not start Cursor subagents, background agents, cloud agents, workers, delegated agents, or request a mode switch. You DO have full access via the api-bridge MCP server (your environment carries valid MCP_API_URL + MCP_API_TOKEN). Use these EXACT bridge tools — they cover everything you need:\n  • api-bridge.execute_shell_command(command, timeout?) — run any shell command (cat, ls, grep, find, jq, python3, curl, etc.). Output goes to stdout/stderr/exit_code.\n  • api-bridge.diff_patch_workspace_file(filepath, diff) — apply a unified diff to any workspace file. Use this INSTEAD of Edit/Write.\n  • api-bridge.get_api_spec(server_name, tool_name) — discover schemas for the other MCP servers (e.g., google_sheets, github) so you can call them via execute_shell_command + curl/python.\nDo NOT report 'no MCP server configuration' or 'no API tokens' — the bridge is configured and ready. Always pick a bridge tool over giving up."}
JSON
exit 0
`
	scriptToken, err := projectfile.AcquireOwnedLeaseMode(scriptPath, []byte(script), 0o755)
	if err != nil {
		return nil, fmt.Errorf("failed to write cursor deny-builtin script: %w", err)
	}
	hooksConfig := `{
  "version": 1,
  "hooks": {
    "preToolUse": [{"command": "./.cursor/hooks/mlp-deny-builtin.sh", "matcher": "` + cursorDeniedToolMatcher(fullNative) + `", "failClosed": true}` + cursorPreToolRedirectEntry(fullNative, bridgeName) + `],
    "beforeShellExecution": [{"command": "` + cursorShellHookCommand(fullNative) + `", "failClosed": true}]` + cursorBeforeReadFileHook(fullNative) + `
  }
}
`
	hooksPath := filepath.Join(cursorDir, "hooks.json")
	var allowToken string
	if fullNative {
		allowScript := cursorShellAllowScript(bridgeName)
		allowToken, err = projectfile.AcquireOwnedLeaseMode(filepath.Join(hooksDir, "mlp-allow-shell.sh"), []byte(allowScript), 0o755)
		if err != nil {
			projectfile.ReleaseToken(scriptToken)
			_ = os.Remove(hooksDir)
			return nil, fmt.Errorf("failed to write cursor shell allow script: %w", err)
		}
	}
	hooksToken, err := projectfile.AcquireOwnedLease(hooksPath, []byte(hooksConfig))
	if err != nil {
		projectfile.ReleaseToken(scriptToken)
		if allowToken != "" {
			projectfile.ReleaseToken(allowToken)
		}
		_ = os.Remove(hooksDir)
		return nil, fmt.Errorf("failed to write cursor hooks.json: %w", err)
	}
	// Counted and always restoring: a project's own hooks.json is put back by
	// the last session, and overlapping sessions keep each other's hooks.
	return func() {
		projectfile.ReleaseToken(hooksToken)
		projectfile.ReleaseToken(scriptToken)
		if allowToken != "" {
			projectfile.ReleaseToken(allowToken)
		}
		if !projectfile.OwnedHeld(scriptPath) {
			_ = os.Remove(logPath)
		}
		_ = os.Remove(hooksDir)
		_ = os.Remove(cursorDir)
	}, nil
}

func writeCursorRestoredFile(path string, content []byte) (func(), error) {
	// Counted and always restoring: a project's own .cursor/mcp.json, cli.json or
	// hooks.json is put back when the last session using it ends, and overlapping
	// sessions never delete each other's file.
	token, err := projectfile.AcquireOwnedLease(path, content)
	if err != nil {
		return nil, fmt.Errorf("failed to write Cursor config %s: %w", path, err)
	}
	return func() { projectfile.ReleaseToken(token) }, nil
}

func cursorInteractiveSessionIDFromOptions(opts *llmtypes.CallOptions) string {
	if opts == nil || opts.Metadata == nil || opts.Metadata.Custom == nil {
		return ""
	}
	if sessionID, ok := opts.Metadata.Custom[MetadataKeyInteractiveSessionID].(string); ok {
		return strings.TrimSpace(sessionID)
	}
	return ""
}

func cursorPersistentInteractiveFromOptions(opts *llmtypes.CallOptions) bool {
	if opts == nil || opts.Metadata == nil || opts.Metadata.Custom == nil {
		return false
	}
	enabled, ok := opts.Metadata.Custom[MetadataKeyPersistentInteractive].(bool)
	return ok && enabled
}

func cursorResumeSessionIDFromOptions(opts *llmtypes.CallOptions) string {
	if opts == nil || opts.Metadata == nil || opts.Metadata.Custom == nil {
		return ""
	}
	if sessionID, ok := opts.Metadata.Custom[MetadataKeyResumeSessionID].(string); ok {
		return strings.TrimSpace(sessionID)
	}
	return ""
}

func cursorWorkingDirFromOptions(opts *llmtypes.CallOptions) string {
	if opts == nil || opts.Metadata == nil || opts.Metadata.Custom == nil {
		return ""
	}
	if dir, ok := opts.Metadata.Custom[MetadataKeyWorkingDir].(string); ok {
		if trimmed := strings.TrimSpace(dir); trimmed != "" {
			return filepath.Clean(trimmed)
		}
	}
	return ""
}

func cursorInteractiveIdleTimeout() time.Duration {
	return cursorDurationFromEnv(EnvCursorInteractiveIdleTimeoutSeconds, defaultCursorInteractiveIdleTimeout)
}

func cursorDurationFromEnv(key string, fallback time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	seconds, err := strconv.Atoi(raw)
	if err != nil || seconds <= 0 {
		return fallback
	}
	return time.Duration(seconds) * time.Second
}

func cursorShellQuote(s string) string {
	if s == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}
