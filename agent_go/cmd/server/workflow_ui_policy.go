package server

import (
	"path"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowtypes"
)

// UI control is an interactive host capability, not a WorkshopMode or write
// permission. Schedules deliberately use workflow-builder too, and read-only
// human Builders use run mode, so neither phase nor mode alone is sufficient.
// Decide at definition construction, before the CLI snapshots its tool catalog;
// do not bolt a second per-turn tool-name allowlist onto get_api_spec.
func workflowUICallerAllowed(phase, session string, req QueryRequest, active *ActiveSessionInfo) bool {
	if phase != workflowtypes.WorkflowStatusWorkflowBuilder || strings.TrimSpace(req.AgentProfileID) != "" {
		return false
	}
	policy := resolveWorkflowChatPolicy(session, req, active, false)
	// Builder authority is broader than control of the user's live workspace UI.
	// Headless callers can author and maintain workflow state, but only an
	// interactive human turn may receive a UI-control lease.
	if policy.Origin != "interactive" {
		return false
	}
	return policy.allows("workspace_ui")
}

func (api *StreamingAPI) registerWorkflowUIForCaller(registrar definitionToolRegistrar, phase, session, workspace string, req QueryRequest, readOnly bool) error {
	active, _ := api.getActiveSession(session)
	if phase == workflowtypes.WorkflowStatusWorkflowBuilder && req.AgentProfileID == "" {
		policy := resolveWorkflowChatPolicy(session, req, active, readOnly)
		if policy.allows("workflow_suggestions") {
			if err := api.registerWorkflowSuggestionTool(registrar, session, workspace); err != nil {
				return err
			}
		}
		if policy.allows("bot_management") {
			if err := api.registerSlackBotTools(registrar, session, workspace, "", policy.Origin == "interactive" && !readOnly && policy.Mode == "builder", !readOnly); err != nil {
				return err
			}
		}
	}

	if !workflowUICallerAllowed(phase, session, req, active) {
		// Invalidates any old lease if the same session changes role. The new
		// definition has none of the six UI tools; no bindings can revive it.
		api.uiBroker().setScope(session, "")
		return nil
	}
	if err := api.registerOpenWorkspaceViewTool(registrar, session, workspace); err != nil {
		return err
	}
	// Viewing the workspace does not grant connection-editing authority.
	policy := resolveWorkflowChatPolicy(session, req, active, readOnly)
	if !policy.allows("secret_management") {
		return nil
	}
	return api.registerGmailConnectionManagementTools(registrar, session, workspace)
}

// restoredWorkflowUIScopeFor reconstructs the workflow a live Builder chat belongs to when the
// server restarted and the chat has not had a turn since: the scope is otherwise set only when
// the chat's tools are registered at a turn, so until then the page's panel cannot connect
// (unsupported_surface) and the agent sees browser_disconnected. It restores for exactly the
// callers that would have received the UI tools (an interactive Builder chat); who may bind is
// still checked by the route. Empty when the session is not such a chat.
func restoredWorkflowUIScopeFor(session string, active *ActiveSessionInfo) string {
	if active == nil || active.AgentMode != "workflow_phase" {
		return ""
	}
	workspace := path.Clean(strings.Trim(strings.TrimSpace(active.WorkspacePath), "/"))
	if !strings.HasPrefix(workspace, "Workflow/") {
		return ""
	}
	req := QueryRequest{TriggeredBy: active.TriggeredBy}
	if !workflowUICallerAllowed(workflowtypes.WorkflowStatusWorkflowBuilder, session, req, active) {
		return ""
	}
	return workspace
}
