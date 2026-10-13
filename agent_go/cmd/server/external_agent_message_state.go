package server

import (
	"fmt"
	"net/http"
)

// This projection is scoped to a freshly authorized target owner. Readers of a
// shared Crew cannot inspect its other callers, and token target bounds apply.
func (api *StreamingAPI) externalAgentMessageState(w http.ResponseWriter, r *http.Request, args map[string]any) {
	claims := GetUserFromContext(r.Context())
	target, err := api.externalMessageTargetForOperation(r.Context(), claims, args, false)
	if err != nil {
		externalError(w, 404, "not_found", "Message state is unavailable or access denied.")
		return
	}
	owned := target.Kind == triggerCallerCrew && target.ownerOr(claims.UserID) == claims.UserID
	if target.Kind == triggerCallerWorkflow {
		owned = workflowAccessForManifest(claims, target.Manifest) == WorkflowAccessOwner
	}
	if !owned {
		externalError(w, 404, "not_found", "Message state is unavailable or access denied.")
		return
	}
	matches := func(e agentMessageEndpoint) bool {
		return !e.External && e.Kind == target.Kind && e.ID == target.stampID() && canonicalCrewWorkspaceRoot(e.Path) == canonicalCrewWorkspaceRoot(targetMessageEndpoint(claims.UserID, target).Path)
	}
	store, err := readAgentMessageSnapshot(r.Context())
	if err != nil {
		externalError(w, 502, "workspace_unavailable", "Cannot read message state.")
		return
	}
	after, limit := externalInt(args, "after", 0), externalInt(args, "limit", 30)
	if after < 0 {
		after = 0
	}
	if limit < 1 {
		limit = 30
	}
	if limit > 100 {
		limit = 100
	}
	rows := []map[string]any{}
	for _, c := range store.Conversations {
		side := -1
		for i, e := range c.Endpoints {
			if matches(e) {
				side = i
				break
			}
		}
		if side < 0 {
			continue
		}
		messages := []map[string]any{}
		start := len(c.Messages) - 20
		if start < 0 {
			start = 0
		}
		for _, m := range c.Messages[start:] {
			messages = append(messages, map[string]any{"message_id": m.ID, "sequence": m.Sequence, "delivery": m.Delivery, "delivery_error": m.Error, "sent_at": m.SentAt, "incoming": m.From != side, "wakeup": m.Wakeup})
		}
		rows = append(rows, map[string]any{"inbox_id": c.ID, "session_id": c.Endpoints[side].Session, "chat_key": c.Endpoints[side].ChatKey, "messages": messages, "message_count": len(c.Messages), "older_messages_omitted": start > 0})
	}
	total := len(rows)
	if after > total {
		after = total
	}
	end := after + limit
	if end > total {
		end = total
	}
	wakeups := []map[string]any{}
	for _, wake := range store.Wakeups {
		if matches(wake.Endpoint) {
			wakeups = append(wakeups, map[string]any{"wakeup_id": wake.ID, "session_id": wake.Endpoint.Session, "status": wake.Status, "due_at": wake.Due})
		}
	}
	// A detach/reassignment during the read cannot release an owner projection.
	current, e := api.externalMessageTargetForOperation(r.Context(), claims, args, false)
	stillOwned := e == nil && current.Kind == target.Kind && current.stampID() == target.stampID()
	if stillOwned && current.Kind == triggerCallerCrew {
		stillOwned = current.ownerOr(claims.UserID) == claims.UserID
	}
	if stillOwned && current.Kind == triggerCallerWorkflow {
		stillOwned = workflowAccessForManifest(claims, current.Manifest) == WorkflowAccessOwner
	}
	if !stillOwned {
		externalError(w, 404, "not_found", "Message state is unavailable or access denied.")
		return
	}
	externalJSON(w, map[string]any{"conversations": rows[after:end], "next_cursor": end, "has_more": end < total, "total": total, "wakeups": wakeups, "note": fmt.Sprintf("Owner-only delivery metadata for %s; message bodies are excluded.", target.Label)})
}
