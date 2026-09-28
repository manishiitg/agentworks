package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	mcpagent "github.com/manishiitg/mcpagent/agent"
	claudecodeadapter "github.com/manishiitg/multi-llm-provider-go/pkg/adapters/claudecode"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/codexcli"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/cursorcli"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/musecli"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/picli"

	"github.com/gorilla/mux"
)

// getSessionStatusString returns the session status for a given session ID
// from the in-memory active sessions map. Returns "" if not found, and
// denyAccess=true when the session belongs to another user.
func (api *StreamingAPI) getSessionStatusString(r *http.Request, sessionID string) (status string, denyAccess bool) {
	currentUserID := GetUserIDFromContext(r.Context())
	activeSession, existsInActive := api.getActiveSession(sessionID)
	if !existsInActive {
		return "", false
	}
	if activeSession.UserID != "" && activeSession.UserID != currentUserID {
		return "", true
	}
	return activeSession.Status, false
}

func (api *StreamingAPI) canSteerSession(sessionID string) bool {
	api.runningAgentsMux.RLock()
	runningAgent, exists := api.runningAgents[sessionID]
	api.runningAgentsMux.RUnlock()
	if !exists || runningAgent == nil {
		return false
	}

	// A structured-transport agent (e.g. Cursor's one-shot `cursor-agent
	// --print`) has no live pane to send-keys into between turns — the same
	// reason deliverUserMessage always resolves to QueuedForInjection for it
	// (see mcpagent/agent/message_delivery.go). Without this check, callers
	// that gate a steer attempt on canSteerSession (background_agents.go's
	// completion-notification path) would retry a steer that structurally can
	// never succeed every few seconds forever, each retry writing a fresh
	// duplicate event into the session — found live as a flickering
	// tool-call entry in a structured Cursor session's activity feed.
	if !mcpagent.AgentSupportsSteering(runningAgent) {
		return false
	}

	// A retained agent object is not enough for foreground steer/control semantics.
	// Main-agent coding CLI sessions can keep an idle tmux pane alive long after
	// the foreground Go turn has finished; treating that as steerable keeps status
	// and stop/escape behavior tied to a turn that no longer exists. The active
	// cancel handle is the server-owned foreground-turn proof.
	if api.hasActiveTurnCancel(sessionID) {
		return api.liveSteerTransportReady(sessionID, runningAgent)
	}
	// A resumed/launch-only coding agent has no server-managed foreground turn,
	// but its tmux pane can still be actively working. Allow steering when the
	// pane currently looks busy — the busy-content heuristic stands in for the
	// missing turn-cancel proof, so foreground control goes to a working agent
	// rather than treating the turn as complete.
	if api.terminalStore == nil || !api.terminalStore.SessionHasBusyMainCodingTmux(sessionID) {
		return false
	}
	return api.liveSteerTransportReady(sessionID, runningAgent)
}

// liveSteerTransportReady verifies the provider's live-injection transport
// is actually registered, not just that a turn is running. Every coding
// CLI registers its interactive session asynchronously after turn start;
// can_steer went true in the gap and the steer 409'd delivery_uncertain
// (found live on muse: "no active Muse interactive session registered").
// Providers without a registry stay ready by default. Cheap map lookups
// only — delivery itself re-verifies pane liveness.
func (api *StreamingAPI) liveSteerTransportReady(sessionID string, runningAgent *mcpagent.Agent) bool {
	provider := strings.ToLower(strings.TrimSpace(string(mcpagent.ReadAgentRuntimeInfo(runningAgent).Provider)))
	if api != nil && api.internalSteerTransportReady != nil {
		return api.internalSteerTransportReady(provider, sessionID)
	}
	switch provider {
	case "claude-code":
		return claudecodeadapter.InteractiveSessionRegistered(sessionID)
	case "cursor-cli":
		return cursorcli.InteractiveSessionRegistered(sessionID)
	case "codex-cli":
		return codexcli.InteractiveSessionRegistered(sessionID)
	case "pi-cli":
		return picli.InteractiveSessionRegistered(sessionID)
	case "muse-cli":
		return musecli.InteractiveSessionRegistered(sessionID)
	default:
		return true
	}
}

// foregroundTurnAlive is the transport-agnostic turn-liveness behind
// canSteerSession: same checks minus transport registration. The idle
// foreground-session completion probe uses this, not canSteerSession —
// a live turn whose CLI is still registering must keep the session
// running, while steer gating must wait for the transport.
func (api *StreamingAPI) foregroundTurnAlive(sessionID string) bool {
	api.runningAgentsMux.RLock()
	runningAgent, exists := api.runningAgents[sessionID]
	api.runningAgentsMux.RUnlock()
	if !exists || runningAgent == nil {
		return false
	}
	if !mcpagent.AgentSupportsSteering(runningAgent) {
		return false
	}
	if api.hasActiveTurnCancel(sessionID) {
		return true
	}
	return api.terminalStore != nil && api.terminalStore.SessionHasBusyMainCodingTmux(sessionID)
}

func (api *StreamingAPI) shouldCompleteIdleForegroundSession(sessionID, status string, hasRunningBackgroundAgents bool) bool {
	if strings.ToLower(strings.TrimSpace(status)) != "running" {
		return false
	}
	if hasRunningBackgroundAgents || api.isSyntheticTurn(sessionID) {
		return false
	}
	if api.hasRunningTrackedExecutionForSession(sessionID) {
		return false
	}
	if api.isSessionBusy(sessionID) {
		return false
	}
	return !api.foregroundTurnAlive(sessionID)
}

func (api *StreamingAPI) hasRunningTrackedExecutionForSession(sessionID string) bool {
	if api == nil || strings.TrimSpace(sessionID) == "" {
		return false
	}
	api.trackedWorkflowExecutionsMux.RLock()
	defer api.trackedWorkflowExecutionsMux.RUnlock()
	return api.runningWorkflowExecutionBySessionLocked(sessionID) != nil ||
		api.runningTrackedExecutionBySessionLocked(sessionID) != nil
}

// --- POLLING API TYPES ---
// Observer APIs removed - events are now stored by sessionID

// GetEventsResponse represents the response for event polling
type GetEventsResponse struct {
	Events                     []events.Event   `json:"events"`
	HasMore                    bool             `json:"has_more"`
	SessionID                  string           `json:"session_id"`
	SessionStatus              string           `json:"session_status,omitempty"`                // Session status: "running", "completed", "error", "stopped", "inactive"
	DisplayStatus              string           `json:"display_status,omitempty"`                // Consolidated live status: "busy", "idle", "stopped"
	LastProcessedIndex         int              `json:"last_processed_index"`                    // Last index processed in unfiltered array (for correct sinceIndex tracking)
	HasRunningBackgroundAgents bool             `json:"has_running_background_agents,omitempty"` // Whether background agents are still running for this session
	IsSyntheticTurn            bool             `json:"is_synthetic_turn,omitempty"`             // True when running auto-notification turn (frontend should not block input)
	CanSteer                   bool             `json:"can_steer,omitempty"`                     // True when a live foreground agent can accept steer injection
	RuntimeState               *RuntimeSnapshot `json:"runtime_state,omitempty"`
	OldestSequence             int64            `json:"oldest_sequence,omitempty"`
	LatestSequence             int64            `json:"latest_sequence,omitempty"`
	// PendingMessages are the viewer's queued messages the CLI has not taken
	// yet (no durable row exists until it does). Restore reads only.
	PendingMessages []PendingQueuedMessage `json:"pending_messages,omitempty"`
}

// --- POLLING API HANDLERS ---
// Observer registration/status/removal handlers removed - no longer needed

// handleGetEvents handles event polling for a session (new session-based API)
// Supports both forward polling (since parameter) and backward pagination (limit/offset)
// Also supports event mode filtering (event_mode parameter: "advanced", "tiny", or "micro")
func (api *StreamingAPI) handleGetSessionEvents(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Extract session ID from URL
	vars := mux.Vars(r)
	sessionID := vars["session_id"]

	if sessionID == "" {
		http.Error(w, "Session ID is required", http.StatusBadRequest)
		return
	}

	// Parse query parameters
	sinceStr := r.URL.Query().Get("since")
	limitStr := r.URL.Query().Get("limit")
	offsetStr := r.URL.Query().Get("offset")
	cursorOnly := r.URL.Query().Get("cursor_only") == "1"
	durableChat := r.URL.Query().Get("durable_chat") == "1"
	beforeSequenceStr := r.URL.Query().Get("before_sequence")
	workspacePath := strings.TrimSpace(r.URL.Query().Get("workspace_path"))

	// Authorize before touching either the live window or durable journal.
	currentUserID := GetUserIDFromContext(r.Context())
	activeSession, existsInActive := api.getActiveSession(sessionID)
	if existsInActive && activeSession.UserID != "" && activeSession.UserID != currentUserID {
		http.Error(w, "Session not found or access denied", http.StatusNotFound)
		return
	}
	if durableChat {
		if status, message := api.authorizeDurableChatRead(r, sessionID, workspacePath); status != 0 {
			http.Error(w, message, status)
			return
		}
		if !existsInActive {
			if err := api.eventStore.SetSessionPersistenceClass(sessionID, events.SessionPersistenceInteractiveChat); err != nil {
				http.Error(w, "Session is not an interactive chat", http.StatusConflict)
				return
			}
		}
	}

	// Build options for GetEvents
	opts := events.GetEventsOptions{
		SinceIndex: -1, // Default: not using sinceIndex
		Limit:      0,  // Default: no limit
		Offset:     0,  // Default: no offset
	}

	// Determine mode: forward polling (since) or backward pagination (limit/offset)
	if cursorOnly {
		// Cursor-only restores pair runtime state with durable chat history. They
		// must not serialize the retained raw event window merely to discover the
		// SSE resume position.
	} else if sinceStr != "" {
		// Forward polling mode
		sinceIndex, err := strconv.Atoi(sinceStr)
		if err != nil {
			http.Error(w, "since parameter must be a valid integer", http.StatusBadRequest)
			return
		}
		opts.SinceIndex = sinceIndex
	} else if limitStr != "" || offsetStr != "" {
		// Backward pagination mode
		if limitStr != "" {
			limit, err := strconv.Atoi(limitStr)
			if err != nil || limit <= 0 {
				http.Error(w, "limit parameter must be a positive integer", http.StatusBadRequest)
				return
			}
			opts.Limit = limit
		} else {
			opts.Limit = 50 // Default limit for pagination
		}

		if offsetStr != "" {
			offset, err := strconv.Atoi(offsetStr)
			if err != nil || offset < 0 {
				http.Error(w, "offset parameter must be a non-negative integer", http.StatusBadRequest)
				return
			}
			opts.Offset = offset
		}
	} else {
		// Neither since nor limit/offset specified - require at least one
		http.Error(w, "either 'since' parameter (for polling) or 'limit' parameter (for pagination) is required", http.StatusBadRequest)
		return
	}

	var sessionEvents []events.Event
	var exists bool
	var lastProcessedIndex int
	var hasMoreFromStore bool
	if durableChat {
		pageOpts := events.DurableEventPageOptions{Limit: 300}
		if cursorOnly {
			pageOpts.Limit = 1
		} else if sinceStr != "" {
			afterSequence, err := strconv.ParseInt(sinceStr, 10, 64)
			if err != nil || afterSequence < 0 {
				http.Error(w, "since parameter must be a non-negative sequence", http.StatusBadRequest)
				return
			}
			pageOpts.Limit = events.MaxPollingLimit
			pageOpts.AfterSequence = afterSequence
			pageOpts.FromStart = afterSequence == 0
		} else {
			if limitStr != "" {
				limit, err := strconv.Atoi(limitStr)
				if err != nil || limit <= 0 {
					http.Error(w, "limit parameter must be a positive integer", http.StatusBadRequest)
					return
				}
				pageOpts.Limit = limit
			}
			if beforeSequenceStr != "" {
				beforeSequence, err := strconv.ParseInt(beforeSequenceStr, 10, 64)
				if err != nil || beforeSequence <= 0 {
					http.Error(w, "before_sequence must be a positive sequence", http.StatusBadRequest)
					return
				}
				pageOpts.BeforeSequence = beforeSequence
			}
		}
		page, err := api.eventStore.ReadDurableChatPage(sessionID, pageOpts)
		if err != nil {
			http.Error(w, "Failed to read durable chat events", http.StatusInternalServerError)
			return
		}
		sessionEvents = page.Events
		exists = page.Exists || existsInActive
		lastProcessedIndex = int(page.LatestSequence)
		if len(page.Events) == 0 && (pageOpts.AfterSequence > 0 || pageOpts.FromStart) {
			// An empty forward page is "caught up", not "rewind to zero": since=0
			// reads from the first row, so a zero cursor would re-walk history.
			lastProcessedIndex = int(durableForwardCursor(pageOpts.AfterSequence, page.JournalLatestSequence))
		}
		if cursorOnly && page.Exists {
			// Tail page limit=1 returns the durable tip.
			lastProcessedIndex = int(page.LatestSequence)
			sessionEvents = []events.Event{}
		}
		if pageOpts.AfterSequence > 0 || pageOpts.FromStart {
			hasMoreFromStore = page.HasNewer
		} else {
			hasMoreFromStore = page.HasOlder
		}
	} else if cursorOnly {
		lastProcessedIndex, exists = api.eventStore.GetLatestEventIndex(sessionID)
		sessionEvents = []events.Event{}
	} else {
		getEventsResult := api.eventStore.GetEvents(sessionID, opts)
		sessionEvents = getEventsResult.Events
		if r.URL.Query().Get("working_set") == "session" {
			sessionEvents = events.FilterSessionWorkingSet(sessionID, sessionEvents)
		}
		exists = getEventsResult.Exists
		lastProcessedIndex = getEventsResult.LastProcessedIndex
		hasMoreFromStore = getEventsResult.HasMore
	}

	// Session status comes from the in-memory active sessions map.
	var sessionStatus string
	if existsInActive {
		sessionStatus = activeSession.Status
	}
	hasRunningBackgroundAgents := api.bgAgentRegistry != nil && api.bgAgentRegistry.HasRunningAgents(sessionID)
	if api.shouldCompleteIdleForegroundSession(sessionID, sessionStatus, hasRunningBackgroundAgents) {
		api.setSessionBusy(sessionID, false)
		api.updateSessionStatus(sessionID, "completed")
		sessionStatus = "completed"
	}
	runtimeState, _ := api.authoritativeRuntimeSnapshot(sessionID)
	runtimeStatus := sessionDisplayStatusFromRuntime(runtimeState)
	canSteer := runtimeStatus.CanSteer
	hasRunningBackgroundAgents = runtimeStatus.HasRunningBackgroundAgents

	// If the session doesn't exist in the in-memory event store, return an
	// empty events payload. This happens when polling starts before events
	// are generated, or after the process has restarted and dropped state.
	if !exists {
		response := GetEventsResponse{
			Events:                     []events.Event{},
			HasMore:                    false,
			SessionID:                  sessionID,
			SessionStatus:              sessionStatus,
			DisplayStatus:              runtimeStatus.Status,
			LastProcessedIndex:         -1,
			HasRunningBackgroundAgents: hasRunningBackgroundAgents,
			CanSteer:                   canSteer,
			RuntimeState:               &runtimeState,
		}
		if err := json.NewEncoder(w).Encode(response); err != nil {
			http.Error(w, fmt.Sprintf("Failed to encode response: %v", err), http.StatusInternalServerError)
			return
		}
		return
	}

	for i, event := range sessionEvents {
		if api.logger != nil {
			api.logger.Debug(fmt.Sprintf("  [%d] %s", i, event.Type))
		}
	}

	// The event store calculates this against the filtered pagination source.
	// Do not infer it from page length: an exact final page would otherwise
	// advertise a nonexistent next page forever.
	hasMore := hasMoreFromStore

	response := GetEventsResponse{
		Events:                     sessionEvents,
		HasMore:                    hasMore,
		SessionID:                  sessionID,
		SessionStatus:              sessionStatus,
		DisplayStatus:              runtimeStatus.Status,
		LastProcessedIndex:         lastProcessedIndex,
		HasRunningBackgroundAgents: hasRunningBackgroundAgents,
		CanSteer:                   canSteer,
		RuntimeState:               &runtimeState,
		OldestSequence:             firstEventSequence(sessionEvents),
		LatestSequence:             lastEventSequence(sessionEvents, int64(lastProcessedIndex)),
	}
	if durableChat && !cursorOnly && sinceStr == "" && beforeSequenceStr == "" {
		response.PendingMessages = api.pendingQueuedMessages(r.Context(), currentUserID, sessionID)
	}

	if err := json.NewEncoder(w).Encode(response); err != nil {
		http.Error(w, fmt.Sprintf("Failed to encode response: %v", err), http.StatusInternalServerError)
		return
	}
}

// durableForwardCursor clamps a caller's cursor to the journal tip so a cursor
// that ran ahead of the journal (pre-cutover volatile index) rewinds instead of
// skipping rows appended below it.
func durableForwardCursor(requested, journalLatest int64) int64 {
	if requested > journalLatest {
		return journalLatest
	}
	return requested
}

func firstEventSequence(events []events.Event) int64 {
	if len(events) == 0 {
		return 0
	}
	return events[0].Sequence
}

func lastEventSequence(events []events.Event, fallback int64) int64 {
	if len(events) == 0 {
		return fallback
	}
	return events[len(events)-1].Sequence
}

// --- In-Memory Session/Agent State Management ---
//
// StreamingAPI maintains a map of sessionID -> *LLMAgentWrapper, allowing each frontend session
// (identified by X-Session-ID header, cookie, or fallback to queryID) to have its own persistent agent instance.
// This enables the frontend to interrupt (stop) and resume conversations with the same agent, preserving
// conversation state in memory for the session's lifetime. No external database or disk persistence is used.
//
// - All /api/query and /api/stream/{query_id} requests use the same agent instance for a given sessionID.
// - The /api/session/stop endpoint (POST) allows explicit interruption/clearing of a session's agent state.
// - When a session is stopped, its agent is removed from memory and a new one will be created on the next request.
// - If the server process is restarted, all in-memory session state is lost (by design).
//
// This design provides efficient, scalable, and stateless (from a persistence perspective) session management
// for interactive, interruptible agent conversations in the frontend.

// --- ACTIVE SESSION API ENDPOINTS ---

// GetActiveSessionsResponse represents the response for getting active sessions
type GetActiveSessionsResponse struct {
	ActiveSessions []*ActiveSessionInfo `json:"active_sessions"`
	Total          int                  `json:"total"`
}

// ReconnectSessionResponse represents the response for reconnecting to a session
type ReconnectSessionResponse struct {
	SessionID string `json:"session_id"`
	Status    string `json:"status"`
	AgentMode string `json:"agent_mode"`
	Message   string `json:"message"`
}

// handleGetActiveSessions handles requests to get all active sessions
// Returns running sessions and recently completed sessions (within 30 minutes)
// This allows the frontend to restore sessions on page refresh
// Also queries database for recent sessions if in-memory map is empty (e.g., after backend restart)
func (api *StreamingAPI) handleGetActiveSessions(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	activeSessions := api.collectActiveSessions(r.Context())

	response := GetActiveSessionsResponse{
		ActiveSessions: activeSessions,
		Total:          len(activeSessions),
	}

	if err := json.NewEncoder(w).Encode(response); err != nil {
		http.Error(w, fmt.Sprintf("Failed to encode response: %v", err), http.StatusInternalServerError)
		return
	}
}

// Bot route principals own their runtime independently from browser accounts.
// Expose workflow bot activity only to viewers with access to that workflow;
// this does not grant permission to resume, stop, or mutate another session.
func monitorSessionVisibleTo(ctx context.Context, session *ActiveSessionInfo) bool {
	if session == nil {
		return false
	}
	claims := GetUserFromContext(ctx)
	if sessionVisibleTo(session.UserID, claims) {
		return true
	}
	if !isWorkflowBotHistory(ChatHistorySession{SessionID: session.SessionID, UserID: session.UserID, BotPlatform: session.BotPlatform}) ||
		!strings.HasPrefix(strings.TrimSpace(session.WorkspacePath), "Workflow/") {
		return false
	}
	access, manifest := workflowAccessForWorkspacePath(ctx, claims, session.WorkspacePath)
	return manifest != nil && manifest.ID == session.PresetQueryID && access != WorkflowAccessNone && userAllowedWorkflowID(claims, manifest.ID)
}

// collectActiveSessions returns the user-scoped active session list, including
// synthesized entries for tracked workflow executions that outlived their chat
// row. Shared by handleGetActiveSessions and handleGetHeaderSummary so both
// endpoints stay in sync without the frontend needing to call both.
func (api *StreamingAPI) collectActiveSessions(ctx context.Context) []*ActiveSessionInfo {
	// Get current user ID for session isolation
	currentUserID := GetUserIDFromContext(ctx)

	// getAllActiveSessions returns running + recently completed sessions from in-memory map
	allActiveSessions := api.getAllActiveSessions()

	// Filter sessions by user ID for isolation
	activeSessions := make([]*ActiveSessionInfo, 0, len(allActiveSessions))
	seenSessionIDs := make(map[string]struct{}, len(allActiveSessions))
	for _, session := range allActiveSessions {
		// Include session if it belongs to this user (or if UserID is empty for backwards compat)
		if monitorSessionVisibleTo(ctx, session) {
			activeSessions = append(activeSessions, api.buildActiveSessionInfoSummary(session))
			seenSessionIDs[session.SessionID] = struct{}{}
		}
	}

	// The active-session response is the complete frontend runtime index. A
	// tracked workflow can outlive its chat row after restart, so synthesize the
	// missing metadata here instead of making every UI surface merge three APIs.
	for _, workflow := range api.listRunningWorkflowExecutions(currentUserID) {
		if _, exists := seenSessionIDs[workflow.SessionID]; exists {
			continue
		}
		label := workflow.PresetName
		if label == "" {
			label = workflow.Title
		}
		if label == "" {
			label = workflowNameFromWorkspacePath(workflow.WorkspacePath)
		}
		synthetic := &ActiveSessionInfo{
			SessionID: workflow.SessionID, AgentMode: "workflow", Status: workflow.Status,
			CreatedAt: workflow.StartedAt, LastActivity: workflow.StartedAt,
			Query: workflow.Query, Title: workflow.Title, WorkspacePath: workflow.WorkspacePath,
			PresetName: workflow.PresetName, PresetQueryID: workflow.PresetQueryID,
			WorkflowName: label, WorkflowLabel: label, TriggeredBy: workflow.TriggeredBy,
			NeedsUserInput: workflow.NeedsUserInput, WaitingMessage: workflow.WaitingMessage,
			WaitingSince: workflow.WaitingSince, PhaseID: workflow.PhaseID, PhaseName: workflow.PhaseName,
		}
		activeSessions = append(activeSessions, api.buildActiveSessionInfoSummary(synthetic))
		seenSessionIDs[workflow.SessionID] = struct{}{}
	}

	return activeSessions
}

func (api *StreamingAPI) buildActiveSessionInfoSummary(session *ActiveSessionInfo) *ActiveSessionInfo {
	if session == nil {
		return nil
	}

	enriched := *session
	enriched.HasRetainedTmuxSession = api.sessionHasRetainedCodingTmux(session.SessionID)

	if api.bgAgentRegistry != nil {
		var newestRunning time.Time
		now := time.Now()
		for _, agent := range api.bgAgentRegistry.GetAll(session.SessionID) {
			snap := agent.GetSnapshot()
			if !backgroundAgentCountsAsLiveActivity(snap, now) {
				continue
			}
			if snap.Status == BGAgentRunning {
				enriched.RunningBackgroundAgentCount++
			}
			enriched.HasRunningBackgroundAgents = true
			if enriched.WorkspacePath == "" && snap.Metadata != nil {
				if workflowPath := strings.TrimSpace(snap.Metadata["workflow_path"]); workflowPath != "" {
					enriched.WorkspacePath = workflowPath
					workflowName := workflowNameFromWorkspacePath(workflowPath)
					enriched.WorkflowName = workflowName
					enriched.WorkflowLabel = workflowName
				}
				if presetQueryID := strings.TrimSpace(snap.Metadata["preset_query_id"]); presetQueryID != "" {
					enriched.PresetQueryID = presetQueryID
				}
			}
			if snap.CreatedAt.After(newestRunning) {
				newestRunning = snap.CreatedAt
				enriched.CurrentExecutionName = snap.Name
			}
		}
	}

	api.trackedWorkflowExecutionsMux.RLock()
	if exec := api.runningWorkflowExecutionBySessionLocked(session.SessionID); exec != nil {
		active := trackedExecutionToActive(exec)
		// Enrichment must not clobber identity the session already resolved.
		// A tracked execution is a narrower record than the session: a
		// workflow-builder/background execution can legitimately carry no
		// workspace path or preset, and overwriting unconditionally erased
		// both from a running scheduled session (PLAT-131). The frontend
		// resolves a session's workflow by preset_query_id or workspace_path,
		// so erasing them made a live pill unable to switch workflows at all —
		// it opened a tab under whichever workflow happened to be on screen.
		// TriggeredBy below was already guarded this way; these three were not.
		if active.PresetQueryID != "" {
			enriched.PresetQueryID = active.PresetQueryID
		}
		if active.PresetName != "" {
			enriched.PresetName = active.PresetName
		}
		if active.WorkspacePath != "" {
			enriched.WorkspacePath = active.WorkspacePath
		}
		if active.TriggeredBy != "" {
			enriched.TriggeredBy = active.TriggeredBy
		}
		if active.PresetName != "" {
			enriched.WorkflowName = active.PresetName
			enriched.WorkflowLabel = active.PresetName
		} else if active.WorkspacePath != "" {
			workspaceName := workflowNameFromWorkspacePath(active.WorkspacePath)
			enriched.WorkflowName = workspaceName
			enriched.WorkflowLabel = workspaceName
		}
		switch {
		case active.CurrentStepTitle != "":
			enriched.CurrentExecutionName = active.CurrentStepTitle
		case active.PhaseName != "":
			enriched.CurrentExecutionName = active.PhaseName
		case active.PresetName != "":
			enriched.CurrentExecutionName = active.PresetName
		case active.Title != "":
			enriched.CurrentExecutionName = active.Title
		}
		if active.Status != "" {
			enriched.Status = active.Status
		}
	} else if exec := api.runningTrackedExecutionBySessionLocked(session.SessionID); exec != nil {
		active := trackedExecutionToActive(exec)
		// Enrichment must not clobber identity the session already resolved.
		// A tracked execution is a narrower record than the session: a
		// workflow-builder/background execution can legitimately carry no
		// workspace path or preset, and overwriting unconditionally erased
		// both from a running scheduled session (PLAT-131). The frontend
		// resolves a session's workflow by preset_query_id or workspace_path,
		// so erasing them made a live pill unable to switch workflows at all —
		// it opened a tab under whichever workflow happened to be on screen.
		// TriggeredBy below was already guarded this way; these three were not.
		if active.PresetQueryID != "" {
			enriched.PresetQueryID = active.PresetQueryID
		}
		if active.PresetName != "" {
			enriched.PresetName = active.PresetName
		}
		if active.WorkspacePath != "" {
			enriched.WorkspacePath = active.WorkspacePath
		}
		if active.TriggeredBy != "" {
			enriched.TriggeredBy = active.TriggeredBy
		}
		if active.PresetName != "" {
			enriched.WorkflowName = active.PresetName
			enriched.WorkflowLabel = active.PresetName
		} else if active.WorkspacePath != "" {
			workflowName := workflowNameFromWorkspacePath(active.WorkspacePath)
			enriched.WorkflowName = workflowName
			enriched.WorkflowLabel = workflowName
		}
		// Workshop background executions should make the session display as
		// background-busy via HasRunningBackgroundAgents, but they must not turn
		// a completed foreground chat turn back into status=running. The frontend
		// uses Status to decide whether the current chat bubble is still generating.
		if active.Status != "" && exec.Source != trackedExecutionSourceWorkshopBackground {
			enriched.Status = active.Status
		}
	}
	api.trackedWorkflowExecutionsMux.RUnlock()

	enriched.NeedsUserInput, enriched.WaitingEventType, enriched.WaitingSince, enriched.WaitingMessage =
		api.deriveSessionUserInputState(session.SessionID)
	if api.shouldCompleteIdleForegroundSession(session.SessionID, enriched.Status, enriched.HasRunningBackgroundAgents) {
		enriched.Status = "completed"
	}
	if snapshot, ok := api.authoritativeRuntimeSnapshot(session.SessionID); ok {
		enriched.RuntimeState = &snapshot
		runtimeStatus := sessionDisplayStatusFromRuntime(snapshot)
		enriched.DisplayStatus = runtimeStatus.Status
		enriched.CanSteer = runtimeStatus.CanSteer
		enriched.HasRunningBackgroundAgents = runtimeStatus.HasRunningBackgroundAgents
	}
	return &enriched
}

func workflowNameFromWorkspacePath(workspacePath string) string {
	trimmed := strings.Trim(strings.TrimSpace(workspacePath), "/")
	if trimmed == "" {
		return ""
	}
	parts := strings.Split(trimmed, "/")
	return parts[len(parts)-1]
}

// workflowDisplayLabel returns the name a session should present to the user
// for its workflow — the configured workflow.json label when one is known,
// the raw workspace-folder name otherwise. A workflow's folder name (its
// stable identity, e.g. "social-media") and its display label (e.g.
// "twitter-automation") can differ; using the folder name as the label made
// the same running session look like a different workflow wherever the UI
// reads the label instead of the folder name (the Global Activity Monitor
// pill, in particular).
func workflowDisplayLabel(workspacePath string, manifest *WorkflowManifest) string {
	if manifest != nil {
		if label := strings.TrimSpace(manifest.Label); label != "" {
			return label
		}
	}
	return workflowNameFromWorkspacePath(workspacePath)
}

func (api *StreamingAPI) deriveSessionUserInputState(sessionID string) (bool, string, *time.Time, string) {
	if api == nil || api.eventStore == nil || sessionID == "" {
		return false, "", nil, ""
	}

	events := api.eventStore.GetAllEventsRaw(sessionID)
	for i, scanned := len(events)-1, 0; i >= 0 && scanned < 300; i, scanned = i-1, scanned+1 {
		event := events[i]
		switch event.Type {
		case "workflow_error", "conversation_end", "context_cancelled":
			return false, "", nil, ""
		case "request_human_feedback", "blocking_human_feedback", "plan_approval":
			waitingSince := event.Timestamp
			return true, event.Type, &waitingSince, summarizeWaitingEvent(event)
		}
	}

	return false, "", nil, ""
}

func summarizeWaitingEvent(event events.Event) string {
	data := map[string]interface{}{}
	if event.Data != nil && event.Data.Data != nil {
		if raw, err := json.Marshal(event.Data.Data); err == nil {
			_ = json.Unmarshal(raw, &data)
		}
	}

	for _, key := range []string{"question", "message", "prompt", "title", "objective", "action_description", "context", "reason"} {
		if value, ok := data[key].(string); ok && value != "" {
			if len(value) > 160 {
				return value[:157] + "..."
			}
			return value
		}
	}

	return ""
}

// handleReconnectSession handles requests to reconnect to an active session
func (api *StreamingAPI) handleReconnectSession(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Extract session ID from URL
	vars := mux.Vars(r)
	sessionID := vars["session_id"]

	if sessionID == "" {
		http.Error(w, "Session ID is required", http.StatusBadRequest)
		return
	}

	// Check if session is active, and the caller's to see (PLAT-362 D5).
	activeSession, exists := api.getActiveSession(sessionID)
	if !exists || activeSession.Status != "running" || !api.canAccessTerminalSession(r, sessionID) {
		http.Error(w, "Session not active or not found", http.StatusNotFound)
		return
	}

	// No observer needed - just return session info
	response := ReconnectSessionResponse{
		SessionID: sessionID,
		Status:    "reconnected",
		AgentMode: activeSession.AgentMode,
		Message:   "Successfully reconnected to active session",
	}

	if err := json.NewEncoder(w).Encode(response); err != nil {
		http.Error(w, fmt.Sprintf("Failed to encode response: %v", err), http.StatusInternalServerError)
		return
	}
}

// handleGetSessionStatus handles requests to get the status of a specific session
func (api *StreamingAPI) handleGetSessionStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Extract session ID from URL
	vars := mux.Vars(r)
	sessionID := vars["session_id"]

	if sessionID == "" {
		http.Error(w, "Session ID is required", http.StatusBadRequest)
		return
	}

	// Get current user ID for session isolation
	currentUserID := GetUserIDFromContext(r.Context())

	activeSession, exists := api.getActiveSession(sessionID)
	if !exists {
		http.Error(w, "Session not found", http.StatusNotFound)
		return
	}
	if activeSession.UserID != "" && activeSession.UserID != currentUserID {
		http.Error(w, "Session not found", http.StatusNotFound)
		return
	}

	// Return active session info
	status := activeSession.Status
	hasRunningBackgroundAgents := api.bgAgentRegistry != nil && api.bgAgentRegistry.HasRunningAgents(sessionID)
	hasRetainedTmuxSession := api.sessionHasRetainedCodingTmux(sessionID)
	if api.shouldCompleteIdleForegroundSession(sessionID, status, hasRunningBackgroundAgents) {
		api.setSessionBusy(sessionID, false)
		api.updateSessionStatus(sessionID, "completed")
		status = "completed"
	}
	runtimeState, _ := api.authoritativeRuntimeSnapshot(sessionID)
	runtimeStatus := sessionDisplayStatusFromRuntime(runtimeState)
	canSteer := runtimeStatus.CanSteer
	response := map[string]interface{}{
		"session_id":                activeSession.SessionID,
		"status":                    status,
		"display_status":            runtimeStatus.Status,
		"agent_mode":                activeSession.AgentMode,
		"created_at":                activeSession.CreatedAt,
		"last_activity":             activeSession.LastActivity,
		"query":                     activeSession.Query,
		"can_steer":                 canSteer,
		"has_retained_tmux_session": hasRetainedTmuxSession,
		"runtime_state":             runtimeState,
	}

	if err := json.NewEncoder(w).Encode(response); err != nil {
		http.Error(w, fmt.Sprintf("Failed to encode response: %v", err), http.StatusInternalServerError)
		return
	}
}
