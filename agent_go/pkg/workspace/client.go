package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"
)

// FolderGuardConfig represents folder access restrictions.
//
// BlockedPaths is the "deny all" primitive — reads and writes are both blocked
// at kernel level (sandbox-exec `(deny file-read* file-write*)` on macOS, tmpfs
// overlay on Linux). Use for paths that must be fully hidden (e.g. secrets/).
//
// BlockedWritePaths denies writes only. Reads pass through. Used for paths where
// the agent should be able to inspect but never modify — e.g.
// `Workflow/<name>/planning/` where plan.json must be readable for the agent to
// understand the workflow but must only be edited through typed plan-mod tools.
// Implementation: macOS `(deny file-write*)`, Linux read-only bind-mount.
type FolderGuardConfig struct {
	Enabled           bool     `json:"enabled"`
	ReadPaths         []string `json:"read_paths"`
	WritePaths        []string `json:"write_paths"`
	BlockedPaths      []string `json:"blocked_paths"`
	BlockedWritePaths []string `json:"blocked_write_paths,omitempty"`
	// StrictAllowlist / DenyNetwork carry an agent profile's sandbox policy
	// to the workspace shell isolator (deny-by-default, optional no-network).
	StrictAllowlist bool `json:"strict_allowlist,omitempty"`
	DenyNetwork     bool `json:"deny_network,omitempty"`
	// BrowserSession scopes the sandbox to this session's own managed browser
	// (common.SandboxBrowserSession).
	BrowserSession string `json:"browser_session,omitempty"`
	// Source is which resolution branch produced this guard (session / ctx / client
	// fallback). Internal only (never serialized); surfaced in denial logs so a
	// denied write shows which guard layer decided, without logging every success.
	Source string `json:"-"`
}

// Client handles communication with the workspace API directly via REST
type Client struct {
	BaseURL           string
	HTTPClient        *http.Client
	FolderGuard       *FolderGuardConfig
	UserID            string            // User ID for auth/database scoping
	ExtraEnv          map[string]string // Extra env vars to inject into shell commands (e.g., MCP_API_URL, MCP_API_TOKEN). Guarded by extraEnvMu; read via extraEnvSnapshot.
	extraEnvMu        sync.RWMutex
	DefaultWorkingDir string // Default working directory for shell commands (relative to docs-dir)
}

// httpStatusError preserves a non-2xx response body for callers that can
// interpret a structured error response (notably shell-command timeouts, which
// contain the process's partial stdout/stderr and prove that it did start).
type httpStatusError struct {
	statusCode int
	body       []byte
}

func (e *httpStatusError) Error() string {
	return fmt.Sprintf("HTTP %d: %s", e.statusCode, string(e.body))
}

type internalContextKey string

const systemManagedWritePathsKey internalContextKey = "system_managed_write_paths"

// WithSystemManagedWritePaths grants trusted Go-side tools write access to
// system-owned paths that remain blocked from shell and general file tools.
// The capability exists only in the in-process context and is never accepted
// from HTTP or LLM tool arguments.
func WithSystemManagedWritePaths(ctx context.Context, paths ...string) context.Context {
	existing, _ := ctx.Value(systemManagedWritePathsKey).([]string)
	granted := clonePathList(existing)
	for _, path := range paths {
		path = filepath.Clean(strings.TrimSpace(path))
		if path == "." || path == "" {
			continue
		}
		granted = append(granted, path)
	}
	return context.WithValue(ctx, systemManagedWritePathsKey, deduplicateStrings(granted))
}

func systemManagedWriteAllowed(ctx context.Context, inputPath string) bool {
	paths, _ := ctx.Value(systemManagedWritePathsKey).([]string)
	inputPath = filepath.Clean(inputPath)
	for _, path := range paths {
		if isPathUnder(inputPath, filepath.Clean(path)) {
			return true
		}
	}
	return false
}

// ClientOption is a functional option for configuring the Client
type ClientOption func(*Client)

// WithFolderGuard sets the folder guard configuration for the client
func WithFolderGuard(config *FolderGuardConfig) ClientOption {
	return func(c *Client) {
		c.FolderGuard = config
	}
}

// WithHTTPClient sets a custom HTTP client
func WithHTTPClient(httpClient *http.Client) ClientOption {
	return func(c *Client) {
		c.HTTPClient = httpClient
	}
}

// WithUserID sets the user ID for auth/database scoping
// When set, the client includes X-User-ID header in all requests
func WithUserID(userID string) ClientOption {
	return func(c *Client) {
		c.UserID = userID
	}
}

// WithExtraEnv sets extra environment variables to inject into shell commands.
// Only MCP_* and SECRET_* prefixed vars are forwarded to the shell (enforced server-side).
func WithExtraEnv(env map[string]string) ClientOption {
	return func(c *Client) {
		c.ExtraEnv = cloneEnvMap(env)
	}
}

// SetExtraEnv adds or replaces one shell env var on a live client. This is how
// a secret created mid-turn (set_workflow_secret) reaches the shell commands of
// the SAME turn: WithExtraEnv deliberately clones its input, so the map handed
// to the client at turn start is a snapshot and in-place writes to the caller's
// map never arrived (a shell run 3s after setting a secret saw no SECRET_* var,
// RTS 2026-09-03).
func (c *Client) SetExtraEnv(key, value string) {
	c.extraEnvMu.Lock()
	defer c.extraEnvMu.Unlock()
	if c.ExtraEnv == nil {
		c.ExtraEnv = make(map[string]string)
	}
	c.ExtraEnv[key] = value
}

// DeleteExtraEnv removes one shell env var from a live client.
func (c *Client) DeleteExtraEnv(key string) {
	c.extraEnvMu.Lock()
	defer c.extraEnvMu.Unlock()
	delete(c.ExtraEnv, key)
}

// ExtraEnvValue reads one shell env var under the lock.
func (c *Client) ExtraEnvValue(key string) (string, bool) {
	c.extraEnvMu.RLock()
	defer c.extraEnvMu.RUnlock()
	v, ok := c.ExtraEnv[key]
	return v, ok
}

// extraEnvSnapshot returns a private copy for one request, so concurrent
// SetExtraEnv calls can never race a request that is being assembled.
func (c *Client) extraEnvSnapshot() map[string]string {
	c.extraEnvMu.RLock()
	defer c.extraEnvMu.RUnlock()
	return cloneEnvMap(c.ExtraEnv)
}

func cloneEnvMap(env map[string]string) map[string]string {
	if env == nil {
		return nil
	}
	cloned := make(map[string]string, len(env))
	for key, value := range env {
		cloned[key] = value
	}
	return cloned
}

// WithDefaultWorkingDir sets the default working directory for shell commands
// (relative to docs-dir, e.g., "Chats/", "Workflow/my-project/").
func WithDefaultWorkingDir(dir string) ClientOption {
	return func(c *Client) {
		c.DefaultWorkingDir = dir
	}
}

// NewClient creates a new workspace REST client with optional configuration
func NewClient(baseURL string, opts ...ClientOption) *Client {
	c := &Client{
		BaseURL:    strings.TrimRight(baseURL, "/"),
		HTTPClient: &http.Client{Timeout: 300 * time.Second},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// sessionIDFromContext returns the trusted session identity used for
// session-scoped workspace policy. HTTP bridge calls normally carry it in the
// context; session-aware clients also retain it in MCP_SESSION_ID so policy is
// not silently lost if an intermediary rebuilds the request context.
func (c *Client) sessionIDFromContext(ctx context.Context) string {
	if ctx != nil {
		if sid, ok := ctx.Value(common.ChatSessionIDKey).(string); ok {
			if sid = strings.TrimSpace(sid); sid != "" {
				return sid
			}
		}
	}
	sid, _ := c.ExtraEnvValue("MCP_SESSION_ID")
	return strings.TrimSpace(sid)
}

// resolveEffectiveFolderGuard returns the effective folder guard for the current request.
// Priority: session config (SetSessionFolderGuard) > context keys > client-level fallback.
// This mirrors the resolution logic in ExecuteShellCommand so that all tools (diff_patch,
// update, delete, move) enforce the same session-scoped restrictions as shell commands.
func (c *Client) resolveEffectiveFolderGuard(ctx context.Context) *FolderGuardConfig {
	// 1. Session config: set by SetSessionFolderGuard() — highest priority.
	//    Covers CLI/Gemini providers that bypass the Go folder guard context wrappers.
	sessionID := c.sessionIDFromContext(ctx)
	if sessionID != "" {
		sessionCfg := GetSessionShellConfig(sessionID)
		if sessionCfg != nil && sessionConfigHasFolderGuard(sessionCfg) {
			readPaths := sessionCfg.WritePaths
			if len(sessionCfg.ReadPaths) > 0 {
				readPaths = deduplicateStrings(append(sessionCfg.ReadPaths, sessionCfg.WritePaths...))
			}
			return &FolderGuardConfig{
				Enabled:           true,
				WritePaths:        clonePathList(sessionCfg.WritePaths),
				ReadPaths:         clonePathList(readPaths),
				BlockedPaths:      clonePathList(sessionCfg.BlockedPaths),
				BlockedWritePaths: clonePathList(sessionCfg.BlockedWritePaths),
				StrictAllowlist:   sessionCfg.StrictAllowlist,
				DenyNetwork:       sessionCfg.DenyNetwork,
				Source:            "session",
			}
		}
	}

	// 2. Context System 1: chat/plan/prototype mode
	if allowedWrites, ok := ctx.Value(common.FolderGuardAllowedWriteFolderKey).([]string); ok {
		ctxReads, hasCtxReads := ctx.Value(common.FolderGuardReadPathsKey).([]string)
		readPaths := allowedWrites
		if hasCtxReads && len(ctxReads) > 0 {
			readPaths = deduplicateStrings(append(ctxReads, allowedWrites...))
		}
		return &FolderGuardConfig{
			Enabled:           true,
			WritePaths:        clonePathList(allowedWrites),
			ReadPaths:         clonePathList(readPaths),
			BlockedPaths:      contextPathList(ctx, common.FolderGuardBlockedPathsKey),
			BlockedWritePaths: contextPathList(ctx, common.FolderGuardBlockedWritePathsKey),
			Source:            "ctx_allowed_write",
		}
	}

	// 3. Context System 2: workflow orchestrator (frozen per-agent snapshot)
	if ctxWrites, ok := ctx.Value(common.FolderGuardWritePathsKey).([]string); ok {
		ctxReads, hasCtxReads := ctx.Value(common.FolderGuardReadPathsKey).([]string)
		readPaths := ctxWrites
		if hasCtxReads && len(ctxReads) > 0 {
			readPaths = deduplicateStrings(append(ctxReads, ctxWrites...))
		}
		// If this branch decides for a workflow step turn, the per-item session guard
		// was missing/empty and the frozen creation-time snapshot decided instead
		// (source "ctx_snapshot" on any denial log makes that visible).
		return &FolderGuardConfig{
			Enabled:           true,
			WritePaths:        clonePathList(ctxWrites),
			ReadPaths:         clonePathList(readPaths),
			BlockedPaths:      contextPathList(ctx, common.FolderGuardBlockedPathsKey),
			BlockedWritePaths: contextPathList(ctx, common.FolderGuardBlockedWritePathsKey),
			Source:            "ctx_snapshot",
		}
	}

	// 4. Client-level fallback
	if c.FolderGuard != nil && c.FolderGuard.Enabled {
		fallback := cloneFolderGuard(c.FolderGuard)
		if fallback != nil {
			fallback.Source = "client_fallback"
		}
		return fallback
	}

	return nil // No folder guard at all
}

func sessionConfigHasFolderGuard(config *SessionShellConfig) bool {
	return config != nil && (config.FolderGuardSet || len(config.ReadPaths) > 0 || len(config.WritePaths) > 0 ||
		len(config.BlockedPaths) > 0 || len(config.BlockedWritePaths) > 0)
}

func clonePathList(paths []string) []string {
	return append([]string(nil), paths...)
}

func contextPathList(ctx context.Context, key common.ContextKey) []string {
	paths, _ := ctx.Value(key).([]string)
	return clonePathList(paths)
}

func cloneFolderGuard(guard *FolderGuardConfig) *FolderGuardConfig {
	if guard == nil {
		return nil
	}
	copy := *guard
	copy.ReadPaths = clonePathList(guard.ReadPaths)
	copy.WritePaths = clonePathList(guard.WritePaths)
	copy.BlockedPaths = clonePathList(guard.BlockedPaths)
	copy.BlockedWritePaths = clonePathList(guard.BlockedWritePaths)
	return &copy
}

// ValidatePathWithContext checks if a path is allowed based on the effective folder guard
// (session > context > client-level). Use this for all file operations from HTTP handlers
// where session-scoped restrictions must be enforced.
func (c *Client) ValidatePathWithContext(ctx context.Context, inputPath string, isWrite bool) error {
	guard := c.resolveEffectiveFolderGuard(ctx)
	if isWrite && systemManagedWriteAllowed(ctx, inputPath) {
		// System-managed access may override a write-only deny, but never a hard
		// read/write deny. This keeps secrets and other absolute exclusions closed.
		if guard != nil {
			for _, blocked := range guard.BlockedPaths {
				if isPathUnder(filepath.Clean(inputPath), filepath.Clean(blocked)) {
					return fmt.Errorf("path %q is blocked", inputPath)
				}
			}
		}
		return nil
	}
	if guard == nil || !guard.Enabled {
		return nil
	}
	validationPath, validationGuard := c.userScopedFolderGuardPath(inputPath, guard)
	if err := validatePathAgainstGuard(validationGuard, validationPath, isWrite); err != nil {
		// Deny-only logging: shows which guard source (session / ctx snapshot /
		// client fallback) denied, so a real learnings/KB denial is diagnosable
		// without logging every successful file op.
		log.Printf("[FOLDER_GUARD_DENY] session=%s source=%s isWrite=%v path=%q writePaths=%v err=%v",
			c.sessionIDFromContext(ctx), guard.Source, isWrite, inputPath, guard.WritePaths, err)
		return err
	}
	return nil
}

// HasEffectiveWriteGuard reports whether the current request/session context has
// an explicit write guard from session or context state. This excludes the
// client-level fallback guard so callers can distinguish "real session guard"
// from a narrow one-off client restriction.
func (c *Client) HasEffectiveWriteGuard(ctx context.Context) bool {
	guard := c.resolveEffectiveFolderGuard(ctx)
	if guard == nil || !guard.Enabled {
		return false
	}
	return len(guard.WritePaths) > 0
}

// ValidatePath checks if a path is allowed based on client-level folder guard configuration.
// For HTTP handler contexts where session-scoped guards must be enforced, use ValidatePathWithContext instead.
func (c *Client) ValidatePath(inputPath string, isWrite bool) error {
	if c.FolderGuard == nil || !c.FolderGuard.Enabled {
		return nil // No folder guard configured, allow all
	}
	validationPath, validationGuard := c.userScopedFolderGuardPath(inputPath, c.FolderGuard)
	return validatePathAgainstGuard(validationGuard, validationPath, isWrite)
}

// userScopedFolderGuardPath makes the workspace API's public per-user paths
// comparable with the physical paths stored in session guards. The API accepts
// Chats/... and resolves it to _users/<user>/Chats/... from X-User-ID, while a
// session guard intentionally records that physical, user-isolated location.
// Normalize both the requested path and every guard path to the physical form
// before authorization so the client-side check and server-side resolution
// enforce the same location.
func (c *Client) userScopedFolderGuardPath(inputPath string, guard *FolderGuardConfig) (string, *FolderGuardConfig) {
	userID := strings.TrimSpace(c.UserID)
	if guard == nil || userID == "" || !validWorkspaceUserID(userID) {
		return inputPath, guard
	}
	normalize := func(path string) string {
		clean := filepath.Clean(strings.TrimSpace(path))
		if clean == "." || clean == "" || filepath.IsAbs(clean) {
			return clean
		}
		parts := strings.Split(filepath.ToSlash(clean), "/")
		if len(parts) == 0 {
			return clean
		}
		switch parts[0] {
		case "Chats", "Downloads", "chat_history", "memories":
			return filepath.Join(workspaceref.UsersDir, userID, clean)
		default:
			return clean
		}
	}
	normalized := cloneFolderGuard(guard)
	for i, path := range normalized.ReadPaths {
		normalized.ReadPaths[i] = normalize(path)
	}
	for i, path := range normalized.WritePaths {
		normalized.WritePaths[i] = normalize(path)
	}
	for i, path := range normalized.BlockedPaths {
		normalized.BlockedPaths[i] = normalize(path)
	}
	for i, path := range normalized.BlockedWritePaths {
		normalized.BlockedWritePaths[i] = normalize(path)
	}
	return normalize(inputPath), normalized
}

func validWorkspaceUserID(userID string) bool {
	if len(userID) == 0 || len(userID) > 128 {
		return false
	}
	for _, char := range userID {
		if (char < 'a' || char > 'z') && (char < 'A' || char > 'Z') &&
			(char < '0' || char > '9') && char != '-' && char != '_' {
			return false
		}
	}
	return true
}

// validatePathAgainstGuard is the core path validation logic used by both
// ValidatePath (client-level) and ValidatePathWithContext (session-resolved).
func validatePathAgainstGuard(guard *FolderGuardConfig, inputPath string, isWrite bool) error {
	// Normalize input path
	inputPath = filepath.Clean(inputPath)

	// Check blocked paths first (applies to both reads and writes — hard deny).
	for _, blocked := range guard.BlockedPaths {
		blocked = filepath.Clean(blocked)
		if isPathUnder(inputPath, blocked) {
			return fmt.Errorf("path %q is blocked", inputPath)
		}
	}

	// Check blocked-write paths when this is a write operation. Reads are
	// intentionally allowed — agents must be able to inspect files in write-blocked
	// folders (e.g. read plan.json for workflow structure) without being allowed
	// to modify them.
	if isWrite {
		for _, blocked := range guard.BlockedWritePaths {
			blocked = filepath.Clean(blocked)
			if isPathUnder(inputPath, blocked) {
				return fmt.Errorf("path %q is blocked for writes", inputPath)
			}
		}
	}

	// Determine allowed paths
	var allowedPaths []string
	if isWrite {
		allowedPaths = guard.WritePaths
	} else {
		// Read operations can use both read and write paths
		allowedPaths = append(guard.ReadPaths, guard.WritePaths...)
	}

	// An enabled guard with no capability for this operation is an explicit deny.
	if len(allowedPaths) == 0 {
		opType := "read"
		if isWrite {
			opType = "write"
		}
		return fmt.Errorf("ACCESS DENIED: no workspace %s paths were granted", opType)
	}

	// Check if path is under any allowed path
	for _, allowed := range allowedPaths {
		allowed = filepath.Clean(allowed)
		if inputPath == allowed {
			return nil
		}
		if isExactFolderGuardFilePath(allowed) {
			continue
		}
		if isPathUnder(inputPath, allowed) {
			return nil
		}
	}

	opType := "read from"
	if isWrite {
		opType = "write to"
	}
	quotedPaths := make([]string, len(allowedPaths))
	for i, p := range allowedPaths {
		quotedPaths[i] = fmt.Sprintf("%q", p)
	}

	// Provide contextual guidance for known read-only folders
	hint := ""
	if isWrite && strings.Contains(inputPath, "planning") {
		hint = " The planning/ folder is READ-ONLY — plan.json and related config are managed by the system and must not be modified by agents. Write your output to the appropriate execution or output folder instead."
	}

	return fmt.Errorf("ACCESS DENIED: Cannot %s %q.%s Writable folders: %s", opType, inputPath, hint, strings.Join(quotedPaths, ", "))
}

func isExactFolderGuardFilePath(path string) bool {
	if filepath.IsAbs(path) {
		if info, err := os.Stat(path); err == nil {
			return !info.IsDir()
		}
	}
	base := filepath.Base(filepath.Clean(strings.TrimSpace(path)))
	return strings.Contains(base, ".")
}

// isPathUnder checks if inputPath is equal to or under basePath
func isPathUnder(inputPath, basePath string) bool {
	// Exact match
	if inputPath == basePath {
		return true
	}

	// Check if input is under base path using filepath.Rel
	// This works correctly when both paths are the same type (both relative or both absolute)
	rel, err := filepath.Rel(basePath, inputPath)
	if err == nil && !strings.HasPrefix(rel, "..") {
		return true
	}

	// Handle mixed relative/absolute case:
	// If input is relative and base is absolute (or vice versa), check if the base path
	// ends with the input path at a directory boundary. This handles the common case where
	// folder guard has absolute paths like "/workspace/docs" and the agent sends "docs/file.txt".
	if !filepath.IsAbs(inputPath) && filepath.IsAbs(basePath) {
		// Check if input path equals the base path's basename or is a subpath of it
		// e.g., base="/workspace/docs", input="docs" → match
		// e.g., base="/workspace/docs", input="docs/file.txt" → match
		// e.g., base="/workspace/src/docs", input="docs" → NO match (ambiguous)
		baseName := filepath.Base(basePath)
		inputSegments := strings.Split(filepath.Clean(inputPath), string(filepath.Separator))

		if len(inputSegments) > 0 && inputSegments[0] == baseName {
			// The input starts with the base's last segment. To avoid ambiguity
			// (e.g., "/a/docs" vs "/b/docs" both ending in "docs"), only match if
			// the base path has exactly one trailing segment that matches.
			// Construct the expected relative path from base's parent and check.
			parentDir := filepath.Dir(basePath)
			resolvedInput := filepath.Join(parentDir, inputPath)
			resolvedInput = filepath.Clean(resolvedInput)
			relCheck, err := filepath.Rel(basePath, resolvedInput)
			if err == nil && !strings.HasPrefix(relCheck, "..") {
				return true
			}
		}
	}

	return false
}

// getUserIDFromContext extracts user ID from context or returns the static UserID
func (c *Client) getUserIDFromContext(ctx context.Context) string {
	// First check if user ID is set on the client directly
	if c.UserID != "" {
		log.Printf("[USER_ID_DEBUGGING] getUserIDFromContext: using client.UserID=%q", c.UserID)
		return c.UserID
	}

	// Then check the context for user ID (set by auth middleware)
	if userID, ok := ctx.Value(common.UserIDKey).(string); ok && userID != "" {
		log.Printf("[USER_ID_DEBUGGING] getUserIDFromContext: using context UserIDKey=%q", userID)
		return userID
	}

	// Return empty string - workspace API will use default user
	log.Printf("[USER_ID_DEBUGGING] WARNING: no user ID available (client.UserID empty, context key missing)")
	return ""
}

// requestWithTimeout executes an HTTP request using a dedicated client with the given timeout.
// Used by ExecuteShellCommand where shell commands (especially those wrapping call_sub_agent)
// can run far longer than the default 5-minute client timeout.
func (c *Client) requestWithTimeout(ctx context.Context, method, path string, body interface{}, timeout time.Duration) ([]byte, error) {
	if timeout <= 0 {
		noTimeoutClient := &http.Client{}
		return c.doRequest(ctx, method, path, body, noTimeoutClient)
	}
	longClient := &http.Client{Timeout: timeout}
	return c.doRequest(ctx, method, path, body, longClient)
}

// request executes a generic HTTP request and returns the response body
func (c *Client) request(ctx context.Context, method, path string, body interface{}) ([]byte, error) {
	return c.doRequest(ctx, method, path, body, c.HTTPClient)
}

// doRequest is the shared implementation for request and requestWithTimeout.
func (c *Client) doRequest(ctx context.Context, method, path string, body interface{}, httpClient *http.Client) ([]byte, error) {
	var bodyReader io.Reader
	if body != nil {
		jsonData, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request: %w", err)
		}
		bodyReader = bytes.NewBuffer(jsonData)
	}

	url := c.BaseURL + path
	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token := strings.TrimSpace(os.Getenv("WORKSPACE_API_TOKEN")); token != "" {
		req.Header.Set("X-Workspace-Token", token)
	}

	// Include user ID header for auth/database scoping
	if userID := c.getUserIDFromContext(ctx); userID != "" {
		req.Header.Set("X-User-ID", userID)
		log.Printf("[USER_ID_DEBUGGING] HTTP request: %s %s with X-User-ID=%q", method, path, userID)
	} else {
		log.Printf("[USER_ID_DEBUGGING] HTTP request: %s %s with NO X-User-ID header", method, path)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("execute request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &httpStatusError{statusCode: resp.StatusCode, body: respBody}
	}

	return respBody, nil
}

// UploadBinary uploads raw binary data as a file via the workspace upload endpoint.
// folderPath is the destination folder (e.g. "Chats/generated-images").
// fileName is the file name including extension (e.g. "image-1234.png").
// data is the raw binary content.
// Returns the saved workspace filepath on success.
func (c *Client) UploadBinary(ctx context.Context, folderPath, fileName string, data []byte) (string, error) {
	if err := c.ValidatePathWithContext(ctx, folderPath, true); err != nil {
		return "", err
	}
	if err := c.ValidatePathWithContext(ctx, filepath.Join(folderPath, fileName), true); err != nil {
		return "", err
	}

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)

	// Add folder_path form field
	if err := mw.WriteField("folder_path", folderPath); err != nil {
		return "", fmt.Errorf("write folder_path field: %w", err)
	}

	// Add file form field
	fw, err := mw.CreateFormFile("file", fileName)
	if err != nil {
		return "", fmt.Errorf("create form file: %w", err)
	}
	if _, err := fw.Write(data); err != nil {
		return "", fmt.Errorf("write file data: %w", err)
	}
	mw.Close()

	url := c.BaseURL + "/api/upload"
	req, err := http.NewRequestWithContext(ctx, "POST", url, &buf)
	if err != nil {
		return "", fmt.Errorf("create upload request: %w", err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	// Multipart upload builds its own request rather than going through
	// doRequest, so the workspace token has to be attached here too. Harmless
	// when the route is unprotected; required the moment it is.
	if token := strings.TrimSpace(os.Getenv("WORKSPACE_API_TOKEN")); token != "" {
		req.Header.Set("X-Workspace-Token", token)
	}
	if userID := c.getUserIDFromContext(ctx); userID != "" {
		req.Header.Set("X-User-ID", userID)
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("execute upload request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read upload response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("upload HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		FilePath string `json:"filepath"`
	}
	if err := json.Unmarshal(respBody, &result); err == nil && result.FilePath != "" {
		noteWorkspaceFileWrite(result.FilePath)
		return result.FilePath, nil
	}
	// Fallback: construct path manually
	noteWorkspaceFileWrite(folderPath + "/" + fileName)
	return folderPath + "/" + fileName, nil
}

// DownloadFile downloads a file from the workspace and returns its raw bytes.
// filePath is the workspace path (e.g. "Chats/generated-images/image-1234.png").
func (c *Client) DownloadFile(ctx context.Context, filePath string) ([]byte, error) {
	if err := c.ValidatePathWithContext(ctx, filePath, false); err != nil {
		return nil, err
	}
	encodedPath := url.PathEscape(filePath)
	return c.request(ctx, "GET", "/api/documents/"+encodedPath+"/raw", nil)
}

// CreateFolder creates a folder via the workspace API: POST /api/folders
func (c *Client) CreateFolder(ctx context.Context, folderPath string) error {
	if err := c.ValidatePathWithContext(ctx, folderPath, true); err != nil {
		return err
	}
	body := map[string]string{"folder_path": folderPath}
	_, err := c.request(ctx, "POST", "/api/folders", body)
	var statusErr *httpStatusError
	if errors.As(err, &statusErr) && statusErr.statusCode == http.StatusConflict {
		// Creating a folder is an ensure operation for every Go-side caller.
		// The workspace API returns 409 when the folder already exists, which
		// is the desired final state and must remain safe to repeat.
		return nil
	}
	return err
}

// DeleteFolder permanently removes a workspace folder through the authenticated
// workspace API. The user id on the client is forwarded so per-user product
// paths resolve under the same _users/<id> root used when they were created.
func (c *Client) DeleteFolder(ctx context.Context, folderPath string) error {
	if err := c.ValidatePathWithContext(ctx, folderPath, true); err != nil {
		return err
	}
	segments := strings.Split(filepath.ToSlash(folderPath), "/")
	for index, segment := range segments {
		segments[index] = url.PathEscape(segment)
	}
	_, err := c.request(ctx, http.MethodDelete, "/api/folders/"+strings.Join(segments, "/")+"?confirm=true", nil)
	var statusErr *httpStatusError
	if errors.As(err, &statusErr) && statusErr.statusCode == http.StatusNotFound {
		return nil
	}
	return err
}
