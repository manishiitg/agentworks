package codexcli

import (
	"strings"
	"sync"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

type codexRetainedProgress struct {
	mu    sync.Mutex
	state *codexTranscriptStreamState
}

// ReadRetainedTurnProgressMessages reads commentary from this terminal's exact
// rollout without treating it as a completed answer.
func ReadRetainedTurnProgressMessages(ownerSessionID string, turnStart time.Time) []llmtypes.MessageContent {
	return llmtypes.TranscriptProgressText(ReadRetainedTurnStructuredProgressMessages(ownerSessionID, turnStart), false)
}

func ReadRetainedTurnStructuredProgressMessages(ownerSessionID string, turnStart time.Time) []llmtypes.MessageContent {
	if turnStart.IsZero() {
		return nil
	}
	session, ok := codexPersistentRegistry.Get(strings.TrimSpace(ownerSessionID))
	if !ok || session == nil {
		return nil
	}
	progress := &session.retainedProgress
	progress.mu.Lock()
	defer progress.mu.Unlock()
	if progress.state == nil || !progress.state.turnStart.Equal(turnStart) {
		progress.state = newCodexTranscriptStreamState(turnStart, "", func(start time.Time) string {
			return resolveCodexRolloutPath(session, start)
		})
	}
	var messages []llmtypes.MessageContent
	for _, chunk := range progress.state.readChunks() {
		if message, ok := llmtypes.TranscriptChunkMessage(chunk); ok {
			messages = append(messages, message)
		}
	}
	return messages
}

// ReadRetainedTurnMessages returns the committed final answer for one
// directly-injected turn. Intermediate assistant commentary is deliberately
// excluded: Codex can emit commentary before calling tools, and treating that
// text as completion ends the retained turn while the CLI is still working.
// It does not start, resume, or otherwise mutate the coding-agent process.
func ReadRetainedTurnMessages(ownerSessionID string, turnStart time.Time) []llmtypes.MessageContent {
	ownerSessionID = strings.TrimSpace(ownerSessionID)
	if ownerSessionID == "" {
		return nil
	}
	session, ok := codexPersistentRegistry.Get(ownerSessionID)
	if !ok || session == nil {
		return nil
	}
	// PLAT-106: resolve THIS session's rollout, not the newest rollout that
	// happens to share its working directory. A workflow's Chat and Schedule run
	// in the same directory, so a directory match could return the other
	// conversation's final answer — which the host then stamped with this
	// session's IDs, making the leak invisible to every downstream consumer.
	rolloutPath := resolveCodexRolloutPath(session, turnStart)
	if rolloutPath == "" {
		return nil
	}
	finalText, _ := readCodexRolloutFinalAssistantText(rolloutPath, turnStart)
	if strings.TrimSpace(finalText) == "" {
		return nil
	}
	return []llmtypes.MessageContent{
		llmtypes.TextPart(llmtypes.ChatMessageTypeAI, finalText),
	}
}
