// Session lifecycle HTTP handlers: cancel current turn, stop session, clear
// session, plus the coding-CLI tmux close helpers and browser-session
// listing/cleanup they use. Relocated verbatim from server.go.
package server

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/browser"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"

	llmproviders "github.com/manishiitg/multi-llm-provider-go"

	mcpagent "github.com/manishiitg/mcpagent/agent"
)

var closeAllCodingCLISessionsForRuntimeCancel = closeAllCodingCLIInteractiveSessionsForOwner
var closeCodingAgentTmuxForRuntimeCancel = closeCodingAgentTmuxSessionByName
var deliverMuseTurnInterrupt = func(ctx context.Context, agent *mcpagent.Agent, sessionID string) error {
	_, err := mcpagent.DeliverAgentControlKey(ctx, agent, mcpagent.ControlKeyDeliveryRequest{SessionID: sessionID, Key: "Escape"})
	return err
}

// A conversational Stop is a CLI interrupt, not a terminal teardown. Deliver
// Escape before canceling the Go turn: after cancellation the provider's tmux
// command would inherit a dead context and never reach the running Muse TUI.
func (api *StreamingAPI) interruptInteractiveMuseTurn(sessionID string) {
	api.activeSessionsMux.RLock()
	session := api.activeSessions[sessionID]
	interactive := session != nil && !isScheduledSessionIdentity(sessionID, session.TriggeredBy) && strings.TrimSpace(session.BotPlatform) == ""
	api.activeSessionsMux.RUnlock()
	if !interactive {
		return
	}
	api.runningAgentsMux.RLock()
	agent := api.runningAgents[sessionID]
	api.runningAgentsMux.RUnlock()
	if agent == nil || !strings.EqualFold(string(mcpagent.ReadAgentRuntimeInfo(agent).Provider), "muse-cli") || !mcpagent.AgentSupportsSteering(agent) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := deliverMuseTurnInterrupt(ctx, agent, sessionID); err != nil {
		log.Printf("[SESSION DEBUG] Could not interrupt Muse turn in place for session %s: %v", sessionID, err)
	}
}

// handleCancelCurrentTurn cancels only the currently running LLM turn for a session.
// It must not mark the session stopped or tear down workshop/background state.
func (api *StreamingAPI) handleCancelCurrentTurn(w http.ResponseWriter, r *http.Request) {
	sessionID := r.Header.Get("X-Session-ID")
	if sessionID == "" {
		http.Error(w, "Session ID required", http.StatusBadRequest)
		return
	}

	if err := api.verifySessionAccess(r, sessionID); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	// Preserve the user's intent even if the foreground cancel function has just
	// completed and disappeared. A scheduled sequencer may be between turns at
	// that exact moment; its waiter must still abort before sending the next one.
	if isScheduledSession(sessionID) {
		api.markSessionTurnInterrupted(sessionID)
	}

	api.agentCancelMux.Lock()
	_, hasTurn := api.agentCancelFuncs[sessionID]
	api.agentCancelMux.Unlock()
	if hasTurn {
		api.interruptInteractiveMuseTurn(sessionID)
	}
	api.agentCancelMux.Lock()
	cancelFunc, exists := api.agentCancelFuncs[sessionID]
	if exists {
		cancelFunc()
		delete(api.agentCancelFuncs, sessionID)
		log.Printf("[SESSION DEBUG] Canceled current LLM turn for session %s", sessionID)
	}
	api.agentCancelMux.Unlock()

	if !exists {
		log.Printf("[SESSION DEBUG] No active LLM turn to cancel for session %s", sessionID)
	}

	w.WriteHeader(http.StatusNoContent)
}

// cancelSessionRuntimeWork stops all runtime work owned by a session. The
// stopped guard prevents in-flight goroutines from spawning more work after
// cancellation; callers remain responsible for recording whether the terminal
// lifecycle is stopped, failed, or another final state.
func (api *StreamingAPI) cancelSessionRuntimeWork(sessionID, closeReason string, terminalPhase RuntimePhase) {
	if api == nil || strings.TrimSpace(sessionID) == "" {
		return
	}
	log.Printf("[STOP] session=%s begin runtime cancel (reason=%q phase=%s)", sessionID, closeReason, terminalPhase)
	api.markSessionStoppedAs(sessionID, terminalPhase, closeReason)

	api.agentCancelMux.Lock()
	turnCanceled := false
	if cancelFunc, exists := api.agentCancelFuncs[sessionID]; exists {
		cancelFunc()
		delete(api.agentCancelFuncs, sessionID)
		turnCanceled = true
	}
	api.agentCancelMux.Unlock()
	log.Printf("[STOP] session=%s active turn context canceled=%v", sessionID, turnCanceled)

	api.sessionQueryIDMux.Lock()
	queryIDs := api.sessionQueryIDs[sessionID]
	delete(api.sessionQueryIDs, sessionID)
	api.sessionQueryIDMux.Unlock()

	orchestratorCanceled := 0
	if len(queryIDs) > 0 {
		api.workflowOrchestratorContextMux.Lock()
		for _, queryID := range queryIDs {
			if cancelFunc, exists := api.workflowOrchestratorContexts[queryID]; exists {
				cancelFunc()
				delete(api.workflowOrchestratorContexts, queryID)
				orchestratorCanceled++
			}
		}
		api.workflowOrchestratorContextMux.Unlock()
	}
	// This is the context the workflow step loop and the message_sequence queue
	// actually run under, so zero here with a live run means Stop reached
	// nothing that matters.
	log.Printf("[STOP] session=%s orchestrator contexts: queries=%d canceled=%d", sessionID, len(queryIDs), orchestratorCanceled)

	api.cancelBackgroundAgents(sessionID)
	api.cancelTrackedExecutionsForSession(sessionID)
	api.setSyntheticTurn(sessionID, false)
	api.setSessionBusy(sessionID, false)

	api.pendingMu.Lock()
	delete(api.pendingCompletions, sessionID)
	delete(api.completionRetryScheduled, sessionID)
	api.pendingMu.Unlock()
	api.pendingStartMu.Lock()
	delete(api.pendingStartNotifications, sessionID)
	delete(api.startNotificationRetryScheduled, sessionID)
	api.pendingStartMu.Unlock()
	api.lastQueryMu.Lock()
	delete(api.lastQueryRequests, sessionID)
	api.lastQueryMu.Unlock()

	closeAllCodingCLISessionsForRuntimeCancel(sessionID, closeReason)
	// Stop closes the coding CLI, so nothing can finish the turn it was running: release every
	// marker that says the session is busy, or the next message queues behind a dead turn forever
	// (excellence, 2026-09-30: every message after a Stop stayed "queued_for_turn").
	api.releaseStoppedSessionTurnMarkers(sessionID)
	if api.terminalStore == nil {
		return
	}
	closedTmux := make(map[string]struct{})
	for _, snapshot := range api.terminalStore.ListRaw(sessionID) {
		tmuxSession := strings.TrimSpace(snapshot.TmuxSession)
		if tmuxSession == "" {
			continue
		}
		if _, seen := closedTmux[tmuxSession]; !seen {
			if !closeCodingAgentTmuxForRuntimeCancel(tmuxSession, closeReason) {
				log.Printf("[SESSION CANCEL] failed to close tmux %q (owner %s)", tmuxSession, strings.TrimSpace(snapshot.OwnerID))
				continue
			}
			closedTmux[tmuxSession] = struct{}{}
			if registry := api.ensureTerminalLeaseRegistry(); registry != nil {
				registry.MarkClosed(tmuxSession, closeReason, time.Now())
			}
		}
		if snapshot.Active {
			api.terminalStore.MarkFailed(snapshot.TerminalID)
		}
		api.terminalStore.MarkProcessClosed(snapshot.TerminalID, closeReason)
	}
}

// closeAllCodingCLIInteractiveSessionsForOwner tears down the persistent
// tmux-backed coding-CLI session registered under the given owner key, across
// every tmux provider. Each adapter's CloseXxxInteractiveSessionForOwner runs
// its own graceful-then-force shutdown (send an exit sequence, then tmux
// kill-session) and is a no-op when no session is registered for the
// owner, so calling all tmux providers is safe and provider-agnostic.
func closeAllCodingCLIInteractiveSessionsForOwner(owner, reason string) {
	llmproviders.CloseCursorCLIInteractiveSessionForOwner(owner, reason)
	llmproviders.CloseCodexCLIInteractiveSessionForOwner(owner, reason)
	llmproviders.CloseClaudeCodeInteractiveSessionForOwner(owner, reason)
	llmproviders.ClosePiCLIInteractiveSessionForOwner(owner, reason)
	llmproviders.CloseMuseCLIInteractiveSessionForOwner(owner, reason)
	llmproviders.CloseAgyCLIInteractiveSessionForOwner(owner, reason)
}

// gracefulCloseCodingCLITmuxByName runs the provider-specific graceful shutdown
// (e.g. claude: "C-u /exit C-m"; codex/cursor/gemini: C-c; muse: tmux
// kill-session -- it has no in-app exit keystroke) plus the adapter's
// file/MCP-lease cleanup for the tmux-backed
// coding CLI identified by its tmux session name. The provider is detected from
// the session-name prefix (set by each adapter's new<Provider>TmuxSessionName).
// This tears the session down by tmux name rather than owner key, so it works
// for workflow sub-agents that registered under a step-execution owner the
// caller can't reconstruct. Returns false when the prefix matches no known
// provider (caller should fall back to a raw kill-session).
func gracefulCloseCodingCLITmuxByName(tmuxName, reason string) bool {
	name := strings.TrimSpace(tmuxName)
	if name == "" {
		return false
	}
	switch {
	case strings.HasPrefix(name, "mlp-claude-code"):
		llmproviders.CloseClaudeCodeInteractiveSessionByTmux(name, reason)
	case strings.HasPrefix(name, "mlp-codex-cli"):
		llmproviders.CloseCodexCLIInteractiveSessionByTmux(name, reason)
	case strings.HasPrefix(name, "mlp-cursor-cli"):
		llmproviders.CloseCursorCLIInteractiveSessionByTmux(name, reason)
	case strings.HasPrefix(name, "mlp-pi-cli"):
		llmproviders.ClosePiCLIInteractiveSessionByTmux(name, reason)
	case strings.HasPrefix(name, "mlp-muse-"):
		llmproviders.CloseMuseCLIInteractiveSessionByTmux(name, reason)
	case strings.HasPrefix(name, "agy-int-"):
		llmproviders.CloseAgyCLIInteractiveSessionByTmux(name, reason)
	default:
		return false
	}
	return true
}

// Add endpoint to stop/clear a session
func (api *StreamingAPI) handleStopSession(w http.ResponseWriter, r *http.Request) {
	sessionID := r.Header.Get("X-Session-ID")
	if sessionID == "" {
		http.Error(w, "Session ID required", http.StatusBadRequest)
		return
	}

	if err := api.verifySessionAccess(r, sessionID); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	// An interactive chat can stop its current work without losing the request
	// template needed by /live-input to start the next user turn. Scheduled,
	// triggered, and bot sessions remain terminal even if a caller supplies this
	// option; their stop must not make an internal continuation resumable.
	preserveConversation := r.URL.Query().Get("preserveConversation") == "true"
	if preserveConversation {
		api.activeSessionsMux.RLock()
		session := api.activeSessions[sessionID]
		preserveConversation = session != nil &&
			!isScheduledSessionIdentity(sessionID, session.TriggeredBy) &&
			strings.TrimSpace(session.BotPlatform) == ""
		api.activeSessionsMux.RUnlock()
	}

	// Mark session as stopped FIRST, before any cancellation, so that in-flight
	// goroutines that race with this stop handler will see the flag and bail out
	// instead of re-creating workshop sessions or spawning new CLI processes.
	// See stoppedSessions field comment for the full race condition description.
	api.markSessionStopped(sessionID)
	if isScheduledSession(sessionID) {
		api.markSessionTurnInterrupted(sessionID)
	}

	// Cancel agent execution context if it exists
	api.agentCancelMux.Lock()
	if cancelFunc, exists := api.agentCancelFuncs[sessionID]; exists {
		cancelFunc() // Cancel the agent execution
		delete(api.agentCancelFuncs, sessionID)
		log.Printf("[SESSION DEBUG] Canceled agent execution context for session %s", sessionID)
	}
	api.agentCancelMux.Unlock()

	// Update active session status to stopped
	api.updateSessionStatus(sessionID, "stopped")
	api.setSessionBusy(sessionID, false)
	api.setSyntheticTurn(sessionID, false)

	// Cancel background agents if explicitly requested (e.g. user pressed the stop button).
	// When called before sending a new message, cancelAgents is NOT set so agents survive
	// across turns and synthetic turns can still fire when they complete.
	if r.URL.Query().Get("cancelAgents") == "true" {
		api.cancelBackgroundAgents(sessionID)
		log.Printf("[SESSION DEBUG] Canceled all background agents for session %s", sessionID)
	}

	// Prevent stopped sessions from being revived by queued background completions or
	// synthetic auto-notification turns that reuse the stored agent after stop.
	api.pendingMu.Lock()
	delete(api.pendingCompletions, sessionID)
	delete(api.completionRetryScheduled, sessionID)
	api.pendingMu.Unlock()

	// Clear pending start notifications so they don't leak across stop/restart
	// (pending-start-notifications-leak fix).
	api.pendingStartMu.Lock()
	delete(api.pendingStartNotifications, sessionID)
	delete(api.startNotificationRetryScheduled, sessionID)
	api.pendingStartMu.Unlock()

	if !preserveConversation {
		api.lastQueryMu.Lock()
		delete(api.lastQueryRequests, sessionID)
		api.lastQueryMu.Unlock()

		api.sessionWorkspaceMu.Lock()
		delete(api.sessionWorkspaceFolders, sessionID)
		api.sessionWorkspaceMu.Unlock()
	}

	api.removeSessionAgent(sessionID)

	api.completionLoopStartedMu.Lock()
	delete(api.completionLoopStarted, sessionID)
	api.completionLoopStartedMu.Unlock()

	api.bgAgentRegistry.Cleanup(sessionID)
	log.Printf("[SESSION DEBUG] Cleared synthetic-turn state for stopped session %s", sessionID)

	// Close workshop chat sessions for this session — cancels all running step executions.
	// Workshop sessions use context.Background() so they survive agent context cancellation above;
	// we must explicitly call Close() to cancel their step goroutines.
	//
	// Close() → cancelFunc() cascades to all execCtx (step goroutines) → kills Codex CLI
	// processes via exec.CommandContext. It also calls CloseWorkshopGroupSessions() which
	// closes MCP connections for group sessions (session-group-*) and isolated sub-sessions.
	//
	// IMPORTANT: The markSessionStopped() call above prevents in-flight goroutines from
	// re-creating the workshop after we close it here. Without that guard, a racing
	// goroutine could call NewWorkshopChatSession() with a fresh context.Background(),
	// creating orphaned CLI processes that are never canceled. See stoppedSessions comment.
	//
	// Historically we keyed this map by sessionID / "eval-"+sessionID, but some workflow
	// execution paths can drift from those exact keys. So first try direct keys, then scan
	// for any workshop session whose owning mainSessionID matches this session.
	closedWorkshopKeys := map[string]struct{}{}
	workshopKeys := []string{sessionID, "eval-" + sessionID}
	for _, wsKey := range workshopKeys {
		if cached, ok := api.workshopChatSessions.Load(wsKey); ok {
			if ws, ok := cached.(interface{ Close() }); ok {
				ws.Close()
				log.Printf("[SESSION DEBUG] Closed workshop session %q (all step executions canceled)", wsKey)
			}
			api.workshopChatSessions.Delete(wsKey)
			closedWorkshopKeys[wsKey] = struct{}{}
		}
	}
	api.workshopChatSessions.Range(func(key, value interface{}) bool {
		wsKey, ok := key.(string)
		if !ok {
			return true
		}
		if _, alreadyClosed := closedWorkshopKeys[wsKey]; alreadyClosed {
			return true
		}
		ws, ok := value.(interface {
			Close()
			MainSessionID() string
		})
		if !ok || ws.MainSessionID() != sessionID {
			return true
		}
		ws.Close()
		api.workshopChatSessions.Delete(wsKey)
		log.Printf("[SESSION DEBUG] Closed workshop session %q via mainSessionID match for session %s", wsKey, sessionID)
		return true
	})

	// Cancel all workflow orchestrator contexts for this session
	// Since we now use queryID as the key, we need to look up all queryIDs for this session
	api.sessionQueryIDMux.Lock()
	queryIDs := api.sessionQueryIDs[sessionID]
	delete(api.sessionQueryIDs, sessionID) // Clear the mapping
	api.sessionQueryIDMux.Unlock()

	if len(queryIDs) > 0 {
		// Cancel all background agents BEFORE canceling workflow contexts.
		// When workflow contexts are canceled, sub-agent goroutines will eventually
		// fail with "context canceled" and call OnExecutionComplete. Without marking
		// them as canceled first, they'd fire stale AUTO-NOTIFICATION synthetic turns.
		api.cancelBackgroundAgents(sessionID)
		log.Printf("[SESSION DEBUG] Canceled all background agents for session %s (workflow stop)", sessionID)

		api.workflowOrchestratorContextMux.Lock()
		for _, qid := range queryIDs {
			if cancelFunc, exists := api.workflowOrchestratorContexts[qid]; exists {
				cancelFunc() // Cancel this workflow execution
				delete(api.workflowOrchestratorContexts, qid)
				log.Printf("[SESSION DEBUG] Canceled workflow execution %s for session %s", qid, sessionID)
			}
		}
		api.workflowOrchestratorContextMux.Unlock()

		// Remove from active executions registry
		api.activeWorkflowExecutionsMux.Lock()
		for _, qid := range queryIDs {
			delete(api.activeWorkflowExecutions, qid)
		}
		api.activeWorkflowExecutionsMux.Unlock()
		log.Printf("[SESSION DEBUG] Canceled %d workflow execution(s) for session %s", len(queryIDs), sessionID)
	}
	// Whether or not the session still had a query-ID mapping (a Builder chat whose
	// turn was lost does not), the tracker must stop listing it as running: a run
	// left "running" blocks every new Builder chat on the workflow (409
	// workflow_busy) and the workflow tab keeps re-adopting it as the live chat.
	api.cancelTrackedExecutionsForSession(sessionID)

	// Clear workflow objective
	api.workflowObjectiveMux.Lock()
	if _, exists := api.workflowObjectives[sessionID]; exists {
		delete(api.workflowObjectives, sessionID)
		log.Printf("[SESSION DEBUG] Cleared workflow objective for session %s", sessionID)
	}
	api.workflowObjectiveMux.Unlock()

	// Close all MCP sessions (browsers, etc.) associated with this HTTP session immediately.
	// This is safe to call even if the defers in the workflow goroutines haven't fired yet —
	// CloseSession is idempotent, so those defers will be no-ops when they eventually run.
	log.Printf("[SESSION DEBUG] Closing MCP sessions for stopped session %s", sessionID)
	mcpagent.CloseHTTPSession(sessionID)

	// Kill headless browser processes for this session
	api.cleanupBrowserSessions(sessionID)

	// Close any tmux-backed coding-CLI session for this chat. Without this,
	// canceling the Go context above tears down the streaming connection
	// server-side but the CLI process inside the tmux pane keeps running
	// its current turn (LLM calls, shell commands) until it finishes
	// naturally — the user pressed stop but codex/cursor/etc. ran for
	// another 30-60 seconds.
	//
	// Each adapter's CloseXxxInteractiveSessionForOwner implements that
	// CLI's graceful-then-force shutdown sequence (codex/cursor/gemini/
	// claude-code: adapter-specific exit → tmux kill-session). All are
	// no-ops when no session is registered for the owner, so calling all
	// four is safe and provider-agnostic.
	closeReason := "user pressed stop"
	closeAllCodingCLIInteractiveSessionsForOwner(sessionID, closeReason)
	log.Printf("[SESSION DEBUG] Closed any tmux-backed coding-CLI session for stopped session %s", sessionID)
	// The Stop button closes the coding CLI, so nothing can finish the turn it was running:
	// release every marker that says the conversation is busy, or the next message is refused
	// (409) or queued behind a dead turn forever (excellence, 2026-09-30: a Muse Code chat).
	// The runtime-cancel path has the same call; this is the one the Stop button takes.
	api.releaseStoppedSessionTurnMarkers(sessionID)

	// The calls above are keyed by the chat / main-agent session ID. Workflow-step
	// sub-agents, however, register their interactive CLI session under the STEP
	// execution-owner ID (e.g. "workflow-step:exec-...:step-name") — not this
	// chat sessionID — so the owner-keyed close above never matches them and
	// their tmux panes orphan: the CLI process keeps running (and keeps holding
	// the workflow's single-session slot, which blocks "new chat" and workflow
	// model changes) long after the user stopped the run. This is provider-wide.
	//
	// When the caller explicitly asked to cancel agents (cancelAgents=true — e.g.
	// the workflow "kill & start new chat" popup, which calls stopSession(sid,
	// true)), enumerate this session's live terminals and tear each sub-agent
	// down by its OWN owner key (graceful, provider-agnostic), plus a guaranteed
	// tmux kill-session backstop for active panes in case the adapter's registry
	// bookkeeping drifted from the terminal store's owner ID.
	if r.URL.Query().Get("cancelAgents") == "true" && api.terminalStore != nil {
		mainOwner := "main:" + sessionID
		for _, snap := range api.terminalStore.ListRaw(sessionID) {
			owner := strings.TrimSpace(snap.OwnerID)
			if owner == "" || owner == sessionID || owner == mainOwner {
				continue // chat / main-agent session already handled above
			}
			tmux := strings.TrimSpace(snap.TmuxSession)
			if tmux == "" {
				continue // non-tmux pane; context cancel above handles it
			}
			// Primary: run the provider's own graceful exit + cleanup, resolved
			// by tmux name so it works even though the sub-agent registered its
			// CLI session under a step-execution owner (not this chat sessionID).
			// The adapter kills the tmux session itself as the final step of that
			// sequence, so a separate kill-session is only needed when no adapter
			// claimed it (e.g. the registry was lost across a server restart).
			if handled := gracefulCloseCodingCLITmuxByName(tmux, closeReason); !handled {
				killCtx, killCancel := context.WithTimeout(context.Background(), terminalTmuxActionTimeout)
				if err := runTerminalTmuxCommand(killCtx, "", "kill-session", "-t", tmux); err != nil {
					log.Printf("[SESSION DEBUG] kill-session %q (owner %s) failed (may already be gone): %v", tmux, owner, err)
				}
				killCancel()
			}
			if snap.Active {
				// Only relabel panes that were still live; leave already completed
				// terminals in their terminal state.
				api.terminalStore.MarkFailed(snap.TerminalID)
			}
			if registry := api.ensureTerminalLeaseRegistry(); registry != nil {
				registry.MarkClosed(tmux, closeReason, time.Now())
			}
			api.terminalStore.MarkProcessClosed(snap.TerminalID, closeReason)
			log.Printf("[SESSION DEBUG] Tore down workflow sub-agent terminal owner=%s tmux=%s active=%v for stopped session %s", owner, tmux, snap.Active, sessionID)
		}
	}

	// Permission state is scoped to the lifetime of the chat session. Keeping it
	// after stop can leak stale workspace roots, secrets, or browser grants into a
	// later session that reuses the same ID.
	workspace.ClearSessionShellConfig(sessionID)

	// Note: Conversation history and orchestrator state are preserved to allow resuming the conversation
	// Use /api/session/clear if you want to clear conversation history

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Session stopped (conversation history and orchestrator state preserved)"))
}

func (api *StreamingAPI) cancelBackgroundAgents(sessionID string) {
	if api == nil || strings.TrimSpace(sessionID) == "" {
		return
	}
	if api.bgAgentRegistry != nil {
		canceled, missingCancel, alreadyDone := api.bgAgentRegistry.CancelAll(sessionID)
		log.Printf("[STOP] session=%s background agents: canceled=%d no_cancel_func=%d already_done=%d",
			sessionID, canceled, missingCancel, alreadyDone)
		if missingCancel > 0 {
			log.Printf("[STOP] session=%s WARNING %d background agent(s) had no cancel func — marked canceled but never told to stop (PLAT-130 shape)",
				sessionID, missingCancel)
		}
	}
}

// handleGetBrowserSessions returns the tracked browser sessions with their owning chat session IDs.
func (api *StreamingAPI) handleGetBrowserSessions(w http.ResponseWriter, r *http.Request) {
	tracker := browser.GetSessionTracker()
	sessions := tracker.ActiveSessions()
	cdpOwners := browser.ActiveCDPOwnersSnapshot()
	// Admins see every browser; everyone else sees only browsers of sessions
	// they may access, so the list does not hand out other users' session IDs
	// (PLAT-362 D5).
	if !userAccessForClaims(GetUserFromContext(r.Context())).Admin {
		visible := func(ids ...string) bool {
			for _, id := range ids {
				if strings.TrimSpace(id) != "" && api.canAccessTerminalSession(r, id) {
					return true
				}
			}
			return false
		}
		ownSessions := sessions[:0:0]
		ownBrowsers := map[string]bool{}
		for _, s := range sessions {
			if visible(s["workflow_session"], s["agent_session"]) {
				ownSessions = append(ownSessions, s)
				if name := strings.TrimSpace(s["browser_session"]); name != "" {
					ownBrowsers[name] = true
				}
			}
		}
		sessions = ownSessions
		// A CDP owner is a session ID or, for per-Crew and per-workflow
		// browsers, a browser name; it is the caller's when either is.
		ownOwners := cdpOwners[:0:0]
		for _, o := range cdpOwners {
			if ownBrowsers[strings.TrimSpace(o["owner"])] || visible(o["owner"]) {
				ownOwners = append(ownOwners, o)
			}
		}
		cdpOwners = ownOwners
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"sessions":   sessions,
		"count":      len(sessions),
		"cdp_owners": cdpOwners,
	})
}

// cleanupBrowserSessions closes all headless browser processes for a session.
// Must be called whenever a session ends (stop, clear, workflow completion).
func (api *StreamingAPI) cleanupBrowserSessions(sessionID string) {
	tracker := browser.GetSessionTracker()
	workspaceAPIURL := os.Getenv("WORKSPACE_API_URL")
	if workspaceAPIURL == "" {
		workspaceAPIURL = "http://127.0.0.1:8081"
	}
	client := browser.NewClient(workspaceAPIURL)
	// Give back this conversation's tab in a shared Crew/workflow browser even
	// when another conversation opened (and is tracked for) that browser.
	browser.ReleaseSessionTabOwner(sessionID, client)
	if tracker.CountForChat(sessionID) == 0 {
		return
	}
	tracker.CloseAllForChat(sessionID, client)
}

// Add endpoint to clear conversation history for a session
func (api *StreamingAPI) handleClearSession(w http.ResponseWriter, r *http.Request) {
	sessionID := r.Header.Get("X-Session-ID")
	if sessionID == "" {
		http.Error(w, "Session ID required", http.StatusBadRequest)
		return
	}

	// Verify session ownership via the in-memory active sessions map.
	if err := api.verifySessionAccess(r, sessionID); err != nil {
		http.Error(w, "Session not found or access denied", http.StatusNotFound)
		return
	}

	// Clearing a live session would remove its shell permission contract while
	// the agent can still issue tool calls. Stop the session first so cancellation
	// and tmux teardown complete before its guard is discarded.
	if api.sessionHasActiveWork(sessionID) {
		http.Error(w, "Session is still active; stop it before clearing conversation history", http.StatusConflict)
		return
	}

	if err := deleteSessionInstructions(GetUserIDFromContext(r.Context()), sessionID); err != nil {
		http.Error(w, "failed to clear prompt snapshot", http.StatusInternalServerError)
		return
	}

	// Clear conversation and coding-agent resume state guarded by the same
	// mutex used by query/resume paths.
	api.conversationMux.Lock()
	if _, exists := api.conversationHistory[sessionID]; exists {
		delete(api.conversationHistory, sessionID)
		log.Printf("[SESSION DEBUG] Cleared conversation history for session %s", sessionID)
	}
	delete(api.lastWorkshopModeBySession, sessionID)
	api.conversationMux.Unlock()

	// Clear orchestrator state (removed - now stateless)

	// Clear orchestrator instance (legacy removed)

	// Clear workflow objective
	api.workflowObjectiveMux.Lock()
	if _, exists := api.workflowObjectives[sessionID]; exists {
		delete(api.workflowObjectives, sessionID)
		log.Printf("[SESSION DEBUG] Cleared workflow objective for session %s", sessionID)
	}
	api.workflowObjectiveMux.Unlock()

	// Kill headless browser processes for this session
	api.cleanupBrowserSessions(sessionID)
	workspace.ClearSessionShellConfig(sessionID)

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Session cleared (conversation history and orchestrator state removed)"))
}

func (api *StreamingAPI) sessionHasActiveWork(sessionID string) bool {
	if api == nil || strings.TrimSpace(sessionID) == "" {
		return false
	}
	if api.hasActiveTurnCancel(sessionID) || api.sessionHasLiveCodingTmux(sessionID) ||
		api.hasRunningTrackedExecutionForSession(sessionID) || api.isSessionBusy(sessionID) ||
		api.isSyntheticTurn(sessionID) {
		return true
	}
	return api.bgAgentRegistry != nil && api.bgAgentRegistry.HasRunningAgents(sessionID)
}

// releaseStoppedSessionTurnMarkers clears what still marks a stopped session as running a turn:
// the retained coding-agent session's active turn (its CLI was just closed), the retained-main-turn
// record, and the durable queue entry of the turn that was running (claimed, never finished). Turns
// still waiting in the queue are kept: they run, in order, when the person sends their next message.
func (api *StreamingAPI) releaseStoppedSessionTurnMarkers(sessionID string) {
	if api == nil || strings.TrimSpace(sessionID) == "" {
		return
	}
	if retained, ok := mcpagent.LookupSession(sessionID); ok && retained != nil && retained.ActiveTurnID() != "" {
		if err := retained.Close(); err != nil {
			log.Printf("[STOP] session=%s could not close the retained session: %v", sessionID, err)
		} else {
			log.Printf("[STOP] session=%s released the retained session's active turn", sessionID)
		}
	}
	api.retainedMainTurnsMu.Lock()
	_, retainedTurn := api.retainedMainTurns[sessionID]
	delete(api.retainedMainTurns, sessionID)
	api.retainedMainTurnsMu.Unlock()
	if retainedTurn {
		log.Printf("[STOP] session=%s released the retained main turn record", sessionID)
	}
	if dropped := api.dropStartedConversationTurns(sessionID); dropped > 0 {
		log.Printf("[STOP] session=%s dropped %d claimed queue entr%s of the stopped turn", sessionID, dropped, map[bool]string{true: "y", false: "ies"}[dropped == 1])
	}
}

// runningServerAPI is the server's API, set when it starts. Code that has no API handle (the access
// and runtime-configuration changes below) uses it to release busy markers; nil (tests, CLI tools)
// means only the CLI is closed.
var runningServerAPI *StreamingAPI

// closeCodingCLIAndReleaseTurnMarkers closes a chat's coding CLI on purpose (its access or runtime
// configuration changed) and clears what marks the conversation busy. A turn that was running in
// that CLI can no longer finish, so leaving its markers made every later message queue behind it.
func closeCodingCLIAndReleaseTurnMarkers(sessionID, reason string) {
	closeAllCodingCLIInteractiveSessionsForOwner(sessionID, reason)
	if runningServerAPI != nil {
		runningServerAPI.releaseStoppedSessionTurnMarkers(sessionID)
	}
}
