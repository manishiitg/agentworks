package server

import (
	"log"
	"strings"
	"sync"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/events"

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
			log.Printf("[MUSE_WAKE] session=%s native=%s turn=%s action=skipped reason=duplicate", sessionID, wake.nativeSessionID, wake.turnID)
			return
		}
	}
	q.muse[sessionID] = append(q.muse[sessionID], wake)
	log.Printf("[MUSE_WAKE] session=%s native=%s turn=%s action=queued", sessionID, wake.nativeSessionID, wake.turnID)
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
	if chatTerminalTurnRefusal(sessionID) != "" {
		dropQueuedNativeWakes(sessionID, "continued_in_terminal")
		return
	}
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
	if reason == "" && !api.executeSyntheticTurnWithContext(sessionID, strings.Join(messages, "\n\n"), "", func(err error) {
		action := "done"
		reason := "completed"
		if err != nil {
			action = "failed"
			reason = "turn_error"
		}
		log.Printf("[NATIVE_WAKE] session=%s commands=%d muse=%d action=%s reason=%s", sessionID, len(commands), len(muse), action, reason)
	}, nil, nativeWakePresentation(commands, muse)) {
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

// Display only native result tails, never the internal continuation instructions.
func nativeWakePresentation(commands []codexFinishedCommand, muse []museWake) events.BackgroundWakePresentation {
	p := events.BackgroundWakePresentation{Kind: "background_work_finished"}
	if len(commands) > 0 && len(muse) == 0 {
		p.Kind = "background_command_finished"
	}
	var output []string
	for _, command := range commands {
		p.Failed = p.Failed || command.failed
		tail := []rune(command.output)
		if len(tail) > codexCommandOutputTail {
			tail = tail[len(tail)-codexCommandOutputTail:]
		}
		if len(tail) > 0 {
			output = append(output, string(tail))
		}
	}
	for _, wake := range muse {
		tail := []rune(wake.text)
		if len(tail) > 2000 {
			tail = tail[len(tail)-2000:]
		}
		if len(tail) > 0 {
			output = append(output, string(tail))
		}
	}
	p.Output = strings.Join(output, "\n\n")
	return p
}
