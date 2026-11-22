package server

import (
	"context"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/costledger"
)

// closedChatTitle names a Crew chat whose tab was closed: its usage stays on the Costs page, but no tab or earlier
// chat in its person's registry still holds the session, so there is no name left to show.
const closedChatTitle = "Closed chat"

// nameCostConversations gives each Crew conversation on a cost page the name people know it by: its tab's name (or
// "Main chat"), the same name for a tab's earlier chats, or "Closed chat" when the tab is gone. Rows outside Crew
// chats keep no title and the page falls back to the chat history title.
func nameCostConversations(ctx context.Context, summary *costledger.Summary) {
	if summary == nil {
		return
	}
	var rows []*costledger.ConversationAggregate
	for _, row := range summary.ByConversation {
		rows = append(rows, row)
	}
	for _, day := range summary.ByDate {
		if day == nil {
			continue
		}
		for _, row := range day.ByConversation {
			rows = append(rows, row)
		}
	}
	names := map[string]map[string]string{}
	store := defaultProductConversationRegistryStore()
	for _, row := range rows {
		if row == nil || !strings.HasPrefix(row.SessionID, "product-") {
			continue
		}
		byUser, ok := names[row.UserID]
		if !ok {
			byUser = costConversationNames(ctx, store, row.UserID)
			names[row.UserID] = byUser
		}
		if byUser == nil {
			continue
		}
		if name := byUser[row.SessionID]; name != "" {
			row.Title = name
		} else {
			row.Title = closedChatTitle
		}
	}
}

// costConversationNames maps each session in a person's registry (live tabs and their earlier chats) to its tab's
// name. nil means the registry could not be read, so no row is called closed.
func costConversationNames(ctx context.Context, store productConversationRegistryStore, userID string) map[string]string {
	document, err := store.directoryReadDocument(ctx, productConversationRegistryPath(userID))
	if err != nil {
		return nil
	}
	nameOf := func(entryKey string, record ProductConversationRecord) string {
		if name := strings.TrimSpace(document.Agents[entryKey].Name); name != "" {
			return name
		}
		if !strings.Contains(entryKey, ":") {
			return "Main chat"
		}
		return strings.TrimSpace(record.Title)
	}
	out := map[string]string{}
	for entryKey, record := range document.Entries {
		if id := strings.TrimSpace(record.SessionID); id != "" {
			out[id] = nameOf(entryKey, record)
		}
	}
	for entryKey, records := range document.Previous {
		for _, record := range records {
			if id := strings.TrimSpace(record.SessionID); id != "" && out[id] == "" {
				out[id] = nameOf(entryKey, record)
			}
		}
	}
	return out
}
