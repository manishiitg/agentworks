package server

import (
	"context"
	"time"
)

// Only aggregate state leaves this function, after the caller admitted the
// source Team row. No asker, task excerpt, private chat or thread ID is exposed.
func crewAgentRequestState(ctx context.Context, row crewTeamRegisteredAgent) string {
	store, err := readAgentMessageSnapshot(ctx)
	if err != nil {
		return ""
	}
	result := ""
	for _, c := range store.Conversations {
		for _, task := range c.Messages {
			if task.MessageType != "request" || task.Kind != "" {
				continue
			}
			dest := c.Endpoints[1-task.From]
			matches := dest.UserID == row.OwnerID && (dest.TeamAgentID == row.ID || dest.ChatKey != "" && dest.ChatKey == row.ChatKey || row.Terminal != nil && dest.NamedAgentID == row.Terminal.NamedAgentID)
			if !matches {
				continue
			}
			state, _ := agentTaskState(c, task, time.Now())
			switch state {
			case "needs_owner":
				return state
			case "blocked":
				result = state
			case "stalled":
				if result == "" {
					result = state
				}
			}
		}
	}
	return result
}
