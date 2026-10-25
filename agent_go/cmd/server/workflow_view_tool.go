package server

import (
	"fmt"
	"strings"

	orchestratorevents "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/events"
)

// workflowWorkspaceViews mirrors the toolbar's view registry in
// frontend/src/components/workflow/workspaceViews.ts (a frontend test keeps
// the two lists identical). The agent opens one of these for the user with
// open_workspace_view; the workflow page switches the right-hand pane.
var workflowWorkspaceViews = func() []struct{ ID, Label, About string } {
	views := make([]struct{ ID, Label, About string }, 0, len(uiControlContract.Views))
	for _, view := range uiControlContract.Views {
		about := view.Label
		views = append(views, struct{ ID, Label, About string }{view.ID, view.Label, about})
	}
	return views
}()

// WorkflowViewPresentationKind is the presentation kind the workflow page
// reacts to by opening a workspace view.
const WorkflowViewPresentationKind = "workflow.view"

func workflowWorkspaceViewIDs() []string {
	ids := make([]string, 0, len(workflowWorkspaceViews))
	for _, v := range workflowWorkspaceViews {
		ids = append(ids, v.ID)
	}
	return ids
}

// workspaceViewPresentation is the event that opens a view for the user.
func workspaceViewPresentation(view, workspacePath string) (*orchestratorevents.PresentationUpdatedEvent, error) {
	return workspaceViewAction(view, workspacePath, "open", "")
}

// workspaceViewAction builds the open or refresh event for a view; the page
// reads payload.action to tell them apart, and payload.target for what to
// focus inside the view.
func workspaceViewAction(view, workspacePath, action, target string) (*orchestratorevents.PresentationUpdatedEvent, error) {
	view = strings.TrimSpace(strings.ToLower(view))
	for _, v := range workflowWorkspaceViews {
		if v.ID != view {
			continue
		}
		label := "Open requested"
		if action == "refresh" {
			label = "Refresh requested"
		}
		detail := v.Label
		payload := map[string]interface{}{"view": v.ID, "action": action}
		if target = strings.TrimSpace(target); target != "" {
			payload["target"] = target
			detail = v.Label + " · " + target
		}
		return &orchestratorevents.PresentationUpdatedEvent{
			PresentationID: "workspace-view:" + v.ID + ":" + action,
			Kind:           WorkflowViewPresentationKind,
			Title:          v.Label,
			WorkspacePath:  workspacePath,
			Payload:        payload,
			Activity:       &orchestratorevents.PresentationActivity{Label: label, Destination: "the workspace pane", Detail: detail},
		}, nil
	}
	return nil, fmt.Errorf("unknown view %q; one of: %s", view, strings.Join(workflowWorkspaceViewIDs(), ", "))
}

// registerOpenWorkspaceViewTool gives the workflow agent the toolbar: it can
// open any workspace view on the right for the user (the report after
// building it, the costs when asked about spend, or the Automation center
// after adding a schedule, trigger, or bot) instead of describing where to click.
func (api *StreamingAPI) registerOpenWorkspaceViewTool(registrar definitionToolRegistrar, sessionID, workspacePath string) error {
	return api.registerUIControlTools(registrar, sessionID, workspacePath)
}
