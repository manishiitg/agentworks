package agycli

import (
	"context"
	"strings"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

type agyRetainedState struct {
	sentAt        time.Time
	progressText  string
	settledIdx    int
	settledAnswer string
	settledAt     time.Time
	seenTools     map[string]bool
	// toolsReadIdx is the newest step the running-turn tool read has covered, so a poll that
	// finds no new step costs nothing extra.
	toolsReadIdx int
	toolsRead    bool
}

// Both the completion reader and the progress reader can observe a new turn
// first. Initialize their shared state identically so completed tool receipts
// can be deduplicated regardless of polling order.
func (state *agyRetainedState) beginTurn(sentAt time.Time) {
	if !state.sentAt.Equal(sentAt) {
		*state = agyRetainedState{sentAt: sentAt, seenTools: map[string]bool{}}
	}
	if state.seenTools == nil {
		state.seenTools = map[string]bool{}
	}
}

func agyLatestRetainedRecord(session *agyInteractiveSession) (agyPendingDurableAck, agyTurnRecord, bool) {
	session.durableMu.Lock()
	if len(session.pendingDurable) == 0 {
		session.durableMu.Unlock()
		return agyPendingDurableAck{}, agyTurnRecord{}, false
	}
	receipt := session.pendingDurable[len(session.pendingDurable)-1]
	session.durableMu.Unlock()
	conversationID := receipt.conversationID
	if conversationID == "" {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		conversationID = agyConversationIDFromPane(ctx, session.tmuxSessionName)
		cancel()
	}
	if conversationID == "" {
		conversationID = agyDiscoverConversationID(session.createdAt, receipt.message, session.transcriptHome)
	}
	if conversationID == "" {
		return receipt, agyTurnRecord{}, false
	}
	indices, err := agyMatchingUserStepIndices(conversationID, receipt.baselineIdx, receipt.message, session.transcriptHome)
	if err != nil || len(indices) < receipt.occurrence {
		return receipt, agyTurnRecord{}, false
	}
	// The answer reader starts after the user row, never at the pre-send
	// baseline where a prior turn's assistant may still be present.
	answer, err := agyReadTurnRecord(conversationID, indices[receipt.occurrence-1], "", session.transcriptHome)
	return receipt, answer, err == nil
}

// ReadRetainedTurnProgressMessages returns newly committed assistant text
// from AGY's conversation record. The pane remains a terminal display only.
func ReadRetainedTurnProgressMessages(ownerSessionID string, turnStart time.Time) []llmtypes.MessageContent {
	return llmtypes.TranscriptProgressText(ReadRetainedTurnStructuredProgressMessages(ownerSessionID, turnStart), false)
}

func ReadRetainedTurnStructuredProgressMessages(ownerSessionID string, _ time.Time) []llmtypes.MessageContent {
	session, ok := activeAgyInteractiveSession(ownerSessionID)
	if !ok {
		return nil
	}
	receipt, record, ok := agyLatestRetainedRecord(session)
	if !ok {
		return nil
	}
	session.retainedMu.Lock()
	defer session.retainedMu.Unlock()
	state := &session.retainedState
	state.beginTurn(receipt.sentAt)
	var messages []llmtypes.MessageContent
	// AGY stores tool results without a live result stream. A call is published once it has
	// finished: when the turn settles, or earlier when a later step follows it. Publishing only
	// at the end left a multi-minute turn showing no tool calls at all (Confida 2026-09-30).
	var calls []agyTurnToolCall
	if record.lastType == agyStepAssistant && record.lastStatus == 3 {
		calls = agyTurnToolCallsSince(receipt.conversationID, record.userIdx, session.transcriptHome)
	} else if !state.toolsRead || state.toolsReadIdx != record.lastIdx {
		// Tool calls are secondary: read them only when the conversation has advanced.
		state.toolsRead, state.toolsReadIdx = true, record.lastIdx
		calls = agyCompletedToolCallsSince(receipt.conversationID, record.userIdx, session.transcriptHome)
	}
	for _, call := range calls {
		if state.seenTools[call.CallID] {
			continue
		}
		state.seenTools[call.CallID] = true
		messages = append(messages,
			llmtypes.MessageContent{Role: llmtypes.ChatMessageTypeAI, Parts: []llmtypes.ContentPart{llmtypes.ToolCall{ID: call.CallID, Type: "function", FunctionCall: &llmtypes.FunctionCall{Name: call.Name, Arguments: call.Args}}}},
			llmtypes.MessageContent{Role: llmtypes.ChatMessageTypeTool, Parts: []llmtypes.ContentPart{llmtypes.ToolCallResponse{ToolCallID: call.CallID, Name: call.Name, Content: call.ErrorText, IsError: call.ErrorText != ""}}},
		)
	}
	if state.progressText == record.answer {
		return messages
	}
	chunk := strings.TrimPrefix(record.answer, state.progressText)
	if !strings.HasPrefix(record.answer, state.progressText) {
		chunk = record.answer
	}
	state.progressText = record.answer
	if strings.TrimSpace(chunk) == "" {
		return messages
	}
	return append(messages, llmtypes.TextPart(llmtypes.ChatMessageTypeAI, chunk))
}

// ReadRetainedTurnMessages polls the current live-input turn's SQLite trail.
// A matching user row must exist, and a completed assistant row must end a
// stable trail while the composer is ready. Unlike the old reader, it never
// returns prompt echo, thought text or tool renderings from the pane.
func ReadRetainedTurnMessages(ownerSessionID string, _ time.Time) []llmtypes.MessageContent {
	session, ok := activeAgyInteractiveSession(ownerSessionID)
	if !ok {
		return nil
	}
	receipt, record, ok := agyLatestRetainedRecord(session)
	if !ok {
		return nil
	}
	session.retainedMu.Lock()
	state := &session.retainedState
	state.beginTurn(receipt.sentAt)
	if record.lastType != agyStepAssistant || record.lastStatus != 3 || record.finalAnswer == "" || agyPendingNativeSubagents(record, 0) {
		state.settledAt = time.Time{}
		session.retainedMu.Unlock()
		return nil
	}
	if record.lastIdx != state.settledIdx || record.finalAnswer != state.settledAnswer {
		state.settledIdx, state.settledAnswer, state.settledAt = record.lastIdx, record.finalAnswer, time.Now()
	}
	settled := time.Since(state.settledAt) >= 2*time.Second
	session.retainedMu.Unlock()
	if !settled {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	pane, err := captureAgyPane(ctx, session.tmuxSessionName)
	cancel()
	if err != nil || agyApprovalMarkerShown(pane) != "" || !PaneReadyForInput(pane) {
		return nil
	}
	return []llmtypes.MessageContent{llmtypes.TextPart(llmtypes.ChatMessageTypeAI, record.finalAnswer)}
}
