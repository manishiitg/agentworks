package server

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/mux"
	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
	agent "github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentwrapper"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/costobserver"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
)

func isScheduledSessionIdentity(sessionID, triggeredBy string) bool {
	trigger := strings.ToLower(strings.TrimSpace(triggeredBy))
	id := strings.ToLower(strings.TrimSpace(sessionID))
	return trigger == "cron" || trigger == "webhook" ||
		strings.Contains(trigger, "schedule") ||
		strings.HasPrefix(id, "schedule-") ||
		strings.Contains(id, "-schedule-")
}

// trackActiveSession tracks a new active session
func (api *StreamingAPI) trackActiveSession(sessionID, agentMode, query, userID, botPlatform, triggeredBy, sessionTitle, parentSessionID, sessionKind string) {
	if api.eventStore != nil {
		api.claimCrewMainSession(sessionID, userID)
		api.eventStore.SetSessionOwner(sessionID, userID)
	}

	defer api.publishSessionChanged(sessionID) // runs after the unlock below
	api.activeSessionsMux.Lock()
	defer api.activeSessionsMux.Unlock()

	if existing := api.activeSessions[sessionID]; existing != nil {
		if botPlatform == "" {
			botPlatform = existing.BotPlatform
		}
		if triggeredBy == "" {
			triggeredBy = existing.TriggeredBy
		}
		if strings.TrimSpace(sessionTitle) == "" {
			sessionTitle = existing.Title
		}
		if strings.TrimSpace(parentSessionID) == "" {
			parentSessionID = existing.ParentSessionID
		}
		if strings.TrimSpace(sessionKind) == "" {
			sessionKind = existing.SessionKind
		}
	}

	api.activeSessions[sessionID] = &ActiveSessionInfo{
		SessionID:       sessionID,
		ParentSessionID: strings.TrimSpace(parentSessionID),
		SessionKind:     strings.TrimSpace(sessionKind),
		AgentMode:       agentMode,
		Status:          "running",
		LastActivity:    time.Now(),
		LastWorkedAt:    time.Now(),
		CreatedAt:       time.Now(),
		Query:           query,
		Title:           strings.TrimSpace(sessionTitle),
		UserID:          userID,
		BotPlatform:     botPlatform,
		TriggeredBy:     triggeredBy,
	}

	logfWithContext(
		newServerLogContext("", "", agentMode, userID, "", sessionID),
		"[ACTIVE_SESSION] Tracked active session: %s (mode: %s, user: %s)",
		sessionID,
		agentMode,
		userID,
	)
}

// updateSessionActivity updates the LastActivity timestamp for a session when events are added
func (api *StreamingAPI) updateSessionActivity(sessionID string) {
	api.activeSessionsMux.Lock()
	if session, exists := api.activeSessions[sessionID]; exists {
		session.LastActivity = time.Now()
		session.LastWorkedAt = session.LastActivity
		// Don't log every activity update to avoid log spam
	}
	api.activeSessionsMux.Unlock()
}

// updateSessionStatus updates the status of an active session in memory.
func (api *StreamingAPI) updateSessionStatus(sessionID, status string) {
	changed, workspacePath, scheduled := false, "", false
	previous, info := "", (*ActiveSessionInfo)(nil)
	api.activeSessionsMux.Lock()
	if session, exists := api.activeSessions[sessionID]; exists {
		previous = session.Status
		copied := *session
		info = &copied
		changed = session.Status != status
		workspacePath = session.WorkspacePath
		scheduled = liveFeedScheduledSession(sessionID, session.TriggeredBy)
		session.Status = status
		session.LastActivity = time.Now()
		session.LastWorkedAt = session.LastActivity
		log.Printf("[ACTIVE_SESSION] Updated session %s status to: %s", sessionID, status)
	}
	api.activeSessionsMux.Unlock()
	api.observeRuntimeSnapshot(sessionID)
	if changed {
		api.notifyTurnEnded(sessionID, previous, status, info) // notifications.go: the server decides every send
		api.publishSessionChanged(sessionID)
		if liveFeedTerminalStatus(status) {
			if scheduled {
				publishWorkflowSettled(workspacePath)
			} else {
				// Coding CLIs can edit plan files directly, outside workspace
				// tools. The terminal notice catches up with those edits.
				publishPlanChanged(workspacePath + "/planning/plan.json")
			}
		}
	}
}

// removeSessionQueryID removes a completed workflow query from the session ->
// query index used by stop/reconnect/scheduler wait logic.
func (api *StreamingAPI) removeSessionQueryID(sessionID, queryID string) {
	if sessionID == "" || queryID == "" {
		return
	}

	api.sessionQueryIDMux.Lock()
	defer api.sessionQueryIDMux.Unlock()

	queryIDs := api.sessionQueryIDs[sessionID]
	if len(queryIDs) == 0 {
		return
	}

	next := queryIDs[:0]
	for _, qid := range queryIDs {
		if qid != queryID {
			next = append(next, qid)
		}
	}
	if len(next) == 0 {
		delete(api.sessionQueryIDs, sessionID)
		return
	}
	api.sessionQueryIDs[sessionID] = next
}

// handleDismissSession marks a session as dismissed so it won't be auto-restored on page refresh.
func (api *StreamingAPI) handleDismissSession(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	vars := mux.Vars(r)
	sessionID := vars["session_id"]

	if sessionID == "" {
		http.Error(w, "Session ID is required", http.StatusBadRequest)
		return
	}
	// Only someone who may see the session can dismiss it (PLAT-362 D5).
	if !api.canAccessTerminalSession(r, sessionID) {
		http.Error(w, "Session not found or access denied", http.StatusNotFound)
		return
	}

	api.activeSessionsMux.Lock()
	if session, exists := api.activeSessions[sessionID]; exists {
		session.Status = "dismissed"
		session.LastActivity = time.Now()
	}
	api.activeSessionsMux.Unlock()
	api.publishSessionChanged(sessionID)

	log.Printf("[ACTIVE_SESSION] Dismissed session %s", sessionID)
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{
		"status":  "dismissed",
		"session": sessionID,
	})
}

// getActiveSession retrieves an active session by ID
func (api *StreamingAPI) getActiveSession(sessionID string) (*ActiveSessionInfo, bool) {
	api.activeSessionsMux.RLock()
	defer api.activeSessionsMux.RUnlock()

	session, exists := api.activeSessions[sessionID]
	if !exists || session == nil {
		return nil, false
	}
	return cloneActiveSessionInfo(session), true
}

func cloneActiveSessionInfo(session *ActiveSessionInfo) *ActiveSessionInfo {
	if session == nil {
		return nil
	}
	copy := *session
	if session.WaitingSince != nil {
		waitingSince := *session.WaitingSince
		copy.WaitingSince = &waitingSince
	}
	if session.RuntimeState != nil {
		runtimeState := cloneRuntimeSnapshot(*session.RuntimeState)
		copy.RuntimeState = &runtimeState
	}
	return &copy
}

// getAllActiveSessions returns live sessions plus retained terminal sessions.
func (api *StreamingAPI) getAllActiveSessions() []*ActiveSessionInfo {
	api.activeSessionsMux.RLock()
	snapshots := make([]*ActiveSessionInfo, 0, len(api.activeSessions))
	for _, session := range api.activeSessions {
		snapshots = append(snapshots, cloneActiveSessionInfo(session))
	}
	api.activeSessionsMux.RUnlock()

	now := time.Now()
	inactivityTimeout := 10 * time.Minute
	sessionRetention := 24 * time.Hour
	sessions := make([]*ActiveSessionInfo, 0, len(snapshots))

	for _, session := range snapshots {
		if normalizeSessionLifecycleStatus(session.Status) == sessionLifecycleRunning {
			runtimeState, _ := api.authoritativeRuntimeSnapshot(session.SessionID)
			if now.Sub(session.LastActivity) < inactivityTimeout || runtimePhaseIsLive(runtimeState.Phase) {
				sessions = append(sessions, session)
			}
			continue
		}

		isTerminal := isStoppedSessionStatus(session.Status)
		if isTerminal && now.Sub(session.LastActivity) < sessionRetention {
			sessions = append(sessions, session)
		}
	}

	return sessions
}

// cleanupInactiveSessions runs periodically to:
// 1. Mark running sessions as inactive if no events for 10 minutes
// 2. Evict event store buffers when the retained session expires
// 3. Fully delete sessions from activeSessions after 24 hours
//
// The session entry is intentionally kept alive for 24 hours (not the old 30 minutes) so
// that verifySessionAccess continues to accept follow-up messages. Evicting a session from
// activeSessions causes the frontend to start a new session with no history, silently
// losing conversation context.
func (api *StreamingAPI) cleanupInactiveSessions() {
	ticker := time.NewTicker(2 * time.Minute) // Check every 2 minutes
	defer ticker.Stop()

	for range ticker.C {
		now := time.Now()
		api.cleanupInactiveSessionsAt(now)
		api.cleanupExpiredPythonAgentSessions(now)
		api.heartbeatTerminalLeases(now)
		if closed := api.cleanupStaleCodingAgentTmuxSessions(now); closed > 0 {
			log.Printf("[ACTIVE_SESSION] Cleanup: Closed %d stale coding-agent tmux session(s)", closed)
		}
		if registry := api.ensureTerminalLeaseRegistry(); registry != nil {
			registry.Prune(now)
		}
	}
}

type inactiveSessionCandidate struct {
	sessionID    string
	status       string
	lastActivity time.Time
}

type idleMemoryCandidate struct {
	sessionID    string
	lastActivity time.Time
	evictAgent   bool
}

func (api *StreamingAPI) cleanupInactiveSessionsAt(now time.Time) {
	const (
		inactivityTimeout     = 10 * time.Minute
		historyCacheRetention = 5 * time.Minute
		idleAgentRetention    = 15 * time.Minute
		sessionRetention      = 24 * time.Hour
		eventBufferRetention  = 24 * time.Hour
	)

	api.activeSessionsMux.RLock()
	candidates := make([]inactiveSessionCandidate, 0, len(api.activeSessions))
	for sessionID, session := range api.activeSessions {
		candidates = append(candidates, inactiveSessionCandidate{
			sessionID: sessionID, status: session.Status, lastActivity: session.LastActivity,
		})
	}
	api.activeSessionsMux.RUnlock()

	markInactive := make(map[string]time.Time)
	for _, candidate := range candidates {
		if normalizeSessionLifecycleStatus(candidate.status) != sessionLifecycleRunning || now.Sub(candidate.lastActivity) < inactivityTimeout {
			continue
		}
		api.pendingMu.RLock()
		hasPending := len(api.pendingCompletions[candidate.sessionID]) > 0 || api.completionRetryScheduled[candidate.sessionID]
		api.pendingMu.RUnlock()
		runtimeState, _ := api.authoritativeRuntimeSnapshot(candidate.sessionID)
		if !hasPending && !runtimePhaseIsLive(runtimeState.Phase) {
			markInactive[candidate.sessionID] = candidate.lastActivity
		}
	}

	sessionsToEvictEventBuffer := make([]string, 0)
	sessionsToEvictRuntime := make([]string, 0)
	sessionsMarkedInactive := make([]string, 0)
	idleMemoryCandidates := make([]idleMemoryCandidate, 0)
	api.activeSessionsMux.Lock()
	for sessionID, session := range api.activeSessions {
		age := now.Sub(session.LastActivity)
		if previousActivity, ok := markInactive[sessionID]; ok &&
			session.LastActivity.Equal(previousActivity) &&
			normalizeSessionLifecycleStatus(session.Status) == sessionLifecycleRunning {
			session.Status = string(sessionLifecycleInactive)
			session.LastActivity = now
			sessionsMarkedInactive = append(sessionsMarkedInactive, sessionID)
			age = 0
			log.Printf("[ACTIVE_SESSION] Marked session %s inactive after verified idle timeout", sessionID)
		}
		if !isStoppedSessionStatus(session.Status) {
			continue
		}
		if age >= historyCacheRetention && age < sessionRetention {
			idleMemoryCandidates = append(idleMemoryCandidates, idleMemoryCandidate{
				sessionID:    sessionID,
				lastActivity: session.LastActivity,
				evictAgent:   age >= idleAgentRetention,
			})
		}
		if age >= eventBufferRetention {
			sessionsToEvictEventBuffer = append(sessionsToEvictEventBuffer, sessionID)
		}
		if age >= sessionRetention {
			delete(api.activeSessions, sessionID)
			sessionsToEvictRuntime = append(sessionsToEvictRuntime, sessionID)
			costobserver.SetRetainedInputSource(sessionID, "", "")
			if api.terminalStore != nil {
				api.terminalStore.RemoveSession(sessionID)
			}
			log.Printf("[ACTIVE_SESSION] Cleanup: Removed old %s session %s from memory (>24h)", session.Status, sessionID)
		}
	}
	api.activeSessionsMux.Unlock()
	if len(sessionsMarkedInactive) > 0 || len(sessionsToEvictRuntime) > 0 {
		publishSessionsChanged()
	}

	for _, sessionID := range sessionsMarkedInactive {
		api.observeRuntimeSnapshot(sessionID)
	}

	// Release heavy per-session memory caches (parsed conversation history and
	// idle agent wrappers) shortly after a reply finishes, while keeping the
	// lightweight activeSessions entry for 24h so follow-up messages keep
	// working (#4080). The next turn reloads the recent window from disk.
	releasedHistorySessions, releasedHistoryMsgs, releasedAgents := 0, 0, 0
	for _, candidate := range idleMemoryCandidates {
		// Serialize against starting/running turns (handleQuery, triggerSyntheticTurn)
		// without blocking cleanup if a turn already holds the session lane (#4366).
		releaseLane, acquired := api.tryLockSessionInputLane(candidate.sessionID, 0)
		if !acquired {
			continue
		}
		var evictedAgent *agent.LLMAgentWrapper
		var agentEvicted bool
		func() {
			defer releaseLane()
			if api.sessionRunsATurnHoldingLane(candidate.sessionID) {
				return
			}
			api.pendingMu.RLock()
			hasPending := len(api.pendingCompletions[candidate.sessionID]) > 0 || api.completionRetryScheduled[candidate.sessionID]
			api.pendingMu.RUnlock()
			if hasPending {
				return
			}
			api.activeSessionsMux.RLock()
			current := api.activeSessions[candidate.sessionID]
			stillIdle := current != nil && current.LastActivity.Equal(candidate.lastActivity) && isStoppedSessionStatus(current.Status)
			api.activeSessionsMux.RUnlock()
			if !stillIdle {
				return
			}
			api.conversationMux.Lock()
			if msgs, exists := api.conversationHistory[candidate.sessionID]; exists {
				releasedHistorySessions++
				releasedHistoryMsgs += len(msgs)
				delete(api.conversationHistory, candidate.sessionID)
			}
			delete(api.resumeHistorySources, candidate.sessionID)
			api.conversationMux.Unlock()
			if candidate.evictAgent && !nativeWakes.hasPending(candidate.sessionID) {
				evictedAgent, agentEvicted = api.detachSessionAgent(candidate.sessionID)
				if agentEvicted {
					releasedAgents++
				}
			}
		}()
		if evictedAgent != nil {
			_ = evictedAgent.Close()
		}
	}
	if releasedHistorySessions > 0 || releasedAgents > 0 {
		log.Printf("[ACTIVE_SESSION] Released idle reply memory: history_sessions=%d history_msgs=%d session_agents=%d", releasedHistorySessions, releasedHistoryMsgs, releasedAgents)
	}

	for _, sessionID := range sessionsToEvictRuntime {
		api.evictSessionState(sessionID)
	}

	for _, sessionID := range sessionsToEvictEventBuffer {
		if api.eventStore != nil {
			api.eventStore.RemoveSession(sessionID)
			log.Printf("[ACTIVE_SESSION] Cleanup: Removed event buffer for session %s", sessionID)
		}
	}
}

// evictSessionState releases all per-session in-memory maps and side state when
// a stopped session ages out of memory retention.
func (api *StreamingAPI) evictSessionState(sessionID string) {
	if api == nil || sessionID == "" {
		return
	}
	workspace.ClearSessionShellConfig(sessionID)
	virtualtools.DeleteSessionNotificationDestination(sessionID)
	if api.runtimeCoordinator != nil {
		api.runtimeCoordinator.Evict(sessionID)
	}

	api.conversationMux.Lock()
	delete(api.conversationHistory, sessionID)
	delete(api.resumeHistorySources, sessionID)
	delete(api.lastWorkshopModeBySession, sessionID)
	delete(api.lastChatPolicyBySession, sessionID)
	delete(api.lastChatPolicyRoleBySession, sessionID)
	delete(api.lastAgentProfileKeyBySession, sessionID)
	delete(api.lastAgentToolsModeBySession, sessionID)
	delete(api.lastWorkflowToolsModeBySession, sessionID)
	delete(api.launchedAgentProfileKeyBySession, sessionID)
	api.conversationMux.Unlock()

	api.restoredConversationPersistMux.Lock()
	delete(api.restoredConversationPersistTargets, sessionID)
	api.restoredConversationPersistMux.Unlock()

	api.workflowObjectiveMux.Lock()
	delete(api.workflowObjectives, sessionID)
	api.workflowObjectiveMux.Unlock()

	api.workflowOrchestratorsMux.Lock()
	delete(api.workflowOrchestrators, sessionID)
	api.workflowOrchestratorsMux.Unlock()

	api.removeSessionAgent(sessionID)

	api.lastQueryMu.Lock()
	delete(api.lastQueryRequests, sessionID)
	api.lastQueryMu.Unlock()

	api.sessionWorkspaceMu.Lock()
	delete(api.sessionWorkspaceFolders, sessionID)
	api.sessionWorkspaceMu.Unlock()

	api.stoppedSessionsMu.Lock()
	delete(api.stoppedSessions, sessionID)
	api.stoppedSessionsMu.Unlock()

	api.interruptedTurnsMu.Lock()
	delete(api.interruptedTurns, sessionID)
	api.interruptedTurnsMu.Unlock()

	api.pendingTurnStopsMu.Lock()
	delete(api.pendingTurnStops, sessionID)
	api.pendingTurnStopsMu.Unlock()

	api.completionLoopStartedMu.Lock()
	delete(api.completionLoopStarted, sessionID)
	api.completionLoopStartedMu.Unlock()

	api.nativeBackgroundMu.Lock()
	delete(api.nativeBackground, sessionID)
	api.nativeBackgroundMu.Unlock()

	api.nativeObserversMu.Lock()
	delete(api.nativeObservers, sessionID)
	api.nativeObserversMu.Unlock()

	api.nativeCostsMu.Lock()
	if accountant := api.nativeCosts[sessionID]; accountant != nil {
		accountant.mu.Lock()
		accountant.stopLocked()
		accountant.mu.Unlock()
		delete(api.nativeCosts, sessionID)
	}
	api.nativeCostsMu.Unlock()

	api.promptParts.Delete(sessionID)
	api.questionsRestored.Delete(sessionID)
	retainedLastSidecarFinal.Delete(sessionID)
}

// storeWorkflowOrchestrator stores a workflow orchestrator for a session
func (api *StreamingAPI) storeWorkflowOrchestrator(sessionID string, orchestrator orchestrator.Orchestrator) {
	api.workflowOrchestratorsMux.Lock()
	defer api.workflowOrchestratorsMux.Unlock()
	api.workflowOrchestrators[sessionID] = orchestrator
	log.Printf("[ORCHESTRATOR] Stored workflow orchestrator for session %s", sessionID)
}
