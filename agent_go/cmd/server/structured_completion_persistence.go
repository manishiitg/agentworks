package server

import (
	"context"
	"encoding/json"
	"log"
	"strings"
	"time"

	storeevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	agentevents "github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

const structuredTurnCheckpointsField = "structured_turn_checkpoints"

// retainedStructuredFinal returns the exact final response carried by the
// canonical retained-turn completion. Other terminal-looking events (notably a
// streaming_end) are lifecycle signals, not durable assistant replies.
func retainedStructuredFinal(event storeevents.Event) (string, string, bool) {
	if !strings.EqualFold(strings.TrimSpace(event.Type), string(agentevents.EventTypeUnifiedCompletion)) || event.Data == nil {
		return "", "", false
	}
	completion, ok := event.Data.Data.(*agentevents.UnifiedCompletionEvent)
	if !ok || completion == nil || !strings.EqualFold(strings.TrimSpace(completion.Status), "completed") {
		return "", "", false
	}
	final := strings.TrimSpace(completion.FinalResult)
	if final == "" {
		return "", "", false
	}
	turnID := strings.TrimSpace(event.Data.TurnID)
	if turnID == "" && completion.Metadata != nil {
		turnID = strings.TrimSpace(structuredStringValue(completion.Metadata["turn_id"]))
	}
	if turnID == "" {
		turnID = strings.TrimSpace(event.ID)
	}
	return final, turnID, turnID != ""
}

func structuredStringValue(value interface{}) string {
	text, _ := value.(string)
	return text
}

// persistRetainedStructuredCompletion makes the structured completion the
// normal durable path for retained CLI turns. Native transcript reconciliation
// is needed only when the structured completion has no final response or this
// exact append fails.
func (api *StreamingAPI) persistRetainedStructuredCompletion(sessionID string, event storeevents.Event) bool {
	final, turnID, ok := retainedStructuredFinal(event)
	if api == nil || strings.TrimSpace(sessionID) == "" {
		return false
	}
	if !ok {
		log.Printf("[CHAT_HISTORY] Retained turn reply not saved session=%s event=%s reason=%s", sessionID, event.ID, retainedFinalMissingReason(event))
		return false
	}
	userID := ""
	if api.eventStore != nil {
		userID = strings.TrimSpace(api.eventStore.GetSessionOwner(sessionID))
	}
	if userID == "" {
		api.activeSessionsMux.RLock()
		if session := api.activeSessions[sessionID]; session != nil {
			userID = strings.TrimSpace(session.UserID)
		}
		api.activeSessionsMux.RUnlock()
	}
	if userID == "" {
		log.Printf("[CHAT_HISTORY] Retained turn reply not saved session=%s event=%s reason=no owner known for the session", sessionID, event.ID)
		return false
	}
	api.sessionWorkspaceMu.RLock()
	workspacePath := strings.TrimSpace(api.sessionWorkspaceFolders[sessionID])
	api.sessionWorkspaceMu.RUnlock()
	saved := api.appendStructuredAssistantReply(userID, sessionID, workspacePath, turnID, final, event)
	if !saved {
		log.Printf("[CHAT_HISTORY] Retained turn reply not saved session=%s event=%s turn=%s reason=conversation record not found or not writable (workspace_known=%t final_chars=%d)", sessionID, event.ID, turnID, workspacePath != "", len(final))
	}
	return saved
}

// retainedFinalMissingReason says why a retained-turn completion carried nothing to save (log text only).
func retainedFinalMissingReason(event storeevents.Event) string {
	if !strings.EqualFold(strings.TrimSpace(event.Type), string(agentevents.EventTypeUnifiedCompletion)) || event.Data == nil {
		return "event is not a unified completion"
	}
	completion, ok := event.Data.Data.(*agentevents.UnifiedCompletionEvent)
	if !ok || completion == nil {
		return "completion payload missing"
	}
	if !strings.EqualFold(strings.TrimSpace(completion.Status), "completed") {
		return "completion status is " + strings.TrimSpace(completion.Status)
	}
	if strings.TrimSpace(completion.FinalResult) == "" {
		return "completion has an empty final result (source=" + structuredStringValue(completion.Metadata["source"]) + ")"
	}
	return "no turn id"
}

func (api *StreamingAPI) appendStructuredAssistantReply(userID, sessionID, workspacePath, turnID, final string, event storeevents.Event) bool {
	if api == nil || strings.TrimSpace(userID) == "" || strings.TrimSpace(sessionID) == "" || strings.TrimSpace(turnID) == "" || strings.TrimSpace(final) == "" {
		return false
	}
	conversationPath, ok, err := api.liveConversationPath(userID, sessionID, workspacePath)
	if err != nil || !ok || strings.TrimSpace(conversationPath) == "" {
		return false
	}

	lock := chatConversationMutex(conversationPath)
	lock.Lock()
	defer lock.Unlock()
	content, exists, err := readFileFromWorkspace(context.Background(), conversationPath)
	if err != nil || !exists {
		return false
	}
	var record map[string]interface{}
	if json.Unmarshal([]byte(content), &record) != nil {
		return false
	}
	owner := stringFromRecord(record, "user_id")
	if owner == "" {
		owner = "default"
	}
	persistedSession := stringFromRecord(record, "session_id")
	if owner != userID || (persistedSession != "" && persistedSession != sessionID) {
		return false
	}

	checkpoints, _ := record[structuredTurnCheckpointsField].(map[string]interface{})
	if checkpoints == nil {
		checkpoints = make(map[string]interface{})
	}
	if _, exists := checkpoints[turnID]; exists {
		return true
	}

	var history []llmtypes.MessageContent
	if rawHistory, present := record["conversation_history"]; present {
		encoded, encodeErr := json.Marshal(rawHistory)
		if encodeErr != nil || json.Unmarshal(encoded, &history) != nil {
			return false
		}
	}
	// A native emergency repair may have won the race before this structured
	// append acquired the conversation lock. Adopt that exact trailing reply and
	// record the turn checkpoint instead of manufacturing a duplicate.
	alreadyPresent := false
	if len(history) > 0 {
		last := history[len(history)-1]
		alreadyPresent = last.Role == llmtypes.ChatMessageTypeAI && strings.TrimSpace(messageContentText(last)) == strings.TrimSpace(final)
	}
	if !alreadyPresent {
		history = append(history, llmtypes.MessageContent{
			Role:  llmtypes.ChatMessageTypeAI,
			Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: final}},
		})
	}

	completedAt := event.Timestamp
	if completedAt.IsZero() {
		completedAt = time.Now()
	}
	checkpoints[turnID] = map[string]interface{}{
		"event_id":     event.ID,
		"sequence":     event.Sequence,
		"completed_at": completedAt.UTC().Format(time.RFC3339Nano),
	}
	record[structuredTurnCheckpointsField] = checkpoints
	record["conversation_history"] = history
	record["updated_at"] = completedAt.UTC().Format(time.RFC3339Nano)
	var persistedUIEvents []storeevents.Event
	if rawEvents, present := record["ui_events"]; present {
		encoded, encodeErr := json.Marshal(rawEvents)
		if encodeErr == nil {
			_ = json.Unmarshal(encoded, &persistedUIEvents)
		}
	}
	record["ui_events"] = mergeChatHistoryUIEvents(persistedUIEvents, []storeevents.Event{event})
	advanceChatConversationRevision(record)
	encoded, err := json.MarshalIndent(record, "", "  ")
	if err != nil || writeRawFileToWorkspace(context.Background(), conversationPath, string(encoded)) != nil {
		return false
	}
	if err := writeChatHistoryResumeSnapshot(context.Background(), conversationPath, encoded); err != nil {
		log.Printf("[CHAT_HISTORY] Structured completion persisted but resume snapshot failed session=%s turn=%s: %v", sessionID, turnID, err)
	}
	if err := updatePersistedChatHistoryIndex(
		userID,
		sessionID,
		stringFromRecord(record, "agent_mode"),
		history,
		runtimeFromRecord(record),
		conversationPath,
		int64(len(encoded)),
		completedAt,
		botMetadataFromRecord(record),
	); err != nil {
		log.Printf("[CHAT_HISTORY] Structured completion persisted but index update failed session=%s turn=%s: %v", sessionID, turnID, err)
	}
	log.Printf("[CHAT_HISTORY] Persisted structured retained completion session=%s turn=%s chars=%d", sessionID, turnID, len(final))
	return true
}

func messageContentText(message llmtypes.MessageContent) string {
	texts := make([]string, 0, len(message.Parts))
	for _, part := range message.Parts {
		if text, ok := part.(llmtypes.TextContent); ok && strings.TrimSpace(text.Text) != "" {
			texts = append(texts, text.Text)
		}
	}
	return strings.Join(texts, "\n")
}
