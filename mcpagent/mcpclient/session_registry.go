package mcpclient

import (
	"context"
	"fmt"
	"log"
	"sort"
	"sync"
	"time"

	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

// SessionConnectionRegistry manages MCP connections scoped to session lifecycle.
//
// Design principles:
//   - Connections are created once per (sessionID, serverName) pair
//   - Multiple agents in same session share connections
//   - Connections persist until CloseSession() is explicitly called
//   - Per-key mutexes prevent duplicate subprocess spawns when concurrent goroutines
//     request the same server connection simultaneously
//
// Usage:
//
//	registry := GetSessionRegistry()
//	client, wasCreated, err := registry.GetOrCreateConnection(ctx, "session-123", "github", config, logger)
//	// ... use client ...
//	registry.CloseSession("session-123") // At workflow end
type SessionConnectionRegistry struct {
	// sessionID -> *sessionConnections
	sessions sync.Map
	// Per-key mutexes to serialize connection creation for the same (session, server) pair.
	// Prevents N goroutines from spawning N subprocesses for the same server.
	connLocks   map[string]*sync.Mutex
	connLocksMu sync.Mutex
}

// sessionConnections holds all connections for a single session
type sessionConnections struct {
	// serverName -> ClientInterface
	clients sync.Map
	// serverName -> MCPServerConfig (for lazy on-demand connection)
	pendingConfigs sync.Map
	// Track creation time for debugging
	createdAt time.Time
}

// Global singleton registry
var globalSessionRegistry = &SessionConnectionRegistry{
	connLocks: make(map[string]*sync.Mutex),
}

// httpSessionTracker maps HTTP session IDs to their active MCP session IDs.
// This allows closing all MCP sessions when an HTTP session (workflow) is stopped.
var globalHTTPSessionTracker = &httpSessionTracker{
	sessions:        make(map[string]map[string]struct{}),
	stoppedSessions: make(map[string]struct{}),
	stoppedByHTTP:   make(map[string]map[string]struct{}),
}

type httpSessionTracker struct {
	mu              sync.Mutex
	sessions        map[string]map[string]struct{} // httpSessionID -> set of mcpSessionIDs
	stoppedSessions map[string]struct{}            // set of stopped MCP session IDs (prevents broken pipe reconnection)
	// stoppedByHTTP records which MCP session IDs were stopped under each HTTP
	// session ID. CloseHTTPSession removes the live sessions mapping, so this is
	// what lets an intentional resume of that HTTP session (a new user message)
	// clear the stopped flags afterward and reconnect its MCP/bridge tools.
	stoppedByHTTP map[string]map[string]struct{} // httpSessionID -> set of stopped mcpSessionIDs
}

func (t *httpSessionTracker) register(httpSessionID, mcpSessionID string) {
	if httpSessionID == "" || mcpSessionID == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.sessions[httpSessionID]; !ok {
		t.sessions[httpSessionID] = make(map[string]struct{})
	}
	t.sessions[httpSessionID][mcpSessionID] = struct{}{}
}

func (t *httpSessionTracker) getMCPSessions(httpSessionID string) []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	mcpIDs := t.sessions[httpSessionID]
	result := make([]string, 0, len(mcpIDs))
	for id := range mcpIDs {
		result = append(result, id)
	}
	return result
}

// parent resolves only an unambiguous live server-owned registration.
func (t *httpSessionTracker) parent(mcpSessionID string) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, stopped := t.stoppedSessions[mcpSessionID]; stopped {
		return ""
	}
	parent := ""
	for httpID, children := range t.sessions {
		if _, ok := children[mcpSessionID]; ok {
			if parent != "" && parent != httpID {
				return ""
			}
			parent = httpID
		}
	}
	return parent
}

func (t *httpSessionTracker) remove(httpSessionID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.sessions, httpSessionID)
}

// markStopped records MCP session IDs as stopped so that broken pipe handlers
// will NOT recreate connections for sessions that were intentionally closed.
func (t *httpSessionTracker) markStopped(mcpSessionIDs []string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, id := range mcpSessionIDs {
		t.stoppedSessions[id] = struct{}{}
	}
}

// isStopped returns true if the given MCP session was closed via CloseHTTPSession.
func (t *httpSessionTracker) isStopped(mcpSessionID string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	_, ok := t.stoppedSessions[mcpSessionID]
	return ok
}

// clearStopped removes the given MCP session IDs from the stopped set so they
// can accept new connections again. This is needed when a deterministic session
// ID (e.g. browser session based on workspace+group hash) is reused across runs.
func (t *httpSessionTracker) clearStopped(mcpSessionIDs []string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, id := range mcpSessionIDs {
		delete(t.stoppedSessions, id)
	}
}

// markStoppedForHTTP marks the given MCP session IDs as stopped AND records them
// under httpSessionID so a later resume of that HTTP session can clear them
// (CloseHTTPSession drops the live sessions mapping, so we keep a separate copy).
func (t *httpSessionTracker) markStoppedForHTTP(httpSessionID string, mcpSessionIDs []string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, id := range mcpSessionIDs {
		t.stoppedSessions[id] = struct{}{}
	}
	if httpSessionID == "" || len(mcpSessionIDs) == 0 {
		return
	}
	set := t.stoppedByHTTP[httpSessionID]
	if set == nil {
		set = make(map[string]struct{})
		t.stoppedByHTTP[httpSessionID] = set
	}
	for _, id := range mcpSessionIDs {
		set[id] = struct{}{}
	}
}

// clearStoppedForHTTP lifts the stopped flag for every MCP session ID stopped
// under httpSessionID (via markStoppedForHTTP) and forgets the record. Called
// when a new user message intentionally resumes a stopped session.
func (t *httpSessionTracker) clearStoppedForHTTP(httpSessionID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for id := range t.stoppedByHTTP[httpSessionID] {
		delete(t.stoppedSessions, id)
	}
	delete(t.stoppedByHTTP, httpSessionID)
}

// GetSessionRegistry returns the global session connection registry
func GetSessionRegistry() *SessionConnectionRegistry {
	return globalSessionRegistry
}

// ResolveConnectionSessionID returns the actual registry session key that should be
// used for the given logical tool session + server combination.
//
// MCP subprocess connections are shared globally; logical session IDs still scope
// pending configuration, stop tracking, and tool-call metadata.
func (r *SessionConnectionRegistry) ResolveConnectionSessionID(_, _ string) string {
	return "global"
}

// getConnLock returns a mutex for the given (session, server) key, creating one if needed.
func (r *SessionConnectionRegistry) getConnLock(key string) *sync.Mutex {
	r.connLocksMu.Lock()
	defer r.connLocksMu.Unlock()
	if mu, ok := r.connLocks[key]; ok {
		return mu
	}
	mu := &sync.Mutex{}
	r.connLocks[key] = mu
	return mu
}

// GetOrCreateConnection returns existing connection or creates new one.
// Uses per-key mutex to ensure only one goroutine connects per (session, server) pair;
// concurrent callers block until the first connection attempt completes.
//
// Parameters:
//   - ctx: context for connection timeout
//   - sessionID: unique identifier for the session (workflow/conversation)
//   - serverName: MCP server name (e.g., "github", "context7")
//   - config: MCP server configuration
//   - logger: logger for connection events
//
// Returns:
//   - ClientInterface: the connection (new or existing)
//   - wasCreated: true if new connection was created, false if reused
//   - error: connection error if creation failed
func (r *SessionConnectionRegistry) GetOrCreateConnection(
	ctx context.Context,
	sessionID string,
	serverName string,
	config MCPServerConfig,
	logger loggerv2.Logger,
) (ClientInterface, bool, error) {

	// Refuse to create connections for sessions that were intentionally stopped.
	// This prevents broken pipe handlers from resurrecting zombie sub-agents
	// after a workflow is stopped by the user.
	//
	// WHY: When a user stops a workflow, CloseHTTPSession closes all MCP connections.
	// In-flight tool calls (e.g., browser_navigate) get "transport closed" errors.
	// The broken pipe handler then tries to recreate the connection via GetOrCreateConnection.
	// Without this guard, the sub-agent gets a fresh connection and continues executing
	// for minutes after the user pressed stop — a zombie sub-agent.
	if globalHTTPSessionTracker.isStopped(sessionID) {
		logger.Info(fmt.Sprintf("🛑 [ZOMBIE PREVENTION] Refusing connection for stopped session=%s server=%s", sessionID, serverName))
		return nil, false, fmt.Errorf("session %s was stopped — refusing to create new connection (zombie prevention)", sessionID)
	}

	// Get or create session's connection map
	sessionConnsRaw, _ := r.sessions.LoadOrStore(sessionID, &sessionConnections{
		createdAt: time.Now(),
	})
	sessionConns := sessionConnsRaw.(*sessionConnections)

	// Fast path (lock-free): connection already exists — verify it's alive
	if existing, ok := sessionConns.clients.Load(serverName); ok {
		client := existing.(ClientInterface)
		pingCtx, pingCancel := context.WithTimeout(ctx, 5*time.Second)
		pingErr := client.Ping(pingCtx)
		pingCancel()
		if pingErr == nil {
			logger.Info(fmt.Sprintf("Reusing existing connection for session=%s server=%s", sessionID, serverName))
			return client, false, nil
		}
		// Connection is dead — close it and fall through to create a new one
		logger.Warn(fmt.Sprintf("Connection dead for session=%s server=%s, will recreate", sessionID, serverName),
			loggerv2.Error(pingErr))
		_ = client.Close()
		sessionConns.clients.Delete(serverName)
	}

	// Serialize connection creation per (session, server) key.
	// Only one goroutine will proceed to connect; others wait here and get the result.
	lockKey := sessionID + "|" + serverName
	mu := r.getConnLock(lockKey)
	mu.Lock()
	defer mu.Unlock()

	// Double-check after acquiring lock: another goroutine may have connected while we waited
	if existing, ok := sessionConns.clients.Load(serverName); ok {
		client := existing.(ClientInterface)
		pingCtx, pingCancel := context.WithTimeout(ctx, 5*time.Second)
		pingErr := client.Ping(pingCtx)
		pingCancel()
		if pingErr == nil {
			logger.Info(fmt.Sprintf("Reusing existing connection (post-lock) for session=%s server=%s", sessionID, serverName))
			return client, false, nil
		}
		// Connection is dead — close it and fall through to create a new one
		logger.Warn(fmt.Sprintf("Connection dead (post-lock) for session=%s server=%s, will recreate", sessionID, serverName),
			loggerv2.Error(pingErr))
		_ = client.Close()
		sessionConns.clients.Delete(serverName)
	}

	// Create new connection — only one goroutine reaches here per key
	logger.Info(fmt.Sprintf("Creating new connection for session=%s server=%s", sessionID, serverName))
	client := New(config, logger)
	if err := client.ConnectWithRetry(ctx); err != nil {
		return nil, false, fmt.Errorf("failed to connect to %s: %w", serverName, err)
	}

	sessionConns.clients.Store(serverName, client)
	logger.Info(fmt.Sprintf("New connection established for session=%s server=%s", sessionID, serverName))
	return client, true, nil
}

// GetSessionConnections returns all connections for a session.
// Used when agent needs to access all its MCP clients.
func (r *SessionConnectionRegistry) GetSessionConnections(sessionID string) map[string]ClientInterface {
	sessionConnsRaw, ok := r.sessions.Load(sessionID)
	if !ok {
		return nil
	}

	sessionConns := sessionConnsRaw.(*sessionConnections)
	result := make(map[string]ClientInterface)
	sessionConns.clients.Range(func(key, value interface{}) bool {
		result[key.(string)] = value.(ClientInterface)
		return true
	})
	return result
}

// StoreConnection stores an existing client into the session registry.
// Used when a connection was created outside the registry (e.g., mcpcache fallback)
// and needs to be registered so future requests can reuse it.
func (r *SessionConnectionRegistry) StoreConnection(sessionID, serverName string, client ClientInterface) {
	sessionConnsRaw, _ := r.sessions.LoadOrStore(sessionID, &sessionConnections{
		createdAt: time.Now(),
	})
	sessionConns := sessionConnsRaw.(*sessionConnections)
	sessionConns.clients.Store(serverName, client)
}

// HasSession checks if a session exists in the registry
func (r *SessionConnectionRegistry) HasSession(sessionID string) bool {
	_, ok := r.sessions.Load(sessionID)
	return ok
}

// StoreServerConfig saves a server config for lazy on-demand connection.
// Called when tool definitions are loaded from cache but no connection is created yet.
func (r *SessionConnectionRegistry) StoreServerConfig(sessionID, serverName string, config MCPServerConfig) {
	sessionConnsRaw, _ := r.sessions.LoadOrStore(sessionID, &sessionConnections{
		createdAt: time.Now(),
	})
	sessionConns := sessionConnsRaw.(*sessionConnections)
	sessionConns.pendingConfigs.Store(serverName, config)
}

// GetServerConfig retrieves a stored server config for lazy connection.
// Returns (config, true) if found, (zero, false) if not.
func (r *SessionConnectionRegistry) GetServerConfig(sessionID, serverName string) (MCPServerConfig, bool) {
	sessionConnsRaw, ok := r.sessions.Load(sessionID)
	if !ok {
		return MCPServerConfig{}, false
	}
	sessionConns := sessionConnsRaw.(*sessionConnections)
	if raw, ok := sessionConns.pendingConfigs.Load(serverName); ok {
		return raw.(MCPServerConfig), true
	}
	return MCPServerConfig{}, false
}

// CloseSession closes all connections for a session and removes it from registry.
// Should be called when workflow/conversation ends.
//
// This is the ONLY way connections get closed when using sessions.
// Agent.Close() does NOT close connections when SessionID is set.
func (r *SessionConnectionRegistry) CloseSession(sessionID string) {
	sessionConnsRaw, ok := r.sessions.LoadAndDelete(sessionID)
	if !ok {
		return
	}

	sessionConns := sessionConnsRaw.(*sessionConnections)
	sessionConns.clients.Range(func(key, value interface{}) bool {
		serverName := key.(string)
		if client, ok := value.(ClientInterface); ok {
			fmt.Printf("Closing connection for session=%s server=%s\n", sessionID, serverName)
			_ = client.Close()
		}
		return true
	})
}

// CloseSessionServer closes a specific server connection within a session.
// Useful for targeted cleanup (e.g., close browser but keep other connections).
func (r *SessionConnectionRegistry) CloseSessionServer(sessionID, serverName string) {
	sessionConnsRaw, ok := r.sessions.Load(sessionID)
	if !ok {
		return
	}

	sessionConns := sessionConnsRaw.(*sessionConnections)
	if clientRaw, ok := sessionConns.clients.LoadAndDelete(serverName); ok {
		if client, ok := clientRaw.(ClientInterface); ok {
			_ = client.Close()
		}
	}
}

// ListSessions returns all active session IDs (for debugging)
func (r *SessionConnectionRegistry) ListSessions() []string {
	var sessions []string
	r.sessions.Range(func(key, value interface{}) bool {
		sessions = append(sessions, key.(string))
		return true
	})
	return sessions
}

// SessionStats returns statistics about a session (for debugging)
type SessionStats struct {
	SessionID       string
	ConnectionCount int
	ServerNames     []string
	CreatedAt       time.Time
}

// GetSessionStats returns statistics about a session's connections.
// Useful for debugging and monitoring.
func (r *SessionConnectionRegistry) GetSessionStats(sessionID string) *SessionStats {
	sessionConnsRaw, ok := r.sessions.Load(sessionID)
	if !ok {
		return nil
	}

	sessionConns := sessionConnsRaw.(*sessionConnections)
	stats := &SessionStats{
		SessionID: sessionID,
		CreatedAt: sessionConns.createdAt,
	}

	sessionConns.clients.Range(func(key, value interface{}) bool {
		stats.ServerNames = append(stats.ServerNames, key.(string))
		stats.ConnectionCount++
		return true
	})

	return stats
}

// RegisterHTTPSession registers an MCP session ID under an HTTP session ID.
// Call this whenever a new MCP session is created for a workflow so that
// CloseHTTPSession can close all of them when the workflow stops.
func (r *SessionConnectionRegistry) RegisterHTTPSession(httpSessionID, mcpSessionID string) {
	globalHTTPSessionTracker.register(httpSessionID, mcpSessionID)
}

// MCPSessionsForHTTPSession lists the live (not stopped) MCP sessions registered under an HTTP run, in no particular
// order. A run's scripted-step bridge sessions use it to find the sibling session that registered the run's tools.
func (r *SessionConnectionRegistry) MCPSessionsForHTTPSession(httpSessionID string) []string {
	live := []string{}
	for _, id := range globalHTTPSessionTracker.getMCPSessions(httpSessionID) {
		if !globalHTTPSessionTracker.isStopped(id) {
			live = append(live, id)
		}
	}
	sort.Strings(live)
	return live
}

// HTTPSessionForMCPSession resolves a live MCP child to its registered HTTP run.
// Missing, stopped and ambiguous registrations return an empty string.
func (r *SessionConnectionRegistry) HTTPSessionForMCPSession(mcpSessionID string) string {
	return globalHTTPSessionTracker.parent(mcpSessionID)
}

// CloseHTTPSession closes all MCP sessions registered under the given HTTP session ID.
// This is the primary cleanup path for workflow stop/completion.
// It also marks all associated MCP session IDs as stopped so that broken pipe
// handlers will NOT recreate connections for intentionally stopped sessions.
func (r *SessionConnectionRegistry) CloseHTTPSession(httpSessionID string) {
	mcpSessionIDs := globalHTTPSessionTracker.getMCPSessions(httpSessionID)
	log.Printf("[ZOMBIE PREVENTION] CloseHTTPSession(%s): marking %d MCP sessions as stopped: %v",
		httpSessionID, len(mcpSessionIDs), mcpSessionIDs)
	// Mark sessions as stopped BEFORE closing, so that any broken pipe handler
	// that fires during close will see the stopped flag and bail out.
	// This is the critical ordering: markStopped → close. If we close first,
	// the broken pipe handler could sneak in between close and markStopped,
	// recreating the connection before we flag it as stopped.
	globalHTTPSessionTracker.markStoppedForHTTP(httpSessionID, mcpSessionIDs)
	globalHTTPSessionTracker.remove(httpSessionID)
	for _, mcpID := range mcpSessionIDs {
		r.CloseSession(mcpID)
	}
}

// ClearHTTPSessionStopped lifts the stopped flag for every MCP session ID that
// was stopped under httpSessionID via CloseHTTPSession. Call this when a new
// user message intentionally resumes a session that was stopped mid-run (e.g.
// the stop icon on a scheduled coding-agent run) — otherwise zombie prevention
// permanently refuses the reused bridge's connections and its api-bridge tools
// read as "No such tool available" until a full reload.
func (r *SessionConnectionRegistry) ClearHTTPSessionStopped(httpSessionID string) {
	globalHTTPSessionTracker.clearStoppedForHTTP(httpSessionID)
}

// IsSessionStopped returns true if the given MCP session was closed via
// CloseHTTPSession (i.e., the workflow was intentionally stopped).
// Used by broken pipe handlers to avoid reconnecting zombie sub-agents.
func (r *SessionConnectionRegistry) IsSessionStopped(mcpSessionID string) bool {
	return globalHTTPSessionTracker.isStopped(mcpSessionID)
}

// ClearSessionsStopped removes the given MCP session IDs from the stopped set,
// allowing new connections to be created for them. This is needed when a
// deterministic session ID (e.g. browser session derived from workspace+group
// hash) is reused across workflow runs — without this, the zombie prevention
// logic would permanently block the reused session ID.
func (r *SessionConnectionRegistry) ClearSessionsStopped(sessionIDs []string) {
	globalHTTPSessionTracker.clearStopped(sessionIDs)
}

// MarkSessionsStopped marks the given MCP session IDs as stopped so that broken
// pipe handlers and per-tool handlers will refuse to create new connections for them.
// Call this BEFORE CloseSession to prevent races where in-flight tool calls
// resurrect connections that are being torn down.
func (r *SessionConnectionRegistry) MarkSessionsStopped(sessionIDs []string) {
	globalHTTPSessionTracker.markStopped(sessionIDs)
}

// CloseAllSessions closes all sessions and their connections.
// Useful for graceful shutdown.
func (r *SessionConnectionRegistry) CloseAllSessions() {
	var sessionsToClose []string
	r.sessions.Range(func(key, value interface{}) bool {
		sessionsToClose = append(sessionsToClose, key.(string))
		return true
	})

	for _, sessionID := range sessionsToClose {
		r.CloseSession(sessionID)
	}
}

// CloseServerConnections drops an exact retired authorization scope from every
// live/pending session. The caller must hold its last-user lifetime lock.
func (r *SessionConnectionRegistry) CloseServerConnections(serverName string) {
	r.sessions.Range(func(key, value interface{}) bool {
		conns := value.(*sessionConnections)
		conns.pendingConfigs.Delete(serverName)
		r.CloseSessionServer(key.(string), serverName)
		return true
	})
}
