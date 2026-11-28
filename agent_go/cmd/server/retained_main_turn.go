package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/terminals"
	mcpagent "github.com/manishiitg/mcpagent/agent"
	"github.com/manishiitg/mcpagent/agent/retainedturn"
	unifiedevents "github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/mcpagent/llm"
	llmproviders "github.com/manishiitg/multi-llm-provider-go"
)

// sessionHasLiveCodingTmux reports whether the session currently has a live
// coding-agent terminal. Active is turn state, while ProcessState is process
// state: a retained main CLI is Active=false/ProcessState=live between turns.
// The idle reaper MarkStale's a closed pane and clears TmuxSession, so this still
// returns false once the tmux is genuinely gone.
func (api *StreamingAPI) sessionHasLiveCodingTmux(sessionID string) bool {
	if api == nil || api.terminalStore == nil {
		return false
	}
	for _, snapshot := range api.terminalStore.ListRaw(sessionID) {
		if terminalSnapshotHasLiveTmux(snapshot) {
			return true
		}
	}
	return false
}

// sessionHasLiveMainCodingTmux reports whether the main chat agent has a live
// tmux pane. A child workflow/background pane is aggregate session activity,
// but it cannot receive or preserve main-chat input.
func (api *StreamingAPI) sessionHasLiveMainCodingTmux(sessionID string) bool {
	_, ok := api.liveMainCodingTmuxSnapshot(sessionID)
	return ok
}

// sessionHasLiveMainCodingTmuxOf is sessionHasLiveMainCodingTmux for one provider: a live pane of ANOTHER coding
// provider owns none of this turn's context. After the person switched Claude to Codex mid-chat the old Claude pane
// still counted, so the conversation was neither resumed nor replayed and Codex started blind (2026-10-15).
func (api *StreamingAPI) sessionHasLiveMainCodingTmuxOf(sessionID, provider string) bool {
	snapshot, ok := api.liveMainCodingTmuxSnapshot(sessionID)
	return ok && strings.EqualFold(strings.TrimSpace(retainedCodingAgentProvider(snapshot)), strings.TrimSpace(provider))
}

// liveMainCodingTmuxSnapshot returns the provider-owned main pane for a chat.
// The terminal is durable across logical turns; the Go Agent instance is not.
func (api *StreamingAPI) liveMainCodingTmuxSnapshot(sessionID string) (terminals.Snapshot, bool) {
	if api == nil || api.terminalStore == nil {
		return terminals.Snapshot{}, false
	}
	for _, snapshot := range api.terminalStore.ListRaw(sessionID) {
		if terminalSnapshotHasLiveTmux(snapshot) && codingAgentSnapshotIsMainAgent(snapshot) {
			return snapshot, true
		}
	}
	return terminals.Snapshot{}, false
}

func (api *StreamingAPI) sessionHasRetainedCodingTmux(sessionID string) bool {
	if api == nil || api.terminalStore == nil {
		return false
	}
	return api.terminalStore.SessionHasRetainedCodingTmux(sessionID)
}

func terminalSnapshotHasLiveTmux(snapshot terminals.Snapshot) bool {
	return strings.TrimSpace(snapshot.TmuxSession) != "" &&
		(snapshot.Active || strings.EqualFold(strings.TrimSpace(snapshot.ProcessState), "live"))
}

// retainedCodingAgentProvider infers the provider from durable terminal data
// when continuation request state has already been cleaned up. The tmux naming
// prefixes are created by the provider adapters and are more reliable than the
// rendered status label; the label remains a compatibility fallback for older
// retained panes.
func retainedCodingAgentProvider(snapshot terminals.Snapshot) string {
	tmuxSession := strings.ToLower(strings.TrimSpace(snapshot.TmuxSession))
	switch {
	case strings.HasPrefix(tmuxSession, "mlp-claude-code"), strings.HasPrefix(tmuxSession, "mlp-claude-"):
		return string(llm.ProviderClaudeCode)
	case strings.HasPrefix(tmuxSession, "mlp-codex-cli"):
		return string(llm.ProviderCodexCLI)
	case strings.HasPrefix(tmuxSession, "mlp-cursor-cli"):
		return string(llm.ProviderCursorCLI)
	case strings.HasPrefix(tmuxSession, "mlp-muse-"):
		return string(llm.ProviderMuseCLI)
	case strings.HasPrefix(tmuxSession, "mlp-pi-cli"):
		return string(llm.ProviderPiCLI)
	case strings.HasPrefix(tmuxSession, "agy-int-") || strings.HasPrefix(tmuxSession, "mlp-agy-"):
		return string(llm.ProviderAgyCLI)
	}

	label := strings.ToLower(strings.TrimSpace(snapshot.Status.ProviderLabel))
	switch {
	case strings.Contains(label, "claude"):
		return string(llm.ProviderClaudeCode)
	case strings.Contains(label, "codex"):
		return string(llm.ProviderCodexCLI)
	case strings.Contains(label, "cursor"):
		return string(llm.ProviderCursorCLI)
	case strings.Contains(label, "muse"):
		return string(llm.ProviderMuseCLI)
	case strings.Contains(label, "pi-cli") || strings.HasPrefix(label, "pi "):
		return string(llm.ProviderPiCLI)
	case strings.Contains(label, "agy-cli"):
		return string(llm.ProviderAgyCLI)
	default:
		return ""
	}
}

// markRetainedMainCodingTurnRunning bridges the process/turn lifecycle when a
// follow-up is submitted directly to an idle retained CLI. This path does not
// bootstrap a fresh /api/query stream, so it must explicitly reactivate the
// existing terminal snapshot and session status after confirmed delivery.
func (api *StreamingAPI) markRetainedMainCodingTurnRunning(sessionID string, executionIDs ...string) {
	api.markRetainedMainCodingTurnRunningWithOwner(sessionID, false, executionIDs...)
}

// markMCPAgentSessionTurnRunning records the host-visible busy lifecycle for a
// retained turn whose completion is owned by mcpagent.Session. Unlike the
// cold-restart compatibility path, it does not start a second provider/tmux
// completion detector in AgentWorks.
func (api *StreamingAPI) markMCPAgentSessionTurnRunning(sessionID string, executionIDs ...string) {
	api.markRetainedMainCodingTurnRunningWithOwner(sessionID, true, executionIDs...)
}

func (api *StreamingAPI) markMCPAgentSessionTurnRunningWithNativeID(sessionID, executionID, nativeTurnID string, acceptedAt ...time.Time) {
	boundary := time.Now()
	if len(acceptedAt) > 0 && !acceptedAt[0].IsZero() {
		boundary = acceptedAt[0]
	}
	identity := retainedTurnIdentity{ID: strings.TrimSpace(nativeTurnID), AcceptedAt: boundary}
	if native, ok := mcpagent.LookupSession(sessionID); ok && native != nil {
		if handle := native.Snapshot(); handle != nil {
			identity.Provider = handle.Provider.Provider
			identity.Transport = handle.Provider.Transport
			identity.NativeSessionID = handle.Provider.NativeSessionID
		}
	}
	api.retainedMainTurnsMu.Lock()
	if api.retainedMainTurnCanonicalIDs == nil {
		api.retainedMainTurnCanonicalIDs = make(map[string]retainedTurnIdentity)
	}
	api.retainedMainTurnCanonicalIDs[executionID] = identity
	api.retainedMainTurnsMu.Unlock()
	api.markMCPAgentSessionTurnRunning(sessionID, executionID)
	api.reconcileAcceptedNativeCompletions(sessionID, nativeTurnID)
}

func (api *StreamingAPI) markRetainedMainCodingTurnRunningWithOwner(sessionID string, sessionOwnsCompletion bool, executionIDs ...string) {
	if api == nil || api.terminalStore == nil {
		return
	}
	executionID := ""
	if len(executionIDs) > 0 {
		executionID = strings.TrimSpace(executionIDs[0])
	}
	snapshots := api.terminalStore.ListRaw(sessionID)
	if sessionOwnsCompletion {
		api.retainedMainTurnsMu.Lock()
		identity := api.retainedMainTurnCanonicalIDs[executionID]
		api.retainedMainTurnsMu.Unlock()
		if descriptor, native := identity.nativeDescriptor(sessionID); native {
			// No terminal is created: reuse the exact-ID lifecycle and its
			// activity watchdog with an in-memory native runtime descriptor.
			snapshots = []terminals.Snapshot{descriptor}
		}
	}
	for _, snapshot := range snapshots {
		if !codingAgentSnapshotIsMainAgent(snapshot) {
			continue
		}
		// A Session acknowledgement proves that the provider accepted this input
		// even when AgentWorks' process_state snapshot is stale. The compatibility
		// path has no Session, so it still requires independent live-tmux proof.
		if !sessionOwnsCompletion && !terminalSnapshotHasLiveTmux(snapshot) {
			continue
		}
		if snapshot.TerminalID != "" {
			api.terminalStore.MarkTurnRunning(snapshot.TerminalID)
		}
		provider := llmproviders.Provider(retainedCodingAgentProvider(snapshot))
		watchCtx, watchCancel := context.WithCancel(context.Background())
		api.retainedMainTurnsMu.Lock()
		if api.retainedMainTurns == nil {
			api.retainedMainTurns = make(map[string]time.Time)
		}
		if api.retainedMainTurnWatchCancels == nil {
			api.retainedMainTurnWatchCancels = make(map[string]context.CancelFunc)
		}
		if api.retainedMainTurnExecutionIDs == nil {
			api.retainedMainTurnExecutionIDs = make(map[string]string)
		}
		if api.retainedMainTurnPendingExecutionIDs == nil {
			api.retainedMainTurnPendingExecutionIDs = make(map[string][]string)
		}
		if _, alreadyTracked := api.retainedMainTurns[sessionID]; alreadyTracked {
			primaryID := strings.TrimSpace(api.retainedMainTurnExecutionIDs[sessionID])
			if executionID != "" && executionID != primaryID {
				pending := api.retainedMainTurnPendingExecutionIDs[sessionID]
				alreadyPending := false
				for _, pendingID := range pending {
					if pendingID == executionID {
						alreadyPending = true
						break
					}
				}
				// PLAT-178: a pending entry must be an input the CLI has not
				// answered yet. Never queue an execution that already finished.
				if !alreadyPending && !api.trackedExecutionFinished(executionID) {
					api.retainedMainTurnPendingExecutionIDs[sessionID] = append(pending, executionID)
				} else if !alreadyPending {
					log.Printf("[RETAINED_TURN] Not queueing already-finished execution %s behind session=%s", executionID, sessionID)
				}
			}
			api.retainedMainTurnsMu.Unlock()
			watchCancel()
			api.setSessionBusy(sessionID, true)
			api.updateSessionStatus(sessionID, "running")
			return
		}
		previousWatchCancel := api.retainedMainTurnWatchCancels[sessionID]
		api.retainedMainTurns[sessionID] = time.Now()
		startedAt := api.retainedMainTurns[sessionID]
		api.retainedMainTurnExecutionIDs[sessionID] = executionID
		api.retainedMainTurnWatchCancels[sessionID] = watchCancel
		api.retainedMainTurnsMu.Unlock()
		if previousWatchCancel != nil {
			previousWatchCancel()
		}
		api.setSessionBusy(sessionID, true)
		api.updateSessionStatus(sessionID, "running")
		if sessionOwnsCompletion {
			go api.watchMCPAgentSessionTurn(watchCtx, sessionID, snapshot, startedAt)
		} else {
			go api.observeRetainedMainTurnStream(watchCtx, sessionID, snapshot, provider)
		}
		return
	}
}

const (
	retainedMainTurnStreamQuietWindow = 350 * time.Millisecond
	retainedMainTurnReadyStableWindow = 1200 * time.Millisecond
	retainedMainTurnCaptureTimeout    = 3 * time.Second
	retainedMainTurnRecheckWindow     = time.Second
)

var inspectRetainedMainTurnTmuxState = inspectCodingTmuxPaneState

var (
	// retainedMainTurnDurableQuietWindow is the initial observation delay
	// before the observer consults the provider's durable
	// turn sidecar. Pane heuristics cannot see idle for every provider (Muse
	// has no PaneReadyForInput signal), but the provider's own retained-turn
	// reader already encodes its completion contract, so an idle pane plus a
	// durable final response settles the turn on runner outcome instead of
	// pixels. Vars (not consts) so tests can shrink them.
	retainedMainTurnDurableQuietWindow = 15 * time.Second
	// retainedMainTurnDurableRecheckWindow bounds how often the observer
	// consults the sidecar while the pane stays quiet. Each consult can
	// capture a pane and read a transcript, so it stays well below the
	// 1s pane-recheck cadence.
	retainedMainTurnDurableRecheckWindow = 5 * time.Second
	// retainedMainTurnStuckWarnAfter emits one diagnostic line per observer
	// when a turn still has neither pane-idle nor a durable final response.
	// It never settles the turn: marathon turns stay untouched.
	retainedMainTurnStuckWarnAfter = 30 * time.Minute
	// retainedMainTurnStuckFailAfter is the PLAT-178 backstop: an observer
	// that has seen neither pane-idle nor a durable final response for this
	// long fails the turn so the chat lane is released and queued turns run
	// instead of waiting behind it forever.
	retainedMainTurnStuckFailAfter = 30 * time.Minute
	// retainedMainTurnPromotionEvidenceGrace bounds how long a promoted
	// pending input may show no new CLI output before it is treated as already
	// answered by the previous response. CLIs often answer several rapid inputs
	// in one response, which leaves pending entries with no reply of their own.
	retainedMainTurnPromotionEvidenceGrace = 75 * time.Second
	// retainedMainTurnPaneCheckInterval throttles the tmux existence check the
	// observer runs while a turn stays busy.
	retainedMainTurnPaneCheckInterval = 5 * time.Second
)

// retainedMainTurnPaneMissesToFail is how many consecutive missing/dead pane
// checks fail a busy retained turn; one transient tmux miss never does.
const retainedMainTurnPaneMissesToFail = 3

// retainedTurnFinalResponse reads the provider's durable retained-turn
// sidecar for this turn. Providers with retained-turn readers report their
// latest turn text there; providers without one return "". Reading is safe
// for every provider (completion payload, stream-closed reconcile); settling
// a live turn on the sidecar is additionally gated by
// retainedTurnDurableSettleAllowed below.
func (api *StreamingAPI) retainedTurnFinalResponse(provider llmproviders.Provider, sessionID string, turnStartedAt time.Time) string {
	readFinalResponse := retainedturn.FinalResponse
	if api != nil && api.internalRetainedTurnFinalResponseReader != nil {
		readFinalResponse = api.internalRetainedTurnFinalResponseReader
	}
	return readFinalResponse(provider, sessionID, turnStartedAt)
}

// retainedTurnDurableSettleAllowed reports whether a durable final response
// on a quiet pane may settle a live turn for this provider. Only readers
// that assert completion — not just "latest text" — qualify:
//   - Muse: museRetainedTurnReady (log quiet + TUI at prompt + pane stable,
//     pending-input excluded) gates ReadRetainedTurnMessages.
//   - Codex: the rollout phase must be final_answer/final for this turn.
//   - Claude: completedAssistantResponseFromTranscript must report Completed.
//   - Cursor: the idle composer is required in addition to the transcript.
//
// Pi is deliberately excluded: its reader returns all in-turn messages with
// no completion gate, so a question awaiting a slow user would settle early.
// Providers outside this set keep pane-only behavior (fail closed).
func retainedTurnDurableSettleAllowed(provider llmproviders.Provider) bool {
	switch llmproviders.Provider(strings.ToLower(strings.TrimSpace(string(provider)))) {
	case llmproviders.ProviderMuseCLI, llmproviders.ProviderCodexCLI, llmproviders.ProviderClaudeCode, llmproviders.ProviderCursorCLI:
		return true
	default:
		return false
	}
}

// observeRetainedMainTurnStream derives the logical end of a direct retained
// turn from the real tmux output stream. Stream output schedules a coherent
// in-band pane capture after a short quiet boundary; the provider adapter then
// decides whether the screen is truly back at its idle composer. Requiring the
// ready screen to remain stable prevents an intermediate repaint from settling
// a turn that immediately continues into another tool/action. When the pane
// never reports idle but stays silent, the provider's durable turn sidecar is
// consulted instead, so runner outcome — not pixels — settles providers whose
// composer the pane heuristics cannot see (Muse).
func (api *StreamingAPI) observeRetainedMainTurnStream(
	ctx context.Context,
	sessionID string,
	snapshot terminals.Snapshot,
	provider llmproviders.Provider,
) {
	api.observeRetainedMainTurnStreamMode(ctx, sessionID, snapshot, provider, false)
}

// observeRetainedMainTurnStreamMode is observeRetainedMainTurnStream; promoted
// marks a turn promoted from the pending list after an earlier completion,
// which must show new CLI output before it can be trusted as a live turn.
func (api *StreamingAPI) observeRetainedMainTurnStreamMode(
	ctx context.Context,
	sessionID string,
	snapshot terminals.Snapshot,
	provider llmproviders.Provider,
	promoted bool,
) {
	if api == nil || api.liveAttach == nil || strings.TrimSpace(snapshot.TmuxSession) == "" || provider == "" {
		return
	}
	api.retainedMainTurnsMu.Lock()
	turnStartedAt := api.retainedMainTurns[sessionID]
	api.retainedMainTurnsMu.Unlock()
	if turnStartedAt.IsZero() {
		turnStartedAt = time.Now()
	}
	stopIdleWatch := api.watchRetainedTurnInactivity(ctx, sessionID, snapshot, turnStartedAt)
	defer stopIdleWatch()
	output := make(chan struct{}, 1)
	var realOutputSeen atomic.Bool
	stream, unsubscribe, err := api.liveAttach.observeOutput(snapshot.TmuxSession, func([]byte) {
		realOutputSeen.Store(true)
		select {
		case output <- struct{}{}:
		default:
		}
	})
	if err != nil {
		log.Printf("[RETAINED_TURN] Could not observe tmux stream session=%s terminal=%s: %v", sessionID, snapshot.TerminalID, err)
		return
	}
	defer unsubscribe()

	// Inspect once even when the turn completed faster than stream attachment.
	// Confirmed delivery plus a stable idle composer is a valid completion.
	output <- struct{}{}
	timer := time.NewTimer(time.Hour)
	if !timer.Stop() {
		<-timer.C
	}
	defer timer.Stop()
	timerArmed := false
	resetTimer := func(delay time.Duration) {
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(delay)
		timerArmed = true
	}

	var readySince time.Time
	observeStartedAt := time.Now()
	lastOutputAt := observeStartedAt
	var lastDurableCheck time.Time
	var lastPaneCheck time.Time
	paneMisses := 0
	warnedStuck := false
	for {
		select {
		case <-ctx.Done():
			return
		case <-stream.done:
			if ctx.Err() != nil {
				return
			}
			api.handleRetainedMainTurnStreamClosed(ctx, sessionID, snapshot, provider)
			return
		case <-output:
			readySince = time.Time{}
			lastOutputAt = time.Now()
			// Cosmetic TUI repaint output can continue after the answer ends.
			// Never postpone an already scheduled lifecycle check: resetting it
			// on every byte starves both durable completion and the backstop.
			if !timerArmed {
				resetTimer(retainedMainTurnStreamQuietWindow)
			}
		case <-timer.C:
			timerArmed = false
			captureCtx, cancel := context.WithTimeout(ctx, retainedMainTurnCaptureTimeout)
			pane, captureErr := stream.capturePane(captureCtx)
			cancel()
			if captureErr != nil {
				if ctx.Err() == nil {
					log.Printf("[RETAINED_TURN] Tmux stream capture failed session=%s terminal=%s: %v", sessionID, snapshot.TerminalID, captureErr)
					api.handleRetainedMainTurnStreamClosed(ctx, sessionID, snapshot, provider)
				}
				return
			}
			if !llmproviders.CodingAgentPaneReady(provider, pane) {
				readySince = time.Time{}
				now := time.Now()
				// The pane heuristics cannot see idle for every provider.
				// After the initial observation delay, a durable final response
				// from the provider's own turn contract settles the turn on
				// runner outcome instead of pixels — but only for readers
				// that assert completion, so an ungated reader (Pi) can never
				// settle a turn that is merely awaiting a slow user.
				if retainedTurnDurableSettleAllowed(provider) &&
					now.Sub(observeStartedAt) >= retainedMainTurnDurableQuietWindow &&
					now.Sub(lastDurableCheck) >= retainedMainTurnDurableRecheckWindow {
					lastDurableCheck = now
					if finalResult := api.retainedTurnFinalResponse(provider, sessionID, turnStartedAt); strings.TrimSpace(finalResult) != "" {
						log.Printf("[RETAINED_TURN] Durable provider sidecar reports completion while pane stays busy; settling session=%s terminal=%s provider=%s quiet=%s", sessionID, snapshot.TerminalID, provider, now.Sub(lastOutputAt).Round(time.Second))
						api.emitRetainedMainTurnStreamCompletion(sessionID, snapshot, provider, "completed", "")
						return
					}
				}
				if !warnedStuck && now.Sub(observeStartedAt) >= retainedMainTurnStuckWarnAfter {
					warnedStuck = true
					log.Printf("[RETAINED_TURN] WARNING: turn still observed with no pane-idle and no durable final response session=%s terminal=%s provider=%s elapsed=%s quiet=%s", sessionID, snapshot.TerminalID, provider, now.Sub(observeStartedAt).Round(time.Second), now.Sub(lastOutputAt).Round(time.Second))
				}
				// PLAT-178: a promoted pending input that never produced new CLI
				// output was already answered by the previous response. Settle it
				// (and any backlog behind it) instead of waiting for a reply that
				// will never come.
				if promoted && !realOutputSeen.Load() && now.Sub(observeStartedAt) >= retainedMainTurnPromotionEvidenceGrace {
					api.settleUnansweredRetainedPromotion(sessionID, snapshot, provider, now.Sub(observeStartedAt))
					return
				}
				// PLAT-178: a vanished pane can never report idle or write a new
				// durable response. Fail after consecutive misses.
				if now.Sub(lastPaneCheck) >= retainedMainTurnPaneCheckInterval {
					lastPaneCheck = now
					switch inspectRetainedMainTurnTmuxState(snapshot.TmuxSession) {
					case codingTmuxPaneMissing, codingTmuxPaneDead:
						paneMisses++
					default:
						paneMisses = 0
					}
					if paneMisses >= retainedMainTurnPaneMissesToFail {
						reason := "retained pane gone"
						log.Printf("[RETAINED_TURN] %s after %d consecutive checks; failing turn session=%s terminal=%s tmux=%s", reason, paneMisses, sessionID, snapshot.TerminalID, snapshot.TmuxSession)
						api.reconcileUnexpectedTerminalExit(snapshot, reason)
						api.terminalStore.MarkFailed(snapshot.TerminalID)
						api.terminalStore.MarkProcessClosed(snapshot.TerminalID, reason)
						api.emitRetainedMainTurnStreamCompletion(sessionID, snapshot, provider, "failed", reason)
						return
					}
				}
				// PLAT-178 backstop: never hold the chat lane forever.
				if now.Sub(observeStartedAt) >= retainedMainTurnStuckFailAfter {
					reason := fmt.Sprintf("retained coding-agent turn produced no completion within %s; releasing the chat", retainedMainTurnStuckFailAfter)
					log.Printf("[RETAINED_TURN] %s session=%s terminal=%s provider=%s quiet=%s", reason, sessionID, snapshot.TerminalID, provider, now.Sub(lastOutputAt).Round(time.Second))
					// The pane may still be alive, so the terminal is not marked
					// failed; fail the tracked inputs directly so the settle below
					// releases the lane instead of promoting the backlog.
					api.failRetainedTurnExecutions(sessionID, reason)
					api.emitRetainedMainTurnStreamCompletion(sessionID, snapshot, provider, "failed", reason)
					return
				}
				// A capture can race the final repaint and leave no later output
				// to wake this observer. Keep checking while the turn is active.
				resetTimer(retainedMainTurnRecheckWindow)
				continue
			}
			if readySince.IsZero() {
				readySince = time.Now()
				resetTimer(retainedMainTurnReadyStableWindow)
				continue
			}
			api.emitRetainedMainTurnStreamCompletion(sessionID, snapshot, provider, "completed", "")
			return
		}
	}
}

// handleRetainedMainTurnStreamClosed reconciles a control-stream exit instead
// of silently leaving the logical turn busy. The provider sidecar wins when it
// already contains a final response. A missing provider process is a failure;
// a still-live pane gets a fresh observer because the control client can fail
// independently of the coding CLI.
func (api *StreamingAPI) handleRetainedMainTurnStreamClosed(
	ctx context.Context,
	sessionID string,
	snapshot terminals.Snapshot,
	provider llmproviders.Provider,
) {
	api.retainedMainTurnsMu.Lock()
	turnStartedAt, tracked := api.retainedMainTurns[sessionID]
	_, sessionOwned := api.retainedMainTurnCanonicalIDs[api.retainedMainTurnExecutionIDs[sessionID]]
	api.retainedMainTurnsMu.Unlock()
	if !tracked || ctx.Err() != nil {
		return
	}
	if sessionOwned {
		// Never reattach a success-inferencing compatibility observer for SDK
		// work. Its failure-only watchdog remains responsible for lost exits.
		return
	}
	if finalResult := api.retainedTurnFinalResponse(provider, sessionID, turnStartedAt); strings.TrimSpace(finalResult) != "" {
		log.Printf("[RETAINED_TURN] Tmux stream closed after a durable final response; settling session=%s terminal=%s", sessionID, snapshot.TerminalID)
		api.emitRetainedMainTurnStreamCompletion(sessionID, snapshot, provider, "completed", "")
		return
	}

	switch inspectRetainedMainTurnTmuxState(snapshot.TmuxSession) {
	case codingTmuxPaneMissing, codingTmuxPaneDead:
		reason := "retained coding-agent terminal exited before producing a final response"
		log.Printf("[RETAINED_TURN] %s session=%s terminal=%s tmux=%s", reason, sessionID, snapshot.TerminalID, snapshot.TmuxSession)
		api.reconcileUnexpectedTerminalExit(snapshot, reason)
		api.terminalStore.MarkFailed(snapshot.TerminalID)
		api.terminalStore.MarkProcessClosed(snapshot.TerminalID, reason)
		api.emitRetainedMainTurnStreamCompletion(sessionID, snapshot, provider, "failed", reason)
	default:
		log.Printf("[RETAINED_TURN] Tmux control stream closed while provider pane remains live; reattaching session=%s terminal=%s", sessionID, snapshot.TerminalID)
		timer := time.NewTimer(retainedMainTurnRecheckWindow)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			go api.observeRetainedMainTurnStream(ctx, sessionID, snapshot, provider)
		}
	}
}

// settleUnansweredRetainedPromotion completes a promoted pending input that
// never produced CLI output of its own, plus any inputs still pending behind
// it: the idle CLI already answered them in its previous response. The
// completion it emits then finds no pending work, releases the chat lane and
// kicks the conversation-turn queue.
func (api *StreamingAPI) settleUnansweredRetainedPromotion(sessionID string, snapshot terminals.Snapshot, provider llmproviders.Provider, waited time.Duration) {
	api.retainedMainTurnsMu.Lock()
	promotedID := strings.TrimSpace(api.retainedMainTurnExecutionIDs[sessionID])
	backlog := append([]string(nil), api.retainedMainTurnPendingExecutionIDs[sessionID]...)
	delete(api.retainedMainTurnPendingExecutionIDs, sessionID)
	for _, id := range backlog {
		delete(api.retainedMainTurnCanonicalIDs, id)
	}
	api.retainedMainTurnsMu.Unlock()
	log.Printf("[RETAINED_TURN] Promoted execution %s showed no new CLI output within %s; treating it and %d pending input(s) as answered by the previous response session=%s terminal=%s provider=%s",
		promotedID, waited.Round(time.Second), len(backlog), sessionID, snapshot.TerminalID, provider)
	meta := map[string]string{"settled_reason": "answered_by_previous_response"}
	for _, pendingID := range backlog {
		if pendingID = strings.TrimSpace(pendingID); pendingID != "" && pendingID != promotedID {
			api.completeTrackedExecution(pendingID, trackedExecutionStatusCompleted, "", meta)
		}
	}
	api.emitRetainedMainTurnStreamCompletion(sessionID, snapshot, provider, "completed", "")
}

// failRetainedTurnExecutions fails the active retained execution and every
// input pending behind it, and clears the pending list.
func (api *StreamingAPI) failRetainedTurnExecutions(sessionID, reason string) {
	api.retainedMainTurnsMu.Lock()
	ids := append([]string{api.retainedMainTurnExecutionIDs[sessionID]}, api.retainedMainTurnPendingExecutionIDs[sessionID]...)
	delete(api.retainedMainTurnPendingExecutionIDs, sessionID)
	for _, id := range ids {
		delete(api.retainedMainTurnCanonicalIDs, id)
	}
	api.retainedMainTurnsMu.Unlock()
	for _, id := range ids {
		if id = strings.TrimSpace(id); id != "" {
			api.completeTrackedExecution(id, trackedExecutionStatusFailed, reason, nil)
		}
	}
}

func (api *StreamingAPI) emitRetainedMainTurnStreamCompletion(sessionID string, snapshot terminals.Snapshot, provider llmproviders.Provider, status, failureReason string) {
	if api == nil {
		return
	}
	now := time.Now()
	api.retainedMainTurnsMu.Lock()
	executionID := strings.TrimSpace(api.retainedMainTurnExecutionIDs[sessionID])
	if _, sessionOwned := api.retainedMainTurnCanonicalIDs[executionID]; sessionOwned {
		api.retainedMainTurnsMu.Unlock()
		return
	}
	turnStartedAt := api.retainedMainTurns[sessionID]
	alreadyEmitted := !turnStartedAt.IsZero() && api.retainedMainTurnCompletionEmitted[sessionID].Equal(turnStartedAt)
	if !alreadyEmitted && !turnStartedAt.IsZero() {
		if api.retainedMainTurnCompletionEmitted == nil {
			api.retainedMainTurnCompletionEmitted = make(map[string]time.Time)
		}
		api.retainedMainTurnCompletionEmitted[sessionID] = turnStartedAt
	}
	api.retainedMainTurnsMu.Unlock()
	if alreadyEmitted {
		log.Printf("[RETAINED_TURN] Completion already emitted for this turn, skipping duplicate session=%s terminal=%s", sessionID, snapshot.TerminalID)
		return
	}
	if turnStartedAt.IsZero() {
		// The turn is no longer tracked: another path (the structured completion) already settled it. With no
		// start time the duplicate guard above cannot work and the reader cannot tell this turn's reply from
		// the thread's first one, so it returned the first turn's answer and the chat showed it as a new
		// reply after every turn (sales-outreach, 2026-10-05: the 3 October sandbox test, over and over).
		log.Printf("[RETAINED_TURN] No active retained turn, not emitting a completion session=%s terminal=%s status=%s", sessionID, snapshot.TerminalID, status)
		return
	}
	if executionID == "" {
		executionID = strings.TrimSpace(snapshot.ExecutionID)
	}
	if executionID == "" {
		executionID = "main:" + sessionID
	}
	// This completion is closed by the server's own sidecar, not by an mcpagent Session, so the turn's tokens are not
	// read from the CLI transcript here (the Session path reports them): say so, so a missing ledger row is explained.
	log.Printf("[RETAINED_USAGE] sidecar completion: usage not read for session=%s provider=%s status=%s (no mcpagent Session closed this turn)", sessionID, provider, status)
	finalResult := api.retainedTurnFinalResponse(provider, sessionID, turnStartedAt)
	if finalResult == "" && status == "completed" {
		finalResult = api.nativeTerminalFinalResponse(snapshot.TmuxSession)
	}
	if strings.TrimSpace(finalResult) == "" && strings.TrimSpace(failureReason) != "" {
		finalResult = strings.TrimSpace(failureReason)
	}
	if strings.TrimSpace(status) == "" {
		status = "completed"
	}
	// A promoted input (typed while the CLI was answering) whose "reply" is the reply just saved was answered by that
	// response: close its turn without a second copy (a Cursor chat showed one answer three times, 2026-10-20).
	answeredByPrevious := false
	if _, promoted := retainedPromotedExecutions.LoadAndDelete(executionID); promoted && status == "completed" {
		if previous, ok := retainedLastSidecarFinal.Load(sessionID); ok && retainedReplyAlreadyGiven(previous.(string), finalResult) {
			log.Printf("[RETAINED_TURN] Promoted input answered by the previous response; not saving its reply again session=%s execution=%s", sessionID, executionID)
			finalResult, answeredByPrevious = "", true
		}
	}
	if strings.TrimSpace(finalResult) != "" && status == "completed" {
		retainedLastSidecarFinal.Store(sessionID, finalResult)
	}
	completion := unifiedevents.NewUnifiedCompletionEvent(
		"coding_agent",
		"retained",
		"",
		finalResult,
		status,
		now.Sub(turnStartedAt),
		1,
	)
	completion.SessionID = sessionID
	completion.Metadata["source"] = "coding_agent_sidecar"
	completion.Metadata["provider"] = string(provider)
	completion.Metadata["tmux_session"] = snapshot.TmuxSession
	completion.Metadata["execution_kind"] = "main_agent"
	completion.Metadata["scope"] = "main_agent"
	if answeredByPrevious {
		completion.Metadata[mcpagent.AnsweredByPreviousResponseMetadataKey] = true
	} else if finalResult == "" {
		completion.Metadata["final_response_missing"] = true
		log.Printf("[RETAINED_TURN] Sidecar had no final assistant response session=%s terminal=%s provider=%s", sessionID, snapshot.TerminalID, provider)
	}
	event := events.Event{
		ID:              fmt.Sprintf("retained-turn-completion-%d", now.UnixNano()),
		Type:            "unified_completion",
		Timestamp:       now,
		SessionID:       sessionID,
		ExecutionID:     executionID,
		ExecutionKind:   "main_agent",
		TerminalOwnerID: "main:" + sessionID,
		TerminalID:      snapshot.TerminalID,
		Data: &unifiedevents.AgentEvent{
			Type:      unifiedevents.EventType("unified_completion"),
			Timestamp: now,
			SessionID: sessionID,
			Data:      completion,
		},
	}
	if api.eventStore != nil {
		api.eventStore.AddEvent(sessionID, event)
		// End the turn's stream too, as a normal turn does, so the event store logs the turn end and clients leave
		// their streaming state (a retained turn streamed chunks but its stream was never ended). Same instant as the
		// completion, so a handler settling on the first terminal event never takes it for a later promoted turn.
		end := &unifiedevents.StreamingEndEvent{
			BaseEventData: unifiedevents.BaseEventData{Timestamp: now, Metadata: map[string]interface{}{
				"kind": "terminal", "provider": string(provider), "source": "coding_agent_sidecar", "retained": true,
				"terminal_outcome": status,
			}},
			Duration:     now.Sub(turnStartedAt).String(),
			FinishReason: "retained_completion",
		}
		api.eventStore.AddEvent(sessionID, events.Event{
			ID:              fmt.Sprintf("retained-turn-stream-end-%d", now.UnixNano()),
			Type:            "streaming_end",
			Timestamp:       now,
			SessionID:       sessionID,
			ExecutionID:     executionID,
			ExecutionKind:   "main_agent",
			TerminalOwnerID: "main:" + sessionID,
			TerminalID:      snapshot.TerminalID,
			Data:            &unifiedevents.AgentEvent{Type: unifiedevents.StreamingEnd, Timestamp: now, SessionID: sessionID, Data: end},
		})
		return
	}
	api.observeRetainedMainTurnEvent(sessionID, event)
}

// completionMetadataFlag reports whether a unified completion carries a true
// boolean metadata flag.
func completionMetadataFlag(event events.Event, key string) bool {
	if event.Data == nil {
		return false
	}
	completion, ok := event.Data.Data.(*unifiedevents.UnifiedCompletionEvent)
	if !ok || completion == nil || completion.Metadata == nil {
		return false
	}
	flag, _ := completion.Metadata[key].(bool)
	return flag
}

// completionMetadataString reads a string field of a completion event's metadata ("" when absent).
func completionMetadataString(event events.Event, key string) string {
	if event.Data == nil {
		return ""
	}
	completion, ok := event.Data.Data.(*unifiedevents.UnifiedCompletionEvent)
	if !ok || completion == nil || completion.Metadata == nil {
		return ""
	}
	value, _ := completion.Metadata[key].(string)
	return value
}

// retainedSteeredRunCompletion reports whether event is the Session.Run
// completion that answered a natively steered live input (SDK/app-server/ACP
// transports). Those inputs join the running turn, so no follow-up completion
// ever comes: unlike the tmux shape it has no live_input_followup flag. Only
// the exact accepted turn ID may settle (Mac 2026-11-05: Claude SDK steer kept
// the chat "running" forever after its Run answered it).
func retainedSteeredRunCompletion(event events.Event, expected retainedTurnIdentity) bool {
	return expected.ID != "" && completionMetadataFlag(event, "canonical_turn_completion") &&
		!completionMetadataFlag(event, mcpagent.LiveInputFollowupMetadataKey) &&
		!retainedSessionCompletion(event) && expected.matchesCompletion(event)
}

// releaseStaleRetainedMainTurn ends a retained main turn that nothing can
// settle any more: the native Session has no active turn and no foreground
// turn runs, so no completion will arrive. Its inputs were answered by the
// previous reply (or never started); the chat returns to idle and queued
// messages run. Returns whether a mark was released.
func (api *StreamingAPI) releaseStaleRetainedMainTurn(sessionID, reason string) bool {
	if api == nil || api.hasActiveTurnCancel(sessionID) {
		return false
	}
	if retained, ok := lookupRetainedSession(sessionID); ok && retained.ActiveTurnID() != "" {
		return false
	}
	api.retainedMainTurnsMu.Lock()
	if _, tracked := api.retainedMainTurns[sessionID]; !tracked {
		api.retainedMainTurnsMu.Unlock()
		return false
	}
	identity := api.retainedMainTurnCanonicalIDs[api.retainedMainTurnExecutionIDs[sessionID]]
	if _, native := identity.nativeDescriptor(sessionID); native {
		// A terminal-less native input settles only on native evidence: the
		// Session itself must exist and report no active turn. An SDK input
		// steered into a running reply joins that reply, so once the Session
		// is idle no completion of its own will ever come (Air-2 2026-11-23:
		// the chat stayed "running" until Stop).
		if retained, ok := lookupRetainedSession(sessionID); !ok || retained.ActiveTurnID() != "" {
			api.retainedMainTurnsMu.Unlock()
			return false
		}
	}
	watchCancel := api.retainedMainTurnWatchCancels[sessionID]
	executionIDs := append([]string{api.retainedMainTurnExecutionIDs[sessionID]}, api.retainedMainTurnPendingExecutionIDs[sessionID]...)
	for _, id := range executionIDs {
		delete(api.retainedMainTurnCanonicalIDs, id)
	}
	delete(api.retainedMainTurns, sessionID)
	delete(api.retainedMainTurnExecutionIDs, sessionID)
	delete(api.retainedMainTurnPendingExecutionIDs, sessionID)
	delete(api.retainedMainTurnWatchCancels, sessionID)
	delete(api.retainedMainTurnCompletionEmitted, sessionID)
	api.retainedMainTurnsMu.Unlock()
	if watchCancel != nil {
		watchCancel()
	}
	for _, id := range executionIDs {
		if id = strings.TrimSpace(id); id != "" {
			api.completeTrackedExecution(id, trackedExecutionStatusCompleted, "", nil)
		}
	}
	if api.terminalStore != nil {
		for _, snapshot := range api.terminalStore.ListRaw(sessionID) {
			if codingAgentSnapshotIsMainAgent(snapshot) {
				api.terminalStore.MarkTurnCompleted(snapshot.TerminalID)
			}
		}
	}
	api.setSessionBusy(sessionID, false)
	api.updateSessionStatus(sessionID, "completed")
	log.Printf("[RETAINED_TURN] Released stale retained turn session=%s reason=%s inputs=%d", sessionID, reason, len(executionIDs))
	api.kickConversationTurnQueue(sessionID)
	return true
}

// retainedMainTurnIsNative reports whether the open retained main turn is a
// terminal-less native Session input (SDK, app-server, ACP, RPC, MSP).
func (api *StreamingAPI) retainedMainTurnIsNative(sessionID string) bool {
	if api == nil {
		return false
	}
	api.retainedMainTurnsMu.Lock()
	defer api.retainedMainTurnsMu.Unlock()
	if _, tracked := api.retainedMainTurns[sessionID]; !tracked {
		return false
	}
	_, native := api.retainedMainTurnCanonicalIDs[api.retainedMainTurnExecutionIDs[sessionID]].nativeDescriptor(sessionID)
	return native
}

// retainedMainTurnTracked reports whether a retained main-agent turn is open.
func (api *StreamingAPI) retainedMainTurnTracked(sessionID string) bool {
	if api == nil {
		return false
	}
	api.retainedMainTurnsMu.Lock()
	defer api.retainedMainTurnsMu.Unlock()
	_, tracked := api.retainedMainTurns[sessionID]
	return tracked
}

func retainedMainTurnCompletionEvent(eventType string) bool {
	// Transport boundaries must not consume another accepted input's reply.
	return strings.EqualFold(strings.TrimSpace(eventType), "unified_completion")
}

func retainedCompletionTurnID(event events.Event) string {
	if event.Data == nil {
		return ""
	}
	if id := strings.TrimSpace(event.Data.TurnID); id != "" {
		return id
	}
	return completionMetadataString(event, "turn_id")
}

// mcpAgentSessionCompletion reports whether this is the canonical completion
// emitted by the durable mcpagent.Session retained-turn lifecycle. Events
// crossing BaseEventBridge intentionally do not carry AgentWorks terminal IDs,
// so the source marker is the authoritative way to distinguish this main-turn
// completion from unrelated child-agent completions in the same session.
func mcpAgentSessionCompletion(event events.Event) bool {
	agentEvent := event.Data
	if agentEvent == nil {
		return false
	}
	completion, ok := agentEvent.Data.(*unifiedevents.UnifiedCompletionEvent)
	if !ok || completion == nil || completion.Metadata == nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(fmt.Sprint(completion.Metadata["source"])), "mcpagent_session")
}

// observeRetainedMainTurnEvent settles the explicit busy lifecycle opened by
// markRetainedMainCodingTurnRunning. These are the same structured events that
// back Terminal Center's Formatted view. A normal foreground turn is unaffected
// because it never enters retainedMainTurns, and a child completion is ignored
// because its terminal ownership does not resolve to the main coding agent.
func (api *StreamingAPI) observeRetainedMainTurnEvent(sessionID string, event events.Event) {
	eventType := strings.ToLower(strings.TrimSpace(event.Type))
	if api == nil || api.terminalStore == nil || !retainedMainTurnCompletionEvent(eventType) {
		return
	}

	api.retainedMainTurnsMu.Lock()
	startedAt, tracked := api.retainedMainTurns[sessionID]
	expectedNativeID, nativeKnown := api.retainedMainTurnCanonicalIDs[api.retainedMainTurnExecutionIDs[sessionID]]
	api.retainedMainTurnsMu.Unlock()
	if !tracked || (!nativeKnown && !event.Timestamp.IsZero() && event.Timestamp.Before(startedAt)) {
		return
	}
	handoff := nativeKnown && retainedRunHandoff(event, expectedNativeID)
	steeredRun := nativeKnown && retainedSteeredRunCompletion(event, expectedNativeID)
	if nativeKnown && !retainedSessionCompletion(event) && !handoff && !steeredRun {
		return
	}
	if nativeID := retainedCompletionTurnID(event); retainedSessionCompletion(event) && nativeKnown && !handoff && !expectedNativeID.matchesCompletion(event) && completionMetadataString(event, "source") != "retained_session_watchdog" {
		log.Printf("[RETAINED_TURN] Ignoring other native turn completion session=%s turn=%s expected=%s", sessionID, nativeID, expectedNativeID.ID)
		return
	}
	// Live input reached the CLI while this completion's Session.Run was
	// still running. The CLI may answer it only after this response, so the
	// mcpagent Session owns a follow-up watch that emits the input's own
	// completion (or closes it as answered by this response). Settling here
	// took the older response as the input's answer and left the CLI's real
	// reply with no stream (server B 2026-09-29 06:27).
	if completionMetadataFlag(event, mcpagent.LiveInputFollowupMetadataKey) {
		// Run's steered inputs move to the SDK's next lifecycle without resending.
		api.retainedMainTurnsMu.Lock()
		if id := retainedCompletionTurnID(event); id != "" {
			for _, execution := range append([]string{api.retainedMainTurnExecutionIDs[sessionID]}, api.retainedMainTurnPendingExecutionIDs[sessionID]...) {
				if nativeID, known := api.retainedMainTurnCanonicalIDs[execution]; known && nativeID.ID == id {
					nativeID.ID, nativeID.FollowupFrom = "", id
					api.retainedMainTurnCanonicalIDs[execution] = nativeID
				}
			}
		}
		api.retainedMainTurnsMu.Unlock()
		log.Printf("[RETAINED_TURN] Completion precedes a live-input follow-up; keeping the input's turn open session=%s", sessionID)
		return
	}

	snapshot, nativeOnly := expectedNativeID.nativeDescriptor(sessionID)
	if nativeOnly {
		// Missing/different IDs never settle an accepted input, including
		// after a previous Run finishes or the provider becomes quiet.
		if expectedNativeID.ID == "" || retainedCompletionTurnID(event) != expectedNativeID.ID {
			return
		}
		if id := completionMetadataString(event, "native_session_id"); id != "" && expectedNativeID.NativeSessionID != "" && id != expectedNativeID.NativeSessionID {
			return
		}
	} else {
		var ok bool
		snapshot, ok = api.terminalStore.GetRaw(event.TerminalID)
		if !ok || !codingAgentSnapshotIsMainAgent(snapshot) {
			// BaseEventBridge preserves the canonical mcpagent event but has no
			// AgentWorks terminal identity to attach to it. Resolve the main terminal
			// from the already-tracked owner session only for the explicit Session
			// completion source. This keeps child completions from settling the turn.
			if !mcpAgentSessionCompletion(event) && !steeredRun {
				return
			}
			ok = false
			for _, candidate := range api.terminalStore.ListRaw(sessionID) {
				if codingAgentSnapshotIsMainAgent(candidate) {
					snapshot = candidate
					ok = true
					break
				}
			}
			if !ok {
				return
			}
		}
	}

	api.retainedMainTurnsMu.Lock()
	currentStart, stillTracked := api.retainedMainTurns[sessionID]
	executionID := strings.TrimSpace(api.retainedMainTurnExecutionIDs[sessionID])
	var watchCancel context.CancelFunc
	var nextWatchCtx context.Context
	var nextSnapshot terminals.Snapshot
	var nextProvider llmproviders.Provider
	var nextStartedAt time.Time
	nextSessionOwned := false
	promotedExecutionID := ""
	failedPendingExecutionIDs := []string(nil)
	completedPendingExecutionIDs := []string(nil)
	if stillTracked && currentStart.Equal(startedAt) {
		if completionMetadataString(event, "source") == "retained_session_watchdog" && event.ExecutionID != executionID {
			api.retainedMainTurnsMu.Unlock()
			return
		}
		watchCancel = api.retainedMainTurnWatchCancels[sessionID]
		pending := api.retainedMainTurnPendingExecutionIDs[sessionID]
		if _, known := api.retainedMainTurnCanonicalIDs[executionID]; known && !retainedSessionCompletion(event) && !steeredRun {
			api.retainedMainTurnsMu.Unlock()
			return
		}
		if retainedSessionCompletion(event) || steeredRun {
			if nativeID := retainedCompletionTurnID(event); nativeID != "" {
				if expected, known := api.retainedMainTurnCanonicalIDs[executionID]; known && !expected.matchesCompletion(event) && completionMetadataString(event, "source") != "retained_session_watchdog" {
					api.retainedMainTurnsMu.Unlock()
					return
				}
				// Send during the SDK's follow-up adopts that lifecycle. Its one
				// completion covers the waiting Run input and these exact-ID inputs.
				remaining := make([]string, 0, len(pending))
				for _, id := range pending {
					if expected, known := api.retainedMainTurnCanonicalIDs[id]; known && expected.matchesCompletion(event) {
						completedPendingExecutionIDs = append(completedPendingExecutionIDs, id)
						delete(api.retainedMainTurnCanonicalIDs, id)
					} else {
						remaining = append(remaining, id)
					}
				}
				pending = remaining
				api.retainedMainTurnPendingExecutionIDs[sessionID] = remaining
			}
		}
		if nativeOnly {
			snapshot.State = "completed"
			if completion, ok := retainedCompletionPayload(event); ok && (completion.Error != "" || completion.Status == "failed" || completion.Status == "error" || completion.Status == "timeout" || completion.Status == "canceled" || completion.Status == "cancelled") {
				snapshot.State = "failed"
			}
		} else {
			// Validate native ownership before changing the terminal snapshot. A late
			// old completion must not make the newer native turn look idle.
			if completion, ok := retainedCompletionPayload(event); ok &&
				(completion.Error != "" || completion.Status == "failed" || completion.Status == "error" || completion.Status == "timeout") {
				if failed, changed := api.terminalStore.MarkTurnFailed(snapshot.TerminalID); changed {
					snapshot = failed
				}
				if completionMetadataFlag(event, "process_exited") {
					if closed, changed := api.terminalStore.MarkProcessClosed(snapshot.TerminalID, completion.Error); changed {
						snapshot = closed
					}
				}
			} else if completed, changed := api.terminalStore.MarkTurnCompleted(snapshot.TerminalID); changed {
				snapshot = completed
			}
		}
		delete(api.retainedMainTurnCanonicalIDs, executionID)
		if len(pending) > 0 && !strings.EqualFold(strings.TrimSpace(snapshot.State), "failed") {
			promotedExecutionID = strings.TrimSpace(pending[0])
			retainedPromotedExecutions.Store(promotedExecutionID, true)
			api.retainedMainTurnPendingExecutionIDs[sessionID] = append([]string(nil), pending[1:]...)
			api.retainedMainTurnExecutionIDs[sessionID] = promotedExecutionID
			// The provider accepted this input while the previous response was
			// active and begins it immediately after that response completes.
			// Move the boundary forward so the prior completion cannot settle it.
			api.retainedMainTurns[sessionID] = time.Now()
			nextStartedAt = api.retainedMainTurns[sessionID]
			_, nextSessionOwned = api.retainedMainTurnCanonicalIDs[promotedExecutionID]
			// SDK-owned promoted inputs keep the SDK as their completion owner.
			// Only cold compatibility turns may infer success from a sidecar.
			var nextCancel context.CancelFunc
			nextWatchCtx, nextCancel = context.WithCancel(context.Background())
			api.retainedMainTurnWatchCancels[sessionID] = nextCancel
			nextSnapshot = snapshot
			if identity := api.retainedMainTurnCanonicalIDs[promotedExecutionID]; nextSessionOwned {
				if descriptor, native := identity.nativeDescriptor(sessionID); native {
					nextSnapshot = descriptor
				}
			}
			nextProvider = llmproviders.Provider(retainedCodingAgentProvider(snapshot))
		} else {
			if strings.EqualFold(strings.TrimSpace(snapshot.State), "failed") {
				failedPendingExecutionIDs = append(failedPendingExecutionIDs, pending...)
				for _, id := range pending {
					delete(api.retainedMainTurnCanonicalIDs, id)
				}
			}
			delete(api.retainedMainTurns, sessionID)
			delete(api.retainedMainTurnExecutionIDs, sessionID)
			delete(api.retainedMainTurnPendingExecutionIDs, sessionID)
			delete(api.retainedMainTurnWatchCancels, sessionID)
			delete(api.retainedMainTurnCompletionEmitted, sessionID)
		}
	} else {
		stillTracked = false
	}
	api.retainedMainTurnsMu.Unlock()
	if !stillTracked {
		return
	}
	api.bindCrewSnapshotCompletion(executionID, event.Sequence)
	if watchCancel != nil {
		watchCancel()
	}

	var settledIdentity map[string]string
	if nativeOnly {
		settledIdentity = map[string]string{"native_turn_id": retainedCompletionTurnID(event)}
	}
	if strings.EqualFold(strings.TrimSpace(snapshot.State), "failed") {
		api.setSessionBusy(sessionID, false)
		api.updateSessionStatus(sessionID, "error")
		if executionID != "" {
			api.completeTrackedExecution(executionID, trackedExecutionStatusFailed, "retained coding-agent turn failed", nil)
		}
		failedPendingExecutionIDs = append(failedPendingExecutionIDs, completedPendingExecutionIDs...)
		for _, pendingID := range failedPendingExecutionIDs {
			if pendingID = strings.TrimSpace(pendingID); pendingID != "" && pendingID != executionID {
				api.completeTrackedExecution(pendingID, trackedExecutionStatusFailed, "retained coding-agent turn failed", nil)
			}
		}
	} else {
		if executionID != "" {
			api.completeTrackedExecution(executionID, trackedExecutionStatusCompleted, "", settledIdentity)
		}
		for _, id := range completedPendingExecutionIDs {
			api.completeTrackedExecution(id, trackedExecutionStatusCompleted, "", settledIdentity)
		}
		if promotedExecutionID != "" {
			if snapshot.TerminalID != "" {
				api.terminalStore.MarkTurnRunning(snapshot.TerminalID)
			}
			api.setSessionBusy(sessionID, true)
			api.updateSessionStatus(sessionID, "running")
			if nextWatchCtx != nil {
				if nextSessionOwned {
					go api.watchMCPAgentSessionTurn(nextWatchCtx, sessionID, nextSnapshot, nextStartedAt)
				} else {
					go api.observeRetainedMainTurnStreamMode(nextWatchCtx, sessionID, nextSnapshot, nextProvider, true)
				}
			}
		} else {
			api.setSessionBusy(sessionID, false)
			api.updateSessionStatus(sessionID, "completed")
		}
	}
	// A live retained CLI turn bypasses handleQuery's normal final-save block.
	// Persist its structured completion directly. The platform's saved history is
	// the chat's only source: the CLI's own transcript is never merged back in
	// (PLAT-525). A follow-up answered by the previous response carries no text of
	// its own; that response is already persisted.
	// A natively steered input was answered inside the foreground Run, which
	// saves that reply itself.
	if !steeredRun && !completionMetadataFlag(event, mcpagent.AnsweredByPreviousResponseMetadataKey) &&
		!api.persistRetainedStructuredCompletion(sessionID, event) {
		log.Printf("[CHAT_HISTORY] Retained turn completion carried no final reply to save session=%s event=%s", sessionID, event.ID)
	}
	outcome := completionMetadataString(event, mcpagent.RetainedTurnOutcomeMetadataKey)
	if outcome == "" {
		outcome = "completed"
	}
	log.Printf("[RETAINED_TURN] Settled retained main-agent turn from structured %s event session=%s query=%s terminal=%s state=%s next_execution=%s outcome=%s turn=%s native_session=%s covered_inputs=%d",
		eventType, sessionID, executionID, snapshot.TerminalID, snapshot.State, promotedExecutionID, outcome, retainedCompletionTurnID(event), expectedNativeID.NativeSessionID, 1+len(completedPendingExecutionIDs))
	if promotedExecutionID == "" {
		api.kickConversationTurnQueue(sessionID)
	} else if nextSessionOwned {
		// A later native completion may already be in the event store. Reconcile
		// it after this reply is saved, preserving accepted-input/history order.
		api.retainedMainTurnsMu.Lock()
		identity := api.retainedMainTurnCanonicalIDs[promotedExecutionID]
		api.retainedMainTurnsMu.Unlock()
		nativeID := identity.ID
		if nativeID == "" {
			nativeID = identity.FollowupFrom
		}
		api.reconcileAcceptedNativeCompletions(sessionID, nativeID)
	}
}

// deliverRetainedMainTerminalInput sends directly to a live main coding-agent
// pane when its short-lived Go Agent object is no longer registered. A retained
// tmux pane is still the same provider conversation, so starting a fresh
// workflow-builder turn here would both lose continuity and allow unrelated
// scheduled work to block the user's message.
//
// handled is true only when this is a live, supported coding-agent session. A
// handled delivery error must be surfaced to the caller; it must never be
// hidden behind an asynchronous next-turn fallback.
func (api *StreamingAPI) deliverRetainedMainTerminalInput(ctx context.Context, sessionID, message string) (provider string, handled bool, err error) {
	if api == nil {
		return "", false, nil
	}
	snapshot, live := api.liveMainCodingTmuxSnapshot(sessionID)
	if !live {
		return "", false, nil
	}

	api.lastQueryMu.RLock()
	request, ok := api.lastQueryRequests[sessionID]
	api.lastQueryMu.RUnlock()
	liveProvider := strings.TrimSpace(retainedCodingAgentProvider(snapshot))
	storedProvider := ""
	modelID := ""
	if ok {
		storedProvider = strings.TrimSpace(request.Provider)
		modelID = strings.TrimSpace(request.ModelID)
		if request.LLMConfig != nil {
			if storedProvider == "" {
				storedProvider = strings.TrimSpace(request.LLMConfig.Primary.Provider)
			}
			if modelID == "" {
				modelID = strings.TrimSpace(request.LLMConfig.Primary.ModelID)
			}
		}
	}
	// The live tmux identifies the process that will receive this input. A
	// continuation record can legitimately lag behind after Update Automation
	// switches the workflow from one coding provider to another, so it may
	// supply a model only when it describes that same provider.
	provider = liveProvider
	if provider == "" {
		provider = storedProvider
	} else if storedProvider != "" && !strings.EqualFold(provider, storedProvider) {
		modelID = ""
	}
	if provider == "" {
		return "", false, nil
	}
	contract, isCodingAgent := llmproviders.GetCodingAgentProviderContract(llmproviders.Provider(provider), modelID)
	if !isCodingAgent || !contract.SupportsLiveInput {
		return provider, false, nil
	}

	if api.internalRetainedTerminalInputHandler != nil {
		return provider, true, api.internalRetainedTerminalInputHandler(ctx, llmproviders.Provider(provider), modelID, sessionID, message)
	}
	return provider, true, llmproviders.SendCodingAgentRetainedInput(ctx, llmproviders.Provider(provider), modelID, sessionID, message)
}

func (api *StreamingAPI) recordRetainedTerminalLiveInput(sessionID, message, provider, clientMessageID string, executionIDs ...string) string {
	return api.recordRetainedLiveInput(sessionID, message, provider, clientMessageID, false, "", time.Time{}, executionIDs...)
}

func (api *StreamingAPI) recordMCPAgentSessionLiveInput(sessionID, message, provider, clientMessageID, nativeTurnID string, executionIDs ...string) string {
	return api.recordMCPAgentSessionLiveInputAt(sessionID, message, provider, clientMessageID, nativeTurnID, time.Now(), executionIDs...)
}

func (api *StreamingAPI) recordMCPAgentSessionLiveInputAt(sessionID, message, provider, clientMessageID, nativeTurnID string, acceptedAt time.Time, executionIDs ...string) string {
	return api.recordRetainedLiveInput(sessionID, message, provider, clientMessageID, true, nativeTurnID, acceptedAt, executionIDs...)
}

func (api *StreamingAPI) recordRetainedLiveInput(sessionID, message, provider, clientMessageID string, sessionOwnsCompletion bool, nativeTurnID string, acceptedAt time.Time, executionIDs ...string) string {
	return api.recordAttributedRetainedLiveInput(nil, sessionID, message, provider, clientMessageID, sessionOwnsCompletion, nativeTurnID, acceptedAt, executionIDs...)
}

func (api *StreamingAPI) recordAttributedRetainedLiveInput(attribution map[string]interface{}, sessionID, message, provider, clientMessageID string, sessionOwnsCompletion bool, nativeTurnID string, acceptedAt time.Time, executionIDs ...string) string {
	messageID := newSteerMessageID()
	executionID := "live-turn:" + messageID
	if len(executionIDs) > 0 && strings.TrimSpace(executionIDs[0]) != "" {
		executionID = strings.TrimSpace(executionIDs[0])
	}
	request := QueryRequest{Query: message, TriggeredBy: "interactive"}
	api.lastQueryMu.RLock()
	if prior, ok := api.lastQueryRequests[sessionID]; ok {
		request = prior
		request.Query = message
	}
	api.lastQueryMu.RUnlock()
	api.trackConversationTurnStart(executionID, sessionID, request)
	api.watchCrewTurnSnapshot(executionID)
	api.recordAttributedCodingAgentUserMessage(sessionID, message, provider, messageID, "sent_to_cli", clientMessageID, attribution)
	if sessionOwnsCompletion {
		api.markMCPAgentSessionTurnRunningWithNativeID(sessionID, executionID, nativeTurnID, acceptedAt)
	} else {
		api.markRetainedMainCodingTurnRunning(sessionID, executionID)
	}
	return messageID
}

func writeRetainedTerminalLiveInputResponse(w http.ResponseWriter, sessionID, message, provider, clientMessageID string, api *StreamingAPI) {
	messageID := api.recordRetainedTerminalLiveInput(sessionID, message, provider, clientMessageID)
	writeRetainedTerminalLiveInputResponseWithMessageID(w, sessionID, provider, messageID)
}

func writeRetainedTerminalLiveInputResponseWithMessageID(w http.ResponseWriter, sessionID, provider, messageID string) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(LiveInputResponse{
		Success:        true,
		Message:        "User message delivered to retained coding-agent CLI",
		DeliveryStatus: "sent_to_cli",
		Provider:       provider,
		MessageID:      messageID,
	})
}
