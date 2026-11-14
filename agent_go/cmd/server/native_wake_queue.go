package server

import (
	"log"
	"strings"
	"sync"

	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/claudecode"
)

// nativeWakes holds "background work finished" wakes (a Codex command that
// outlived its turn, Claude's own follow-up turn) until the chat is free.
// They used to be dropped when another turn held the chat, so agents stopped
// with their work half done (owner, 14 Nov 2026). Like T3 Code, a wake now
// waits for the current turn and runs when it ends: the end of every turn
// calls runQueuedNativeWakes through drainPendingAutoNotificationsAfterTurn.
// The wake runs with the chat's current turn setup, even when a different
// person spoke in between (owner, 14 Nov 2026: ease of use over security).
type codexFinishedCommand struct {
	id     string
	failed bool
	output string
}

type museWake struct {
	nativeSessionID, turnID, sources, text string
}

type nativeWakeQueue struct {
	mu     sync.Mutex
	codex  map[string][]codexFinishedCommand
	claude map[string]bool
	muse   map[string][]museWake
}

var nativeWakes = &nativeWakeQueue{codex: map[string][]codexFinishedCommand{}, claude: map[string]bool{}}

func (q *nativeWakeQueue) addCodex(sessionID string, command codexFinishedCommand) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, queued := range q.codex[sessionID] {
		if queued.id == command.id {
			return
		}
	}
	q.codex[sessionID] = append(q.codex[sessionID], command)
}

func (q *nativeWakeQueue) addMuse(sessionID string, wake museWake) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.muse == nil {
		q.muse = map[string][]museWake{}
	}
	for _, queued := range q.muse[sessionID] {
		if queued.nativeSessionID == wake.nativeSessionID && queued.turnID == wake.turnID {
			return
		}
	}
	q.muse[sessionID] = append(q.muse[sessionID], wake)
}

func (q *nativeWakeQueue) addClaude(sessionID string) {
	q.mu.Lock()
	q.claude[sessionID] = true
	q.mu.Unlock()
}

// take removes and returns everything waiting for one chat.
func (q *nativeWakeQueue) take(sessionID string) ([]codexFinishedCommand, bool, []museWake) {
	q.mu.Lock()
	defer q.mu.Unlock()
	commands, claude, muse := q.codex[sessionID], q.claude[sessionID], q.muse[sessionID]
	delete(q.codex, sessionID)
	delete(q.claude, sessionID)
	delete(q.muse, sessionID)
	return commands, claude, muse
}

// runQueuedNativeWakes starts the follow-up turn for a chat's waiting wakes,
// unless a turn still holds the chat: that turn's end calls this again.
func (api *StreamingAPI) runQueuedNativeWakes(sessionID string) {
	stopped := api.isSessionMarkedStopped(sessionID)
	if !stopped && api.isSessionBusyForAutoNotification(sessionID) {
		return
	}
	commands, claude, muse := nativeWakes.take(sessionID)
	if claude {
		api.adoptQueuedClaudeWake(sessionID)
	}
	if len(commands) == 0 && len(muse) == 0 {
		return
	}
	var messages []string
	if len(commands) > 0 {
		messages = append(messages, codexCommandsFinishedText(commands))
	}
	if len(muse) > 0 {
		messages = append(messages, museWakesFinishedText(muse))
	}
	reason := backgroundFollowUps.skipReason(sessionID, stopped)
	if reason == "" && !api.executeSyntheticTurn(sessionID, strings.Join(messages, "\n\n")) {
		reason = "turn_not_started"
	}
	if reason != "" {
		log.Printf("[CODEX_BACKGROUND] follow_up session=%s commands=%d muse=%d action=skip reason=%s", sessionID, len(commands), len(muse), reason)
		return
	}
	api.noteAutomaticFollowUpStarted(sessionID)
	log.Printf("[CODEX_BACKGROUND] follow_up session=%s commands=%d muse=%d action=started", sessionID, len(commands), len(muse))
}

// dropQueuedNativeWakes forgets a chat's waiting wakes (the person pressed
// Stop) and releases a Claude wake whose timeout was stopped when queued.
func dropQueuedNativeWakes(sessionID, reason string) {
	commands, claude, muse := nativeWakes.take(sessionID)
	if claude {
		claudecode.DismissClaudeSDKWake(sessionID, reason)
	}
	if claude || len(commands) > 0 || len(muse) > 0 {
		log.Printf("[NATIVE_WAKE] dropped session=%s commands=%d claude=%t muse=%d reason=%s", sessionID, len(commands), claude, len(muse), reason)
	}
}
