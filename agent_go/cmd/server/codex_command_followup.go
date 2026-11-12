package server

import (
	"fmt"
	"log"
	"sync"
	"time"
)

// A Codex command can outlive the turn that started it: the agent says "I've
// started the checks" and ends its turn, and the command finishes later.
// Codex never starts a turn for that by itself, so the result went unread
// (PLAT-746-899). Like T3 Code, start one turn when such a command finishes,
// so the agent reads the result and carries on (owner, 12 Nov 2026). The
// Claude equivalent is followUpFinishedClaudeTasks (PLAT-746-856).
const codexCommandFinishedMessage = "[Background command finished] A command you started earlier in this chat has finished %s. Check its result and carry on with what you said you would do. If you did not promise anything, give the person a one-line summary.\n\nLast output:\n%s"

// codexCommandOutputTail keeps the follow-up prompt short; the full output is
// on the command's card.
const codexCommandOutputTail = 2000

// codexFollowUpChainLimit caps automatic follow-ups in a row: a follow-up can
// start another long command that finishes and asks for another follow-up,
// and each one is a full turn on the person's account.
const codexFollowUpChainLimit = 3

// codexFollowUpChain counts follow-up turns started since the person last
// wrote in each chat.
type codexFollowUpChain struct {
	mu    sync.Mutex
	count map[string]int
}

var codexFollowUps = &codexFollowUpChain{count: map[string]int{}}

// skipReason says why no follow-up may start now, or "" when one may.
func (c *codexFollowUpChain) skipReason(sessionID string, stopped, busy bool) string {
	switch {
	case stopped:
		return "stopped_by_person"
	case busy:
		return "chat_busy"
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.count[sessionID] >= codexFollowUpChainLimit {
		return "chain_limit"
	}
	return ""
}

func (c *codexFollowUpChain) started(sessionID string) {
	c.mu.Lock()
	c.count[sessionID]++
	c.mu.Unlock()
}

// personWrote starts a new chain: called for every message the person sends.
func (c *codexFollowUpChain) personWrote(sessionID string) {
	c.mu.Lock()
	delete(c.count, sessionID)
	c.mu.Unlock()
}

// followUpFinishedCodexCommand runs once per command: CompleteNativeTool
// closes a command only once. It never starts a turn after the person pressed
// Stop, while another turn holds the chat, or past the chain limit.
func (api *StreamingAPI) followUpFinishedCodexCommand(sessionID, commandID string, failed bool, output string) {
	// Let commands that finish together and the reply's own end settle first;
	// the first follow-up then carries on and the rest see a busy chat.
	time.Sleep(3 * time.Second)
	if reason := codexFollowUps.skipReason(sessionID, api.isSessionMarkedStopped(sessionID), api.isSessionBusyForAutoNotification(sessionID)); reason != "" {
		log.Printf("[CODEX_BACKGROUND] follow_up session=%s command=%s action=skip reason=%s", sessionID, commandID, reason)
		return
	}
	status := "successfully"
	if failed {
		status = "with an error"
	}
	tail := []rune(output)
	if len(tail) > codexCommandOutputTail {
		tail = append([]rune("..."), tail[len(tail)-codexCommandOutputTail:]...)
	}
	text := string(tail)
	if text == "" {
		text = "(no output)"
	}
	if !api.executeSyntheticTurn(sessionID, fmt.Sprintf(codexCommandFinishedMessage, status, text)) {
		log.Printf("[CODEX_BACKGROUND] follow_up session=%s command=%s action=skip reason=turn_not_started", sessionID, commandID)
		return
	}
	codexFollowUps.started(sessionID)
	log.Printf("[CODEX_BACKGROUND] follow_up session=%s command=%s action=started failed=%t output_len=%d", sessionID, commandID, failed, len(output))
}
