package codeexec

import (
	"context"
	"errors"
	"fmt"
	"log"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/mcpagent/mcpclient"
	"github.com/manishiitg/mcpagent/toolguard"

	"github.com/mark3labs/mcp-go/mcp"
)

// registryLockDebug stays silent for the normal uncontended path and reports
// only a lock that remains blocked long enough to be operationally meaningful.
func registryLockDebug(caller string) {
	if registryMu.TryLock() {
		return
	}
	start := time.Now()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	done := make(chan struct{})
	go func() {
		registryMu.Lock()
		close(done)
	}()
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			log.Printf("[REGISTRY_LOCK] %s still waiting after %v — possible deadlock", caller, time.Since(start))
		}
	}
}

func registryUnlockDebug(_ string) {
	registryMu.Unlock()
}

// GoBuildError is a custom error type for Go build/compilation errors
type GoBuildError struct {
	Message string
	Output  string
}

func (e *GoBuildError) Error() string {
	return e.Message
}

// ToolRegistry holds references to MCP clients, custom tools, and virtual tools for runtime execution
type ToolRegistry struct {
	mcpClients   map[string]mcpclient.ClientInterface
	customTools  map[string]func(ctx context.Context, args map[string]interface{}) (string, error)
	virtualTools map[string]func(ctx context.Context, args map[string]interface{}) (string, error)
	toolToServer map[string]string
	// Expected cross-session mapping conflicts are reported once per mapping,
	// not on every registry refresh.
	reportedToolMappingConflicts map[string]struct{}
	mu                           sync.RWMutex
	logger                       loggerv2.Logger

	// Session-scoped custom tools to prevent cross-workflow contamination
	// Key: sessionID, Value: map of toolName -> executor
	// When multiple workflows run concurrently, each gets its own scoped tools
	sessionCustomTools map[string]map[string]func(ctx context.Context, args map[string]interface{}) (string, error)

	// Session-scoped virtual tools to prevent cross-workflow contamination
	// Key: sessionID, Value: map of toolName -> executor
	// When multiple workflows run concurrently, each gets its own virtual tool handlers
	sessionVirtualTools map[string]map[string]func(ctx context.Context, args map[string]interface{}) (string, error)

	// Latest virtual tool scope registered for a base session. This lets older
	// restored bridge processes recover when they still carry a stale
	// session:vt:<trace> scope from before workspace tools were registered.
	latestVirtualScopeBySession map[string]string

	// Session-scoped tool allow lists for mode-based restriction.
	// Key: sessionID, Value: set of allowed tool names (nil = no restriction).
	// Set via SetSessionToolAllowList, checked in CallCustomToolWithSession.
	// Protected by allowListMu (NOT registry.mu) to avoid blocking on long-running tool executions.
	sessionToolAllowLists map[string]map[string]bool
	allowListMu           sync.RWMutex
}

var (
	globalRegistry *ToolRegistry
	registryMu     sync.Mutex
)

// InitRegistry initializes the global tool registry from an agent
func InitRegistry(mcpClients map[string]mcpclient.ClientInterface, customTools map[string]func(ctx context.Context, args map[string]interface{}) (string, error), toolToServer map[string]string, logger loggerv2.Logger) {
	InitRegistryWithVirtualTools(mcpClients, customTools, nil, toolToServer, logger)
}

// InitRegistryWithVirtualTools initializes or updates the global tool registry with virtual tools support
// If the registry already exists, it merges new tools/clients into the existing registry
// This allows multiple agents with different servers to coexist
func InitRegistryWithVirtualTools(mcpClients map[string]mcpclient.ClientInterface, customTools map[string]func(ctx context.Context, args map[string]interface{}) (string, error), virtualTools map[string]func(ctx context.Context, args map[string]interface{}) (string, error), toolToServer map[string]string, logger loggerv2.Logger) {
	registryLockDebug("InitRegistryWithVirtualTools")
	defer registryUnlockDebug("InitRegistryWithVirtualTools")

	// Use logger directly (already v2.Logger)
	if logger == nil {
		logger = loggerv2.NewNoop()
	}

	if virtualTools == nil {
		virtualTools = make(map[string]func(ctx context.Context, args map[string]interface{}) (string, error))
	}

	if globalRegistry == nil {
		// First initialization - create new registry
		globalRegistry = &ToolRegistry{
			mcpClients:                   make(map[string]mcpclient.ClientInterface),
			customTools:                  make(map[string]func(ctx context.Context, args map[string]interface{}) (string, error)),
			virtualTools:                 make(map[string]func(ctx context.Context, args map[string]interface{}) (string, error)),
			toolToServer:                 make(map[string]string),
			reportedToolMappingConflicts: make(map[string]struct{}),
			sessionCustomTools:           make(map[string]map[string]func(ctx context.Context, args map[string]interface{}) (string, error)),
			sessionVirtualTools:          make(map[string]map[string]func(ctx context.Context, args map[string]interface{}) (string, error)),
			latestVirtualScopeBySession:  make(map[string]string),
			logger:                       logger,
		}
		logger.Debug("Creating new tool registry")
	} else {
		// Registry exists - update logger if provided
		if logger != nil && globalRegistry.logger == nil {
			globalRegistry.logger = logger
		}
		logger.Debug("Updating existing tool registry (merging new tools/clients)")
	}

	globalRegistry.mu.Lock()
	defer globalRegistry.mu.Unlock()

	// Merge MCP clients
	for serverName, client := range mcpClients {
		if existing, exists := globalRegistry.mcpClients[serverName]; exists {
			logger.Debug("MCP client for server already exists, keeping existing",
				loggerv2.String("server", serverName))
			_ = existing // Keep existing client
		} else {
			globalRegistry.mcpClients[serverName] = client
			logger.Debug("Added MCP client for server", loggerv2.String("server", serverName))
		}
	}

	// Merge custom tools - UPDATE existing ones (orchestrator-wrapped executors should replace unwrapped)
	// This ensures folder guard validation is always applied when orchestrator wraps executors
	for toolName, executor := range customTools {
		globalRegistry.customTools[toolName] = executor
	}

	// Merge virtual tools - UPDATE existing ones (new agent's handler should take priority)
	// When multiple agents share a session (e.g., planning → todo task), the latest agent's
	// virtual tool handlers must replace the old ones so get_api_spec returns the correct
	// tool categories for the currently active agent.
	for toolName, executor := range virtualTools {
		if _, exists := globalRegistry.virtualTools[toolName]; exists {
			globalRegistry.virtualTools[toolName] = executor
			logger.Debug("Virtual tool already exists, updating with new executor",
				loggerv2.String("tool", toolName))
		} else {
			globalRegistry.virtualTools[toolName] = executor
			logger.Debug("Added virtual tool", loggerv2.String("tool", toolName))
		}
	}

	// Merge tool-to-server mapping
	for toolName, serverName := range toolToServer {
		if existing, exists := globalRegistry.toolToServer[toolName]; exists {
			if existing != serverName {
				if globalRegistry.reportedToolMappingConflicts == nil {
					globalRegistry.reportedToolMappingConflicts = make(map[string]struct{})
				}
				conflictKey := toolName + "\x00" + mappingLogServer(existing) + "\x00" + mappingLogServer(serverName)
				if _, reported := globalRegistry.reportedToolMappingConflicts[conflictKey]; !reported {
					globalRegistry.reportedToolMappingConflicts[conflictKey] = struct{}{}
					fields := []loggerv2.Field{loggerv2.String("tool", toolName), loggerv2.String("existing_server", mappingLogServer(existing)), loggerv2.String("new_server", mappingLogServer(serverName))}
					if mappingLogServer(existing) == mappingLogServer(serverName) {
						logger.Debug("Tool mapping reused across delegation scopes", fields...)
					} else {
						logger.Warn("Tool already mapped to different server, new mapping will be ignored", fields...)
					}
				}
			}
		} else {
			globalRegistry.toolToServer[toolName] = serverName
			logger.Debug("Mapped tool to server",
				loggerv2.String("tool", toolName),
				loggerv2.String("server", serverName))
		}
	}

	logger.Info("Tool registry updated",
		loggerv2.Int("mcp_clients", len(globalRegistry.mcpClients)),
		loggerv2.Int("custom_tools", len(globalRegistry.customTools)),
		loggerv2.Int("virtual_tools", len(globalRegistry.virtualTools)),
		loggerv2.Int("tool_mappings", len(globalRegistry.toolToServer)))
}

// GetRegistry returns the global tool registry
func GetRegistry() *ToolRegistry {
	registryMu.Lock()
	defer registryMu.Unlock()
	return globalRegistry
}

// IsServerInScope returns true if the given MCP server is registered in the active session's
// tool registry. Returns true (permissive) if the registry is not initialized or has no clients,
// so scope enforcement only kicks in when a session is actually active.
// It normalizes hyphen/underscore variants (e.g. "google_sheets" matches "google-sheets").
func IsServerInScope(serverName string) bool {
	registry := GetRegistry()
	if registry == nil {
		return true
	}
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	if len(registry.mcpClients) == 0 {
		return true
	}
	if _, exists := registry.mcpClients[serverName]; exists {
		return true
	}
	// Try the alternate form: underscores↔hyphens
	alt := strings.ReplaceAll(serverName, "_", "-")
	if alt == serverName {
		alt = strings.ReplaceAll(serverName, "-", "_")
	}
	_, exists := registry.mcpClients[alt]
	return exists
}

// ScopedServerNames returns the names of all MCP servers currently registered in the global
// tool registry. Returns nil if the registry is not initialized or has no clients.
func ScopedServerNames() []string {
	registry := GetRegistry()
	if registry == nil {
		return nil
	}
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	names := make([]string, 0, len(registry.mcpClients))
	for name := range registry.mcpClients {
		names = append(names, name)
	}
	return names
}

// MCPClientForServer resolves only the explicitly requested provider. Never use
// toolToServer here: different providers routinely expose search and fetch.
func MCPClientForServer(serverName string) mcpclient.ClientInterface {
	registry := GetRegistry()
	if registry == nil {
		return nil
	}
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	return registry.mcpClients[serverName]
}

// InitRegistryVirtualToolsForSession registers virtual tools scoped to a specific session
// This prevents cross-workflow contamination when multiple workflows run concurrently
// Each session gets its own set of virtual tool executors
func InitRegistryVirtualToolsForSession(sessionID string, virtualTools map[string]func(ctx context.Context, args map[string]interface{}) (string, error), logger loggerv2.Logger) {
	if sessionID == "" {
		if logger != nil {
			logger.Warn("InitRegistryVirtualToolsForSession called with empty sessionID, skipping")
		}
		return
	}

	registryLockDebug("InitRegistryVirtualToolsForSession:" + sessionID)
	defer registryUnlockDebug("InitRegistryVirtualToolsForSession:" + sessionID)

	if logger == nil {
		logger = loggerv2.NewNoop()
	}

	// Ensure global registry exists
	if globalRegistry == nil {
		globalRegistry = &ToolRegistry{
			mcpClients:                  make(map[string]mcpclient.ClientInterface),
			customTools:                 make(map[string]func(ctx context.Context, args map[string]interface{}) (string, error)),
			virtualTools:                make(map[string]func(ctx context.Context, args map[string]interface{}) (string, error)),
			toolToServer:                make(map[string]string),
			sessionCustomTools:          make(map[string]map[string]func(ctx context.Context, args map[string]interface{}) (string, error)),
			sessionVirtualTools:         make(map[string]map[string]func(ctx context.Context, args map[string]interface{}) (string, error)),
			latestVirtualScopeBySession: make(map[string]string),
			logger:                      logger,
		}
	}

	globalRegistry.mu.Lock()
	defer globalRegistry.mu.Unlock()

	// Ensure sessionVirtualTools map is initialized
	if globalRegistry.sessionVirtualTools == nil {
		globalRegistry.sessionVirtualTools = make(map[string]map[string]func(ctx context.Context, args map[string]interface{}) (string, error))
	}

	// Replace the entire session entry (each agent provides a complete set)
	globalRegistry.sessionVirtualTools[sessionID] = make(map[string]func(ctx context.Context, args map[string]interface{}) (string, error))
	for toolName, executor := range virtualTools {
		globalRegistry.sessionVirtualTools[sessionID][toolName] = executor
		logger.Debug("Registered session-scoped virtual tool",
			loggerv2.String("session_id", sessionID),
			loggerv2.String("tool", toolName))
	}
	if globalRegistry.latestVirtualScopeBySession == nil {
		globalRegistry.latestVirtualScopeBySession = make(map[string]string)
	}
	baseSessionID := baseSessionFromVirtualScopeID(sessionID)
	if baseSessionID != "" {
		globalRegistry.latestVirtualScopeBySession[baseSessionID] = sessionID
	}

	logger.Info("Session-scoped virtual tools registered",
		loggerv2.String("session_id", sessionID),
		loggerv2.Int("tool_count", len(virtualTools)),
		loggerv2.Int("total_sessions", len(globalRegistry.sessionVirtualTools)))
}

// CallVirtualToolWithSession calls a virtual tool with session scoping
// It first checks session-scoped tools, then falls back to global tools
func CallVirtualToolWithSession(ctx context.Context, sessionID string, toolName string, args map[string]interface{}) (string, error) {
	if guarded := toolguard.Check(ctx, toolguard.Call{SessionID: sessionID, Kind: toolguard.KindVirtual, Tool: toolName, Args: args}); guarded.Stub {
		return guardedResult(guarded)
	}
	registry := GetRegistry()
	if registry == nil {
		return "", fmt.Errorf("tool registry not initialized")
	}

	executor, scoped, err := registry.virtualToolExecutor(sessionID, toolName)
	if err != nil {
		return "", err
	}
	result, err := executor(ctx, args)
	if scoped && err != nil && shouldRetryVirtualToolWithLatestScope(err) {
		registry.mu.RLock()
		latestScopeID, latestExecutor := registry.latestVirtualToolExecutorLocked(sessionID, toolName)
		registry.mu.RUnlock()
		if latestExecutor != nil {
			if registry.logger != nil {
				registry.logger.Warn("Retrying virtual tool with latest scope after stale empty discovery",
					loggerv2.String("tool", toolName),
					loggerv2.String("stale_scope_id", sessionID),
					loggerv2.String("latest_scope_id", latestScopeID),
					loggerv2.Error(err))
			}
			return latestExecutor(ctx, args)
		}
	}
	return result, err
}

func (registry *ToolRegistry) virtualToolExecutor(sessionID, toolName string) (func(context.Context, map[string]interface{}) (string, error), bool, error) {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	if sessionID != "" {
		if executor := registry.sessionVirtualTools[sessionID][toolName]; executor != nil {
			return executor, true, nil
		}
		if _, executor := registry.latestVirtualToolExecutorLocked(sessionID, toolName); executor != nil {
			return executor, false, nil
		}
		// Global virtual closures belong to whichever session registered last.
		return nil, false, fmt.Errorf("virtual tool %s is not registered for session %s", toolName, sessionID)
	}
	executor := registry.virtualTools[toolName]
	if executor == nil {
		return nil, false, fmt.Errorf("virtual tool %s not found (checked session: %s)", toolName, sessionID)
	}
	return executor, false, nil
}

func baseSessionFromVirtualScopeID(scopeID string) string {
	if scopeID == "" {
		return ""
	}
	if idx := strings.Index(scopeID, ":vt:"); idx >= 0 {
		return scopeID[:idx]
	}
	return scopeID
}

// shouldRetryVirtualToolWithLatestScope decides whether a virtual tool call's
// error looks like it came from a stale, incomplete view of the registry --
// the shape a restored/relaunched bridge process produces when it still
// carries an old session:vt:<trace> scope from before this session's full
// tool set was registered (see latestVirtualScopeBySession above) -- as
// opposed to a real, specific error that a different scope would not
// change.
//
// "Available servers/categories: []" is the legacy empty-discovery phrasing
// this was originally written against. get_api_spec's current wording
// (agent/code_execution_tools.go's unavailableToolsError) reports the same
// stale-view shape differently: "these names are not registered by any
// currently connected server." is unique to its healthy-servers-name-is-
// unknown branch, the one instance where a tool that is registered
// elsewhere plausibly cannot be found through this specific scope. Its
// other branches (a configured server that is down, or a genuine
// permission denial) deliberately do not contain either phrase -- retrying
// those against a "latest" scope would not help, matching how
// TestCallVirtualToolWithSessionKeepsScopedNonEmptyDiscoveryErrors already
// requires a legitimate non-empty-discovery error not to trigger a retry.
func shouldRetryVirtualToolWithLatestScope(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "Available servers/categories: []") ||
		strings.Contains(msg, "these names are not registered by any currently connected server")
}

func (r *ToolRegistry) latestVirtualToolExecutorLocked(sessionID, toolName string) (string, func(ctx context.Context, args map[string]interface{}) (string, error)) {
	if r == nil || sessionID == "" || toolName == "" || r.latestVirtualScopeBySession == nil || r.sessionVirtualTools == nil {
		return "", nil
	}
	baseSessionID := baseSessionFromVirtualScopeID(sessionID)
	latestScopeID := r.latestVirtualScopeBySession[baseSessionID]
	if latestScopeID == "" || latestScopeID == sessionID {
		return "", nil
	}
	latestTools := r.sessionVirtualTools[latestScopeID]
	if latestTools == nil {
		return "", nil
	}
	executor := latestTools[toolName]
	if executor == nil {
		return "", nil
	}
	return latestScopeID, executor
}

// InitRegistryForSession registers custom tools scoped to a specific session
// This prevents cross-workflow contamination when multiple workflows run concurrently
// Each session gets its own set of custom tool executors (with their own folder guard paths)
func InitRegistryForSession(sessionID string, customTools map[string]func(ctx context.Context, args map[string]interface{}) (string, error), logger loggerv2.Logger) {
	if sessionID == "" {
		// No session ID - fall back to global registration
		if logger != nil {
			logger.Warn("InitRegistryForSession called with empty sessionID, falling back to global registry")
		}
		return
	}

	registryLockDebug("InitRegistryForSession:" + sessionID)
	defer registryUnlockDebug("InitRegistryForSession:" + sessionID)

	if logger == nil {
		logger = loggerv2.NewNoop()
	}

	// Ensure global registry exists
	if globalRegistry == nil {
		globalRegistry = &ToolRegistry{
			mcpClients:                  make(map[string]mcpclient.ClientInterface),
			customTools:                 make(map[string]func(ctx context.Context, args map[string]interface{}) (string, error)),
			virtualTools:                make(map[string]func(ctx context.Context, args map[string]interface{}) (string, error)),
			toolToServer:                make(map[string]string),
			sessionCustomTools:          make(map[string]map[string]func(ctx context.Context, args map[string]interface{}) (string, error)),
			sessionVirtualTools:         make(map[string]map[string]func(ctx context.Context, args map[string]interface{}) (string, error)),
			latestVirtualScopeBySession: make(map[string]string),
			logger:                      logger,
		}
	}

	globalRegistry.mu.Lock()
	defer globalRegistry.mu.Unlock()

	// Ensure sessionCustomTools map is initialized
	if globalRegistry.sessionCustomTools == nil {
		globalRegistry.sessionCustomTools = make(map[string]map[string]func(ctx context.Context, args map[string]interface{}) (string, error))
	}

	// Create or update session-scoped tools
	if globalRegistry.sessionCustomTools[sessionID] == nil {
		globalRegistry.sessionCustomTools[sessionID] = make(map[string]func(ctx context.Context, args map[string]interface{}) (string, error))
	}

	// Register all custom tools for this session
	for toolName, executor := range customTools {
		globalRegistry.sessionCustomTools[sessionID][toolName] = executor
	}

	logger.Info("Session-scoped custom tools registered",
		loggerv2.String("session_id", sessionID),
		loggerv2.Int("tool_count", len(customTools)),
		loggerv2.Int("total_sessions", len(globalRegistry.sessionCustomTools)))
}

// IsSessionToolAllowed lets metadata discovery enforce the same current policy
// as HTTP execution. HTTP requests do not carry the in-process turn context.
func IsSessionToolAllowed(sessionID, toolName string) bool {
	registry := GetRegistry()
	if registry == nil || sessionID == "" {
		return true
	}
	registry.allowListMu.RLock()
	defer registry.allowListMu.RUnlock()
	allowed, restricted := registry.sessionToolAllowLists[sessionID]
	return !restricted || allowed[toolName]
}

// SetSessionToolAllowList sets the tool allow list for a session in the code execution registry.
// When set, CallCustomToolWithSession will reject tools not in the list.
// Pass nil to clear the restriction (all tools allowed).
func SetSessionToolAllowList(sessionID string, allowList map[string]bool) {
	// Policy has its own mutex; registry lookups never hold a lock during execution.
	registry := GetRegistry()
	if registry == nil {
		return
	}
	registry.allowListMu.Lock()
	defer registry.allowListMu.Unlock()
	if registry.sessionToolAllowLists == nil {
		registry.sessionToolAllowLists = make(map[string]map[string]bool)
	}
	if allowList == nil {
		delete(registry.sessionToolAllowLists, sessionID)
	} else {
		registry.sessionToolAllowLists[sessionID] = allowList
	}
}

// allowedToolNamesInError caps how many allowed names a single rejection lists.
// Enough for the model to pick a real alternative, small enough that a denial
// cannot dominate the turn.
const allowedToolNamesInError = 30

// toolNotAllowedError explains a session allow-list denial without inventing a
// cause for it.
//
// The old text was "tool %q is not available in the current workshop mode". This
// registry has no concept of workshop mode: the list it consults is a generic
// per-session tool-access policy written by Agent.setToolAccess and by
// turn_session.go from the turn policy. Naming a mode asserted something the code
// cannot know, and worse, named a condition the model believes it can change — so
// a denial read as "retry after switching modes" rather than "this agent was not
// given this tool".
//
// It cost a real run. On 2026-08-04 a Pulse Fixer stage was denied update_schedule,
// which its bounded surface (pulseFixerStageToolAgentAllowedToolNames) omits on
// purpose. It concluded "The Pulse Fixer session is not Workshop mode", verified
// workflow.json was unchanged, and abandoned the edit. The mode was never the
// reason, so no action it could take would have worked.
//
// This mirrors Agent.unavailableToolsError: say what is true, say plainly that
// retrying will not help, and name the surface that is actually available.
// toolNameExists reports whether any registry partition knows this name at all,
// for this session or globally. It is the difference between "withheld" and
// "gone", which the allow-list check cannot see on its own because it runs
// before name resolution.
func (r *ToolRegistry) toolNameExists(sessionID, toolName string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if _, ok := r.customTools[toolName]; ok {
		return true
	}
	if _, ok := r.virtualTools[toolName]; ok {
		return true
	}
	if _, ok := r.toolToServer[toolName]; ok {
		return true
	}
	if sessionID != "" {
		if tools, ok := r.sessionCustomTools[sessionID]; ok {
			if _, ok := tools[toolName]; ok {
				return true
			}
		}
		if tools, ok := r.sessionVirtualTools[sessionID]; ok {
			if _, ok := tools[toolName]; ok {
				return true
			}
		}
	}
	return false
}

// toolNotAllowedError explains an allow-list denial without inventing a cause.
//
// exists separates the two cases the allow-list check collapses. A tool that was
// removed and a tool that is merely withheld produced identical text, so an agent
// reaching for a retired name read "not allowed" as "wrong permissions" and tried
// a variant instead of stopping. On 2026-08-04, the night after the Pulse surface
// was consolidated from eight tools to four, one Pulse Gate session invented six
// names in a row — mark_final_command, mark_pulse_command, pulse_command_state,
// record_pulse_command, set_pulse_command_state, update_final_command_state —
// none of which exist anywhere in the codebase, all clearly reconstructed from
// the removed mark_pulse_final_command_result.
func toolNotAllowedError(toolName string, allowed map[string]bool, exists bool) error {
	var b strings.Builder
	if !exists {
		fmt.Fprintf(&b, "tool_not_found: no tool named %q is registered for this session, under any name partition. ", toolName)
		b.WriteString("It does not exist — it was never registered, or it was removed or renamed. ")
		b.WriteString("Guessing a variant of this name will NOT work; every variant fails the same way. ")
		b.WriteString("Use a name from the list below, or do the task without it. ")
	} else {
		fmt.Fprintf(&b, "tool_not_allowed: %q is registered but is not in this session's allowed tool set. ", toolName)
		b.WriteString("This is a fixed grant for this agent, not a transient failure or a mode you can switch: ")
		b.WriteString("retrying, renaming, or changing settings will NOT make it callable. ")
		b.WriteString("Do the task without it, or report it as outside this agent's scope. ")
	}

	names := make([]string, 0, len(allowed))
	for name, ok := range allowed {
		if ok {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		b.WriteString("This session has no tools allowed at all.")
		return errors.New(b.String())
	}
	sort.Strings(names)
	if len(names) > allowedToolNamesInError {
		fmt.Fprintf(&b, "Allowed here (%d of %d): %s, ...", allowedToolNamesInError, len(names),
			strings.Join(names[:allowedToolNamesInError], ", "))
	} else {
		fmt.Fprintf(&b, "Allowed here: %s", strings.Join(names, ", "))
	}
	return errors.New(b.String())
}

// registryScopeForSession returns the session whose registry should serve a
// call to toolName: the session itself when it has a registry, otherwise the
// live parent run it was registered under via mcpclient.RegisterHTTPSession,
// but only when that parent registered toolName (custom or virtual). Anything
// else resolves to the session itself and keeps the legacy lookup, so this
// never turns a call that used to resolve into a "not registered" failure.
// Unregistered, stopped and ambiguous children never resolve to a parent.
func (r *ToolRegistry) registryScopeForSession(sessionID, toolName string) string {
	if sessionID == "" {
		return sessionID
	}
	r.mu.RLock()
	_, own := r.sessionCustomTools[sessionID]
	r.mu.RUnlock()
	if own {
		return sessionID
	}
	parent := mcpclient.GetSessionRegistry().HTTPSessionForMCPSession(sessionID)
	if parent == "" || parent == sessionID {
		return sessionID
	}
	r.mu.RLock()
	_, custom := r.sessionCustomTools[parent][toolName]
	_, virtual := r.sessionVirtualTools[parent][toolName]
	r.mu.RUnlock()
	if !custom && !virtual {
		// The run's own tools are registered by one of its other live sessions (its workflow session), not by the HTTP
		// run id itself. A step's bridge session has no registry, so without this it fell to the global table, where
		// the last registration wins: a Crew chat running at the same moment could hand it ITS executor, and the
		// ownership check then rejected the step (PLAT-514). Use the run's sibling session only when exactly one
		// registered the tool; none or several keeps the legacy lookup.
		return r.siblingScopeForTool(parent, sessionID, toolName)
	}
	if r.logger != nil {
		r.logger.Debug("Resolved child session to its parent run's tool registry",
			loggerv2.String("session_id", sessionID),
			loggerv2.String("parent_session_id", parent),
			loggerv2.String("tool", toolName))
	}
	return parent
}

// siblingScopeForTool returns the one live session under httpSessionID, other than self, whose registry holds toolName
// (custom or virtual); self when there is not exactly one.
func (r *ToolRegistry) siblingScopeForTool(httpSessionID, self, toolName string) string {
	var found []string
	r.mu.RLock()
	for _, candidate := range mcpclient.GetSessionRegistry().MCPSessionsForHTTPSession(httpSessionID) {
		if candidate == self {
			continue
		}
		_, custom := r.sessionCustomTools[candidate][toolName]
		_, virtual := r.sessionVirtualTools[candidate][toolName]
		if custom || virtual {
			found = append(found, candidate)
		}
	}
	r.mu.RUnlock()
	if len(found) != 1 {
		return self
	}
	if r.logger != nil {
		r.logger.Debug("Resolved child session to its sibling session's tool registry",
			loggerv2.String("session_id", self),
			loggerv2.String("sibling_session_id", found[0]),
			loggerv2.String("tool", toolName))
	}
	return found[0]
}

// CallCustomToolWithSession calls a custom tool with session scoping.
// Once a session registry exists it is authoritative: a missing tool must fail
// instead of borrowing the most recently registered global executor, which may
// belong to another concurrently running workflow. Global fallback is retained
// only for callers that do not yet have a session registry.
//
// A workflow's scripted-step bridge session (session-group-*) never gets a
// registry of its own. When mcpclient knows it as the live child of a parent
// run whose registry has the tool, the parent's executor serves the call, so it
// cannot land on another run's global executor. The allow list is checked
// against the calling session before that resolution: the parent chat's
// per-turn ToolPolicy governs the parent's agent, not a step script.
func CallCustomToolWithSession(ctx context.Context, sessionID string, toolName string, args map[string]interface{}) (string, error) {
	if guarded := toolguard.Check(ctx, toolguard.Call{SessionID: sessionID, Kind: toolguard.KindCustom, Tool: toolName, Args: args}); guarded.Stub {
		return guardedResult(guarded)
	}
	registry := GetRegistry()
	if registry == nil {
		return "", fmt.Errorf("tool registry not initialized")
	}
	// Check session-scoped tool allow list first (uses allowListMu, independent of registry.mu)
	if sessionID != "" {
		registry.allowListMu.RLock()
		blocked := false
		var allowed map[string]bool
		if registry.sessionToolAllowLists != nil {
			if allowList, exists := registry.sessionToolAllowLists[sessionID]; exists && !allowList[toolName] {
				blocked = true
				allowed = allowList
			}
		}
		registry.allowListMu.RUnlock()
		if blocked {
			if registry.logger != nil {
				registry.logger.Info("🔒 [TOOL_ALLOW_LIST] Blocked code-exec HTTP call",
					loggerv2.String("session_id", sessionID),
					loggerv2.String("tool", toolName))
			}
			return "", toolNotAllowedError(toolName, allowed, registry.toolNameExists(sessionID, toolName))
		}
	}

	sessionID = registry.registryScopeForSession(sessionID, toolName)

	executor, err := registry.customToolExecutor(sessionID, toolName)
	if err != nil {
		return "", err
	}
	return executor(ctx, args)
}

// Copy the selected closure while locked. Routes can create and clean up child
// sessions, so invoking that closure under the registry lock would deadlock.
func (registry *ToolRegistry) customToolExecutor(sessionID, toolName string) (func(context.Context, map[string]interface{}) (string, error), error) {
	registry.mu.RLock()
	defer registry.mu.RUnlock()

	// Priority 1: Check session-scoped tools first (if sessionID provided)
	if sessionID != "" && registry.sessionCustomTools != nil {
		if sessionTools, exists := registry.sessionCustomTools[sessionID]; exists {
			if executor, exists := sessionTools[toolName]; exists {
				if registry.logger != nil {
					registry.logger.Debug("Using session-scoped custom tool",
						loggerv2.String("session_id", sessionID),
						loggerv2.String("tool", toolName))
				}
				return executor, nil
			}
			// Reserved tools like get_api_spec are registered in this session's
			// VIRTUAL partition, not its custom one. The model addresses every
			// tool by a single name and cannot know which endpoint owns it, so a
			// bridge that routes get_api_spec to /api/custom/execute used to die
			// here even though the session had it. Same session, different
			// partition — this is not the cross-session fallback that
			// TestCallCustomToolWithSessionDoesNotBorrowGlobalExecutor forbids.
			if registry.sessionVirtualTools != nil {
				if virtualTools, hasVirtual := registry.sessionVirtualTools[sessionID]; hasVirtual {
					if executor, isVirtual := virtualTools[toolName]; isVirtual {
						if registry.logger != nil {
							registry.logger.Debug("Custom-tool request resolved from the session's virtual tools",
								loggerv2.String("session_id", sessionID),
								loggerv2.String("tool", toolName))
						}
						return executor, nil
					}
				}
			}
			return nil, fmt.Errorf("custom tool %s is not registered for session %s; use this session’s declared tools: search_tools -> get_api_spec -> call_tool when available; do not retry this unregistered name or bypass it with shell HTTP calls", toolName, sessionID)
		}
		// No registry exists for this session yet. Keep the legacy global path for
		// non-workflow callers that have not initialized session-scoped tools.
		if registry.logger != nil {
			registry.logger.Debug("Session registry not found, falling back to global",
				loggerv2.String("session_id", sessionID),
				loggerv2.String("tool", toolName))
		}
	}

	// Priority 2: Fall back to global custom tools
	executor, exists := registry.customTools[toolName]
	if !exists {
		return nil, fmt.Errorf("custom tool %s not found (checked session: %s)", toolName, sessionID)
	}

	if registry.logger != nil {
		registry.logger.Debug("Using global custom tool (no session scope)",
			loggerv2.String("tool", toolName),
			loggerv2.String("session_id", sessionID))
	}

	return executor, nil
}

// CleanupSession removes all session-scoped tools for a given session
// Call this when a workflow/session completes to free memory
func CleanupSession(sessionID string) {
	if sessionID == "" {
		return
	}

	registry := GetRegistry()
	if registry == nil {
		return
	}

	registry.mu.Lock()
	defer registry.mu.Unlock()

	if registry.sessionCustomTools != nil {
		if _, exists := registry.sessionCustomTools[sessionID]; exists {
			delete(registry.sessionCustomTools, sessionID)
		}
	}

	if registry.sessionVirtualTools != nil {
		// Clean up exact match (legacy: virtual tools keyed by sessionID)
		if _, exists := registry.sessionVirtualTools[sessionID]; exists {
			delete(registry.sessionVirtualTools, sessionID)
		}
		// Clean up per-agent virtual tool scopes (keyed as "sessionID:vt:traceID")
		prefix := sessionID + ":vt:"
		for key := range registry.sessionVirtualTools {
			if strings.HasPrefix(key, prefix) {
				delete(registry.sessionVirtualTools, key)
			}
		}
	}
	if registry.latestVirtualScopeBySession != nil {
		delete(registry.latestVirtualScopeBySession, sessionID)
		prefix := sessionID + ":vt:"
		for base, scope := range registry.latestVirtualScopeBySession {
			if scope == sessionID || strings.HasPrefix(scope, prefix) {
				delete(registry.latestVirtualScopeBySession, base)
			}
		}
	}

	// Clean up allow list (uses allowListMu, independent of registry.mu)
	registry.allowListMu.Lock()
	if registry.sessionToolAllowLists != nil {
		delete(registry.sessionToolAllowLists, sessionID)
	}
	registry.allowListMu.Unlock()

	if registry.logger != nil {
		registry.logger.Info("Cleaned up session-scoped tools",
			loggerv2.String("session_id", sessionID))
	}
}

// convertResultToString converts MCP CallToolResult to string
// Simply extracts all text content directly without any JSON parsing
func convertResultToString(result *mcp.CallToolResult) string {
	if result == nil {
		return ""
	}

	// Extract all text content directly
	var textParts []string
	for _, content := range result.Content {
		// Try both pointer and value type assertions
		if textContent, ok := content.(*mcp.TextContent); ok {
			// Return text content directly, no JSON parsing
			textParts = append(textParts, textContent.Text)
		} else if textContent, ok := content.(mcp.TextContent); ok {
			// Handle value type (not pointer)
			textParts = append(textParts, textContent.Text)
		} else if embedded, ok := content.(*mcp.EmbeddedResource); ok {
			// Extract text from embedded resources
			switch r := embedded.Resource.(type) {
			case *mcp.TextResourceContents:
				textParts = append(textParts, r.Text)
			}
		}
		// Ignore ImageContent and other types - we only want text
	}

	joined := strings.Join(textParts, "\n")

	if result.IsError {
		if joined == "" {
			return "Tool execution error (no error details available)"
		}
		return fmt.Sprintf("Error: %s", joined)
	}

	if joined == "" {
		return "Tool execution completed (no output returned)"
	}

	return joined
}

// isActualGoBuildError checks if an error is actually a Go build/compilation error
// Uses multiple heuristics to avoid false positives:
// 1. Explicit build error markers
// 2. Go compiler error pattern matching (filename:line:column:)
// 3. Compilation error keywords in build context
// 4. Error type checking (for future enhancement)
func isActualGoBuildError(errorMsg, fullContent string) bool {
	// Normalize for case-insensitive comparison
	errorLower := strings.ToLower(errorMsg)
	contentLower := strings.ToLower(fullContent)

	// Heuristic 1: Check for explicit Go build error markers
	// These are definitive indicators of build errors
	buildErrorMarkers := []string{
		"failed to build plugin",
		"build output:",
		"go build",
	}

	for _, marker := range buildErrorMarkers {
		if strings.Contains(errorLower, marker) || strings.Contains(contentLower, marker) {
			return true
		}
	}

	// Heuristic 2: Check for Go compiler error patterns
	// Go compiler errors have specific formats like "filename:line:column: message"
	// Pattern: file.go:line:column: error message
	goCompilerPattern := `\.go:\d+:\d+:`
	if matched, _ := regexp.MatchString(goCompilerPattern, errorMsg); matched {
		return true
	}
	if matched, _ := regexp.MatchString(goCompilerPattern, fullContent); matched {
		return true
	}

	// Heuristic 3: Check for specific Go compilation error keywords
	// Only if they appear in a build context (not just random text)
	compilationErrors := []string{
		"syntax error",
		"undefined:",
		"cannot use",
		"wrong signature",
		"does not export",
		"cannot find package",
		"no required module",
		"missing go.sum",
	}

	// Check if any compilation error appears AND it's in a build context
	for _, errKeyword := range compilationErrors {
		if strings.Contains(contentLower, errKeyword) {
			// Additional check: make sure it's not just part of normal output
			// Build errors typically appear with file paths or in error contexts
			if strings.Contains(contentLower, ".go:") ||
				strings.Contains(contentLower, "compilation") ||
				strings.Contains(contentLower, "build") {
				return true
			}
		}
	}

	// Heuristic 4: Check error chain for GoBuildError type
	// This would be set if the error was properly wrapped
	// (Future enhancement: if we wrap build errors with GoBuildError type)
	// For now, we can check if errors.Is would match (if we implement it)

	// If none of the heuristics match, it's not a Go build error
	return false
}

// guardedResult turns a tool guard's stub into the bridge result (PLAT-562).
func guardedResult(d toolguard.Decision) (string, error) {
	if d.IsError {
		return "", errors.New(d.Result)
	}
	return d.Result, nil
}
