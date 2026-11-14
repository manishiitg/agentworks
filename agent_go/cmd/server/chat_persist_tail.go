package server

import (
	"context"
	"encoding/json"
	"github.com/manishiitg/coding-agent-loop/workspace/chatlog"
	"log"
	"maps"
	"os"
	"strings"
	"time"

	internalevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func visibleResumeUser(message llmtypes.MessageContent) bool {
	role := strings.ToLower(strings.TrimSpace(string(message.Role)))
	if role != "human" && role != "user" {
		return false
	}
	// Match the projection's exact role/text rules, but never encode old tool rows.
	raw, err := json.Marshal(message)
	if err != nil {
		return false
	}
	_, text := chatHistoryMessageRoleAndText(raw)
	return !isClaudeLocalCommandRecord(text) && !isProviderTaskNotification(text)
}
func visibleResumeUserCount(history []llmtypes.MessageContent) int {
	n := 0
	for _, message := range history {
		if visibleResumeUser(message) {
			n++
		}
	}
	return n
}

// Called under chatConversationMutex, after current session/file admission.
// A proven saved prefix means full alignment's cursor ends at the old tail:
// every remaining incoming row is new, even when its text repeats an old row.
func (api *StreamingAPI) persistableChatTail(user, session, path string, history []llmtypes.MessageContent) (*resumeHistorySource, []llmtypes.MessageContent, bool) {
	api.conversationMux.RLock()
	source := api.resumeHistorySources[session]
	current := api.conversationHistory[session]
	api.conversationMux.RUnlock()
	if source == nil || !source.prefixStable || source.user != user || source.path != path || !source.same(statResumeHistory(user, source.workspace, path)) {
		return nil, nil, false
	}
	body, canonical := chatHistoryBody(history), chatHistoryBody(current)
	if source.persistedSystem && (len(history) == 0 || history[0].Role != llmtypes.ChatMessageTypeSystem) || !appendableHistoryShape(history) || len(body) < source.persistedBody || len(canonical) < source.persistedBody || !samePersistedPrefix(body, canonical, source.persistedBody) {
		return nil, nil, false
	}
	return source, body[source.persistedBody:], true
}

func readChatConversationHeader(path string) (map[string]interface{}, error) {
	raw, err := os.ReadFile(resolvedChatHistoryPath(chatHistoryStoragePath(path)))
	if os.IsNotExist(err) {
		text, found, readErr := readFileFromWorkspaceQuery(context.Background(), path, "header_only=true")
		if readErr != nil {
			return nil, readErr
		}
		if !found {
			return nil, os.ErrNotExist
		}
		raw, err = []byte(text), nil
	}
	if err != nil {
		return nil, err
	}
	var header map[string]interface{}
	if err = json.Unmarshal(raw, &header); err != nil {
		return nil, err
	}
	for _, key := range []string{"conversation_history", "history_log", "history_count", "history_system", "history_tail", "history_total"} {
		delete(header, key)
	}
	return header, nil
}

func (api *StreamingAPI) tryPersistChatTail(session, mode, user string, history []llmtypes.MessageContent, runtime *ChatHistoryAgentRuntime, ui []internalevents.Event, path string, patch map[string]interface{}, now time.Time, bot *ChatHistoryBotMetadata) bool {
	source, tail, ok := api.persistableChatTail(user, session, path, history)
	if !ok {
		return false
	}
	// A failed attempt must leave the full merger's input intact.
	patch = maps.Clone(patch)
	header, err := readChatConversationHeader(path)
	if err != nil {
		log.Printf("[CHAT_PERSIST_TAIL] session=%s header_error=true", session)
		return false
	}
	if owner := stringFromRecord(header, "user_id"); owner != "" && owner != user {
		return false
	}
	if !source.same(statResumeHistory(user, source.workspace, path)) {
		return false
	}
	delete(patch, "conversation_history")
	if _, present := patch["title"]; !present {
		if title := stringFromRecord(header, "title"); title != "" {
			patch["title"] = title
		} else if title = chatHistoryTitleFromPersistedIndex(user, session, path); title != "" {
			patch["title"] = title
		}
	}
	patch["revision"] = header["revision"]
	advanceChatConversationRevision(patch)
	var previousUI []internalevents.Event
	if value, exists := header["ui_events"]; exists {
		raw, err := json.Marshal(value)
		if err != nil || json.Unmarshal(raw, &previousUI) != nil {
			return false
		}
	}
	ui = trimChatHistoryUIEvents(mergeChatHistoryUIEvents(previousUI, ui))
	if len(ui) > 0 {
		patch["ui_events"] = ui
	}
	messages := tail
	if len(history) > 0 && history[0].Role == llmtypes.ChatMessageTypeSystem {
		messages = make([]llmtypes.MessageContent, 0, len(tail)+1)
		messages = append(messages, history[0])
		messages = append(messages, tail...)
	}
	encoded, err := chatHistoryToRaw(messages)
	if err != nil {
		return false
	}
	if !source.same(statResumeHistory(user, source.workspace, path)) {
		return false
	}
	expected, err := chatlog.VersionFromInfos(source.header, source.log)
	if err != nil {
		return false
	}
	if err = appendConversationHistoryInWorkspace(context.Background(), path, encoded, patch, &expected); err != nil {
		log.Printf("[CHAT_PERSIST_TAIL] session=%s append_error=true", session)
		return false
	}
	// No old row can be removed by Append. Its saved prefix therefore already
	// satisfies the full writer's user-turn-loss guard without another file read.
	totalTurns := source.persistedTurns + visibleResumeUserCount(tail)
	api.rememberWrittenResumeHistory(user, session, path, history, runtime, totalTurns)
	for key, value := range patch {
		header[key] = value
	}
	if err = writeChatTailResumeSnapshot(path, header, history, totalTurns); err != nil {
		log.Printf("[CHAT_PERSIST_TAIL] session=%s resume_error=true", session)
		return false
	}
	size := int64(0)
	if stat := statResumeHistory(user, source.workspace, path); stat != nil {
		size = stat.header.Size()
	} else {
		return false
	}
	if err = updatePersistedChatHistoryIndex(user, session, mode, history, runtime, path, size, now, bot); err != nil {
		log.Printf("[CHAT_PERSIST_TAIL] session=%s index_error=true", session)
		return false
	}
	log.Printf("[CHAT_PERSIST_TAIL] session=%s prior_messages=%d appended_messages=%d messages=%d", session, source.persistedBody, len(tail), len(history))
	return true
}

func writeChatTailResumeSnapshot(path string, metadata map[string]interface{}, history []llmtypes.MessageContent, totalTurns int) error {
	start, count := len(history), 0
	for start > 0 {
		start--
		if visibleResumeUser(history[start]) {
			count++
			if count == chatHistoryResumeSnapshotTurns {
				break
			}
		}
	}
	metadata["conversation_history"] = history[start:]
	raw, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	projected := projectChatHistoryResumeWindow(raw, chatHistoryResumeSnapshotTurns, 0, start, len(history), max(0, totalTurns-count))
	return writeRawFileToWorkspace(context.Background(), chatHistoryResumeSnapshotPath(path), string(boundChatHistoryResumeSnapshot(projected)))
}
