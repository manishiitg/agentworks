package common

import (
	"crypto/sha256"
	"encoding/hex"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// FolderGuardConfig represents folder access restrictions
type FolderGuardConfig struct {
	Enabled           bool     `json:"enabled"`
	ReadPaths         []string `json:"read_paths"`
	WritePaths        []string `json:"write_paths"`
	BlockedPaths      []string `json:"blocked_paths"`
	BlockedWritePaths []string `json:"blocked_write_paths,omitempty"`
}

// ContextKey is a custom type for context keys to avoid collisions
type ContextKey string

const (
	// FolderGuardReadPathsKey is the context key for folder guard read paths
	FolderGuardReadPathsKey ContextKey = "folder_guard_read_paths"
	// FolderGuardWritePathsKey is the context key for folder guard write paths
	FolderGuardWritePathsKey ContextKey = "folder_guard_write_paths"
	// FolderGuardBlockedPathsKey is the context key for blocked paths (deny list)
	FolderGuardBlockedPathsKey ContextKey = "folder_guard_blocked_paths"
	// FolderGuardBlockedWritePathsKey is the context key for write-only deny paths.
	FolderGuardBlockedWritePathsKey ContextKey = "folder_guard_blocked_write_paths"
	// FolderGuardAllowedWriteFolderKey is the context key for allowed write folders ([]string) in chat/plan mode
	FolderGuardAllowedWriteFolderKey ContextKey = "folder_guard_allowed_write_folder"
	// UserIDKey is the context key for the user ID (used for auth/database scoping)
	UserIDKey ContextKey = "user_id"
	// UsernameKey is the human-readable account name attached to logs and
	// diagnostics. Authorization must continue to use UserIDKey.
	UsernameKey ContextKey = "username"
	// WorkflowNameKey is the short, human-readable workflow name attached to
	// logs. It is diagnostic metadata, not a workspace authorization input.
	WorkflowNameKey ContextKey = "workflow_name"
	// BrowserDownloadsPathKey is the context key for the browser downloads folder path (relative to workspace root)
	// Used by agent-browser executor to set the working directory for screenshot/download commands
	BrowserDownloadsPathKey ContextKey = "browser_downloads_path"
	// DefaultWorkingDirKey is the context key for the session-level default working directory.
	// Used by execute_shell_command as a safety-net: if the LLM passes "." it is replaced
	// with this value so commands run in the correct folder.
	// Set per mode:
	//   - plan mode  → "Chats"  (wrapExecutorsWithPlanFolderGuard)
	//   - chat mode  → "Chats"  (wrapExecutorsWithChatModeFolderGuard)
	DefaultWorkingDirKey ContextKey = "default_working_dir"
	// ChatSessionIDKey is the context key for the agent-level session ID.
	// Delegated agents inherit the parent workflow browser session.
	// Used by browser session tracker for per-agent limits.
	ChatSessionIDKey ContextKey = "chat_session_id"
	// WorkflowSessionIDKey is the context key for the root workflow/chat session ID.
	// Always the parent session — never changes for isolated sub-agents.
	// Used by browser session tracker for per-workflow limits.
	WorkflowSessionIDKey ContextKey = "workflow_session_id"
)

// WorkspaceFolders are the standard workspace folders.
var WorkspaceFolders = []string{"Chats", "Downloads"}

// HostDownloadsPath returns the host Downloads directory used by native Chrome
// in CDP mode. PI_HOST_DOWNLOADS_PATH/HOST_DOWNLOADS_PATH can override this for
// packaged or Docker deployments where the backend HOME is not the user's HOME.
func HostDownloadsPath() string {
	for _, key := range []string{"PI_HOST_DOWNLOADS_PATH", "HOST_DOWNLOADS_PATH"} {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return filepath.Clean(expandHomePath(value))
		}
	}
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return ""
	}
	return filepath.Join(home, "Downloads")
}

func expandHomePath(path string) string {
	if path == "~" || strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
			if path == "~" {
				return home
			}
			return filepath.Join(home, strings.TrimPrefix(path, "~/"))
		}
	}
	return path
}

// CDPHostDownloadsPath returns the host Downloads path only for CDP mode.
// Headless browser downloads should stay inside workspace folders.
func CDPHostDownloadsPath(browserMode string) string {
	if strings.EqualFold(strings.TrimSpace(browserMode), "cdp") {
		return HostDownloadsPath()
	}
	return ""
}

// CDPHostDownloadsReadPath is the compatibility name used by callers that
// intentionally keep Downloads read-only. New read-write sessions use
// CDPHostDownloadsPath with GrantSessionCDPHostDownloadsReadWrite.
func CDPHostDownloadsReadPath(browserMode string) string {
	return CDPHostDownloadsPath(browserMode)
}

// GrantSessionCDPHostDownloadsReadOnly keeps the existing read-only contract
// available while workflow-owned CDP sessions migrate to explicit read-write
// access. The write deny is required on macOS, where the sandbox otherwise
// permits much of the user's home directory.
func GrantSessionCDPHostDownloadsReadOnly(sessionID, browserMode string) string {
	hostDownloads := CDPHostDownloadsReadPath(browserMode)
	if strings.TrimSpace(sessionID) == "" || hostDownloads == "" {
		return ""
	}
	updateSessionShellConfig(sessionID, func(cfg *SessionShellConfig) {
		cfg.ReadPaths = DeduplicateStrings(append(cfg.ReadPaths, hostDownloads))
		cfg.BlockedWritePaths = DeduplicateStrings(append(cfg.BlockedWritePaths, hostDownloads))
	})
	log.Printf("[SHELL] Granted read-only host Downloads for CDP session %s: %s", sessionID, hostDownloads)
	return hostDownloads
}

// GrantSessionCDPHostDownloadsReadWrite adds the host Downloads directory to
// both read and write paths for a CDP session.
// GrantSessionCrewAttachmentReads adds crew attachment roots to a session's
// read paths and blocked-write paths: workflow steps may read crew files
// through attachment aliases but never write them. Appends only, so it
// composes with other session grants.
func GrantSessionCrewAttachmentReads(sessionID string, roots []string) {
	if strings.TrimSpace(sessionID) == "" || len(roots) == 0 {
		return
	}
	updateSessionShellConfig(sessionID, func(cfg *SessionShellConfig) {
		cfg.ReadPaths = DeduplicateStrings(append(cfg.ReadPaths, roots...))
		cfg.BlockedWritePaths = DeduplicateStrings(append(cfg.BlockedWritePaths, roots...))
	})
	log.Printf("[SHELL] Granted read-only crew attachments for session %s: %v", sessionID, roots)
}

func GrantSessionCDPHostDownloadsReadWrite(sessionID, browserMode string) string {
	hostDownloads := CDPHostDownloadsPath(browserMode)
	if strings.TrimSpace(sessionID) == "" || hostDownloads == "" {
		return ""
	}
	updateSessionShellConfig(sessionID, func(cfg *SessionShellConfig) {
		cfg.ReadPaths = DeduplicateStrings(append(cfg.ReadPaths, hostDownloads))
		cfg.WritePaths = DeduplicateStrings(append(cfg.WritePaths, hostDownloads))
		kept := cfg.BlockedWritePaths[:0]
		for _, blocked := range cfg.BlockedWritePaths {
			if filepath.Clean(strings.TrimSpace(blocked)) != filepath.Clean(hostDownloads) {
				kept = append(kept, blocked)
			}
		}
		cfg.BlockedWritePaths = kept
	})
	log.Printf("[SHELL] Granted read-write host Downloads for CDP session %s: %s", sessionID, hostDownloads)
	return hostDownloads
}

// PopulateMCPBridgeShortEnv derives short shell-friendly MCP bridge variables
// from MCP_API_URL and MCP_API_TOKEN. It mutates env in-place.
func PopulateMCPBridgeShortEnv(env map[string]string) {
	if env == nil {
		return
	}
	baseURL := strings.TrimRight(strings.TrimSpace(env["MCP_API_URL"]), "/")
	if baseURL == "" {
		delete(env, "MCP_MCP")
		delete(env, "MCP_CUSTOM")
		delete(env, "MCP_VIRTUAL")
	} else {
		env["MCP_MCP"] = baseURL + "/tools/mcp"
		env["MCP_CUSTOM"] = baseURL + "/tools/custom"
		env["MCP_VIRTUAL"] = baseURL + "/tools/virtual"
	}

	// An env that runs as a session carries that session's own token, so
	// every place that sets MCP_SESSION_ID (steps, background agents, session
	// changes) re-derives it here (bridge_token.go).
	if sessionToken := BridgeTokenForSession(env["MCP_SESSION_ID"]); sessionToken != "" {
		env["MCP_API_TOKEN"] = sessionToken
	}
	token := strings.TrimSpace(env["MCP_API_TOKEN"])
	if token == "" {
		delete(env, "MCP_AUTH")
		return
	}
	env["MCP_AUTH"] = "Authorization: Bearer " + token
}

// SessionShellConfig holds per-session shell execution settings.
// Shared by execute_shell_command and agent_browser to ensure identical sandboxing.
//
// BlockedWritePaths denies writes only (reads pass through). See FolderGuardConfig
// docs in pkg/workspace for the semantic distinction vs the isolator's BlockedPaths
// (which is "deny all"). Typical use: chat-agent with #workflow grants Workflow/<name>/
// as a write prefix but adds Workflow/<name>/planning/ to BlockedWritePaths so the
// agent can read plan.json but not raw-write it.
type SessionShellConfig struct {
	WorkflowPath      string   // Owning workflow for live capability reconciliation
	WorkingDir        string   // Default working directory (relative to workspace-docs)
	FolderGuardSet    bool     // An explicit guard exists; empty capabilities must fail closed
	ReadPaths         []string // Folder guard read paths for Isolator
	WritePaths        []string // Folder guard write paths for Isolator
	BlockedPaths      []string // Deny reads and writes
	BlockedWritePaths []string // Deny writes; reads allowed (flows to FolderGuardConfig.BlockedWritePaths)
	// StrictAllowlist switches the shell isolator to deny-by-default: only the
	// guard's paths, system binaries and scratch space are visible. DenyNetwork
	// additionally removes outbound network (strict mode only). Both come from
	// an agent profile's runtime.sandbox policy.
	StrictAllowlist  bool
	DenyNetwork      bool
	BrowserMode      string // Resolved browser mode: "headless", "cdp", ""
	BrowserSessionID string // Legacy browser binding; authenticated namespaces take precedence
	// BrowserSessionNamespace isolates every public browser name (including
	// explicit names) to the signed-in user and owning chat/workflow.
	BrowserSessionNamespace string
	// Env is extra environment variables exported into this session's shell
	// (bridge execute_shell_command). Lets per-step values like DB_PATH and
	// STEP_OUTPUT_DIR reach the server-side bridge shell, which — unlike the
	// in-process built-in executor — has no other channel for them.
	Env map[string]string
}

// SetSessionWorkflowPath associates a shell policy with one workflow so live
// workflow-level capabilities can be narrowed or revoked without touching
// another workflow that happens to grant the same host path.
func SetSessionWorkflowPath(sessionID, workflowPath string) {
	updateSessionShellConfig(sessionID, func(cfg *SessionShellConfig) {
		cfg.WorkflowPath = strings.Trim(strings.TrimSpace(workflowPath), "/")
	})
}

// ReconcileSessionWorkflowFolderAccess updates attached-folder capabilities in
// every still-open session for one workflow. Normal workspace paths are kept;
// only roots from the previous folder_access snapshot are replaced.
func ReconcileSessionWorkflowFolderAccess(workflowPath string, previousRoots, readRoots, writeRoots, readOnlyRoots []string, env map[string]string) {
	workflowPath = strings.Trim(strings.TrimSpace(workflowPath), "/")
	if workflowPath == "" {
		return
	}
	previous := make(map[string]struct{}, len(previousRoots))
	for _, path := range previousRoots {
		previous[filepath.Clean(strings.TrimSpace(path))] = struct{}{}
	}
	removePrevious := func(paths []string) []string {
		kept := paths[:0]
		for _, path := range paths {
			if _, remove := previous[filepath.Clean(strings.TrimSpace(path))]; !remove {
				kept = append(kept, path)
			}
		}
		return kept
	}
	appendUnique := func(paths []string, additions []string) []string {
		seen := make(map[string]struct{}, len(paths)+len(additions))
		result := make([]string, 0, len(paths)+len(additions))
		for _, path := range append(paths, additions...) {
			clean := filepath.Clean(strings.TrimSpace(path))
			if clean == "." || clean == "" {
				continue
			}
			if _, exists := seen[clean]; exists {
				continue
			}
			seen[clean] = struct{}{}
			result = append(result, clean)
		}
		return result
	}

	sessionShellConfigsMu.Lock()
	defer sessionShellConfigsMu.Unlock()
	for sessionID, existing := range sessionShellConfigs {
		if existing == nil || !sessionBelongsToWorkflow(existing, workflowPath) {
			continue
		}
		cfg := cloneSessionShellConfig(existing)
		cfg.ReadPaths = appendUnique(removePrevious(cfg.ReadPaths), readRoots)
		cfg.WritePaths = appendUnique(removePrevious(cfg.WritePaths), writeRoots)
		cfg.BlockedWritePaths = appendUnique(removePrevious(cfg.BlockedWritePaths), readOnlyRoots)
		if cfg.Env == nil {
			cfg.Env = make(map[string]string)
		}
		for key := range cfg.Env {
			if strings.HasPrefix(key, "WORKFLOW_FOLDER_") {
				delete(cfg.Env, key)
			}
		}
		for key, value := range env {
			cfg.Env[key] = value
		}
		sessionShellConfigs[sessionID] = cfg
	}
}

// ReconcileSessionCrewAttachments updates crew-attachment capabilities in
// every still-open session for one workflow when attachments change. Only
// roots from the previous crew snapshot are replaced; folder grants and
// normal workspace paths are untouched. Detaching an attachment revokes
// its root from live sessions immediately instead of waiting for the next
// run. Crew roots are read-only: they enter ReadPaths and BlockedWritePaths,
// never WritePaths.
func ReconcileSessionCrewAttachments(workflowPath string, previousRoots, readOnlyRoots []string, env map[string]string) {
	workflowPath = strings.Trim(strings.TrimSpace(workflowPath), "/")
	if workflowPath == "" {
		return
	}
	previous := make(map[string]struct{}, len(previousRoots))
	for _, path := range previousRoots {
		previous[filepath.Clean(strings.TrimSpace(path))] = struct{}{}
	}
	removePrevious := func(paths []string) []string {
		kept := paths[:0]
		for _, path := range paths {
			if _, remove := previous[filepath.Clean(strings.TrimSpace(path))]; !remove {
				kept = append(kept, path)
			}
		}
		return kept
	}
	appendUnique := func(paths []string, additions []string) []string {
		seen := make(map[string]struct{}, len(paths)+len(additions))
		result := make([]string, 0, len(paths)+len(additions))
		for _, path := range append(paths, additions...) {
			clean := filepath.Clean(strings.TrimSpace(path))
			if clean == "." || clean == "" {
				continue
			}
			if _, exists := seen[clean]; exists {
				continue
			}
			seen[clean] = struct{}{}
			result = append(result, clean)
		}
		return result
	}

	sessionShellConfigsMu.Lock()
	defer sessionShellConfigsMu.Unlock()
	for sessionID, existing := range sessionShellConfigs {
		if existing == nil || !sessionBelongsToWorkflow(existing, workflowPath) {
			continue
		}
		cfg := cloneSessionShellConfig(existing)
		cfg.ReadPaths = appendUnique(removePrevious(cfg.ReadPaths), readOnlyRoots)
		cfg.BlockedWritePaths = appendUnique(removePrevious(cfg.BlockedWritePaths), readOnlyRoots)
		if cfg.Env == nil {
			cfg.Env = make(map[string]string)
		}
		for key := range cfg.Env {
			if strings.HasPrefix(key, "WORKFLOW_CREW_") {
				delete(cfg.Env, key)
			}
		}
		for key, value := range env {
			cfg.Env[key] = value
		}
		sessionShellConfigs[sessionID] = cfg
	}
}

// ApplySessionWorkflowFolderAccess refreshes one live session from the current
// workflow manifest. Previous attached roots are derived from the session's
// WORKFLOW_FOLDER_*, WORKFLOW_KB_*, and WORKFLOW_CREW_* environment, so this
// also handles approval after the agent process was already created, and so
// detached crew attachments lose their session grant on refresh.
func ApplySessionWorkflowFolderAccess(sessionID, workflowPath string, readRoots, writeRoots, readOnlyRoots []string, env map[string]string) {
	if strings.TrimSpace(sessionID) == "" || strings.TrimSpace(workflowPath) == "" {
		return
	}
	updateSessionShellConfig(sessionID, func(cfg *SessionShellConfig) {
		previous := make(map[string]struct{})
		for key, value := range cfg.Env {
			if workflowCapabilityEnv(key) && key != "WORKFLOW_KB_ACCESS" {
				previous[filepath.Clean(strings.TrimSpace(value))] = struct{}{}
			}
		}
		removePrevious := func(paths []string) []string {
			kept := make([]string, 0, len(paths))
			for _, path := range paths {
				if _, remove := previous[filepath.Clean(strings.TrimSpace(path))]; !remove {
					kept = append(kept, path)
				}
			}
			return kept
		}
		cfg.WorkflowPath = strings.Trim(strings.TrimSpace(workflowPath), "/")
		cfg.ReadPaths = DeduplicateStrings(append(removePrevious(cfg.ReadPaths), readRoots...))
		cfg.WritePaths = DeduplicateStrings(append(removePrevious(cfg.WritePaths), writeRoots...))
		cfg.BlockedWritePaths = DeduplicateStrings(append(removePrevious(cfg.BlockedWritePaths), readOnlyRoots...))
		if cfg.Env == nil {
			cfg.Env = make(map[string]string)
		}
		for key := range cfg.Env {
			if workflowCapabilityEnv(key) {
				delete(cfg.Env, key)
			}
		}
		for key, value := range env {
			cfg.Env[key] = value
		}
	})
}

// sessionBelongsToWorkflow keeps live grant reconciliation working for sessions
// created before WorkflowPath ownership tagging was added. Exact ownership is
// preferred; the fallback only accepts a working/read/write path at or beneath
// the same workflow root and then upgrades the session to the explicit tag.
func sessionBelongsToWorkflow(config *SessionShellConfig, workflowPath string) bool {
	if config == nil {
		return false
	}
	workflowPath = strings.Trim(strings.TrimSpace(workflowPath), "/")
	if workflowPath == "" {
		return false
	}
	if strings.Trim(strings.TrimSpace(config.WorkflowPath), "/") == workflowPath {
		return true
	}
	belongs := func(candidate string) bool {
		candidate = strings.Trim(strings.TrimSpace(candidate), "/")
		return candidate == workflowPath || strings.HasPrefix(candidate, workflowPath+"/")
	}
	if belongs(config.WorkingDir) {
		config.WorkflowPath = workflowPath
		return true
	}
	for _, candidate := range append(append([]string{}, config.ReadPaths...), config.WritePaths...) {
		if belongs(candidate) {
			config.WorkflowPath = workflowPath
			return true
		}
	}
	return false
}

var (
	sessionShellConfigs   = make(map[string]*SessionShellConfig)
	sessionShellConfigsMu sync.RWMutex
)

func cloneStrings(values []string) []string {
	if values == nil {
		return nil
	}
	return append([]string(nil), values...)
}

func cloneStringMap(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func cloneSessionShellConfig(cfg *SessionShellConfig) *SessionShellConfig {
	if cfg == nil {
		return &SessionShellConfig{}
	}
	copy := *cfg
	copy.ReadPaths = cloneStrings(cfg.ReadPaths)
	copy.WritePaths = cloneStrings(cfg.WritePaths)
	copy.BlockedPaths = cloneStrings(cfg.BlockedPaths)
	copy.BlockedWritePaths = cloneStrings(cfg.BlockedWritePaths)
	copy.Env = cloneStringMap(cfg.Env)
	return &copy
}

func updateSessionShellConfig(sessionID string, update func(*SessionShellConfig)) {
	sessionShellConfigsMu.Lock()
	defer sessionShellConfigsMu.Unlock()
	cfg := cloneSessionShellConfig(sessionShellConfigs[sessionID])
	update(cfg)
	sessionShellConfigs[sessionID] = cfg
}

// SetSessionWorkingDir sets the default working directory for a session.
func SetSessionWorkingDir(sessionID, dir string) {
	updateSessionShellConfig(sessionID, func(cfg *SessionShellConfig) {
		cfg.WorkingDir = dir
	})
	log.Printf("[SHELL] Set default working dir for session %s: %s", sessionID, dir)
}

// SetSessionFolderGuard sets the folder guard read/write paths for a session.
// Does not touch BlockedWritePaths — use SetSessionFolderGuardBlockedWritePaths
// to set those independently, so existing callers don't need updating when a
// session adds a deny prefix later.
func SetSessionFolderGuard(sessionID string, readPaths, writePaths []string) {
	updateSessionShellConfig(sessionID, func(cfg *SessionShellConfig) {
		cfg.FolderGuardSet = true
		cfg.ReadPaths = cloneStrings(readPaths)
		cfg.WritePaths = cloneStrings(writePaths)
	})
	log.Printf("[SHELL] Set folder guard for session %s: read=%v write=%v", sessionID, readPaths, writePaths)
}

// SetSessionFolderGuardBlockedPaths sets hard deny prefixes for both reads and writes.
func SetSessionFolderGuardBlockedPaths(sessionID string, blockedPaths []string) {
	updateSessionShellConfig(sessionID, func(cfg *SessionShellConfig) {
		cfg.FolderGuardSet = true
		cfg.BlockedPaths = cloneStrings(blockedPaths)
	})
	log.Printf("[SHELL] Set folder guard blocked paths for session %s: %v", sessionID, blockedPaths)
}

// SetSessionFolderGuardBlockedWritePaths sets the write-denied prefix list for
// a session. Reads stay allowed. BlockedWritePaths flow through to the isolator's
// FolderGuardConfig.BlockedWritePaths so kernel-level enforcement (sandbox-exec
// `(deny file-write*)` on macOS, read-only bind-mount on Linux) blocks writes
// even when the path is under a WritePath prefix. Used by the chat-agent
// #workflow setup to grant `Workflow/<name>/` as a broad write prefix while
// denying writes to `Workflow/<name>/planning/`.
// SetSessionSandbox applies an agent profile's sandbox policy to the
// session's shell: deny-by-default confinement and, optionally, no network.
func SetSessionSandbox(sessionID string, strictAllowlist, denyNetwork bool) {
	updateSessionShellConfig(sessionID, func(cfg *SessionShellConfig) {
		cfg.FolderGuardSet = true
		cfg.StrictAllowlist = strictAllowlist
		cfg.DenyNetwork = denyNetwork
	})
	log.Printf("[SHELL] Set sandbox for session %s: strict=%v deny_network=%v", sessionID, strictAllowlist, denyNetwork)
}

func SetSessionFolderGuardBlockedWritePaths(sessionID string, blockedWritePaths []string) {
	updateSessionShellConfig(sessionID, func(cfg *SessionShellConfig) {
		cfg.FolderGuardSet = true
		cfg.BlockedWritePaths = cloneStrings(blockedWritePaths)
	})
	log.Printf("[SHELL] Set folder guard blocked-write paths for session %s: %v", sessionID, blockedWritePaths)
}

// SetSessionShellEnv merges extra environment variables into a session's shell
// config (later calls override earlier keys). The map is replaced wholesale
// under lock so concurrent readers always observe a complete map, never a
// partially-written one.
func SetSessionShellEnv(sessionID string, env map[string]string) {
	if len(env) == 0 {
		return
	}
	updateSessionShellConfig(sessionID, func(cfg *SessionShellConfig) {
		if cfg.Env == nil {
			cfg.Env = make(map[string]string, len(env))
		}
		for key, value := range env {
			cfg.Env[key] = value
		}
	})
	log.Printf("[SHELL] Set %d shell env var(s) for session %s", len(env), sessionID)
}

// ReplaceSessionShellEnvPrefix atomically replaces one managed namespace in a
// session's shell environment while preserving variables owned by other
// features. This is used when a live attachment set changes: merging alone
// cannot remove the alias of a detached folder.
func ReplaceSessionShellEnvPrefix(sessionID, prefix string, env map[string]string) {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		return
	}
	updateSessionShellConfig(sessionID, func(cfg *SessionShellConfig) {
		if cfg.Env == nil {
			cfg.Env = make(map[string]string, len(env))
		}
		for key := range cfg.Env {
			if strings.HasPrefix(key, prefix) {
				delete(cfg.Env, key)
			}
		}
		for key, value := range env {
			if strings.HasPrefix(key, prefix) {
				cfg.Env[key] = value
			}
		}
	})
	log.Printf("[SHELL] Replaced %s shell env namespace with %d var(s) for session %s", prefix, len(env), sessionID)
}

// GetSessionShellEnv returns a copy of the session's shell env vars (nil if none),
// safe to read without holding the config lock.
func GetSessionShellEnv(sessionID string) map[string]string {
	cfg := GetSessionShellConfig(sessionID)
	if cfg == nil {
		return nil
	}
	return cloneStringMap(cfg.Env)
}

// SetSessionBrowserMode stores configured browser intent for a session
// (auto/cdp/headless/none), never a one-time auto-mode reachability result.
// Used by execute_shell_command for context-aware guidance and by delegated
// turns as the policy from which they perform their own live resolution.
func SetSessionBrowserMode(sessionID, mode string) {
	updateSessionShellConfig(sessionID, func(cfg *SessionShellConfig) {
		cfg.BrowserMode = mode
	})
	log.Printf("[SHELL] Set browser mode for session %s: %s", sessionID, mode)
}

// GetSessionBrowserMode returns configured browser intent, or "" if none was
// set. CDP reachability must be queried live through agent_browser status.
func GetSessionBrowserMode(sessionID string) string {
	sessionShellConfigsMu.RLock()
	defer sessionShellConfigsMu.RUnlock()
	if cfg := sessionShellConfigs[sessionID]; cfg != nil {
		return cfg.BrowserMode
	}
	return ""
}

// SetSessionBrowserSessionID stores the shared browser session identity for a tool/chat session.
// Browser tools can use this to converge on a stable browser state while keeping tool routing
// scoped to the original chat/workflow session.
func SetSessionBrowserSessionID(sessionID, browserSessionID string) {
	updateSessionShellConfig(sessionID, func(cfg *SessionShellConfig) {
		cfg.BrowserSessionID = browserSessionID
	})
	log.Printf("[SHELL] Set browser session ID for session %s: %s", sessionID, browserSessionID)
}

// Browser isolation is one browser per workflow and one per project (a Crew
// or other product project), never per user: everyone with access to the same
// workflow or project shares its tabs, cookies and logins. Only a conversation
// with no workspace at all falls back to a browser scoped to that session.

func browserPathHash(kind, path string) string {
	sum := sha256.Sum256([]byte(kind + "\x00" + path))
	return hex.EncodeToString(sum[:8])
}

func cleanBrowserWorkspacePath(path string) string {
	path = strings.ReplaceAll(path, "\\", "/")
	path = filepath.ToSlash(filepath.Clean(strings.TrimSpace(path)))
	path = strings.Trim(path, "/")
	if path == "." {
		return ""
	}
	return path
}

// WorkflowBrowserSessionNamespace names the browser shared by one workflow's
// authorized users, runs, builder chats, groups and delegated agents.
func WorkflowBrowserSessionNamespace(workflowPath string) string {
	workflowPath = cleanBrowserWorkspacePath(workflowPath)
	if workflowPath == "" {
		return ""
	}
	return "workflow-" + browserPathHash("workflow", workflowPath)
}

// ProjectBrowserSessionNamespace names the browser shared by one project
// (a Crew or another product project). projectKey must be the project's
// owner-qualified physical path so every user of the project derives the
// same name and two owners' same-named projects never collide.
func ProjectBrowserSessionNamespace(projectKey string) string {
	projectKey = cleanBrowserWorkspacePath(projectKey)
	if projectKey == "" {
		return ""
	}
	return "project-" + browserPathHash("project", projectKey)
}

// SessionBrowserSessionNamespace is the fallback for a conversation with no
// workspace: isolated to that one session rather than shared.
func SessionBrowserSessionNamespace(sessionID string) string {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return ""
	}
	return "session-" + browserPathHash("session", sessionID)
}

func bindSessionBrowserNamespace(sessionID, namespace string) {
	if strings.TrimSpace(sessionID) == "" || namespace == "" {
		return
	}
	updateSessionShellConfig(sessionID, func(cfg *SessionShellConfig) {
		cfg.BrowserSessionNamespace = namespace
		cfg.BrowserSessionID = namespace + "--browser"
	})
}

// BindSessionBrowserIsolationForWorkflow binds a tool/chat session to its
// workflow's shared browser. Always replaces obsolete bindings.
func BindSessionBrowserIsolationForWorkflow(sessionID, workflowPath string) {
	bindSessionBrowserNamespace(sessionID, WorkflowBrowserSessionNamespace(workflowPath))
}

// BindSessionBrowserIsolationForProject binds a tool/chat session to its
// project's shared browser (see ProjectBrowserSessionNamespace).
func BindSessionBrowserIsolationForProject(sessionID, projectKey string) {
	bindSessionBrowserNamespace(sessionID, ProjectBrowserSessionNamespace(projectKey))
}

// BindSessionBrowserIsolationForSession binds a conversation that has no
// workspace to a browser scoped to that session only.
func BindSessionBrowserIsolationForSession(sessionID string) {
	bindSessionBrowserNamespace(sessionID, SessionBrowserSessionNamespace(sessionID))
}

// SetSessionBrowserNamespace propagates an already-authenticated browser
// namespace to an internal workflow/group tool session.
func SetSessionBrowserNamespace(sessionID, namespace string) {
	if strings.TrimSpace(sessionID) == "" || strings.TrimSpace(namespace) == "" {
		return
	}
	updateSessionShellConfig(sessionID, func(cfg *SessionShellConfig) {
		cfg.BrowserSessionNamespace = strings.TrimSpace(namespace)
	})
}

// CopySessionFolderGuard copies the folder guard (ReadPaths + WritePaths +
// BlockedWritePaths) from one session to another. Used to propagate restrictions
// from a parent HTTP session to child group sessions (batch execution, workshop
// groups) so that sub-agents running under the new session ID inherit the same
// write restrictions AND the same blocked-write exceptions. Returns true if a
// guard was copied.
func CopySessionFolderGuard(fromSessionID, toSessionID string) bool {
	src := GetSessionShellConfig(fromSessionID)
	if src == nil || (!src.FolderGuardSet && len(src.ReadPaths) == 0 && len(src.WritePaths) == 0 && len(src.BlockedPaths) == 0 && len(src.BlockedWritePaths) == 0) {
		return false
	}
	SetSessionFolderGuard(toSessionID, src.ReadPaths, src.WritePaths)
	InheritCodeSession(fromSessionID, toSessionID)
	if src.WorkflowPath != "" {
		SetSessionWorkflowPath(toSessionID, src.WorkflowPath)
	}
	folderEnv := make(map[string]string)
	for key, value := range src.Env {
		if workflowCapabilityEnv(key) {
			folderEnv[key] = value
		}
	}
	SetSessionShellEnv(toSessionID, folderEnv)
	if len(src.BlockedPaths) > 0 {
		SetSessionFolderGuardBlockedPaths(toSessionID, src.BlockedPaths)
	}
	if len(src.BlockedWritePaths) > 0 {
		SetSessionFolderGuardBlockedWritePaths(toSessionID, src.BlockedWritePaths)
	}
	log.Printf("[FOLDER_GUARD] Copied folder guard from session %s to %s: read=%v write=%v blocked-write=%v", fromSessionID, toSessionID, src.ReadPaths, src.WritePaths, src.BlockedWritePaths)
	return true
}

// ClearSessionShellConfig removes all shell config for a session.
func ClearSessionShellConfig(sessionID string) {
	sessionShellConfigsMu.Lock()
	defer sessionShellConfigsMu.Unlock()
	delete(sessionShellConfigs, sessionID)
}

// GetSessionShellConfig looks up the shell config for a session.
// SessionWorkflowCapabilityResolver is installed once by the workflow runtime.
// It resolves current manifests outside the session lock and must not call back
// into session getters. Every shell/file read gets current grants, not a cached
// authorization snapshot. Existing subprocesses retain their launch-time policy.
var SessionWorkflowCapabilityResolver func(workflowPath string, kbRead bool) (reads, writes, readOnly []string, env map[string]string)

func GetSessionShellConfig(sessionID string) *SessionShellConfig {
	sessionShellConfigsMu.RLock()
	original := sessionShellConfigs[sessionID]
	if original == nil {
		sessionShellConfigsMu.RUnlock()
		return nil
	}
	cfg := cloneSessionShellConfig(original)
	sessionShellConfigsMu.RUnlock()
	if cfg.WorkflowPath != "" && SessionWorkflowCapabilityResolver != nil {
		reads, writes, blocked, env := SessionWorkflowCapabilityResolver(cfg.WorkflowPath, cfg.Env["WORKFLOW_KB_ACCESS"] != "none")
		previous := map[string]bool{}
		for key, value := range cfg.Env {
			if workflowCapabilityEnv(key) && key != "WORKFLOW_KB_ACCESS" {
				previous[filepath.Clean(value)] = true
			}
		}
		remove := func(paths []string) []string {
			out := []string{}
			for _, path := range paths {
				if !previous[filepath.Clean(path)] {
					out = append(out, path)
				}
			}
			return out
		}
		cfg.ReadPaths = DeduplicateStrings(append(remove(cfg.ReadPaths), reads...))
		cfg.WritePaths = DeduplicateStrings(append(remove(cfg.WritePaths), writes...))
		cfg.BlockedWritePaths = DeduplicateStrings(append(remove(cfg.BlockedWritePaths), blocked...))
		if cfg.Env == nil {
			cfg.Env = map[string]string{}
		}
		for key := range cfg.Env {
			if workflowCapabilityEnv(key) {
				delete(cfg.Env, key)
			}
		}
		for key, value := range env {
			cfg.Env[key] = value
		}
	}
	return cfg
}

func workflowCapabilityEnv(key string) bool {
	return strings.HasPrefix(key, "WORKFLOW_FOLDER_") || strings.HasPrefix(key, "WORKFLOW_KB_") || strings.HasPrefix(key, "WORKFLOW_CREW_")
}

// SandboxBrowserSession is the managed browser a session's shell and browser
// commands may use: its workflow's, project's or session's browser, derived
// from the server-side binding (never from model input). The workspace
// sandbox then grants only that browser's socket folder and profile. Empty
// for an unbound session, which keeps the shared browser grants.
func SandboxBrowserSession(sessionID string) string {
	cfg := GetSessionShellConfig(sessionID)
	if cfg == nil || strings.TrimSpace(cfg.BrowserSessionNamespace) == "" {
		return ""
	}
	return PrefixBrowserSessionID(strings.TrimSpace(cfg.BrowserSessionNamespace) + "--browser")
}

// ResolveBrowserSessionID ignores agent-chosen names within an ownership boundary.
// Unbound legacy callers retain their configured session behavior.
func ResolveBrowserSessionID(sessionID, requested string) string {
	requested = strings.TrimSpace(requested)
	cfg := GetSessionShellConfig(sessionID)
	if cfg != nil && strings.TrimSpace(cfg.BrowserSessionNamespace) != "" {
		return PrefixBrowserSessionID(strings.TrimSpace(cfg.BrowserSessionNamespace) + "--browser")
	}
	if (requested == "" || requested == "default") && cfg != nil && strings.TrimSpace(cfg.BrowserSessionID) != "" {
		return PrefixBrowserSessionID(strings.TrimSpace(cfg.BrowserSessionID))
	}
	return PrefixBrowserSessionID(requested)
}

// PrefixBrowserSessionID namespaces agent-browser's process and state-file
// identity for named AgentWorks instances. An empty prefix preserves the
// historical session names used by the normal desktop application.
func PrefixBrowserSessionID(session string) string {
	session = strings.TrimSpace(session)
	prefix := strings.TrimSpace(os.Getenv("AGENTWORKS_BROWSER_SESSION_PREFIX"))
	if session == "" || prefix == "" {
		return session
	}
	qualifiedPrefix := prefix + "--"
	if strings.HasPrefix(session, qualifiedPrefix) {
		return session
	}
	return qualifiedPrefix + session
}

// DeduplicateStrings removes duplicate strings from a slice.
func DeduplicateStrings(strs []string) []string {
	seen := make(map[string]bool, len(strs))
	result := make([]string, 0, len(strs))
	for _, s := range strs {
		if !seen[s] {
			seen[s] = true
			result = append(result, s)
		}
	}
	return result
}

// IsNativeWorkspace reports whether the workspace server is running natively
// on the host (not inside Docker). When true, shell commands, CDP connections,
// and MCP API URLs should use localhost/127.0.0.1 instead of host.docker.internal.
//
// Detection: NATIVE_WORKSPACE=true is set by run_server_with_logging.sh --with-workspace.
func IsNativeWorkspace() bool {
	return os.Getenv("NATIVE_WORKSPACE") == "true"
}

// QuoteShellArg quotes a shell argument if it contains special characters
func QuoteShellArg(arg string) string {
	if strings.ContainsAny(arg, " \t\n()[]{}|&;'\"\\$<>*?!") {
		escaped := strings.ReplaceAll(arg, "'", "'\"'\"'")
		return "'" + escaped + "'"
	}
	return arg
}
