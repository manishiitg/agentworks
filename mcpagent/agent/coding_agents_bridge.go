package mcpagent

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/manishiitg/mcpagent/agent/codeexec"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// DefaultToolOutputFolder is the folder under the agent working directory where the coding-agent
// bridge saves tool output that is too large to return inline.
const DefaultToolOutputFolder = "tool_output_folder"

// liveBridgeReadyFiles tracks the active chat-stable MCP_READY_FILE path per
// sessionID so repeated buildBridgeMCPConfig calls during a running persistent
// CLI session do not delete a live marker written by an already-running
// mcpbridge subprocess. CloseSession / CloseHTTPSession clears the entry and
// removes the marker file so a future cold launch starts clean.
var liveBridgeReadyFiles sync.Map // sessionID -> readyPath

func prepareChatStableReadyFile(sessionID, readyPath string) {
	if prev, loaded := liveBridgeReadyFiles.Load(sessionID); loaded {
		if prevPath, ok := prev.(string); ok && prevPath == readyPath {
			return
		}
		if prevPath, ok := prev.(string); ok && prevPath != "" {
			_ = os.Remove(prevPath)
		}
	}
	_ = os.Remove(readyPath)
	liveBridgeReadyFiles.Store(sessionID, readyPath)
}

func clearBridgeReadyFile(sessionID string) {
	if strings.TrimSpace(sessionID) == "" {
		return
	}
	if prev, loaded := liveBridgeReadyFiles.LoadAndDelete(sessionID); loaded {
		if prevPath, ok := prev.(string); ok && prevPath != "" {
			_ = os.Remove(prevPath)
		}
	}
}

// BridgeToolDef is the serialized tool definition for the MCP bridge binary.
type BridgeToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
	Server      string          `json:"server,omitempty"` // MCP server name (empty for custom/virtual)
	Type        string          `json:"type"`             // "mcp", "custom", or "virtual"
	// Resolved direct-tool timeout; zero means no tool deadline. The CLI bridge
	// still applies the existing provider/backstop cap (PLAT-861-344).
	TimeoutMillis *int64 `json:"timeout_ms,omitempty"`
	// CallTimeoutsMillis (call_tool only): each reachable direct tool's
	// resolved deadline, so a call through call_tool keeps it.
	CallTimeoutsMillis map[string]int64 `json:"call_timeouts_ms,omitempty"`
}

// bridgeTools is the explicit list of tools exposed through the coding-agent MCP
// bridge as native MCP tools. Attached skills additionally contribute the
// intrinsic read_skill tool through additionalBridgeTools. All other
// MCP/custom tools are discovered via get_api_spec and called through HTTP.
var bridgeTools = []struct {
	name     string
	toolType string // "custom" or "virtual"
}{
	{"execute_shell_command", "custom"},
	{"diff_patch_workspace_file", "custom"},
	{"agent_browser", "custom"},
	{"get_api_spec", "virtual"},
	{"search_tools", "virtual"},
	{"call_tool", "call"},
}

// claudeBridgeAllowedToolIdentifiers returns the full "mcp__api-bridge__<name>"
// identifier for every tool actually exposed through the bridge MCP server —
// the fixed core set (bridgeTools) plus whatever a caller registered via
// withAdditionalBridgeTools — deduped the same way BuildBridgeMCPConfig itself
// dedupes them. This is the single source of truth for Claude's tool-name
// allowlist, used both for --allowedTools and the enforced-mode PreToolUse
// hook, so an additional bridge tool is never silently unusable in one path
// while working in the other.
// admitsBridgeTool applies the owning profile's tool policy to one core bridge
// tool, using the same rule BuildBridgeMCPConfig enforces: only the hardcoded
// "custom" core tools are subject to it. Virtual tools (get_api_spec) are the
// discovery door and must always survive, and withAdditionalBridgeTools is an
// explicit per-agent opt-in that already made its own decision.
//
// This exists so the MCP configuration, Claude's --allowedTools, the
// PreToolUse hook, and the routing prompt are all derived from ONE filtered
// surface. When they were derived independently, a profile could remove a tool
// from the catalog while the prompt still told the model to call it and the
// allowlist still permitted it.
func (a *Agent) admitsBridgeTool(name, toolType string) bool {
	if toolType != "custom" || a == nil {
		return true
	}
	// With its own tools on, the CLI edits files natively inside the sandbox,
	// which refuses protected files; a second, bridge edit tool only splits the
	// model's choice. mcp_only chats, Pi and workflow step agents keep it.
	if name == "diff_patch_workspace_file" && a.nativeCodingToolsEnabled() {
		return false
	}
	if a.bridgeToolAdmit == nil {
		return true
	}
	return a.bridgeToolAdmit(name)
}

// admitsCoreBridgeTool answers for a core tool by name, looking up its type in
// bridgeTools. Unknown names are treated as admitted: they are not part of the
// policed core set.
func (a *Agent) admitsCoreBridgeTool(name string) bool {
	for _, tool := range bridgeTools {
		if tool.name == name {
			return a.admitsBridgeTool(tool.name, tool.toolType)
		}
	}
	return true
}

func claudeBridgeAllowedToolIdentifiers(additional []string, admits func(name, toolType string) bool) []string {
	seen := make(map[string]bool, len(bridgeTools)+len(additional))
	names := make([]string, 0, len(bridgeTools)+len(additional))
	for _, want := range bridgeTools {
		if seen[want.name] {
			continue
		}
		// Never advertise a tool the owning profile removed from the catalog:
		// Claude would be permitted to call something the bridge will not
		// register, which surfaces as "tool not registered for session".
		if admits != nil && !admits(want.name, want.toolType) {
			continue
		}
		seen[want.name] = true
		names = append(names, want.name)
	}
	for _, name := range additional {
		if seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	identifiers := make([]string, 0, len(names))
	for _, name := range names {
		identifiers = append(identifiers, "mcp__api-bridge__"+name)
	}
	return identifiers
}

// BuildBridgeMCPConfig creates an MCP config that launches the mcpbridge binary
// as a stdio server, forwarding tool calls to the HTTP API endpoints.
// This is used by CLI-native coding agents to access selected tools natively
// via the bridge.
//
// The bridge exposes a small fixed native tool set plus intrinsic read_skill
// whenever skills are attached. All other MCP tools are discovered via
// get_api_spec and called through HTTP API endpoints.
func (a *Agent) buildBridgeMCPConfig() (string, error) {
	if protect := ProtectManagedProjectionWrites; protect != nil && strings.TrimSpace(a.sessionID) != "" && strings.TrimSpace(a.codingAgentArtifactDir()) != "" {
		protect(a.sessionID, a.codingAgentArtifactDir())
	}
	logger := getLogger(a)

	// 1. Resolve bridge binary path: explicit config, then environment, then PATH.
	bridgePath := a.bridgeBinary
	if bridgePath == "" {
		bridgePath = os.Getenv("MCP_BRIDGE_BINARY")
	}
	if bridgePath == "" {
		var err error
		bridgePath, err = exec.LookPath("mcpbridge")
		if err != nil {
			// Fallback: check ~/go/bin/mcpbridge (common Go install location)
			if home, hErr := os.UserHomeDir(); hErr == nil {
				candidate := home + "/go/bin/mcpbridge"
				if _, sErr := os.Stat(candidate); sErr == nil {
					bridgePath = candidate
				}
			}
			if bridgePath == "" {
				return "", fmt.Errorf("mcpbridge binary not found in PATH or ~/go/bin/ (set MCP_BRIDGE_BINARY env var): %w", err)
			}
		}
	}

	// 2. Collect the bridge tools by name — the fixed shared set (bridgeTools)
	// plus whatever this agent instance registered via withAdditionalBridgeTools.
	logger.Debug("BuildBridgeMCPConfig: agent state",
		loggerv2.Int("tools_count", len(a.tools)),
		loggerv2.Int("custom_tools_count", len(a.directToolSnapshot())),
		loggerv2.Int("additional_bridge_tools_count", len(a.additionalBridgeTools)),
		loggerv2.Any("use_code_execution_mode", a.useCodeExecutionMode))

	seen := make(map[string]bool, len(bridgeTools)+len(a.additionalBridgeTools))
	wanted := make([]struct {
		name     string
		toolType string
	}, 0, len(bridgeTools)+len(a.additionalBridgeTools))
	for _, want := range bridgeTools {
		// Only the hardcoded core list is subject to the owning profile's tool
		// policy. Virtual tools (get_api_spec) are the discovery door and must
		// always survive, and withAdditionalBridgeTools below is an explicit
		// per-agent opt-in that has already made its own decision — neither ever
		// passed through the registration predicate, so asking it about them
		// would return false and strip discovery and skills.
		if !a.admitsBridgeTool(want.name, want.toolType) {
			logger.Debug("Core bridge tool excluded by profile tool policy",
				loggerv2.String("tool", want.name))
			continue
		}
		seen[want.name] = true
		wanted = append(wanted, want)
	}
	for _, name := range a.additionalBridgeTools {
		// A caller's additional-tools list may legitimately name one of the
		// fixed defaults above (e.g. it registers its own "agent_browser"
		// executor) — skip the duplicate rather than emitting the same tool
		// name twice in the generated MCP config.
		if seen[name] {
			continue
		}
		seen[name] = true
		wanted = append(wanted, struct {
			name     string
			toolType string
		}{name, "custom"})
	}

	var toolDefs []BridgeToolDef
	for _, want := range wanted {
		def := a.lookupBridgeTool(want.name, want.toolType, logger)
		if def == nil {
			def = defaultBridgeToolDef(want.name, want.toolType, logger)
		}
		if def != nil {
			toolDefs = append(toolDefs, *def)
		} else {
			logger.Warn("Bridge tool not found — skipping",
				loggerv2.String("tool", want.name),
				loggerv2.String("type", want.toolType))
		}
	}

	// 3. Resolve API URL and token for the bridge process
	// The bridge binary runs on the host (not in Docker), so it needs a host-reachable URL.
	// Explicit configuration wins over the environment so that several
	// executors can live in one process (each agent carries its own); the
	// MCP_BRIDGE_API_URL / MCP_API_URL variables remain the fallback for
	// consumers that configure one executor process-wide.
	apiURL := a.bridgeAPIBaseURL
	if apiURL == "" {
		apiURL = os.Getenv("MCP_BRIDGE_API_URL")
	}
	if apiURL == "" {
		apiURL = a.apiBaseURL
	}
	if apiURL == "" {
		apiURL = os.Getenv("MCP_API_URL")
	}
	apiToken := a.apiToken
	if apiToken == "" {
		apiToken = os.Getenv("MCP_API_TOKEN")
		// A consumer that authenticates the shared executor per session hands
		// the bridge a token for the session it runs as, minted from
		// a.sessionID at launch so it always matches the MCP_SESSION_ID the
		// bridge sends. An agent configured with its own executor token keeps
		// that token.
		if mint := BridgeTokenForSession; mint != nil && strings.TrimSpace(a.sessionID) != "" {
			if token := mint(a.sessionID); token != "" {
				apiToken = token
			}
		}
	}
	if apiURL == "" {
		return "", fmt.Errorf("API base URL not configured (set APIBaseURL or MCP_API_URL)")
	}
	apiURL, err := normalizeBridgeAPIURL(apiURL)
	if err != nil {
		return "", err
	}
	if apiToken == "" {
		return "", fmt.Errorf("API token not configured (set APIToken or MCP_API_TOKEN)")
	}

	// In Approach C, we do NOT embed the session ID in the API URL path.
	// Instead, the MCP_API_URL remains static, and we pass the session ID via
	// the MCP_SESSION_ID environment variable. This ensures that the generated
	// mcp-config JSON can be easily normalized or kept identical across sessions.
	// We no longer append session ID path prefixes here.

	// 4. Build MCP config JSON
	toolsJSON, err := json.Marshal(toolDefs)
	if err != nil {
		return "", fmt.Errorf("failed to marshal tool definitions: %w", err)
	}

	// The tool list can be hundreds of KiB, and Linux rejects a single
	// environment string over 128 KiB (spawn E2BIG), which left Builder chats
	// with no tools at all. The bridge reads the list from a file instead.
	toolsFile, err := a.writeBridgeToolsFile(toolsJSON)
	if err != nil {
		return "", err
	}

	bridgeEnv := map[string]string{
		"MCP_API_URL":    apiURL,
		"MCP_API_TOKEN":  apiToken,
		"MCP_TOOLS_FILE": toolsFile,
	}
	if a.sessionID != "" {
		bridgeEnv["MCP_SESSION_ID"] = a.sessionID
	}
	// Coding CLI providers call mcpbridge outside the normal Agent conversation
	// loop, so their large tool results are handled by the bridge.
	// Give the bridge an explicit persistent folder under the agent's real
	// working directory; this remains stable even when the CLI itself runs in an
	// isolated temporary cwd.
	if workingDir := strings.TrimSpace(a.codingAgentArtifactDir()); workingDir != "" {
		toolOutputDir := filepath.Join(workingDir, DefaultToolOutputFolder)
		// Folder Guard grants this path before the first bridge tool executes.
		// Landlock requires every granted path to exist when its ruleset is built,
		// so waiting for the first oversized result to create the directory makes
		// ordinary shell and browser calls fail with SANDBOX_UNAVAILABLE on fresh
		// workflows. Create the bridge-owned directory as part of bridge setup.
		if err := os.MkdirAll(toolOutputDir, 0o700); err != nil {
			return "", fmt.Errorf("create MCP tool output directory %q: %w", toolOutputDir, err)
		}
		bridgeEnv["MCP_TOOL_OUTPUT_DIR"] = toolOutputDir
	}
	// Route mcpbridge stderr to a user-private log file for debugging startup/crash issues.
	// Claude Code swallows the subprocess stderr, so without this there is no
	// record of why the bridge failed (e.g. an unreadable MCP_TOOLS_FILE, parse errors, crashes).
	bridgeLogDir := filepath.Join(os.TempDir(), fmt.Sprintf("agentworks-bridge-%d", os.Getuid()))
	if err := os.MkdirAll(bridgeLogDir, 0o700); err == nil {
		bridgeEnv["MCP_BRIDGE_LOG"] = filepath.Join(bridgeLogDir, "mcpbridge.log")
	}
	// A marker belongs to one chat, not the most recent chat in a workspace.
	// Other chats cannot satisfy or delete this chat's readiness signal.
	a.bridgeReadyFile = ""
	workingDir := strings.TrimSpace(a.codingAgentArtifactDir())
	if workingDir != "" && strings.TrimSpace(a.sessionID) != "" {
		sum := sha256.Sum256([]byte(a.sessionID))
		readyPath := filepath.Join(workingDir, ".mcpbridge-ready-"+hex.EncodeToString(sum[:16])+".marker")
		prepareChatStableReadyFile(a.sessionID, readyPath)
		a.bridgeReadyFile = readyPath
		bridgeEnv["MCP_READY_FILE"] = readyPath
		logger.Info("[CODING_BRIDGE] readiness configured", loggerv2.String("session", a.sessionID), loggerv2.String("mode", "chat_stable"))
		if timeoutsPath, err := a.writeBridgeCallTimeoutsFile(workingDir, hex.EncodeToString(sum[:16])); err == nil {
			bridgeEnv["MCP_CALL_TIMEOUTS_FILE"] = timeoutsPath
		} else {
			logger.Warn("Failed to write MCP call timeouts file; falling back to default bridge timeout", loggerv2.Error(err))
		}
	} else if f, tmpErr := os.CreateTemp("", "mcpbridge-ready-*.marker"); tmpErr == nil {
		readyPath := f.Name()
		_ = f.Close()
		_ = os.Remove(readyPath)
		a.bridgeReadyFile = readyPath
		bridgeEnv["MCP_READY_FILE"] = readyPath
	} else {
		logger.Warn("Failed to allocate MCP readiness marker; cold-turn tool-connect gate disabled for this launch", loggerv2.Error(tmpErr))
	}
	// Pass per-agent virtual tool scope so the bridge can route get_api_spec
	// to the correct agent's handler (prevents parent/child overwrite)
	if virtualScopeID := a.virtualToolScopeID(); virtualScopeID != "" {
		bridgeEnv["MCP_VIRTUAL_SCOPE_ID"] = virtualScopeID
		mode := "per_build"
		if a.bridgeVirtualScopeID != "" {
			mode = "stable_slot"
		}
		logger.Info("[CODING_BRIDGE] virtual scope configured", loggerv2.String("session", a.sessionID), loggerv2.String("mode", mode))
	}

	config := map[string]interface{}{
		"mcpServers": map[string]interface{}{
			"api-bridge": map[string]interface{}{
				"command": bridgePath,
				"args":    []string{},
				"env":     bridgeEnv,
				"trust":   true, // Auto-trust: bypass confirmation dialogs for non-interactive usage
			},
		},
	}

	configJSON, err := json.Marshal(config)
	if err != nil {
		return "", fmt.Errorf("failed to marshal MCP config: %w", err)
	}

	logger.Info("Built bridge MCP config",
		loggerv2.Int("tool_count", len(toolDefs)),
		loggerv2.String("bridge_path", bridgePath))

	return string(configJSON), nil
}

func normalizeBridgeAPIURL(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", fmt.Errorf("API base URL not configured (set APIBaseURL or MCP_API_URL)")
	}
	if strings.HasPrefix(trimmed, "[") {
		if close := strings.Index(trimmed, "]("); close > 1 && strings.HasSuffix(trimmed, ")") {
			trimmed = strings.TrimSpace(strings.TrimSuffix(trimmed[close+2:], ")"))
		}
	}
	trimmed = strings.Trim(trimmed, "<>")
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("invalid MCP bridge API URL %q; expected plain http(s) URL", raw)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("invalid MCP bridge API URL %q; expected http(s) URL", raw)
	}
	return trimmed, nil
}

func defaultBridgeToolDef(name, toolType string, logger loggerv2.Logger) *BridgeToolDef {
	if toolType != "custom" {
		return nil
	}
	switch name {
	case "execute_shell_command":
		def := marshalBridgeToolDef(name, codeexec.ShellCommandDescription, codeexec.ShellCommandParams, toolType, logger)
		if def != nil {
			millis := int64(0) // The intrinsic shell uses its existing long-running backstop.
			def.TimeoutMillis = &millis
		}
		return def
	case "diff_patch_workspace_file":
		return marshalBridgeToolDef(name, "Apply a unified diff patch to a workspace file.", map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"filepath": map[string]interface{}{
					"type":        "string",
					"description": "Workspace-relative path or absolute path under the workspace docs root.",
				},
				"diff": map[string]interface{}{
					"type":        "string",
					"description": "Unified diff content to apply.",
				},
			},
			"required": []string{"filepath", "diff"},
		}, toolType, logger)
	case "agent_browser":
		return marshalBridgeToolDef(name, "Control a browser agent session.", map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"command": map[string]interface{}{
					"type":        "string",
					"description": "Browser command such as get, snapshot, click, type, wait, or tab.",
				},
			},
			"required": []string{"command"},
		}, toolType, logger)
	default:
		return nil
	}
}

// lookupBridgeTool finds a tool by name from the agent's registered tools.
// Direct tools come from the canonical registry; virtual schemas are generated
// from the provider-facing projection or their intrinsic definitions.
func (a *Agent) lookupBridgeTool(name, toolType string, logger loggerv2.Logger) *BridgeToolDef {
	switch toolType {
	case "custom":
		tool, ok := a.lookupDirectTool(name)
		if !ok {
			return nil
		}
		description := ""
		var parameters interface{}
		if tool.Definition.Function != nil {
			description = tool.Definition.Function.Description
			parameters = tool.Definition.Function.Parameters
		}
		def := marshalBridgeToolDef(name, description, parameters, toolType, logger)
		if def != nil {
			timeout := tool.Timeout
			if timeout < 0 {
				timeout = getToolExecutionTimeout(a)
			}
			millis := timeout.Milliseconds()
			def.TimeoutMillis = &millis
		}
		return def

	case "call":
		// call_tool runs a discovered tool through the same per-tool route,
		// session token and deadline as a direct bridge call; the server's
		// session registry decides whether that tool exists and may run.
		// When the chat has a stable artifact directory and session ID, per-tool
		// timeouts live in the chat-stable MCP_CALL_TIMEOUTS_FILE instead of the
		// content-hashed MCP_TOOLS_FILE so toggling custom HTTP tools (such as
		// laptop tools on browser vs agent-message turns) never changes mcp_config
		// or restarts the coding CLI (#4560).
		def := marshalBridgeToolDef(name, callToolDescription, callToolParameters, toolType, logger)
		if def != nil && (strings.TrimSpace(a.codingAgentArtifactDir()) == "" || strings.TrimSpace(a.sessionID) == "") {
			def.CallTimeoutsMillis = a.directToolCallTimeouts()
		}
		return def

	case "virtual":
		for _, tool := range a.tools {
			if tool.Function != nil && tool.Function.Name == name {
				return marshalBridgeToolDef(name, tool.Function.Description, tool.Function.Parameters, toolType, logger)
			}
		}
		// Coding-agent bridges must always be able to discover HTTP-backed tools,
		// even when the current provider path filtered virtual tools out of a.Tools.
		// Recreate virtual definitions here so get_api_spec remains available while
		// advanced/custom tools stay behind API discovery.
		for _, tool := range a.createVirtualTools() {
			if tool.Function != nil && tool.Function.Name == name {
				return marshalBridgeToolDef(name, tool.Function.Description, tool.Function.Parameters, toolType, logger)
			}
		}
		return nil

	default:
		return nil
	}
}

const callToolDescription = "Run a tool you found with search_tools or get_api_spec that is not in your direct tool list. Pass its exact tool_name and its arguments object (as get_api_spec describes); for a connected-app tool, server_name is optional. Same access rules as a direct call."

var callToolParameters = map[string]interface{}{
	"type": "object",
	"properties": map[string]interface{}{
		"tool_name": map[string]interface{}{
			"type":        "string",
			"description": "Exact tool name from search_tools or get_api_spec.",
		},
		"arguments": map[string]interface{}{
			"type":        "object",
			"description": "The tool's arguments, matching its get_api_spec input schema.",
		},
		"server_name": map[string]interface{}{
			"type":        "string",
			"description": "Only for a connected-app (MCP server) tool: its server name from get_api_spec.",
		},
	},
	"required": []string{"tool_name"},
}

// marshalBridgeToolDef creates a BridgeToolDef, marshalling parameters to JSON.
func marshalBridgeToolDef(name, description string, parameters interface{}, toolType string, logger loggerv2.Logger) *BridgeToolDef {
	var inputSchema json.RawMessage
	if parameters != nil {
		schemaBytes, err := json.Marshal(parameters)
		if err != nil {
			logger.Warn("Failed to marshal tool parameters, skipping",
				loggerv2.String("tool", name), loggerv2.Error(err))
			return nil
		}
		var full map[string]interface{}
		if err := json.Unmarshal(schemaBytes, &full); err != nil {
			logger.Warn("Invalid tool schema, skipping", loggerv2.String("tool", name), loggerv2.Error(err))
			return nil
		}
		inputSchema, err = json.Marshal(llmtypes.ModelToolSchema(full))
		if err != nil {
			return nil
		}
	}
	return &BridgeToolDef{
		Name:        name,
		Description: description,
		InputSchema: inputSchema,
		Type:        toolType,
	}
}

// writeBridgeToolsFile stores the serialized tool definitions in a file the
// bridge reads at startup and returns its path. The name is a hash of the
// content, so launches with the same tools reuse one stable path (a path that
// changed every launch would defeat pi-cli's MCP config cache, see PLAT-186).
// The list names a project's custom tools, so the file is private (0600) and
// lives in the agent's own working directory or a private 0700 directory,
// never loose in a shared temp directory.
func (a *Agent) writeBridgeToolsFile(toolsJSON []byte) (string, error) {
	dir := strings.TrimSpace(a.codingAgentArtifactDir())
	if dir == "" {
		// No stable directory to keep it in, so there is no path to keep stable
		// either: use a fresh 0700 directory, removed when the agent closes.
		private, err := os.MkdirTemp("", "mcpbridge-tools-")
		if err != nil {
			return "", fmt.Errorf("bridge_start_failed: create private directory for the tool list file (%d bytes): %w", len(toolsJSON), err)
		}
		a.bridgeToolsTempDir = private
		dir = private
	}
	sum := sha256.Sum256(toolsJSON)
	path := filepath.Join(dir, ".mcpbridge-tools-"+hex.EncodeToString(sum[:8])+".json")
	if existing, err := os.ReadFile(path); err == nil && bytes.Equal(existing, toolsJSON) { //nolint:gosec // path is built from the agent's own working directory
		return path, nil
	}
	tmp, err := os.CreateTemp(dir, ".mcpbridge-tools-*.tmp")
	if err != nil {
		return "", fmt.Errorf("bridge_start_failed: create tool list file in %q (%d bytes): %w", dir, len(toolsJSON), err)
	}
	tmpPath := tmp.Name()
	_, writeErr := tmp.Write(toolsJSON)
	closeErr := tmp.Close()
	if writeErr == nil && closeErr == nil {
		writeErr = os.Chmod(tmpPath, 0o600)
	}
	if writeErr == nil && closeErr == nil {
		writeErr = os.Rename(tmpPath, path)
	}
	if writeErr != nil || closeErr != nil {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("bridge_start_failed: write tool list file %q (%d bytes): %v %v", path, len(toolsJSON), writeErr, closeErr)
	}
	return path, nil
}

func (a *Agent) directToolCallTimeouts() map[string]int64 {
	timeouts := map[string]int64{}
	for _, tool := range a.directToolSnapshot() {
		timeout := tool.Timeout
		if timeout < 0 {
			timeout = getToolExecutionTimeout(a)
		}
		timeouts[tool.Name] = timeout.Milliseconds()
	}
	return timeouts
}

func (a *Agent) writeBridgeCallTimeoutsFile(dir, sessionHash string) (string, error) {
	data, err := json.Marshal(a.directToolCallTimeouts())
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, ".mcpbridge-call-timeouts-"+sessionHash+".json")
	if existing, err := os.ReadFile(path); err == nil && bytes.Equal(existing, data) { //nolint:gosec // path is built from the agent's own working directory
		return path, nil
	}
	tmp, err := os.CreateTemp(dir, ".mcpbridge-call-timeouts-*.tmp")
	if err != nil {
		return "", err
	}
	tmpPath := tmp.Name()
	_, writeErr := tmp.Write(data)
	closeErr := tmp.Close()
	if writeErr == nil && closeErr == nil {
		writeErr = os.Chmod(tmpPath, 0o600)
	}
	if writeErr == nil && closeErr == nil {
		writeErr = os.Rename(tmpPath, path)
	}
	if writeErr != nil || closeErr != nil {
		_ = os.Remove(tmpPath)
		if writeErr != nil {
			return "", writeErr
		}
		return "", closeErr
	}
	return path, nil
}

// SecretReadTokenSuffix marks a token that reads only secrets for its session (`mcpbridge secret NAME`): the server
// accepts it on the secret read alone, never on a tool route.
const SecretReadTokenSuffix = "#secret-read"
