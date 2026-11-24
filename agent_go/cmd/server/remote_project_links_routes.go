package server

import (
	"encoding/json"
	"net/http"
	"strings"
)

// The Linked projects panel's rows for links to a project on the other machine (#3663, #3664). The same reads and
// removal as the chat tools (attach_workflow_reference / detach_workflow_reference with project_id).
type remoteProjectLinkRow struct {
	ProjectID string `json:"project_id"`
	Name      string `json:"name"`
	Where     string `json:"where"`
	CanRemove bool   `json:"can_remove"`
}

func (api *StreamingAPI) handleRemoteProjectLinks(w http.ResponseWriter, r *http.Request) {
	userID := GetUserIDFromContext(r.Context())
	var in struct {
		WorkspacePath string `json:"workspace_path"`
		ProjectID     string `json:"project_id"`
	}
	if r.Method == http.MethodPost {
		r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
		if json.NewDecoder(r.Body).Decode(&in) != nil {
			externalError(w, 400, "invalid_arguments", "Give the project and the link to remove.")
			return
		}
	} else {
		in.WorkspacePath = r.URL.Query().Get("workspace_path")
	}
	workspace := strings.TrimRight(strings.TrimSpace(in.WorkspacePath), "/")
	if userID == "" || workspace == "" || !isProjectWorkspacePath(workspace) || !crewProjectOwnedByCaller(userID, workspace) {
		externalError(w, 403, "forbidden", "Only this project's owner or co-owners see and change its links.")
		return
	}
	if r.Method == http.MethodPost {
		line, err := unlinkRemoteProject(r.Context(), userID, workspace, in.ProjectID)
		if err != nil {
			externalError(w, 400, "unlink_failed", err.Error())
			return
		}
		externalJSON(w, map[string]any{"note": line})
		return
	}
	links, err := readRemoteProjectLinks(r.Context(), workspace)
	if err != nil {
		externalError(w, 503, "links_unavailable", "Linked projects can't be read now. Try again in a minute.")
		return
	}
	rows := []remoteProjectLinkRow{}
	for _, link := range links {
		row := remoteProjectLinkRow{ProjectID: link.ProjectID, Name: firstNonEmptyTrimmed(link.Name, "Project"), Where: "on a Mac", CanRemove: true}
		if link.Server != "" {
			row.Where = "on a server this Mac is no longer signed in to"
			if conn, err := desktopConnectionFor(userID, link.Server); err == nil {
				row.Where = "on " + firstNonEmptyTrimmed(conn.Name, conn.Server)
			}
		}
		rows = append(rows, row)
	}
	externalJSON(w, map[string]any{"links": rows})
}
