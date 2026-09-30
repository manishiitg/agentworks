package server

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/terminalleases"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/terminals"
	mcpagent "github.com/manishiitg/mcpagent/agent"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/pathidentity"
)

const (
	defaultCodingAgentTmuxOrphanIdleTimeout = time.Hour
	envCodingAgentTmuxOrphanIdleSeconds     = "MCP_CODING_AGENT_TMUX_ORPHAN_IDLE_TIMEOUT_SECONDS"
)

type codingAgentTmuxCleanupCandidate struct {
	snapshot terminals.Snapshot
	lease    terminalleases.Lease
	reason   string
}

type codingAgentTmuxSessionState struct {
	status      string
	triggeredBy string
}

func codingAgentTmuxOrphanIdleTimeout() time.Duration {
	raw := strings.TrimSpace(os.Getenv(envCodingAgentTmuxOrphanIdleSeconds))
	if raw == "" {
		return defaultCodingAgentTmuxOrphanIdleTimeout
	}
	seconds, err := strconv.Atoi(raw)
	if err != nil || seconds <= 0 {
		return defaultCodingAgentTmuxOrphanIdleTimeout
	}
	timeout := time.Duration(seconds) * time.Second
	// A retained interactive shell is a short continuation window, never an
	// unbounded process lease. Configuration may shorten this ceiling but may
	// not extend it beyond one hour.
	if timeout > defaultCodingAgentTmuxOrphanIdleTimeout {
		return defaultCodingAgentTmuxOrphanIdleTimeout
	}
	return timeout
}

func (api *StreamingAPI) cleanupStaleCodingAgentTmuxSessions(now time.Time) int {
	if api == nil || api.terminalStore == nil {
		return 0
	}
	if now.IsZero() {
		now = time.Now()
	}
	candidates := api.staleCodingAgentTmuxCleanupCandidates(now, codingAgentTmuxOrphanIdleTimeout())
	closed := 0
	for _, candidate := range candidates {
		snapshot := candidate.snapshot
		tmuxSession := strings.TrimSpace(candidate.lease.TmuxSession)
		if tmuxSession == "" {
			tmuxSession = strings.TrimSpace(snapshot.TmuxSession)
		}
		if tmuxSession == "" {
			continue
		}
		reason := "stale coding-agent tmux cleanup"
		if candidate.reason != "" {
			reason += ": " + candidate.reason
		}
		if !closeCodingAgentTmuxSessionByName(tmuxSession, reason) {
			continue
		}
		if registry := api.ensureTerminalLeaseRegistry(); registry != nil {
			registry.MarkClosed(tmuxSession, candidate.reason, now)
		}
		api.terminalStore.MarkArchived(snapshot.TerminalID, candidate.reason)
		api.closeRetainedSessionForReapedTmux(snapshot.SessionID, tmuxSession)
		// The pane is gone, so nothing can finish the turn it was running: clear what still marks
		// the conversation busy (retained-turn record, claimed queue entry), or every later message
		// queues behind the dead turn (excellence, 2026-09-30, a Muse Code chat).
		api.releaseStoppedSessionTurnMarkers(snapshot.SessionID)
		closed++
		log.Printf("[TMUX_REAPER] Closed stale coding-agent tmux session %q terminal=%q owner=%q session=%q reason=%s",
			tmuxSession, snapshot.TerminalID, snapshot.OwnerID, snapshot.SessionID, candidate.reason)
	}
	return closed
}

// closeRetainedSessionForReapedTmux keeps the durable mcpagent registry in
// lockstep with the provider process registry. Closing only the tmux pane leaves
// Session.Send addressable and makes the next message look like an ambiguous
// failed delivery instead of a clean resumed turn.
func (api *StreamingAPI) closeRetainedSessionForReapedTmux(sessionID, tmuxSession string) bool {
	retainedSession, ok := mcpagent.LookupSession(sessionID)
	if !ok || retainedSession == nil {
		return false
	}
	handle := retainedSession.Snapshot()
	if handle == nil || handle.Provider.Transport != llmtypes.CodingProviderTransportTmux ||
		strings.TrimSpace(handle.Provider.TmuxSession) != strings.TrimSpace(tmuxSession) {
		return false
	}
	if err := retainedSession.Close(); err != nil {
		log.Printf("[TMUX_REAPER] Failed to close retained session %q after reaping tmux %q: %v", sessionID, tmuxSession, err)
		return false
	}
	return true
}

// closeIdleRetainedTmuxSessionWithoutLiveTerminal is the routing backstop for
// stale state left by older cleanup paths after the terminal was marked closed.
// It is deliberately limited to an idle Session: a failed send during an active
// turn remains delivery-uncertain because the CLI may have accepted the bytes.
func (api *StreamingAPI) closeIdleRetainedTmuxSessionWithoutLiveTerminal(sessionID string, retainedSession *mcpagent.Session) bool {
	if api == nil || retainedSession == nil {
		return false
	}
	if retainedSession.ActiveTurnID() != "" || api.hasActiveTurnCancel(sessionID) || api.sessionHasLiveMainCodingTmux(sessionID) {
		return false
	}
	handle := retainedSession.Snapshot()
	if handle == nil || handle.Provider.Transport != llmtypes.CodingProviderTransportTmux {
		return false
	}
	if err := retainedSession.Close(); err != nil {
		log.Printf("[LIVE INPUT] Failed to close stale retained tmux session %q: %v", sessionID, err)
		return false
	}
	log.Printf("[LIVE INPUT] Closed idle retained tmux session %q with no live terminal; starting a resumed turn", sessionID)
	return true
}

func (api *StreamingAPI) staleCodingAgentTmuxCleanupCandidates(now time.Time, idleTimeout time.Duration) []codingAgentTmuxCleanupCandidate {
	if api == nil || api.terminalStore == nil {
		return nil
	}
	if now.IsZero() {
		now = time.Now()
	}
	if idleTimeout <= 0 {
		idleTimeout = defaultCodingAgentTmuxOrphanIdleTimeout
	}

	sessionStates := api.codingAgentTmuxActiveSessionStates()
	registry := api.ensureTerminalLeaseRegistry()
	if registry == nil {
		return nil
	}
	leases := registry.List()
	candidates := make([]codingAgentTmuxCleanupCandidate, 0)
	for _, lease := range leases {
		if lease.ProcessState == terminalleases.ProcessClosed {
			continue
		}
		tmuxSession := strings.TrimSpace(lease.TmuxSession)
		if tmuxSession == "" || !isCodingAgentTmuxSessionName(tmuxSession) {
			continue
		}
		snapshot, ok := api.terminalStore.GetRaw(lease.TerminalID)
		if !ok {
			snapshot = terminals.Snapshot{
				TerminalID:    lease.TerminalID,
				TmuxSession:   lease.TmuxSession,
				SessionID:     lease.SessionID,
				OwnerID:       lease.OwnerID,
				ExecutionID:   lease.ExecutionID,
				ExecutionKind: lease.ExecutionKind,
				WorkflowPath:  lease.WorkflowPath,
				UpdatedAt:     lease.LastActivity,
			}
		}

		state, sessionExists := sessionStates[lease.SessionID]
		switch {
		case !sessionExists:
			candidates = append(candidates, codingAgentTmuxCleanupCandidate{
				snapshot: snapshot,
				lease:    lease,
				reason:   "owner session missing",
			})
		case codingAgentTmuxSessionStatusClosesImmediately(state.status):
			candidates = append(candidates, codingAgentTmuxCleanupCandidate{
				snapshot: snapshot,
				lease:    lease,
				reason:   "owner session " + state.status,
			})
		case lease.Policy == terminalleases.PolicyBounded &&
			lease.ProcessDeadline != nil && !now.Before(*lease.ProcessDeadline):
			candidates = append(candidates, codingAgentTmuxCleanupCandidate{
				snapshot: snapshot,
				lease:    lease,
				reason:   "bounded process grace elapsed",
			})
		case codingAgentTmuxSessionStatusUsesIdleBackstop(state.status) &&
			lease.Policy == terminalleases.PolicyPersistent &&
			codingAgentTmuxSnapshotIdleFor(snapshot, now, idleTimeout):
			candidates = append(candidates, codingAgentTmuxCleanupCandidate{
				snapshot: snapshot,
				lease:    lease,
				reason:   "idle backstop elapsed",
			})
		}
	}
	return candidates
}

func (api *StreamingAPI) codingAgentTmuxActiveSessionStates() map[string]codingAgentTmuxSessionState {
	states := make(map[string]codingAgentTmuxSessionState)
	if api == nil {
		return states
	}
	api.activeSessionsMux.RLock()
	defer api.activeSessionsMux.RUnlock()
	for sessionID, session := range api.activeSessions {
		if session == nil {
			continue
		}
		states[sessionID] = codingAgentTmuxSessionState{
			status:      strings.TrimSpace(session.Status),
			triggeredBy: strings.TrimSpace(session.TriggeredBy),
		}
	}
	return states
}

func codingAgentTmuxSessionStatusClosesImmediately(status string) bool {
	switch strings.TrimSpace(status) {
	case "stopped", "error", "dismissed":
		return true
	default:
		return false
	}
}

func codingAgentTmuxSessionStatusUsesIdleBackstop(status string) bool {
	switch strings.TrimSpace(status) {
	case "completed", "inactive":
		return true
	default:
		return false
	}
}

func codingAgentTmuxSnapshotIdleFor(snapshot terminals.Snapshot, now time.Time, idleTimeout time.Duration) bool {
	lastUpdate := snapshot.UpdatedAt
	if lastUpdate.IsZero() {
		lastUpdate = snapshot.CreatedAt
	}
	if lastUpdate.IsZero() {
		return false
	}
	return !now.Before(lastUpdate.Add(idleTimeout))
}

func isCodingAgentTmuxSessionName(tmuxSession string) bool {
	name := strings.TrimSpace(tmuxSession)
	return strings.HasPrefix(name, "mlp-claude-code") ||
		strings.HasPrefix(name, "mlp-codex-cli") ||
		strings.HasPrefix(name, "mlp-cursor-cli") ||
		strings.HasPrefix(name, "mlp-pi-cli") ||
		strings.HasPrefix(name, "mlp-muse-") ||
		strings.HasPrefix(name, "agy-int-")
}

func closeCodingAgentTmuxSessionByName(tmuxSession, reason string) bool {
	tmuxSession = strings.TrimSpace(tmuxSession)
	if tmuxSession == "" {
		return false
	}
	_ = gracefulCloseCodingCLITmuxByName(tmuxSession, reason)
	ctx, cancel := context.WithTimeout(context.Background(), terminalTmuxActionTimeout)
	defer cancel()
	if err := runTerminalTmuxCommand(ctx, "", "kill-session", "-t", tmuxSession); err != nil && !isMissingTmuxTargetError(err) {
		log.Printf("[TMUX_REAPER] kill-session %q failed: %v", tmuxSession, err)
		return false
	}
	return true
}

// cleanupConflictingPiCLIInteractiveSessions closes older manual Pi CLI main
// sessions in the same working directory before launching a new one. Pi CLI
// rejects concurrent sessions in one directory when their MCP bridge configs
// differ; multi-agent chats intentionally share _users/<user>/Chats, so a
// completed app session whose tmux process is still alive can block new chats.
// closeBusy also closes a session mid-turn: only after waiting for it ran
// out (it is likely stuck).
func (api *StreamingAPI) cleanupConflictingPiCLIInteractiveSessions(currentSessionID, workingDir, reason string, closeBusy bool) int {
	if api == nil || api.terminalStore == nil {
		return 0
	}
	currentSessionID = strings.TrimSpace(currentSessionID)
	targetWorkingDir := cleanCodingAgentWorkingDir(workingDir)
	if currentSessionID == "" || targetWorkingDir == "" {
		return 0
	}

	sessionStates := api.codingAgentTmuxActiveSessionStates()
	registry := api.ensureTerminalLeaseRegistry()
	if registry == nil {
		return 0
	}
	closed := 0
	for _, snapshot := range api.terminalStore.ListRaw("") {
		if snapshot.SessionID == currentSessionID {
			continue
		}
		tmuxSession := strings.TrimSpace(snapshot.TmuxSession)
		if !strings.HasPrefix(tmuxSession, "mlp-pi-cli") {
			continue
		}
		if !codingAgentSnapshotIsMainAgent(snapshot) {
			continue
		}
		if !sameCodingAgentWorkingDir(codingAgentSnapshotWorkingDir(snapshot), targetWorkingDir) {
			continue
		}
		lease, leased := registry.GetByTerminal(snapshot.TerminalID)
		if !leased || lease.ProcessState == terminalleases.ProcessClosed {
			continue
		}
		// Never close a Pi CLI that is in the middle of a turn: that killed a
		// Crew's running chat when a function call started in the same
		// folder (issue #213, C7). The caller waits for it to finish first.
		if !closeBusy && api.conversationTurnOccupied(snapshot.SessionID) {
			log.Printf("[PI_CLI_CONFLICT] Keeping busy Pi CLI session %s tmux=%s while starting %s",
				snapshot.SessionID, tmuxSession, currentSessionID)
			continue
		}
		ownershipCtx, ownershipCancel := context.WithTimeout(context.Background(), terminalTmuxActionTimeout)
		owned := tmuxSessionOwnedByRegistry(ownershipCtx, tmuxSession, registry)
		ownershipCancel()
		if !owned {
			log.Printf("[PI_CLI_CONFLICT] Preserving unowned Pi CLI tmux=%s old_session=%s while starting %s",
				tmuxSession, snapshot.SessionID, currentSessionID)
			continue
		}

		state := sessionStates[snapshot.SessionID]
		if strings.EqualFold(strings.TrimSpace(state.triggeredBy), "cron") {
			log.Printf("[PI_CLI_CONFLICT] Keeping cron Pi CLI session %s tmux=%s working_dir=%s while starting %s",
				snapshot.SessionID, tmuxSession, targetWorkingDir, currentSessionID)
			continue
		}

		closeReason := "pi-cli same-working-dir conflict"
		if reason != "" {
			closeReason += ": " + reason
		}
		if !closeCodingAgentTmuxSessionByName(tmuxSession, closeReason) {
			continue
		}
		registry.MarkClosed(tmuxSession, closeReason, time.Now())
		api.detachConflictingPiCLISession(snapshot.SessionID, state.status)
		if snapshot.Active {
			api.terminalStore.MarkFailed(snapshot.TerminalID)
		}
		if _, ok := api.terminalStore.MarkProcessClosed(snapshot.TerminalID, closeReason); ok {
			closed++
			log.Printf("[PI_CLI_CONFLICT] Closed conflicting Pi CLI tmux session %q terminal=%q old_session=%q new_session=%q status=%q working_dir=%s",
				tmuxSession, snapshot.TerminalID, snapshot.SessionID, currentSessionID, state.status, targetWorkingDir)
		}
	}
	return closed
}

func (api *StreamingAPI) detachConflictingPiCLISession(sessionID, status string) {
	sessionID = strings.TrimSpace(sessionID)
	if api == nil || sessionID == "" {
		return
	}

	api.agentCancelMux.Lock()
	cancelFunc := api.agentCancelFuncs[sessionID]
	delete(api.agentCancelFuncs, sessionID)
	api.agentCancelMux.Unlock()
	if cancelFunc != nil {
		cancelFunc()
	}

	api.runningAgentsMux.Lock()
	delete(api.runningAgents, sessionID)
	api.runningAgentsMux.Unlock()

	api.removeSessionAgent(sessionID)

	api.setSessionBusy(sessionID, false)
	api.setSyntheticTurn(sessionID, false)
	if piCLIConflictStatusShouldStop(status) {
		api.updateSessionStatus(sessionID, "stopped")
	}
}

func piCLIConflictStatusShouldStop(status string) bool {
	switch strings.TrimSpace(status) {
	case "running", "inactive", "paused":
		return true
	default:
		return false
	}
}

func codingAgentSnapshotIsMainAgent(snapshot terminals.Snapshot) bool {
	if kind := strings.ToLower(strings.TrimSpace(snapshot.ExecutionKind)); kind != "" {
		return kind == "main_agent" || kind == "main" || kind == "chat"
	}
	owner := strings.TrimSpace(snapshot.OwnerID)
	return owner != "" && (owner == snapshot.SessionID || strings.HasPrefix(owner, "main:"))
}

func codingAgentSnapshotWorkingDir(snapshot terminals.Snapshot) string {
	if snapshot.Status.StatusMeta != nil {
		for _, key := range []string{"working_dir", "pi_working_dir"} {
			if value := strings.TrimSpace(stringValue(snapshot.Status.StatusMeta[key])); value != "" {
				return value
			}
		}
	}
	if value := strings.TrimSpace(snapshot.WorkflowPath); value != "" {
		if filepath.IsAbs(value) {
			return value
		}
		return codingAgentWorkspaceWorkingDir(value)
	}
	return ""
}

func sameCodingAgentWorkingDir(left, right string) bool {
	left = cleanCodingAgentWorkingDir(left)
	right = cleanCodingAgentWorkingDir(right)
	return pathidentity.Same(left, right)
}

func cleanCodingAgentWorkingDir(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	return filepath.Clean(path)
}

// busyConflictingPiCLISession returns another session running a Pi CLI turn
// in workingDir, or "" when none is: a new Pi session there waits for it
// instead of closing it mid-turn.
func (api *StreamingAPI) busyConflictingPiCLISession(currentSessionID, workingDir string) string {
	if api == nil || api.terminalStore == nil {
		return ""
	}
	targetWorkingDir := cleanCodingAgentWorkingDir(workingDir)
	if targetWorkingDir == "" {
		return ""
	}
	for _, snapshot := range api.terminalStore.ListRaw("") {
		if snapshot.SessionID == strings.TrimSpace(currentSessionID) || !snapshot.Active {
			continue
		}
		if !strings.HasPrefix(strings.TrimSpace(snapshot.TmuxSession), "mlp-pi-cli") || !codingAgentSnapshotIsMainAgent(snapshot) {
			continue
		}
		if !sameCodingAgentWorkingDir(codingAgentSnapshotWorkingDir(snapshot), targetWorkingDir) {
			continue
		}
		if api.conversationTurnOccupied(snapshot.SessionID) {
			return snapshot.SessionID
		}
	}
	return ""
}

// piCLIConflictWait bounds how long a new Pi session waits for another
// session's running turn in the same folder; a var so tests can shorten it.
var piCLIConflictWait = 15 * time.Minute

// waitForBusyPiCLISessions blocks until no other Pi CLI turn runs in
// workingDir, ctx ends, or the wait limit passes. It reports whether a busy
// session is still there after the limit (likely stuck: close it).
func (api *StreamingAPI) waitForBusyPiCLISessions(ctx context.Context, currentSessionID, workingDir string) bool {
	deadline := time.Now().Add(piCLIConflictWait)
	logged := false
	for {
		busy := api.busyConflictingPiCLISession(currentSessionID, workingDir)
		if busy == "" {
			return false
		}
		if !logged {
			log.Printf("[PI_CLI_CONFLICT] %s waits for %s to finish its turn in %s", currentSessionID, busy, workingDir)
			logged = true
		}
		if time.Now().After(deadline) {
			log.Printf("[PI_CLI_CONFLICT] %s stopped waiting for %s after %s", currentSessionID, busy, piCLIConflictWait)
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(2 * time.Second):
		}
	}
}
