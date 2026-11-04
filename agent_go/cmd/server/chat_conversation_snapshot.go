package server

import (
	"context"
	"encoding/json"
	"fmt"

	internalevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
)

// persistRawConversationSnapshot is the whole-record writer that Goals builder
// chats still use (Crew and home chats are chat folders, PLAT-856-521). It
// preserves canonical rows accepted/recovered since their snapshot was read.
func persistRawConversationSnapshot(ctx context.Context, path, content string) error {
	lock := chatConversationMutex(path)
	lock.Lock()
	defer lock.Unlock()
	var incoming map[string]interface{}
	var next builderConversationLog
	if err := json.Unmarshal([]byte(content), &incoming); err != nil {
		return err
	}
	if err := json.Unmarshal([]byte(content), &next); err != nil {
		return err
	}
	raw, exists, err := readFileFromWorkspace(ctx, path)
	if err != nil {
		return err
	}
	if exists {
		var previous map[string]interface{}
		var old builderConversationLog
		if err := json.Unmarshal([]byte(raw), &previous); err != nil {
			return err
		}
		if err := json.Unmarshal([]byte(raw), &old); err != nil {
			return err
		}
		if old.SessionID != "" && next.SessionID != old.SessionID {
			return fmt.Errorf("conversation snapshot session mismatch")
		}
		if old.UserID != "" && next.UserID != "" && next.UserID != old.UserID {
			return fmt.Errorf("conversation snapshot owner mismatch")
		}
		if old.UserID != "" && next.UserID == "" {
			incoming["user_id"] = old.UserID
		}
		merged, err := mergeChatConversationSnapshots(previous, incoming)
		if err != nil {
			return err
		}
		incoming["conversation_history"] = merged
		var oldEvents, nextEvents []internalevents.Event
		oldEventJSON, _ := json.Marshal(previous["ui_events"])
		nextEventJSON, _ := json.Marshal(incoming["ui_events"])
		if json.Unmarshal(oldEventJSON, &oldEvents) == nil && json.Unmarshal(nextEventJSON, &nextEvents) == nil {
			if events := trimChatHistoryUIEvents(mergeChatHistoryUIEvents(oldEvents, nextEvents)); len(events) > 0 {
				incoming["ui_events"] = events
			}
		}
		for key, value := range previous {
			if _, ok := incoming[key]; !ok {
				incoming[key] = value
			}
		}
		incoming["revision"] = previous["revision"]
	}
	advanceChatConversationRevision(incoming)
	encoded, err := json.MarshalIndent(incoming, "", "  ")
	if err != nil {
		return err
	}
	if err := writeRawFileToWorkspace(ctx, path, string(encoded)); err != nil {
		return err
	}
	return writeChatHistoryResumeSnapshot(ctx, path, encoded)
}
