package server

import (
	"context"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/livefeed"
)

// IDs only; current Crew admission is checked at delivery as well as on GET.
func publishCrewPanelChanged(project string) {
	if project != "" {
		livefeed.Default.PublishProject(livefeed.CrewPanel, project)
	}
}
func (api *StreamingAPI) crewPanelNoticeAllowed(ctx context.Context, claims *UserClaims, notice livefeed.Notice) bool {
	if notice.Project == "" {
		return true
	}
	if !externalWorkspaceAllowed(claims) {
		return false
	}
	_, _, ok := api.resolveExternalWorkspace(ctx, claims, notice.Project)
	return ok
}

// Shared-folder rounds use a remote project ID. The local panel belongs to
// LocalProject, so publish that ID, not the remote Crew's ID on the Mac feed.
func publishCrewPanelRoundChanged(dir string) {
	if binding, err := readCrewTeamLocal(dir); err == nil {
		publishCrewPanelChanged(binding.LocalProject)
	}
}

// A committed workspace write already passed its existing file guard. Resolve
// its registered Crew ID rather than placing any private path on the feed.
func publishCrewPanelPathChanged(filePath string) {
	parts := strings.Split(strings.Trim(filePath, "/"), "/")
	if len(parts) > 1 && parts[0] == "Crew" {
		if record, ok := defaultProjectOwners().Lookup("work", parts[1]); ok {
			publishCrewPanelChanged(record.ProjectID)
		}
	} else if len(parts) > 2 && parts[0] == "_system" && parts[1] == "crew-data" {
		publishCrewPanelChanged(parts[2])
	}
}
func publishCrewPanelForPerson(person string) {
	livefeed.Default.PublishToUser(livefeed.CrewPanel, person, "")
}
