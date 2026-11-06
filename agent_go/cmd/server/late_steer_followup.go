package server

import (
	"log"
	"strings"
	"sync"
	"time"
)

// A message steered into a running turn over a native protocol (Muse serve,
// the Claude/Cursor SDKs, the Codex app server) is taken by the CLI at once,
// so it gets its receipt and never enters the queued-messages path. If that
// turn ends before the agent acts on it, nothing answers it: the CLI does not
// start a turn of its own for it (Showcase 2026-11-06 07:55:23, Muse took the
// steer 140 ms before its turn completed; no further turn ran). tmux CLIs are
// not covered here: mcpagent's Session already hands their steered input to a
// follow-up watch when the turn ends.
//
// The rule: when the turn ends, a steer that no reply text followed gets one
// follow-up turn. Reply text streamed after the CLI took the message means the
// agent answered it in that turn, so nothing more happens.

// lateSteer is one message a running turn's CLI took over a native protocol.
type lateSteer struct {
	message         string
	messageID       string
	clientMessageID string
	provider        string
	// sentAt is when delivery started, ackAt when the CLI confirmed it took the
	// message. Reply text after ackAt answered it; text before belongs to the
	// reply that was already being written.
	sentAt time.Time
	ackAt  time.Time
}

// lateSteerTurnEnd is the turn that ended last in a chat, kept so a steer whose
// acknowledgement arrives after its turn ended is still checked.
type lateSteerTurnEnd struct {
	startedAt    time.Time
	endedAt      time.Time
	lastOutputAt time.Time
	completed    bool
}

var lateSteers = struct {
	sync.Mutex
	pending map[string][]lateSteer
	lastEnd map[string]lateSteerTurnEnd
}{pending: map[string][]lateSteer{}, lastEnd: map[string]lateSteerTurnEnd{}}

// startLateSteerFollowup runs the follow-up turn; tests replace it.
var startLateSteerFollowup = func(api *StreamingAPI, sessionID, prompt string) bool {
	return api.executeSyntheticTurn(sessionID, prompt)
}

// lateSteerFollowupPrompt quotes the person's message. A native steer's receipt
// only proves the CLI accepted it, not that its conversation kept it (Muse can
// drop input that lands as its turn completes), so the text is sent again; the
// wording keeps an agent that did keep it from answering twice.
func lateSteerFollowupPrompt(message string) string {
	return "[AUTO-NOTIFICATION] The person sent this message just as your last reply finished, and it has not been answered yet. Answer it now (once):\n\n" + strings.TrimSpace(message)
}

// noteNativeSteer records a message a running turn's CLI took over a native
// protocol. When that turn already ended while the message was being
// delivered, it is checked at once.
func (api *StreamingAPI) noteNativeSteer(sessionID string, steer lateSteer) {
	if api == nil || strings.TrimSpace(sessionID) == "" || strings.TrimSpace(steer.message) == "" {
		return
	}
	lateSteers.Lock()
	end, ended := lateSteers.lastEnd[sessionID]
	if !ended || end.endedAt.Before(steer.sentAt) {
		lateSteers.pending[sessionID] = append(lateSteers.pending[sessionID], steer)
		lateSteers.Unlock()
		return
	}
	lateSteers.Unlock()
	api.decideLateSteer(sessionID, steer, end)
}

// settleLateSteers runs when a foreground or synthetic turn ends. lastOutputAt
// is when that turn last streamed reply text (zero when it streamed none).
func (api *StreamingAPI) settleLateSteers(sessionID string, startedAt, lastOutputAt time.Time, completed bool) {
	if api == nil || strings.TrimSpace(sessionID) == "" {
		return
	}
	end := lateSteerTurnEnd{startedAt: startedAt, endedAt: time.Now(), lastOutputAt: lastOutputAt, completed: completed}
	lateSteers.Lock()
	pending := lateSteers.pending[sessionID]
	delete(lateSteers.pending, sessionID)
	lateSteers.lastEnd[sessionID] = end
	lateSteers.Unlock()
	for _, steer := range pending {
		api.decideLateSteer(sessionID, steer, end)
	}
}

func (api *StreamingAPI) decideLateSteer(sessionID string, steer lateSteer, end lateSteerTurnEnd) {
	reason := ""
	switch {
	case !end.startedAt.IsZero() && steer.sentAt.Before(end.startedAt):
		reason = "older_than_turn"
	case !end.completed:
		// A stopped or failed turn shows its own state; the person decides what to resend.
		reason = "turn_not_completed"
	case end.lastOutputAt.After(steer.ackAt):
		reason = "answered_in_turn"
	}
	if reason == "" {
		if native, ok := lookupRetainedSession(sessionID); ok && native.ActiveTurnID() != "" {
			// The agent session already runs a turn that reads the CLI's next reply.
			reason = "session_turn_running"
		}
	}
	if reason != "" {
		log.Printf("[LATE_STEER] session=%s message_id=%s no follow-up reason=%s", sessionID, steer.messageID, reason)
		return
	}
	lastOutput := "none"
	if !end.lastOutputAt.IsZero() {
		lastOutput = end.lastOutputAt.Format("15:04:05.000")
	}
	// One line per receipt decision, keyed by submission and message id. No message text.
	log.Printf("[DELIVERY] submission=%s message_id=%s session=%s decision=late_followup source=turn_end reason=no_reply_after_steer provider=%s acked=%s last_reply_output=%s turn_ended=%s",
		steer.clientMessageID, steer.messageID, sessionID, steer.provider, steer.ackAt.Format("15:04:05.000"), lastOutput, end.endedAt.Format("15:04:05.000"))
	go func() {
		if !startLateSteerFollowup(api, sessionID, lateSteerFollowupPrompt(steer.message)) {
			log.Printf("[LATE_STEER] session=%s message_id=%s follow-up turn could not start", sessionID, steer.messageID)
		}
	}()
}
