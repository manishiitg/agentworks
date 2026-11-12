package server

import (
	"fmt"
	"log"
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

// followUpFinishedCodexCommand runs once per command: CompleteNativeTool
// closes a command only once. It never starts a turn after the person pressed
// Stop or while another turn holds the chat.
func (api *StreamingAPI) followUpFinishedCodexCommand(sessionID, commandID string, failed bool, output string) {
	// Let commands that finish together and the reply's own end settle first;
	// the first follow-up then carries on and the rest see a busy chat.
	time.Sleep(3 * time.Second)
	if api.isSessionMarkedStopped(sessionID) {
		log.Printf("[CODEX_BACKGROUND] follow_up session=%s command=%s action=skip reason=stopped_by_person", sessionID, commandID)
		return
	}
	if api.isSessionBusyForAutoNotification(sessionID) {
		log.Printf("[CODEX_BACKGROUND] follow_up session=%s command=%s action=skip reason=chat_busy", sessionID, commandID)
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
	log.Printf("[CODEX_BACKGROUND] follow_up session=%s command=%s action=started failed=%t output_len=%d", sessionID, commandID, failed, len(output))
}
