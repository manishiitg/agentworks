package claudecode

import (
	"strings"
	"sync"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

type claudeRetainedProgress struct {
	mu           sync.Mutex
	turnStart    time.Time
	offset       int64
	pendingTools map[string]time.Time
}

// ReadRetainedTurnProgressMessages publishes newly committed assistant text
// while a directly submitted turn is still running. It never decides whether
// the turn is complete; ReadRetainedTurnMessages owns that separate decision.
func ReadRetainedTurnProgressMessages(ownerSessionID string, turnStart time.Time) []llmtypes.MessageContent {
	return llmtypes.TranscriptProgressText(ReadRetainedTurnStructuredProgressMessages(ownerSessionID, turnStart), false)
}

func ReadRetainedTurnStructuredProgressMessages(ownerSessionID string, turnStart time.Time) []llmtypes.MessageContent {
	if turnStart.IsZero() {
		return nil
	}
	session, ok := claudeInteractivePersistentRegistry.Get(strings.TrimSpace(ownerSessionID))
	if !ok || session == nil {
		return nil
	}
	session.mu.Lock()
	nativeSessionID, workingDir := session.nativeSessionID, session.workingDir
	accountHome := session.accountHome
	session.mu.Unlock()
	progress := &session.retainedProgress
	progress.mu.Lock()
	defer progress.mu.Unlock()
	if !progress.turnStart.Equal(turnStart) {
		progress.turnStart, progress.offset = turnStart, 0
		progress.pendingTools = map[string]time.Time{}
	}
	path, err := resolveClaudeTranscriptPath(nativeSessionID, workingDir, true, accountHome)
	if err != nil || path == "" {
		return nil
	}
	rows, next, err := readClaudeTranscriptEventsFromFile(path, progress.offset, turnStart, progress.pendingTools)
	if err != nil {
		return nil
	}
	progress.offset = next
	var messages []llmtypes.MessageContent
	for _, row := range rows {
		if message, ok := llmtypes.TranscriptChunkMessage(transcriptEventToChunk(nativeSessionID, row)); ok {
			messages = append(messages, message)
		}
	}
	return messages
}

// ReadRetainedTurnMessages returns the committed final answer for one
// directly-injected turn from the Claude Code transcript owned by an
// already-running interactive session. Intermediate assistant narration is
// deliberately excluded: Claude attaches that text to messages whose
// stop_reason is tool_use, and treating it as completion ends the retained
// turn while the CLI is still working.
//
// It does not start, resume, or otherwise mutate the coding-agent process.
func ReadRetainedTurnMessages(ownerSessionID string, turnStart time.Time) []llmtypes.MessageContent {
	return ReadRetainedTurnResponse(ownerSessionID, turnStart).Messages
}

func ReadRetainedTurnResponse(ownerSessionID string, turnStart time.Time) llmtypes.RetainedTurnResponse {
	ownerSessionID = strings.TrimSpace(ownerSessionID)
	if ownerSessionID == "" {
		return llmtypes.RetainedTurnResponse{}
	}
	session, ok := claudeInteractivePersistentRegistry.Get(ownerSessionID)
	if !ok || session == nil {
		return llmtypes.RetainedTurnResponse{}
	}
	session.mu.Lock()
	nativeSessionID := session.nativeSessionID
	workingDir := session.workingDir
	accountHome := session.accountHome
	session.mu.Unlock()
	response := completedAssistantResponseFromTranscript(nativeSessionID, workingDir, turnStart, accountHome)
	// A queued user or tool result after an earlier end_turn means Claude is
	// working again. Completed remains true for that earlier answer (PLAT-872).
	if !response.Found || !response.LastIsEndTurn || strings.TrimSpace(response.Text) == "" {
		return llmtypes.RetainedTurnResponse{}
	}
	result := llmtypes.RetainedTurnResponse{NativeSessionID: nativeSessionID, Messages: []llmtypes.MessageContent{llmtypes.TextPart(llmtypes.ChatMessageTypeAI, response.Text)}}
	if response.FinalRoot != "" {
		result.NativeContinuationRoots = []string{response.FinalRoot}
	}
	return result
}
