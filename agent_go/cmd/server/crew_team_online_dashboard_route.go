package server

import (
	"context"
	"fmt"

	"github.com/manishiitg/coding-agent-loop/workspace/dashboards"
)

type teamOnlineDashboardLocation struct {
	ProjectID string `json:"project_id"`
	Workspace string `json:"workspace"`
	ItemID    string `json:"item_id"`
	Document  string `json:"document"`
	Revision  string `json:"revision"`
}

// The common accepted-item mapping supplies destination and itemID. Source
// cards/routes use this normal published location, not a Mac snapshot or a
// caller-chosen DB path. The normal report token/runtime then binds buttons
// and database reads to the Team Crew. Editable source is a separate action.
func (api *StreamingAPI) teamOnlineDashboardLocation(ctx context.Context, destination triggerTarget, itemID string) (teamOnlineDashboardLocation, error) {
	location := teamOnlineDashboardLocation{}
	claims := GetUserFromContext(ctx)
	if claims == nil || destination.CrewID == "" || itemID == "" {
		return location, fmt.Errorf("this Team item is unavailable")
	}
	target, err := api.dashboardTarget(ctx, claims, destination.Path)
	if err != nil || target.ID != destination.CrewID {
		return location, fmt.Errorf("this Team item is unavailable with your current access")
	}
	published, err := dashboardWorkspaceRequest(ctx, dashboards.Request{Root: target.Root, ID: itemID, Action: "get"})
	if err != nil {
		return location, err
	}
	if published.Dashboard == nil || published.Dashboard.Published == "" {
		return location, fmt.Errorf("this Team item is not published")
	}
	location = teamOnlineDashboardLocation{ProjectID: destination.CrewID, Workspace: target.Root, ItemID: itemID, Document: published.Dashboard.DocumentPath, Revision: published.Dashboard.Published}
	return location, nil
}
