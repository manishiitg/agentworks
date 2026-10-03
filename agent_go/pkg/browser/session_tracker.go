package browser

import (
	"context"
	"fmt"
	"log"
	"os"
	"strconv"
	"sync"
	"time"
)

// Default limits — overridden by env vars at init time.
var (
	// MaxBrowserSessionsPerAgent is the max concurrent headless browser sessions per agent.
	// Delegated agents use the parent workflow session.
	MaxBrowserSessionsPerAgent = 1

	// MaxBrowserSessionsPerWorkflow is the max concurrent headless browser sessions across
	// all agents in the same workflow (including isolated sub-agents).
	MaxBrowserSessionsPerWorkflow = 2

	// MaxBrowserSessionsGlobal is the absolute max across all sessions.
	// When exceeded, the oldest session globally is auto-evicted.
	MaxBrowserSessionsGlobal = 8
)

func init() {
	if v, err := strconv.Atoi(os.Getenv("MAX_BROWSER_SESSIONS_PER_AGENT")); err == nil && v > 0 {
		MaxBrowserSessionsPerAgent = v
		log.Printf("[BROWSER_TRACKER] Per-agent limit set from env: %d", v)
	}
	if v, err := strconv.Atoi(os.Getenv("MAX_BROWSER_SESSIONS_PER_WORKFLOW")); err == nil && v > 0 {
		MaxBrowserSessionsPerWorkflow = v
		log.Printf("[BROWSER_TRACKER] Per-workflow limit set from env: %d", v)
	}
	if v, err := strconv.Atoi(os.Getenv("MAX_BROWSER_SESSIONS_GLOBAL")); err == nil && v > 0 {
		MaxBrowserSessionsGlobal = v
		log.Printf("[BROWSER_TRACKER] Global limit set from env: %d", v)
	}
}

// browserSessionInfo tracks a headless browser session
type browserSessionInfo struct {
	browserSession    string // agent-browser session name (e.g., "twitter_research")
	agentSessionID    string // agent-level browser owner ID
	workflowSessionID string // root workflow/chat session ID (always the parent, for per-workflow limit)
	lastUsed          time.Time
	captureActive     bool
	captureOwner      string
	captureWorkspace  string
	createdAt         time.Time
	lastAction        string
	lastActionAt      time.Time
}

// SessionTracker tracks active headless browser sessions to prevent unbounded growth.
// Thread-safe — shared across all Executor instances.
type SessionTracker struct {
	mu       sync.Mutex
	sessions map[string]*browserSessionInfo // browser session name → info
}

var globalTracker = &SessionTracker{
	sessions: make(map[string]*browserSessionInfo),
}

// GetSessionTracker returns the global session tracker
func GetSessionTracker() *SessionTracker {
	return globalTracker
}

// StartIdleReaper launches a background goroutine that kills browser sessions
// idle for longer than idleTimeout. This prevents Chrome/daemon accumulation
// from agents that open sessions but never close them (workflow crash, LLM forget).
// Call once at server startup.
func StartIdleReaper(idleTimeout time.Duration) {
	if idleTimeout <= 0 {
		idleTimeout = 15 * time.Minute
	}
	go func() {
		ticker := time.NewTicker(2 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			reapIdleSessions(idleTimeout)
		}
	}()
	log.Printf("[BROWSER_REAPER] Started idle session reaper (timeout=%s, check interval=2m)", idleTimeout)
}

func reapIdleSessions(idleTimeout time.Duration) {
	tracker := GetSessionTracker()
	tracker.mu.Lock()
	var stale []string
	for name, s := range tracker.sessions {
		if time.Since(s.lastUsed) > idleTimeout && !s.captureActive && !(SharedBrowserEnabled() && name == SharedSessionName) {
			stale = append(stale, name)
		}
	}
	tracker.mu.Unlock()

	// Conversations idle in a browser that others keep alive give their tab
	// back (session_tabs.go); an entirely idle browser is reaped whole below.
	releaseIdleSessionTabOwners(sessionOwnerTabIdleRelease, defaultWorkspaceBrowserClient())

	if len(stale) == 0 {
		return
	}
	log.Printf("[BROWSER_REAPER] Reaping %d idle session(s) (idle > %s): %v", len(stale), idleTimeout, stale)
	for _, session := range stale {
		release, ok := TryTakeBrowserControl(session)
		if !ok {
			continue
		}
		tracker.mu.Lock()
		info := tracker.sessions[session]
		eligible := info != nil && !info.captureActive && time.Since(info.lastUsed) > idleTimeout
		tracker.mu.Unlock()
		if !eligible {
			release()
			continue
		}
		killSessionRuntimeFully(session)
		tracker.Remove(session)
		release()
		log.Printf("[BROWSER_REAPER] Reaped idle session %q", session)
	}
}

// Touch marks a browser session as recently used (or registers it if new).
// agentSessionID is the agent-level ID, workflowSessionID is the root workflow/chat ID.
// Returns true if this is a new browser session.
func (t *SessionTracker) Touch(browserSession, agentSessionID, workflowSessionID string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	existing, exists := t.sessions[browserSession]
	if exists {
		existing.lastUsed = time.Now()
		if agentSessionID != "" {
			existing.agentSessionID = agentSessionID
		}
		if workflowSessionID != "" {
			existing.workflowSessionID = workflowSessionID
		}
		return false
	}
	t.sessions[browserSession] = &browserSessionInfo{
		browserSession:    browserSession,
		agentSessionID:    agentSessionID,
		workflowSessionID: workflowSessionID,
		lastUsed:          time.Now(),
		createdAt:         time.Now(),
	}
	log.Printf("[BROWSER_TRACKER] New session registered: browser=%q agent=%q workflow=%q (total: %d)",
		browserSession, agentSessionID, workflowSessionID, len(t.sessions))
	return true
}

// TouchExisting updates lastUsed only if the session already exists.
// Does NOT register new sessions — prevents non-open commands from bypassing limits.
func (t *SessionTracker) TouchExisting(browserSession string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if existing, exists := t.sessions[browserSession]; exists {
		existing.lastUsed = time.Now()
	} else {
		log.Printf("[BROWSER_TRACKER] TouchExisting: session %q not tracked (non-open command on unregistered session — this is expected before first open)", browserSession)
	}
}

// Remove removes a browser session from tracking
func (t *SessionTracker) Remove(browserSession string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if info, exists := t.sessions[browserSession]; exists {
		log.Printf("[BROWSER_TRACKER] Session removed: browser=%q agent=%q workflow=%q age=%s (remaining: %d)",
			browserSession, info.agentSessionID, info.workflowSessionID,
			time.Since(info.createdAt).Round(time.Second), len(t.sessions)-1)
	}
	delete(t.sessions, browserSession)
	BindViewerCDPPort(browserSession, 0)
}

// CountForAgent returns the number of browser sessions owned by an agent session
func (t *SessionTracker) CountForAgent(agentSessionID string) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	count := 0
	for _, s := range t.sessions {
		if s.agentSessionID == agentSessionID {
			count++
		}
	}
	return count
}

// CountForWorkflow returns the number of browser sessions in the same workflow
func (t *SessionTracker) CountForWorkflow(workflowSessionID string) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	count := 0
	for _, s := range t.sessions {
		if s.workflowSessionID == workflowSessionID {
			count++
		}
	}
	return count
}

// SessionsForAgent returns browser session names owned by an agent session
func (t *SessionTracker) SessionsForAgent(agentSessionID string) []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	var names []string
	for _, s := range t.sessions {
		if s.agentSessionID == agentSessionID {
			names = append(names, s.browserSession)
		}
	}
	return names
}

// SessionsForWorkflow returns browser session names in the same workflow
func (t *SessionTracker) SessionsForWorkflow(workflowSessionID string) []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	var names []string
	for _, s := range t.sessions {
		if s.workflowSessionID == workflowSessionID {
			names = append(names, s.browserSession)
		}
	}
	return names
}

// Count returns the total number of tracked sessions
func (t *SessionTracker) Count() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.sessions)
}

// GetOldestSession returns the name of the least-recently-used session globally.
func (t *SessionTracker) GetOldestSession() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	var oldest *browserSessionInfo
	for _, s := range t.sessions {
		if s.captureActive {
			continue
		}
		if oldest == nil || s.lastUsed.Before(oldest.lastUsed) {
			oldest = s
		}
	}
	if oldest == nil {
		return ""
	}
	return oldest.browserSession
}

// GetOldestSessionForAgent returns the least-recently-used session for a specific agent session.
func (t *SessionTracker) GetOldestSessionForAgent(agentSessionID string) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	var oldest *browserSessionInfo
	for _, s := range t.sessions {
		if s.agentSessionID == agentSessionID {
			if oldest == nil || s.lastUsed.Before(oldest.lastUsed) {
				oldest = s
			}
		}
	}
	if oldest == nil {
		return ""
	}
	return oldest.browserSession
}

// CheckLimits checks if adding a new browser session would exceed limits.
// Checks two levels: per-agent limit and per-workflow limit.
// Returns an error message if limits are exceeded, empty string if OK.
func (t *SessionTracker) CheckLimits(browserSession, agentSessionID, workflowSessionID string) string {
	t.mu.Lock()
	defer t.mu.Unlock()

	// If this browser session already exists, it's a reuse — always OK
	if _, exists := t.sessions[browserSession]; exists {
		return ""
	}

	// Check per-agent limit
	if agentSessionID != "" {
		agentCount := 0
		var agentSessions []string
		for _, s := range t.sessions {
			if s.agentSessionID == agentSessionID {
				agentCount++
				agentSessions = append(agentSessions, s.browserSession)
			}
		}
		if agentCount >= MaxBrowserSessionsPerAgent {
			return fmt.Sprintf(
				"ERROR: Cannot open browser session %q — you already have %d active browser session(s) (max %d per agent). "+
					"Active sessions: %v. Close one first using agent_browser(command=\"close\", session=\"<name>\") before opening a new one.",
				browserSession, agentCount, MaxBrowserSessionsPerAgent, agentSessions)
		}
	}

	// Check per-workflow limit
	if workflowSessionID != "" {
		wfCount := 0
		var wfSessions []string
		for _, s := range t.sessions {
			if s.workflowSessionID == workflowSessionID {
				wfCount++
				wfSessions = append(wfSessions, s.browserSession)
			}
		}
		if wfCount >= MaxBrowserSessionsPerWorkflow {
			return fmt.Sprintf(
				"ERROR: Cannot open browser session %q — this workflow already has %d active browser session(s) (max %d per workflow). "+
					"Active sessions: %v. Reuse an existing session, or wait for a running sub-agent to finish.",
				browserSession, wfCount, MaxBrowserSessionsPerWorkflow, wfSessions)
		}
	}

	return ""
}

// ActiveSessions returns a snapshot of all active sessions with details
func (t *SessionTracker) ActiveSessions() []map[string]string {
	t.mu.Lock()
	defer t.mu.Unlock()
	result := make([]map[string]string, 0, len(t.sessions))
	for _, s := range t.sessions {
		item := map[string]string{
			"browser_session":  s.browserSession,
			"agent_session":    s.agentSessionID,
			"workflow_session": s.workflowSessionID,
			"age":              time.Since(s.createdAt).Round(time.Second).String(),
			"idle":             time.Since(s.lastUsed).Round(time.Second).String(),
		}
		if s.lastAction != "" {
			item["last_action"] = s.lastAction
			item["last_action_at"] = s.lastActionAt.UTC().Format(time.RFC3339)
		}
		result = append(result, item)
	}
	return result
}

// RemoveAllForAgent removes all browser sessions for a given agent session
func (t *SessionTracker) RemoveAllForAgent(agentSessionID string) []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	var removed []string
	for name, s := range t.sessions {
		if s.agentSessionID == agentSessionID && !IsUserBrowserSession(name) {
			removed = append(removed, name)
			delete(t.sessions, name)
		}
	}
	if len(removed) > 0 {
		log.Printf("[BROWSER_TRACKER] Removed %d sessions for agent %q: %v (remaining: %d)",
			len(removed), agentSessionID, removed, len(t.sessions))
	}
	return removed
}

// RemoveAllForWorkflow removes all browser sessions for a given workflow session
func (t *SessionTracker) RemoveAllForWorkflow(workflowSessionID string) []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	var removed []string
	for name, s := range t.sessions {
		if s.workflowSessionID == workflowSessionID && !IsUserBrowserSession(name) {
			removed = append(removed, name)
			delete(t.sessions, name)
		}
	}
	if len(removed) > 0 {
		log.Printf("[BROWSER_TRACKER] Removed %d sessions for workflow %q: %v (remaining: %d)",
			len(removed), workflowSessionID, removed, len(t.sessions))
	}
	return removed
}

// CloseAllForWorkflow closes all browser processes for a given workflow session
// and removes them from the tracker. This should be called when a workflow ends
// (workflow completion, stop, or clear) to prevent chromium process accumulation.
func (t *SessionTracker) CloseAllForWorkflow(workflowSessionID string, client *Client) {
	// The ending conversation gives back its tab in any shared browser first;
	// a browser other conversations still use is kept open for them.
	ReleaseSessionTabOwner(workflowSessionID, client)

	sessions := t.SessionsForWorkflow(workflowSessionID)
	if len(sessions) == 0 {
		return
	}

	log.Printf("[BROWSER_CLEANUP] Closing %d browser session(s) for workflow %q: %v", len(sessions), workflowSessionID, sessions)
	removed := 0
	for _, session := range sessions {
		if others := otherSessionTabOwners(session, workflowSessionID); len(others) > 0 {
			t.reassignOwner(session, others[0])
			log.Printf("[BROWSER_CLEANUP] Keeping shared browser %q open: %d other conversation(s) still use it (%v)", session, len(others), others)
			continue
		}
		if IsUserBrowserSession(session) {
			continue // a user's own browser outlives the conversation (never untracked here)
		}
		if SharedBrowserEnabled() && session == SharedSessionName {
			t.Remove(session)
			removed++
			continue
		}
		if client != nil {
			closeArgs := []string{
				"--user-agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
				"--args", "--disable-blink-features=AutomationControlled",
				"--session", session, "close", "--json",
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			_, err := client.ExecuteCommand(ctx, closeArgs, &ExecuteOptions{Timeout: 10 * time.Second})
			cancel()
			if err != nil {
				log.Printf("[BROWSER_CLEANUP] Failed to close session %q: %v (will remove from tracker anyway)", session, err)
			} else {
				log.Printf("[BROWSER_CLEANUP] Closed browser session %q", session)
			}
		}
		clearSessionTabScope(session)
		t.Remove(session)
		removed++
	}
	log.Printf("[BROWSER_CLEANUP] Cleanup complete for workflow %q: removed %d session(s) from tracker", workflowSessionID, removed)
}

// reassignOwner attributes a still-shared browser to a conversation that is
// still using it, so that conversation's own end (or the idle reaper) closes it.
func (t *SessionTracker) reassignOwner(browserSession, ownerID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if info, ok := t.sessions[browserSession]; ok {
		info.agentSessionID = ownerID
		info.workflowSessionID = ownerID
	}
}

// CloseSession closes a single browser session and removes it from the tracker.
func (t *SessionTracker) CloseSession(browserSession string, client *Client) {
	t.mu.Lock()
	_, exists := t.sessions[browserSession]
	t.mu.Unlock()
	if !exists {
		return
	}

	log.Printf("[BROWSER_CLEANUP] Closing isolated browser session %q", browserSession)
	if client != nil {
		closeArgs := []string{
			"--user-agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
			"--args", "--disable-blink-features=AutomationControlled",
			"--session", browserSession, "close", "--json",
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		_, err := client.ExecuteCommand(ctx, closeArgs, &ExecuteOptions{Timeout: 10 * time.Second})
		cancel()
		if err != nil {
			log.Printf("[BROWSER_CLEANUP] Failed to close session %q: %v (removing from tracker anyway)", browserSession, err)
		}
	}
	t.Remove(browserSession)
}

// Clear removes all tracked sessions
func (t *SessionTracker) Clear() {
	t.mu.Lock()
	defer t.mu.Unlock()
	count := len(t.sessions)
	t.sessions = make(map[string]*browserSessionInfo)
	log.Printf("[BROWSER_TRACKER] All %d sessions cleared", count)
}

// === Backward-compatible aliases (for callers that still use chatSessionID) ===

// CountForChat is an alias for CountForWorkflow (backward compat)
func (t *SessionTracker) CountForChat(chatSessionID string) int {
	return t.CountForWorkflow(chatSessionID)
}

// SessionsForChat is an alias for SessionsForWorkflow (backward compat)
func (t *SessionTracker) SessionsForChat(chatSessionID string) []string {
	return t.SessionsForWorkflow(chatSessionID)
}

// RemoveAllForChat is an alias for RemoveAllForWorkflow (backward compat)
func (t *SessionTracker) RemoveAllForChat(chatSessionID string) []string {
	return t.RemoveAllForWorkflow(chatSessionID)
}

// CloseAllForChat is an alias for CloseAllForWorkflow (backward compat)
func (t *SessionTracker) CloseAllForChat(chatSessionID string, client *Client) {
	t.CloseAllForWorkflow(chatSessionID, client)
}

// SetCapture retains browser lifetime for a recording and identifies its owning run.
func (t *SessionTracker) SetCapture(session string, active bool, owner, workspace string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.sessions[session] == nil && active && IsUserBrowserSession(session) {
		t.sessions[session] = &browserSessionInfo{browserSession: session, agentSessionID: owner, workflowSessionID: owner, createdAt: time.Now()}
	}
	if s := t.sessions[session]; s != nil {
		s.captureActive = active
		s.captureOwner = owner
		s.captureWorkspace = workspace
		s.lastUsed = time.Now()
	}
}
func (t *SessionTracker) CaptureConflict(session, owner, workspace, command string) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	s := t.sessions[session]
	if s == nil || !s.captureActive {
		return ""
	}
	if command == "close" || command == "quit" || command == "exit" || command == "reset" {
		return "Stop the active capture before closing or resetting the workflow browser."
	}
	if (s.captureOwner != "" && owner != s.captureOwner) || (s.captureWorkspace != "" && workspace != s.captureWorkspace) {
		return "The workflow browser is recording another run. Wait until that capture stops before using this browser."
	}
	return ""
}
func (t *SessionTracker) Recording(session string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	s := t.sessions[session]
	return s != nil && s.captureActive
}

func (t *SessionTracker) HasSession(session string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.sessions[session] != nil
}
