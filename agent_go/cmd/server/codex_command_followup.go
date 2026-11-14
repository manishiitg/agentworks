package server

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// A Codex command can outlive the turn that started it: the agent says "I've
// started the checks" and ends its turn, and the command finishes later.
// Codex never starts a turn for that by itself, so the result went unread
// (PLAT-746-899). Like T3 Code, start one turn when such a command finishes,
// so the agent reads the result and carries on (owner, 12 Nov 2026). Claude
// wakes itself instead; showClaudeWake shows that turn.
const codexCommandFinishedMessage = "[Background command finished] A command you started earlier in this chat has finished %s. Check its result and carry on with what you said you would do. If you did not promise anything, give the person a one-line summary.\n\nLast output:\n%s"

// codexCommandOutputTail keeps the follow-up prompt short; the full output is
// on the command's card.
const codexCommandOutputTail = 2000

// backgroundFollowUpChainLimit caps automatic follow-ups in a row, for Codex
// commands and Claude's own background tasks alike: a follow-up can start
// more background work that finishes and asks for another follow-up. It is
// only a loop guard; a long task with several background steps must finish
// without the person nudging it (owner, 14 Nov 2026), so it is generous.
const backgroundFollowUpChainLimit = 20

// backgroundFollowUpChain counts follow-up turns started since the person last
// wrote in each chat.
type backgroundFollowUpChain struct {
	mu    sync.Mutex
	count map[string]int
}

var backgroundFollowUps = &backgroundFollowUpChain{count: map[string]int{}}

// skipReason says why a follow-up must not run at all, or "" when it may. A
// busy chat is not a reason: the wake waits for the turn to end instead
// (nativeWakes), as T3 Code queues it.
func (c *backgroundFollowUpChain) skipReason(sessionID string, stopped bool) string {
	if stopped {
		return "stopped_by_person"
	}
	if c.atLimit(sessionID) {
		return "chain_limit"
	}
	return ""
}

func (c *backgroundFollowUpChain) atLimit(sessionID string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.count[sessionID] >= backgroundFollowUpChainLimit
}

func (c *backgroundFollowUpChain) started(sessionID string) {
	c.mu.Lock()
	c.count[sessionID]++
	c.mu.Unlock()
}

// personWrote starts a new chain: called for every message the person sends.
func (c *backgroundFollowUpChain) personWrote(sessionID string) {
	c.mu.Lock()
	delete(c.count, sessionID)
	c.mu.Unlock()
}

// followUpFinishedCodexCommand runs once per command: CompleteNativeTool
// closes a command only once. The command waits in nativeWakes until the chat
// is free; commands that finish together go into one follow-up turn.
func (api *StreamingAPI) followUpFinishedCodexCommand(sessionID, commandID string, failed bool, output string) {
	// Let commands that finish together and the reply's own end settle first.
	time.Sleep(3 * time.Second)
	nativeWakes.addCodex(sessionID, codexFinishedCommand{id: commandID, failed: failed, output: output})
	api.runQueuedNativeWakes(sessionID)
}

// codexCommandsFinishedText is the follow-up prompt for one or more finished commands.
func codexCommandsFinishedText(commands []codexFinishedCommand) string {
	parts := make([]string, 0, len(commands))
	for _, command := range commands {
		status := "successfully"
		if command.failed {
			status = "with an error"
		}
		tail := []rune(command.output)
		if len(tail) > codexCommandOutputTail {
			tail = append([]rune("..."), tail[len(tail)-codexCommandOutputTail:]...)
		}
		text := string(tail)
		if text == "" {
			text = "(no output)"
		}
		parts = append(parts, fmt.Sprintf(codexCommandFinishedMessage, status, text))
	}
	return strings.Join(parts, "\n\n")
}
