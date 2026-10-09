package server

import (
	"context"
	"strings"

	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
)

// humanFeedbackVisibleTo reports whether a person may see and answer one
// pending agent question: it must come from their own chat or run, or from a
// run of a workflow they own or edit (a scheduled run). Fails closed: a
// question whose session is unknown is hidden. Single-user installs see all.
func (api *StreamingAPI) humanFeedbackVisibleTo(ctx context.Context, request virtualtools.HumanFeedbackRequest) bool {
	if !IsMultiUserMode() {
		return true
	}
	claims := GetUserFromContext(ctx)
	if claims == nil || strings.TrimSpace(claims.UserID) == "" {
		return false
	}
	sessionID := strings.TrimSpace(request.SessionID)
	if sessionID == "" {
		return false
	}
	api.activeSessionsMux.RLock()
	session, exists := api.activeSessions[sessionID]
	var owner, workspace string
	if exists && session != nil {
		owner, workspace = session.UserID, session.WorkspacePath
	}
	api.activeSessionsMux.RUnlock()
	if !exists {
		return false
	}
	if owner != "" && owner == claims.UserID {
		return true
	}
	if strings.TrimSpace(workspace) == "" {
		return false
	}
	level, manifest := workflowAccessForWorkspacePath(ctx, claims, workspace)
	return manifest != nil && (level == WorkflowAccessOwner || level == WorkflowAccessWrite)
}

// visibleHumanFeedback filters pending questions to those the caller may see.
func (api *StreamingAPI) visibleHumanFeedback(ctx context.Context, requests []virtualtools.HumanFeedbackRequest) []virtualtools.HumanFeedbackRequest {
	out := make([]virtualtools.HumanFeedbackRequest, 0, len(requests))
	for _, request := range requests {
		if api.humanFeedbackVisibleTo(ctx, request) {
			out = append(out, request)
		}
	}
	return out
}
