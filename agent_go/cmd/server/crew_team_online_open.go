package server

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	"github.com/manishiitg/coding-agent-loop/workspace/dashboards"
)

type teamOnlineOpenResult struct {
	Found      bool                         `json:"found"`
	Location   *teamOnlineDashboardLocation `json:"location,omitempty"`
	Connection string                       `json:"connection,omitempty"`
	ShortPath  string                       `json:"short_path,omitempty"`
	ResourceID string                       `json:"resource_id,omitempty"`
	Notice     string                       `json:"notice,omitempty"`
}

const teamOnlineUnreachableNotice = "Couldn't reach your Team's server, so this is the copy on this computer."

// A missing answer cannot take away the person's normal access to their own
// files. An actual server refusal, malformed answer or local binding error
// never becomes an offline fallback. No destination is trusted from a cache.
func teamOnlineOpenFailure(ctx context.Context, source string, err error) (teamOnlineOpenResult, error) {
	var unreachable *crewRemoteTransportError
	if !errors.As(err, &unreachable) {
		return teamOnlineOpenResult{}, err
	}
	log.Printf("[TEAM_ONLINE] event=open result=local_unreachable person=%s source_project=%s", GetUserIDFromContext(ctx), source)
	return teamOnlineOpenResult{Notice: teamOnlineUnreachableNotice}, nil
}

// Resolve Open, never authoring: the editable source and explicit revision
// previews stay on the source computer. A live Open uses the normal destination
// page, so its entire report runtime (including DB actions) uses the Team DB.
func (api *StreamingAPI) teamOnlineOpen(ctx context.Context, workspace, source, document string) (teamOnlineOpenResult, error) {
	out := teamOnlineOpenResult{}
	claims := GetUserFromContext(ctx)
	if claims == nil || claims.Scope != "" || !dashboardScopeAllowed(claims, "get") {
		return out, fmt.Errorf("sign in with dashboard access to open this item")
	}
	parts := strings.Split(document, "/")
	if len(parts) != 5 || strings.Join(parts[:3], "/") != "db/reports/managed" || parts[4] != "index.html" || !teamOnlineIdentifier.MatchString(parts[3]) {
		return out, nil
	}
	if source == "" {
		target, err := api.dashboardTarget(ctx, claims, workspace)
		if err != nil {
			return out, err
		}
		if target.Kind != "crew" {
			return out, nil
		}
		source = target.ID
		if IsLocalMode() {
			binding, err := crewroot.ReadSync(source)
			if err != nil {
				return out, err
			}
			if binding == nil || binding.Side != "local" || binding.ConnectionID == "" {
				return out, nil
			}
			connection, err := desktopConnectionFor(claims.UserID, binding.ConnectionID)
			if err != nil {
				return teamOnlineOpenFailure(ctx, source, &crewRemoteTransportError{Message: "the saved Team connection is unavailable"})
			}
			// Only the selector crosses the saved connection, never a source path or
			// caller-supplied destination. The server rechecks its own Team records.
			err = crewTeamRemoteJSON(ctx, claims.UserID, connection, http.MethodPost, "/api/dashboards", map[string]string{"action": "online_location", "source_project_id": source, "document_path": document}, &out)
			if err != nil {
				return teamOnlineOpenFailure(ctx, source, err)
			}
			if out.Found {
				out.Connection = binding.ConnectionID
			}
			return out, nil
		}
	} else if IsLocalMode() {
		return out, fmt.Errorf("open the item from its source Crew")
	}
	if !teamOnlineIdentifier.MatchString(source) {
		return out, fmt.Errorf("choose the source Crew")
	}
	root, err := crewroot.StateRoot()
	if err != nil {
		return out, err
	}
	teams, err := os.ReadDir(filepath.Join(root, "team-folders"))
	if os.IsNotExist(err) {
		return out, nil
	}
	if err != nil {
		return out, fmt.Errorf("the Team item location could not be read")
	}
	for _, entry := range teams {
		if !entry.IsDir() {
			continue
		}
		team, err := readCrewTeam(entry.Name())
		if os.IsNotExist(err) {
			continue
		} // Other Team state can exist before Team setup.
		if err != nil {
			return out, fmt.Errorf("the Team item location could not be read")
		}
		if team.RosterSource != "" || team.OnlineHome == "" || !team.member(claims.UserID) {
			continue
		}
		// Inspect only this person's Teams. The accepted lookup rechecks On,
		// membership, normal destination access and the protected stable mapping.
		destination := triggerTarget{CrewID: team.OnlineHome}
		mappings, err := readTeamOnlineMappings(ctx, destination)
		if err != nil {
			return out, fmt.Errorf("the Team item location could not be read")
		}
		for _, mapping := range mappings {
			if mapping.SourceProjectID != source || mapping.ItemID != parts[3] || (mapping.Kind != "dashboard" && mapping.Kind != "artifact") {
				continue
			}
			if out.Found {
				return teamOnlineOpenResult{}, fmt.Errorf("this item is on more than one Team; open it from the Team you want")
			}
			resolved, err := api.teamOnlineMappedOpen(ctx, team.ProjectID, source, mapping.Kind, mapping.ItemID)
			if err != nil {
				return out, err
			}
			out = resolved
		}
	}
	return out, nil
}

// A row selected from a particular Team does not search another Team or trust
// a destination path from the browser. Mapping, membership and access are fresh.
func (api *StreamingAPI) teamOnlineMappedOpen(ctx context.Context, team, source, kind, item string) (teamOnlineOpenResult, error) {
	out := teamOnlineOpenResult{}
	claims := GetUserFromContext(ctx)
	if IsLocalMode() || !dashboardReadPurposeAllowed(claims) {
		return out, fmt.Errorf("sign in with dashboard access to open this item")
	}
	if (kind != "dashboard" && kind != "artifact") || !teamOnlineIdentifier.MatchString(team) || !teamOnlineIdentifier.MatchString(source) || !teamOnlineIdentifier.MatchString(item) {
		return out, fmt.Errorf("choose a published Team item")
	}
	location, found, err := api.teamOnlineAcceptedLocation(ctx, team, source, kind, item)
	if err != nil {
		return out, err
	}
	if !found {
		return out, fmt.Errorf("this Team item is unavailable")
	}
	target, err := api.dashboardTarget(ctx, claims, location.Workspace)
	if err != nil {
		return out, err
	}
	published, err := dashboardWorkspaceRequest(ctx, dashboards.Request{Root: target.Root, ID: location.ItemID, Action: "get", Guard: dashboardFileGuard(claims)})
	if err != nil {
		return out, err
	}
	if published.Dashboard == nil {
		return out, fmt.Errorf("this Team item is unavailable")
	}
	row := map[string]any{}
	addDashboardQuestionResource(ctx, target, *published.Dashboard, row)
	shortPath, _ := row["short_path"].(string)
	if !strings.HasPrefix(shortPath, "/d/") || !validDashboardShortCode(strings.TrimPrefix(shortPath, "/d/")) {
		return out, fmt.Errorf("the Team dashboard link is unavailable; try again")
	}
	log.Printf("[TEAM_ONLINE] event=open person=%s team=%s source_project=%s destination=%s item_id=%s", claims.UserID, team, source, location.ProjectID, location.ItemID)
	resourceID, _ := row["resource_id"].(string)
	return teamOnlineOpenResult{Found: true, Location: &location, ShortPath: shortPath, ResourceID: resourceID}, nil
}
