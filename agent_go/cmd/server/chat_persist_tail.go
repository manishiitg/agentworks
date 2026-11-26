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
	source, tail, _, fallback := api.persistableChatTailWithReason(user, session, session, path, history)
	return source, tail, fallback == ""
}

func (api *StreamingAPI) persistableChatTailWithReason(user, liveSession, persistSession, path string, history []llmtypes.MessageContent) (*resumeHistorySource, []llmtypes.MessageContent, string, string) {
	api.conversationMux.RLock()
	lookupSession := liveSession
	source := api.resumeHistorySources[lookupSession]
	current := api.conversationHistory[lookupSession]
	if source == nil && persistSession != "" && persistSession != lookupSession {
		lookupSession = persistSession
		source = api.resumeHistorySources[lookupSession]
		current = api.conversationHistory[lookupSession]
	}
	api.conversationMux.RUnlock()
	if source == nil {
		return nil, nil, lookupSession, "no_source"
	}
	if !source.prefixStable {
		return nil, nil, lookupSession, "prefix_unstable"
	}
	if source.user != user {
		return nil, nil, lookupSession, "owner_mismatch"
	}
	if source.path != path {
		return nil, nil, lookupSession, "path_mismatch"
	}
	if !source.same(statResumeHistory(user, source.workspace, path)) {
		return nil, nil, lookupSession, "file_changed"
	}
	body, canonical := chatHistoryBody(history), chatHistoryBody(current)
	if source.persistedSystem && (len(history) == 0 || history[0].Role != llmtypes.ChatMessageTypeSystem) {
		return nil, nil, lookupSession, "missing_system"
	}
	if !appendableHistoryShape(history) {
		return nil, nil, lookupSession, "bad_shape"
	}
	if len(body) < source.persistedBody {
		return nil, nil, lookupSession, "shorter_history"
	}
	if len(canonical) < source.persistedBody {
		return nil, nil, lookupSession, "shorter_canonical"
	}
	if !samePersistedPrefix(body, canonical, source.persistedBody) {
		return nil, nil, lookupSession, "prefix_mismatch"
	}
	return source, body[source.persistedBody:], lookupSession, ""
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

func (api *StreamingAPI) tryPersistChatTail(liveSession, persistSession, mode, user string, history []llmtypes.MessageContent, runtime *ChatHistoryAgentRuntime, ui []internalevents.Event, path string, patch map[string]interface{}, now time.Time, bot *ChatHistoryBotMetadata) (bool, string) {
	source, tail, lookupSession, fallback := api.persistableChatTailWithReason(user, liveSession, persistSession, path, history)
	if fallback != "" {
		if fallback != "no_source" {
			log.Printf("[CHAT_PERSIST_TAIL] session=%s persist_session=%s fallback=%s", liveSession, persistSession, fallback)
		}
		return false, fallback
	}
	// A failed attempt must leave the full merger's input intact.
	patch = maps.Clone(patch)
	header, err := readChatConversationHeader(path)
	if err != nil {
		log.Printf("[CHAT_PERSIST_TAIL] session=%s header_error=true", liveSession)
		return false, "header_error"
	}
	if owner := stringFromRecord(header, "user_id"); owner != "" && owner != user {
		return false, "header_owner_mismatch"
	}
	if !source.same(statResumeHistory(user, source.workspace, path)) {
		return false, "file_changed_after_header"
	}
	delete(patch, "conversation_history")
	if _, present := patch["title"]; !present {
		if title := stringFromRecord(header, "title"); title != "" {
			patch["title"] = title
		} else if title = chatHistoryTitleFromPersistedIndex(user, persistSession, path); title != "" {
			patch["title"] = title
		}
	}
	patch["revision"] = header["revision"]
	advanceChatConversationRevision(patch)
	var previousUI []internalevents.Event
	if value, exists := header["ui_events"]; exists {
		raw, err := json.Marshal(value)
		if err != nil || json.Unmarshal(raw, &previousUI) != nil {
			return false, "ui_events_decode_error"
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
		return false, "encode_error"
	}
	if !source.same(statResumeHistory(user, source.workspace, path)) {
		return false, "file_changed_before_append"
	}
	expected, err := chatlog.VersionFromInfos(source.header, source.log)
	if err != nil {
		return false, "version_error"
	}
	if err = appendConversationHistoryInWorkspace(context.Background(), path, encoded, patch, &expected); err != nil {
		log.Printf("[CHAT_PERSIST_TAIL] session=%s append_error=true", liveSession)
		return false, "append_error"
	}
	// No old row can be removed by Append. Its saved prefix therefore already
	// satisfies the full writer's user-turn-loss guard without another file read.
	totalTurns := source.persistedTurns + visibleResumeUserCount(tail)
	api.rememberWrittenResumeHistory(user, lookupSession, path, history, runtime, totalTurns)
	for key, value := range patch {
		header[key] = value
	}
	if err = writeChatTailResumeSnapshot(path, header, history, totalTurns, source.target.Base); err != nil {
		log.Printf("[CHAT_PERSIST_TAIL] session=%s resume_error=true", liveSession)
		return false, "resume_error"
	}
	size := int64(0)
	if stat := statResumeHistory(user, source.workspace, path); stat != nil {
		size = stat.header.Size()
	} else {
		return false, "stat_error"
	}
	if err = updatePersistedChatHistoryIndexWithCount(user, persistSession, mode, history, len(history)+source.target.Base, runtime, path, size, now, bot); err != nil {
		log.Printf("[CHAT_PERSIST_TAIL] session=%s index_error=true", liveSession)
		return false, "index_error"
	}
	log.Printf("[CHAT_PERSIST_TAIL] session=%s prior_messages=%d appended_messages=%d messages=%d window_base=%d", liveSession, source.persistedBody, len(tail), len(history), source.target.Base)
	return true, ""
}

// base: saved messages before history (a recent window); positions stay the file's.
func writeChatTailResumeSnapshot(path string, metadata map[string]interface{}, history []llmtypes.MessageContent, totalTurns, base int) error {
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
	projected := projectChatHistoryResumeWindow(raw, chatHistoryResumeSnapshotTurns, 0, start+base, len(history)+base, max(0, totalTurns-count))
	return writeRawFileToWorkspace(context.Background(), chatHistoryResumeSnapshotPath(path), string(boundChatHistoryResumeSnapshot(projected)))
}
