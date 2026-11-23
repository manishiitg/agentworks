package claudecode

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/manishiitg/multi-llm-provider-go/internal/slotfs"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/internal/fileowner"
)

// The bridge's platform credentials (MCP_CUSTOM, MCP_AUTH, ...) exist only in
// the bridge's own shell tool. Claude's native Bash has none, so a platform
// call made there can only fail with "missing or invalid Authorization
// header". In Full CLI mode (native tools on) agents keep trying it, so a
// PreToolUse hook on Bash refuses exactly such a call and names the bridge's
// shell tool. It grants nothing, reveals nothing and passes every other
// command through untouched. No token ever enters the CLI environment.
//
// Verified against Claude Code 2.1.x hook docs/behaviour: the payload on stdin
// carries tool_name "Bash" and tool_input.command; a deny is the
// hookSpecificOutput permissionDecision object on stdout with exit 0.

// claudeShellRedirectScriptTemplate is the node hook; %s is the JS string
// literal of the bridge's execute_shell_command tool name.
const claudeShellRedirectScriptTemplate = `const fs = require('fs');
let payload = {};
try { payload = JSON.parse(fs.readFileSync(0, 'utf8') || '{}'); } catch (_) {}
const name = payload && typeof payload.tool_name === 'string' ? payload.tool_name : '';
if (!['Bash', 'PowerShell', 'Monitor', 'WebFetch'].includes(name)) process.exit(0);
const input = payload.tool_input || {};
const platformRoute = /\$\{?MCP_(CUSTOM|AUTH|MCP|API_TOKEN)\b|\/tools\/(custom|virtual|mcp)\//;
const credVar = /\bMCP_(CUSTOM|AUTH|MCP|API_TOKEN)\b/;
const envRead = /\$\{?[!A-Za-z_]|(^|[\s;|&(])(printenv|env|export|set)(\s|$)|compgen\s+-[a-zA-Z]*e|declare\s+-[a-zA-Z]*p|typeset\s+-[a-zA-Z]*p|\[\[?\s+-[nz]\s|\btest\s+-[nz]\s|os\.environ|getenv|process\.env|\$ENV\{|ENV\[/;
const platformHosts = [];
for (const key of ['MCP_API_URL', 'MCP_BRIDGE_API_URL', 'MCP_AGENT_SERVER_URL']) {
  const raw = process.env[key];
  if (!raw) continue;
  try {
    const u = new URL(raw);
    if (u.port) {
      platformHosts.push(u.host);
      if (u.hostname === '127.0.0.1') platformHosts.push('localhost:' + u.port);
      if (u.hostname === 'localhost') platformHosts.push('127.0.0.1:' + u.port);
    }
  } catch (_) {}
}
const hostRe = (h) => new RegExp('(^|[^A-Za-z0-9.])' + h.replace(/[.*+?^${}()|[\]\\]/g, '\\$&') + '(?![0-9])', 'i');
const mentionsPlatformHost = (text) => platformHosts.some((h) => hostRe(h).test(text));
let hit = false;
if (name === 'WebFetch') {
  const url = typeof input.url === 'string' ? input.url : JSON.stringify(input);
  hit = platformRoute.test(url) || mentionsPlatformHost(url);
  const api = process.env.MCP_API_URL;
  if (!hit && api) {
    try { hit = new URL(url).host === new URL(api).host; } catch (_) {}
  }
} else {
  const command = typeof input.command === 'string' ? input.command : JSON.stringify(input);
  hit = platformRoute.test(command) || mentionsPlatformHost(command) || (credVar.test(command) && envRead.test(command));
}
if (!hit) process.exit(0);
const tool = %s;
const server = tool.replace(/__execute_shell_command$/, '');
const reason = 'Refused: this shell is not the platform bridge. The variables MCP_AUTH, MCP_CUSTOM, MCP_MCP and MCP_API_TOKEN exist only inside the bridge shell, so their absence here is expected and does NOT mean the bridge is missing. The ' + server + '__* tools in your tool list are the working bridge. Run platform calls (including anything that needs those variables or the platform address) through ' + tool + '. To find a platform tool use ' + server + '__search_tools, then read its schema and route with ' + server + '__get_api_spec.';
process.stdout.write(JSON.stringify({hookSpecificOutput:{hookEventName:'PreToolUse',permissionDecision:'deny',permissionDecisionReason:reason}}) + '\n');
`

// claudeShellRedirectMatcher lists the native tools that run a command
// (tool_input.command) or fetch a URL (tool_input.url).
const claudeShellRedirectMatcher = "Bash|PowerShell|Monitor|WebFetch"

// claudeMCPServerNameUnsafe mirrors how Claude Code normalises a server name
// inside mcp__<server>__<tool> identifiers.
var claudeMCPServerNameUnsafe = regexp.MustCompile(`[^a-zA-Z0-9_-]`)

// claudeBridgeShellToolName returns the identifier Claude gives the bridge's
// execute_shell_command tool, or "" when the config mounts no bridge. The
// bridge is the server whose env carries MCP_API_URL.
func claudeBridgeShellToolName(mcpConfigJSON string) string {
	var doc struct {
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
	}
	if strings.TrimSpace(mcpConfigJSON) == "" || json.Unmarshal([]byte(mcpConfigJSON), &doc) != nil {
		return ""
	}
	found := ""
	for name, entry := range doc.MCPServers {
		var server struct {
			Env map[string]string `json:"env"`
		}
		if json.Unmarshal(entry, &server) != nil {
			continue
		}
		if _, ok := server.Env["MCP_API_URL"]; !ok {
			continue
		}
		// Prefer a stable pick when several qualify.
		if found == "" || name < found {
			found = name
		}
	}
	if found == "" {
		return ""
	}
	return "mcp__" + claudeMCPServerNameUnsafe.ReplaceAllString(found, "_") + "__execute_shell_command"
}

// claudeHookDir is the folder for hook scripts. The Landlock launch already
// grants reads on it (claudeLandlockReads).
func claudeHookDir() (string, error) {
	dir := filepath.Join(os.TempDir(), "claude-code-hooks")
	mode := os.FileMode(0o700)
	if slotfs.On() {
		// A fixed policy script, not a secret; every slot's Claude runs it.
		mode = 0o755
	}
	if err := os.MkdirAll(dir, mode); err != nil {
		return "", fmt.Errorf("create claude hook dir: %w", err)
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return "", fmt.Errorf("check claude hook dir: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || !fileowner.OwnedByMe(dir, info) {
		return "", fmt.Errorf("claude hook dir %s is not a directory owned by this user", dir)
	}
	if info.Mode().Perm() != mode {
		if err := os.Chmod(dir, mode); err != nil {
			return "", fmt.Errorf("secure claude hook dir: %w", err)
		}
	}
	return dir, nil
}

// claudeWriteShellRedirectHook publishes the hook script (content-addressed,
// so concurrent launches share one file) and returns its path.
func claudeWriteShellRedirectHook(toolName string) (string, error) {
	quoted, err := json.Marshal(toolName)
	if err != nil {
		return "", err
	}
	script := fmt.Sprintf(claudeShellRedirectScriptTemplate, quoted)
	digest := sha256.Sum256([]byte(script))
	dir, err := claudeHookDir()
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, fmt.Sprintf("shell-redirect-%x.js", digest[:8]))
	tmp, err := os.CreateTemp(dir, ".shell-redirect-*")
	if err != nil {
		return "", fmt.Errorf("create claude shell redirect hook: %w", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(script); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("write claude shell redirect hook: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("close claude shell redirect hook: %w", err)
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return "", fmt.Errorf("share claude shell redirect hook: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return "", fmt.Errorf("publish claude shell redirect hook: %w", err)
	}
	return path, nil
}

// claudeAddShellRedirectHook appends the redirect PreToolUse hook (matcher
// Bash) to settings, keeping every hook the person already configured. It does
// nothing, and reports false, when the config mounts no bridge or node is
// missing: the hook only improves a failure message and must never block a
// launch. The settings go to Claude through --settings (a per-launch file), so
// nothing the person owns is rewritten and nothing needs restoring.
func claudeAddShellRedirectHook(settings map[string]any, mcpConfigJSON string) (bool, error) {
	toolName := claudeBridgeShellToolName(mcpConfigJSON)
	if toolName == "" {
		return false, nil
	}
	if _, err := exec.LookPath("node"); err != nil {
		return false, nil
	}
	hookPath, err := claudeWriteShellRedirectHook(toolName)
	if err != nil {
		return false, nil
	}
	hooks, _ := settings["hooks"].(map[string]any)
	if hooks == nil {
		if settings["hooks"] != nil {
			return false, fmt.Errorf("claude settings hooks is not an object")
		}
		hooks = map[string]any{}
	}
	var pre []any
	if existing, ok := hooks["PreToolUse"]; ok {
		pre, ok = existing.([]any)
		if !ok {
			return false, fmt.Errorf("claude settings hooks.PreToolUse is not an array")
		}
	}
	hooks["PreToolUse"] = append(pre, map[string]any{
		"matcher": claudeShellRedirectMatcher,
		"hooks": []any{map[string]any{
			"type":    "command",
			"command": "node '" + strings.ReplaceAll(hookPath, "'", "'\\''") + "'",
			"timeout": 5,
		}},
	})
	settings["hooks"] = hooks
	return true, nil
}

// claudeLoadSettingsMap reads the caller's --settings value (a JSON object or
// a file path) into a map; an empty value is an empty map.
func claudeLoadSettingsMap(settings string) map[string]any {
	out := map[string]any{}
	settings = strings.TrimSpace(settings)
	switch {
	case settings == "":
	case strings.HasPrefix(settings, "{"):
		_ = json.Unmarshal([]byte(settings), &out)
	default:
		if raw, err := os.ReadFile(settings); err == nil {
			_ = json.Unmarshal(raw, &out)
		}
	}
	if out == nil {
		out = map[string]any{}
	}
	return out
}
