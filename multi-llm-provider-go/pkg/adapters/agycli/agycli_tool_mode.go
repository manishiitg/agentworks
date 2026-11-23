package agycli

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/internal/filelock"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/nativeshell"
)

const agyToolModeHookName = "agentworks-native-tool-mode"

// agyToolMode validates the optional product setting. An absent setting
// retains the adapter's legacy behavior for direct SDK callers.
func agyToolMode(raw string) (string, error) {
	switch mode := strings.ToLower(strings.TrimSpace(raw)); mode {
	case "", "mcp_only", "full":
		return mode, nil
	default:
		return "", fmt.Errorf("agy native tools mode %q: want mcp_only or full", raw)
	}
}

func agyFullNativeToolsMode(mode string) bool {
	return mode == "full"
}

// Full CLI must never start with host rights: it needs the host's lock
// (Landlock on Linux, Seatbelt on a Mac).
func agyToolModeForLaunch(opts *llmtypes.CallOptions) (string, error) {
	mode, err := agyToolMode(agyStringMetadata(opts, MetadataKeyNativeToolsMode))
	if err != nil {
		return "", err
	}
	if mode == "full" && (opts == nil || !opts.CLISecurity.Confined()) {
		return "", fmt.Errorf("agy full native tools require a confined launch (Landlock on Linux, Seatbelt on a Mac)")
	}
	return mode, nil
}

func agyToolModeFingerprint(mcpJSON, mode string) string {
	base := agyMountFingerprint(mcpJSON)
	if mode == "" {
		return base
	}
	return base + ":" + mode
}

func agyShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}

// agyShellRedirectReason is shown to the agent when its native shell targets the
// platform API. That shell has no platform credentials (they exist only in the
// bridge's own shell), so the call could only fail; this names the tool that can
// make it. It grants nothing and reveals nothing. No single quotes: it is
// embedded in a quoted python literal.
const agyShellProbeReason = `Refused here: this shell has no platform credentials and no route to the platform. The variables MCP_AUTH, MCP_CUSTOM, MCP_MCP and MCP_API_TOKEN exist only inside the bridge shell, so their absence in this shell is expected and does NOT mean the bridge is missing. The call_mcp_tool tool on the api-bridge MCP server (ServerName starting with agentworks-api-bridge) is the working bridge: run platform calls with ToolName execute_shell_command (its shell has the credentials).`

const agyShellRedirectReason = `This shell has no platform credentials, so this call would fail with "missing or invalid Authorization header". Run the same command through the call_mcp_tool tool on the api-bridge MCP server (ServerName starting with agentworks-api-bridge) with ToolName execute_shell_command: its shell has MCP_CUSTOM and MCP_AUTH.`

// agyNativeShellOffReason is shown to the agent when it tries the CLI's own
// shell in Full mode. No single quotes: it is embedded in a python literal.
const agyNativeShellOffReason = `The built-in shell is turned off in this chat. Run shell commands through the call_mcp_tool tool on the api-bridge MCP server (ServerName starting with agentworks-api-bridge) with ToolName execute_shell_command. File read and edit tools still work.`

// AGY has no --tools allowlist. Its PreToolUse hook is the execution gate:
// mcp_only fails closed for every native tool except search_web; Full CLI
// admits the CLI's native toolset alongside MCP. search_web is read-only and
// is the agent's own web search: the platform's search tool was removed
// (PLAT-508), so without it a workflow step on AGY could not search at all
// (owner, 2026-10-07). Claude keeps WebSearch the same way.
//
// hostPorts are the platform's own host:port values (not secrets); a native
// shell command that names one is redirected like a credential use.
func agyToolModeHookCommand(python, mode string, hostPorts ...string) string {
	fullEnabled := "False"
	if agyFullNativeToolsMode(mode) {
		fullEnabled = "True"
	}
	// Owner decision 2026-10-05 (PLAT-491): in Full mode the CLI's own shell is
	// refused unless the nativeshell escape hatch is on. The bridge shell
	// (execute_shell_command) is an MCP tool and is not affected.
	shellOff := "False"
	if agyFullNativeToolsMode(mode) && !nativeshell.Enabled() {
		shellOff = "True"
	}
	program := `import json,sys,signal,re
def timeout(_signum,_frame):
    raise TimeoutError("AGY hook input timed out")
if hasattr(signal,"SIGALRM"):
    signal.signal(signal.SIGALRM,timeout)
    signal.alarm(3)
call={}
try:
    call=json.load(sys.stdin).get("toolCall",{})
    name=call.get("name","")
except Exception:
    name=""
if hasattr(signal,"SIGALRM"):
    signal.alarm(0)
bridge=name=="call_mcp_tool" or name.startswith("mcp__")
allowed=isinstance(name,str) and bool(name) and (bridge or name=="search_web" or ` + fullEnabled + `)
if ` + shellOff + ` and name in ("run_command","send_command_input"):
    print(json.dumps({"decision":"deny","reason":'` + agyNativeShellOffReason + `'}))
    sys.exit(0)
if ` + fullEnabled + ` and name=="run_command":
    args=call.get("args")
    command=args.get("CommandLine") if isinstance(args,dict) else None
    if not isinstance(command,str):
        command=json.dumps(call)
    if re.search(r'\$\{?MCP_(CUSTOM|AUTH|MCP|API_TOKEN)\b|/tools/(custom|virtual|mcp)/',command) or any(h in command for h in sys.argv[1:] if h):
        print(json.dumps({"decision":"deny","reason":'` + agyShellRedirectReason + `'}))
        sys.exit(0)
    envread=r'(^|[;&|(\x60]|\$\()\s*env\s*($|[;&|)\x60])|\bprintenv\b|\$\{!|\[\[?\s+-[nz]\b|\btest\s+-[nz]\b|\bcompgen\s+-[ev]\b|\b(declare|typeset)\s+-[pxg]+\b|\bexport\s+-p\b|\bos\.environ\b|\bos\.getenv\b|\bprocess\.env\b|/proc/[^\s]*/environ|\b(echo|printf)\b[^\n]*\$'
    if re.search(r'\bMCP_(CUSTOM|AUTH|MCP|API_TOKEN)\b',command) and re.search(envread,command):
        print(json.dumps({"decision":"deny","reason":'` + agyShellProbeReason + `'}))
        sys.exit(0)
print(json.dumps({"decision":"allow" if allowed else "deny","reason":"Use the AgentWorks MCP bridge for this tool" if not allowed else ""}))`
	// A missing or crashing interpreter still emits an explicit denial. The
	// internal alarm returns before AGY's outer hook timeout fires.
	command := agyShellQuote(python) + " -c " + agyShellQuote(program)
	for _, hostPort := range hostPorts {
		if hostPort = strings.TrimSpace(hostPort); hostPort != "" {
			command += " " + agyShellQuote(hostPort)
		}
	}
	return command +
		" || printf '%s\\n' '{\"decision\":\"deny\",\"reason\":\"AGY tool gate failed\"}'"
}

type agyWorkspaceHookHold struct {
	mode         string
	holders      int
	hadFile      bool
	original     []byte
	originalMode os.FileMode
	entry        interface{}
	lockFile     *os.File
}

type agyWorkspaceHookBackup struct {
	HadFile  bool   `json:"had_file"`
	Original []byte `json:"original"`
	Mode     uint32 `json:"mode"`
}

var agyWorkspaceHooks = struct {
	sync.Mutex
	active map[string]*agyWorkspaceHookHold
}{active: make(map[string]*agyWorkspaceHookHold)}

// agyHoldToolModeHook installs a workspace-level hook before CLI boot and
// removes only its own entry after the last turn/session releases it. AGY
// 1.2.12 demonstrably runs workspace hooks with --dangerously-skip-permissions;
// its global settings hooks were ignored in the same live probe.
func agyHoldToolModeHook(workingDir, mode string, hostPorts ...string) (func(), error) {
	if mode == "" {
		return func() {}, nil
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		return nil, fmt.Errorf("agy native tools mode requires python3 for the PreToolUse gate: %w", err)
	}
	workingDir, err = filepath.Abs(workingDir)
	if err != nil {
		return nil, err
	}
	if err := agyRejectAncestorHooks(workingDir, python); err != nil {
		return nil, err
	}
	agyWorkspaceHooks.Lock()
	defer agyWorkspaceHooks.Unlock()
	if hold := agyWorkspaceHooks.active[workingDir]; hold != nil {
		if hold.mode != mode {
			return nil, fmt.Errorf("agy workspace %q already holds native tool mode %q", workingDir, hold.mode)
		}
		hold.holders++
		return agyToolModeReleaseFunc(workingDir), nil
	}
	path := filepath.Join(workingDir, ".agents", "hooks.json")
	if err := agyEnsureHookDir(filepath.Dir(path)); err != nil {
		return nil, err
	}
	lockPath := filepath.Join(filepath.Dir(path), ".agentworks-agy-hooks.lock")
	if err := agyRejectSymlink(lockPath); err != nil {
		return nil, err
	}
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := lockFile.Chmod(0o600); err != nil {
		lockFile.Close()
		return nil, err
	}
	if ok, err := filelock.TryLock(lockFile); err != nil || !ok {
		lockFile.Close()
		if err == nil {
			err = errors.New("lock is held")
		}
		return nil, fmt.Errorf("agy workspace %q already has a hook holder in another process: %w", workingDir, err)
	}
	locked := true
	defer func() {
		if locked {
			filelock.Unlock(lockFile)
			_ = lockFile.Close()
		}
	}()
	backupPath := path + ".agentworks-backup"
	if err := agyRejectSymlink(path); err != nil {
		return nil, err
	}
	if err := agyRejectSymlink(backupPath); err != nil {
		return nil, err
	}
	if err := agyRestoreStaleHookBackup(path, backupPath); err != nil {
		return nil, err
	}
	original, err := os.ReadFile(path)
	hadFile := err == nil
	originalMode := os.FileMode(0o600)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if hadFile {
		if info, statErr := os.Stat(path); statErr == nil {
			originalMode = info.Mode().Perm()
		}
	}
	hooks := map[string]interface{}{}
	if hadFile {
		if err := json.Unmarshal(original, &hooks); err != nil {
			return nil, fmt.Errorf("parse agy workspace hooks: %w", err)
		}
		if hooks == nil {
			return nil, fmt.Errorf("agy workspace hooks must be an object")
		}
	}
	if previous, exists := hooks[agyToolModeHookName]; exists {
		if !agyIsManagedToolHook(previous) {
			return nil, fmt.Errorf("agy workspace hook %q already exists; refusing to replace it", agyToolModeHookName)
		}
		// Recover the managed entry left by a killed backend process.
		delete(hooks, agyToolModeHookName)
		hadFile = len(hooks) > 0
		if hadFile {
			original, _ = json.MarshalIndent(hooks, "", "  ")
			original = append(original, '\n')
		} else {
			original = nil
		}
	}
	entry := agyManagedToolHookEntry(python, mode, hostPorts...)
	// AGY executes every workspace hook even with permissions skipped. A
	// foreign hook can run arbitrary code before our tool gate, so the live
	// file must contain only the managed gate. Preserve the user's bytes for
	// restoration after the last holder releases it.
	hooks = map[string]interface{}{agyToolModeHookName: entry}
	backup := agyWorkspaceHookBackup{HadFile: hadFile, Original: original, Mode: uint32(originalMode)}
	if err := agyWriteHookBackup(backupPath, backup); err != nil {
		return nil, err
	}
	if err := agyWriteHooksFile(path, hooks); err != nil {
		_ = os.Remove(backupPath)
		return nil, err
	}
	agyWorkspaceHooks.active[workingDir] = &agyWorkspaceHookHold{mode: mode, holders: 1, hadFile: hadFile, original: original, originalMode: originalMode, entry: entry, lockFile: lockFile}
	locked = false
	return agyToolModeReleaseFunc(workingDir), nil
}

func agyManagedToolHookEntry(python, mode string, hostPorts ...string) map[string]interface{} {
	return map[string]interface{}{
		"PreToolUse": []interface{}{map[string]interface{}{
			"matcher": "*",
			"hooks": []interface{}{map[string]interface{}{
				"type": "command", "command": agyToolModeHookCommand(python, mode, hostPorts...), "timeout": 10,
			}},
		}},
	}
}

// AGY's parent-directory hook discovery has not been certified. Refuse a
// nested run beneath a foreign hook file. The exact managed gate is safe for
// delegated AGY runs underneath an already active parent workspace.
func agyRejectAncestorHooks(workingDir, python string) error {
	for parent := filepath.Dir(workingDir); parent != workingDir; parent = filepath.Dir(parent) {
		path := filepath.Join(parent, ".agents", "hooks.json")
		if _, err := os.Stat(path); err == nil {
			if !agyIsManagedAncestorHookFile(path, python) {
				return fmt.Errorf("agy workspace %q is below another hook file %q", workingDir, path)
			}
		} else if !os.IsNotExist(err) {
			return err
		}
		if next := filepath.Dir(parent); next == parent {
			break
		}
	}
	return nil
}

func agyIsManagedAncestorHookFile(path, python string) bool {
	if err := agyEnsureHookDir(filepath.Dir(path)); err != nil {
		return false
	}
	if err := agyRejectSymlink(path); err != nil {
		return false
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var hooks map[string]interface{}
	if json.Unmarshal(raw, &hooks) != nil || len(hooks) != 1 {
		return false
	}
	for _, mode := range []string{"mcp_only"} {
		if agySameHookEntry(hooks[agyToolModeHookName], agyManagedToolHookEntry(python, mode)) {
			return true
		}
	}
	return false
}

func agyEnsureHookDir(dir string) error {
	info, err := os.Lstat(dir)
	if os.IsNotExist(err) {
		if err := os.Mkdir(dir, 0o700); err != nil && !os.IsExist(err) {
			return err
		}
		info, err = os.Lstat(dir)
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("agy workspace hook directory %q must be a real directory", dir)
	}
	return nil
}

func agyRejectSymlink(path string) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("agy workspace hook path %q must be a regular file", path)
	}
	return nil
}

func agyWriteHookBackup(path string, backup agyWorkspaceHookBackup) error {
	raw, err := json.Marshal(backup)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".agentworks-agy-backup-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	if err := tmp.Chmod(0o600); err != nil {
		return err
	}
	if _, err := tmp.Write(raw); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func agyRestoreStaleHookBackup(path, backupPath string) error {
	raw, err := os.ReadFile(backupPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var backup agyWorkspaceHookBackup
	if err := json.Unmarshal(raw, &backup); err != nil {
		return fmt.Errorf("parse agy workspace hook backup: %w", err)
	}
	current, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	var hooks map[string]interface{}
	if json.Unmarshal(current, &hooks) == nil && agyIsManagedToolHook(hooks[agyToolModeHookName]) {
		if backup.HadFile {
			if err := agyWriteHookBytes(path, backup.Original, os.FileMode(backup.Mode)); err != nil {
				return err
			}
		} else if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return os.Remove(backupPath)
}

func agyIsManagedToolHook(value interface{}) bool {
	entry, ok := value.(map[string]interface{})
	if !ok {
		return false
	}
	pre, ok := entry["PreToolUse"].([]interface{})
	if !ok || len(pre) != 1 {
		return false
	}
	rule, ok := pre[0].(map[string]interface{})
	if !ok || rule["matcher"] != "*" {
		return false
	}
	handlers, ok := rule["hooks"].([]interface{})
	if !ok || len(handlers) != 1 {
		return false
	}
	handler, ok := handlers[0].(map[string]interface{})
	if !ok {
		return false
	}
	command, _ := handler["command"].(string)
	return handler["type"] == "command" && strings.Contains(command, "Use the AgentWorks MCP bridge for this tool")
}

func agyToolModeReleaseFunc(workingDir string) func() {
	var once sync.Once
	return func() { once.Do(func() { agyReleaseToolModeHook(workingDir) }) }
}

func agyReleaseToolModeHook(workingDir string) {
	agyWorkspaceHooks.Lock()
	defer agyWorkspaceHooks.Unlock()
	hold := agyWorkspaceHooks.active[workingDir]
	if hold == nil {
		return
	}
	hold.holders--
	if hold.holders > 0 {
		return
	}
	delete(agyWorkspaceHooks.active, workingDir)
	path := filepath.Join(workingDir, ".agents", "hooks.json")
	removeBackup := true
	defer func() {
		if removeBackup {
			_ = os.Remove(path + ".agentworks-backup")
		}
		filelock.Unlock(hold.lockFile)
		_ = hold.lockFile.Close()
	}()
	raw, err := os.ReadFile(path)
	if err != nil {
		removeBackup = false
		return
	}
	hooks := map[string]interface{}{}
	if json.Unmarshal(raw, &hooks) != nil || !agySameHookEntry(hooks[agyToolModeHookName], hold.entry) {
		return // A user changed the entry; leave their edit intact.
	}
	if len(hooks) != 1 {
		return // A new hook appeared while held; do not overwrite it on release.
	}
	if hold.hadFile {
		if err := agyWriteHookBytes(path, hold.original, hold.originalMode); err != nil {
			removeBackup = false
		}
		return
	}
	if !hold.hadFile {
		if err := os.Remove(path); err != nil {
			removeBackup = false
		}
	}
}

func agySameHookEntry(a, b interface{}) bool {
	left, errA := json.Marshal(a)
	right, errB := json.Marshal(b)
	return errA == nil && errB == nil && string(left) == string(right)
}

func agyWriteHooksFile(path string, hooks map[string]interface{}) error {
	data, err := json.MarshalIndent(hooks, "", "  ")
	if err != nil {
		return err
	}
	return agyWriteHookBytes(path, append(data, '\n'), 0o600)
}

func agyWriteHookBytes(path string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".agentworks-hooks-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// agyBridgeHostPorts lists the platform host:port values the mounted bridge talks
// to (a host and port are not secrets; no token is ever read).
func agyBridgeHostPorts(mcpJSON string) []string {
	servers, err := agyParseMCPServers(mcpJSON)
	if err != nil {
		return nil
	}
	var out []string
	for _, server := range servers {
		if _, ok := server.env["MCP_API_URL"]; !ok {
			continue
		}
		for _, key := range []string{"MCP_API_URL", "MCP_BRIDGE_API_URL", "MCP_AGENT_SERVER_URL"} {
			if u, err := url.Parse(strings.TrimSpace(server.env[key])); err == nil && u.Host != "" && !slices.Contains(out, u.Host) {
				out = append(out, u.Host)
			}
		}
	}
	slices.Sort(out)
	return out
}

// Bound local Crews preserve project hooks. The CLI loads global hooks from
// .gemini/config/hooks.json in this run's isolated HOME (not settings.json).
func agyHoldToolModeHookForCall(workingDir, privateHome, mode string, opts *llmtypes.CallOptions, hostPorts ...string) (func(), error) {
	if !llmtypes.CLIHasPrivateRuntime(opts) {
		return agyHoldToolModeHook(workingDir, mode, hostPorts...)
	}
	if mode == "" {
		return func() {}, nil
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		return nil, fmt.Errorf("agy native tools mode requires python3: %w", err)
	}
	if privateHome == "" {
		return nil, fmt.Errorf("agy private hook home is unavailable")
	}
	hookPath := filepath.Join(privateHome, ".gemini", "config", "hooks.json")
	if err := agyWritePrivateJSON(hookPath, map[string]interface{}{agyToolModeHookName: agyManagedToolHookEntry(python, mode, hostPorts...)}); err != nil {
		return nil, err
	}
	return func() { _ = os.Remove(hookPath) }, nil
}
