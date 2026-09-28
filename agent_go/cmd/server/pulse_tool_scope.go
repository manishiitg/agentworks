package server

import (
	"context"
	"fmt"
	"path"
	"strings"

	"github.com/manishiitg/mcpagent/executor"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	step "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
)

// pulseToolScope is the workflow and principal a Pulse platform tool
// (search_platform, ask_platform_crew, read_crew_calls) acts for. Both come
// from trusted session state, never from the model's arguments:
//   - the calling session is the one the MCP bridge (or the in-process agent)
//     attached, not an argument;
//   - the workflow is that session's own: a background agent's tool session
//     maps to the workflow and chat that started it (workshop_tool_sessions.go),
//     and a Builder chat session maps to its workshop;
//   - the principal is that chat session's owner; claims on the context must
//     match it, and there is no fallback to a workflow owner or the default
//     user;
//   - that principal must still have access to the workflow (write for work
//     that acts, read otherwise).
//
// Anything else is refused, so a run of workflow A can never act for workflow
// B or as B's owner (reported 2026-09-27 against 4f84023ed / e6682ff73).
func (api *StreamingAPI) pulseToolScope(ctx context.Context, requestedWorkspace string, needWrite bool) (string, *UserClaims, error) {
	if api == nil {
		return "", nil, fmt.Errorf("platform tools are unavailable in this process")
	}
	sessionID := strings.TrimSpace(executor.SessionIDFromContext(ctx))
	if sessionID == "" {
		sessionID, _ = ctx.Value(common.ChatSessionIDKey).(string)
		sessionID = strings.TrimSpace(sessionID)
	}
	if sessionID == "" {
		return "", nil, fmt.Errorf("this tool is only available inside a workflow session")
	}

	workspacePath, chatSessionID := "", ""
	if owner, ok := step.LookupWorkshopToolSession(sessionID); ok {
		workspacePath, chatSessionID = owner.WorkspacePath, owner.ChatSessionID
	} else if value, ok := api.workshopChatSessions.Load(sessionID); ok {
		if workshop, ok := value.(interface{ GetConfig() *step.WorkshopConfig }); ok && workshop.GetConfig() != nil {
			workspacePath, chatSessionID = workshop.GetConfig().WorkspacePath, sessionID
		}
	}
	workspacePath = strings.Trim(strings.TrimSpace(workspacePath), "/")
	if workspacePath == "" || workspacePath != path.Clean(workspacePath) || strings.Contains(workspacePath, "..") {
		return "", nil, fmt.Errorf("this session has no workflow to act for")
	}
	if requested := strings.Trim(strings.TrimSpace(requestedWorkspace), "/"); requested != "" && path.Clean(requested) != workspacePath {
		return "", nil, fmt.Errorf("workspace_path must be this workflow (%s); a Pulse tool cannot act for another workflow", workspacePath)
	}

	userID := ""
	if api.eventStore != nil && chatSessionID != "" {
		userID = strings.TrimSpace(api.eventStore.GetSessionOwner(chatSessionID))
	}
	if claims := GetUserFromContext(ctx); claims != nil && strings.TrimSpace(claims.UserID) != "" {
		if userID != "" && strings.TrimSpace(claims.UserID) != userID {
			return "", nil, fmt.Errorf("caller identity does not match this session's owner")
		}
		userID = strings.TrimSpace(claims.UserID)
	}
	if userID == "" {
		return "", nil, fmt.Errorf("this session has no authenticated owner")
	}
	claims := &UserClaims{UserID: userID}
	if record := directoryUserFor(userID, "", ""); record != nil {
		claims.Username, claims.Email = record.Username, record.Email
	}
	if access := userAccessForClaims(claims); access.Disabled || directoryUserIsUnknown(claims) {
		return "", nil, fmt.Errorf("this session's account is unavailable")
	}

	manifest, exists, err := ReadWorkflowManifest(ctx, workspacePath)
	if err != nil || !exists {
		return "", nil, fmt.Errorf("cannot read this session's workflow")
	}
	switch workflowAccessForManifest(claims, manifest) {
	case WorkflowAccessNone:
		return "", nil, fmt.Errorf("this session's owner no longer has access to the workflow")
	case WorkflowAccessRead:
		if needWrite {
			return "", nil, fmt.Errorf("this session's owner can only read the workflow")
		}
	}
	return workspacePath, claims, nil
}
