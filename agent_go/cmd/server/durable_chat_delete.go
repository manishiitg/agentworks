package server

import (
	"errors"
	"fmt"
	"log"
	pathpkg "path"
	"path/filepath"
)

// durableSessionIDsFromConversationPaths maps removed
// session-<id>-conversation.json paths to their durable journal keys.
func durableSessionIDsFromConversationPaths(paths []string) []string {
	seen := make(map[string]bool, len(paths))
	ids := make([]string, 0, len(paths))
	for _, conversationPath := range paths {
		id := chatHistorySessionIDFromFileName(pathpkg.Base(filepath.ToSlash(conversationPath)))
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	return ids
}

func (api *StreamingAPI) deleteDurableChatSessions(sessionIDs []string) error {
	if api == nil || api.eventStore == nil {
		return nil
	}
	var errs []error
	for _, sessionID := range sessionIDs {
		owner, err := api.eventStore.DurableChatOwner(sessionID)
		if err != nil {
			errs = append(errs, fmt.Errorf("read chat owner %s: %w", sessionID, err))
			continue
		}
		if owner != "" {
			if err := deleteSessionInstructions(owner, sessionID); err != nil {
				errs = append(errs, fmt.Errorf("delete chat instructions %s: %w", sessionID, err))
				continue
			}
		}
		if err := api.eventStore.DeleteDurableChatSession(sessionID); err != nil {
			errs = append(errs, fmt.Errorf("delete durable chat %s: %w", sessionID, err))
		}
	}
	return errors.Join(errs...)
}

// deleteDurableChatSessionsAfterBulkDelete runs once the transcripts are
// already gone, so a retry cannot rediscover these IDs: log instead of failing
// the request, which has already done its user-visible work.
func (api *StreamingAPI) deleteDurableChatSessionsAfterBulkDelete(scope string, sessionIDs []string) {
	for _, sessionID := range sessionIDs {
		deleteCodeSessionPin(sessionID)
	}
	if err := api.deleteDurableChatSessions(sessionIDs); err != nil {
		log.Printf("[EVENT_JOURNAL] %s removed transcripts but left durable rows: %v", scope, err)
	}
}
