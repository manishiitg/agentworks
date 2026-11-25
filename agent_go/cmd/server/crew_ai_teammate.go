package server

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/gorilla/mux"
)

// The AI teammate switch (owner, 25 Nov): a project on the server is offered to Teams as an AI teammate only when its
// owner or a co-owner turns this on. Off by default. Turning it off takes the project out of every Team it is in. Who
// adds it to a Team is unchanged (crewTeamProjectChangeRefusal).

// crewAITeammateView is the switch as the project's Access page shows it.
type crewAITeammateView struct {
	On        bool   `json:"on"`
	CanChange bool   `json:"can_change"`
	Line      string `json:"line"`
}

// crewProjectAITeammate says whether Teams may add the server project as an AI teammate.
func crewProjectAITeammate(project string) bool {
	if IsLocalMode() || !crewLivesOnServer(project) {
		return false
	}
	rec, ok := crewOwnerRecordAt(crewProjectRootByID(project))
	return ok && rec.AITeammate
}

// crewAITeammateFor is the Access page's view of the switch, or nil when the project is not a server project.
func crewAITeammateFor(project, viewer string) *crewAITeammateView {
	if IsLocalMode() || !crewLivesOnServer(project) {
		return nil
	}
	root := crewProjectRootByID(project)
	v := &crewAITeammateView{On: crewProjectAITeammate(project), CanChange: root != "" && crewProjectOwnedByCaller(viewer, root)}
	switch n := len(crewProjectInTeams(project, viewer)); {
	case !v.On:
		v.Line = "Off · no Team can add it."
	case n == 0:
		v.Line = "On · your Teams can add it."
	case n == 1:
		v.Line = "On · in 1 Team."
	default:
		v.Line = fmt.Sprintf("On · in %d Teams.", n)
	}
	return v
}

// setCrewProjectAITeammate is the one place the switch changes (Access page and the project's chat). Turning it off
// takes the project out of every Team it is in; a Team that refuses is named in the returned note.
func setCrewProjectAITeammate(project, user string, on bool) (int, string, string, string) {
	root := crewProjectRootByID(project)
	if IsLocalMode() || root == "" || !crewLivesOnServer(project) {
		return http.StatusConflict, "server_only", "Only a project on the server can be an AI teammate.", ""
	}
	if !crewProjectOwnedByCaller(user, root) {
		return http.StatusForbidden, "ai_teammate_forbidden", "Only this project's owner or a co-owner can change this.", ""
	}
	id, ok := projectIdentityOf(root)
	if !ok {
		return http.StatusConflict, "project_unavailable", "This project can't be found now. Try again in a minute.", ""
	}
	if _, err := defaultProjectOwners().SetAITeammate(id.Product, id.Folder, on); err != nil {
		return http.StatusConflict, "ai_teammate_save_failed", "It didn't save. Try again.", ""
	}
	left, failed := 0, []string{}
	if !on {
		for _, in := range crewProjectInTeams(project, user) {
			if _, _, _, line := changeCrewTeamProject(in.TeamID, user, "remove", project); line != "" {
				failed = append(failed, in.Name)
				continue
			}
			left++
		}
	}
	log.Printf("[CREW_TEAM] project=%s actor=%s event=ai_teammate on=%t left_teams=%d failed=%d", project, logUsernameForUserID(user), on, left, len(failed))
	if len(failed) > 0 {
		return http.StatusOK, "", "", "It is off, but it is still in " + strings.Join(failed, ", ") + ". Take it out there with Remove from Team."
	}
	return http.StatusOK, "", "", ""
}

// handleCrewAITeammate is POST /crew-teams/{project}/ai-teammate {"on": bool} for a project on the server.
func (api *StreamingAPI) handleCrewAITeammate(w http.ResponseWriter, r *http.Request) {
	claims := GetUserFromContext(r.Context())
	project := mux.Vars(r)["project"]
	var input struct {
		On *bool `json:"on"`
	}
	if claims == nil || json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&input) != nil || input.On == nil {
		externalError(w, http.StatusBadRequest, "invalid_request", "Say whether AI teammate is on or off.")
		return
	}
	status, code, line, note := setCrewProjectAITeammate(project, claims.UserID, *input.On)
	if status >= 300 {
		externalError(w, status, code, line)
		return
	}
	publishCrewPanelChanged(project)
	externalJSON(w, map[string]any{"ai_teammate": crewAITeammateFor(project, claims.UserID), "note": note})
}
