package server

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"time"

	"github.com/manishiitg/coding-agent-loop/workspace/chatlog"
)

// Only called after the context route resolved this person's own saved tab.
// Do not use the turn's resume resolver here: that loads every message just to
// display the provider/model/native handle in the composer's context controls.
func chatContextRuntimeMetadata(owner string, record ProductConversationRecord) *ChatHistoryAgentRuntime {
	started := time.Now()
	found := false
	defer func() {
		log.Printf("[CHAT_CONTEXT_RUNTIME] session=%s elapsed_ms=%d found=%t", record.SessionID, time.Since(started).Milliseconds(), found)
	}()
	workspace := productConversationRuntimeWorkspace(owner, normalizeConversationWorkspace(record.WorkspacePath))
	// Match the existing history gate before any header read. A Run person's
	// settings must not expose the owner's private Builder runtime metadata.
	if crewBuilderDiskHiddenFromUser(owner, workspace) {
		return nil
	}
	path, ok, err := FindChatHistoryConversationPathForSession(owner, record.SessionID, workspace)
	if err != nil || !ok {
		return nil
	}
	data, err := chatlog.RuntimeMetadata(resolvedChatHistoryPath(chatHistoryStoragePath(path)))
	if os.IsNotExist(err) {
		content, exists, readErr := readFileFromWorkspaceQuery(context.Background(), chatHistoryStoragePath(path), "runtime_metadata=true")
		if readErr != nil || !exists {
			return nil
		}
		data, err = []byte(content), nil
	}
	if err != nil {
		return nil
	}
	var metadata struct {
		Runtime *ChatHistoryAgentRuntime `json:"runtime"`
		Mode    string                   `json:"workshop_mode"`
	}
	if json.Unmarshal(data, &metadata) != nil || metadata.Runtime == nil {
		return nil
	}
	if metadata.Runtime.WorkshopMode == "" {
		metadata.Runtime.WorkshopMode = normalizeChatHistoryWorkshopMode(metadata.Mode)
	}
	normalizeChatHistoryRuntime(metadata.Runtime)
	found = true
	return metadata.Runtime
}
