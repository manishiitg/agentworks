package server

import (
	"net/http"
	"time"
)

// Only the exact receiving app chat can publish its own no-progress state.
// This is display/continuation metadata, never message or execution authority.
func (api *StreamingAPI) recordAppTaskStalled(w http.ResponseWriter, r *http.Request, args map[string]interface{}) {
	claims := GetUserFromContext(r.Context())
	a, _, err := currentAgentAttachment(r.Context(), claims, externalArg(args, "agent_name"), externalArg(args, "attachment_id"))
	if err != nil || a.Attachment == nil || a.Attachment.DeliveryRoute != appAgentDeliveryRoute {
		externalError(w, 403, "attachment_refused", "Exact app recipient required.")
		return
	}
	agentMessageMu.Lock()
	defer agentMessageMu.Unlock()
	store, err := readAgentMessageStore(r.Context())
	if err != nil {
		externalError(w, 503, "store_unavailable", "Task state unavailable.")
		return
	}
	found := false
	for i := range store.Conversations {
		c := &store.Conversations[i]
		if c.ID != externalArg(args, "inbox_id") {
			continue
		}
		for j := range c.Messages {
			m := &c.Messages[j]
			dest := c.Endpoints[1-m.From]
			if m.ID != externalArg(args, "message_id") || m.MessageType != "request" || dest.NamedAgentID != a.ID || dest.UserID != claims.UserID {
				continue
			}
			if api.recheckAgentMessageAdmission(r.Context(), *c, *m) != nil || api.externalAuthorizeConversationPeer(r.Context(), claims, *c, 1-m.From, false) != nil {
				externalError(w, 403, "task_refused", "This request is no longer admitted.")
				return
			}
			state, _ := agentTaskState(*c, *m, time.Now())
			if state == "done" || state == "blocked" || state == "needs_you" {
				found = true
				break
			}
			m.TaskProgress.NoProgress = 2
			m.TaskProgress.Status = "stalled"
			found = true
		}
	}
	if !found {
		externalError(w, 404, "task_unavailable", "Request unavailable.")
		return
	}
	if saveAgentMessageStore(r.Context(), store) != nil {
		externalError(w, 503, "store_unavailable", "Task state unavailable.")
		return
	}
	go api.tellAskerTaskStalled(externalArg(args, "inbox_id"), externalArg(args, "message_id"), agentTaskStallNoProgress)
	externalJSON(w, map[string]bool{"saved": true})
}
