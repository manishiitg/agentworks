package server

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/gorilla/mux"
)

// New agent, where it runs (owner, 26 Nov): in a Mac project whose Team uses online projects, New agent can make the
// agent in one of them instead of on the Mac. Only an online project the person is the main owner of is offered, since
// the Team reaches only its main owner's agents (crewTeamProjectAgents). The agent is opened by the same code as
// manage_crew_chats side_open, as that person, so the project's own checks decide.

// handleCrewTeamOnlineAgent is POST /crew-teams/{team}/online-agent {"project_id", "name"} on the server.
func (api *StreamingAPI) handleCrewTeamOnlineAgent(w http.ResponseWriter, r *http.Request) {
	claims := GetUserFromContext(r.Context())
	team := mux.Vars(r)["project"]
	if IsLocalMode() {
		externalError(w, 409, "server_only", "An online agent is made on the server; this Mac sends the request there.")
		return
	}
	if !crewTeamClaims(claims, team, true) {
		externalError(w, 403, "team_forbidden", "Use your own account for this.")
		return
	}
	var in struct {
		ProjectID string `json:"project_id"`
		Name      string `json:"name"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	if json.NewDecoder(r.Body).Decode(&in) != nil || strings.TrimSpace(in.ProjectID) == "" || strings.TrimSpace(in.Name) == "" {
		externalError(w, 400, "invalid_arguments", "Give the agent's name and the online project.")
		return
	}
	project := strings.TrimSpace(in.ProjectID)
	t, err := readCrewTeam(team)
	if err != nil || !t.On || !t.member(claims.UserID) || !t.hasProject(project) {
		externalError(w, 404, "project_not_in_team", "That online project isn't used by this Team now.")
		return
	}
	root := crewProjectRootByID(project)
	if root == "" || !crewProjectOwnedByCallerStrict(claims.UserID, root) {
		externalError(w, 403, "owner_only", "Only the project's owner makes agents there from New agent. Ask "+crewOwnerDisplayName(ownerOfCrewProjectID(project))+", or ask its Main.")
		return
	}
	args := map[string]any{"crew_id": project, "action": "side_open", "name": strings.TrimSpace(in.Name)}
	body, _ := json.Marshal(args)
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body)).WithContext(context.WithValue(r.Context(), UserContextKey, claims))
	rec := httptest.NewRecorder()
	api.externalCrewChatsCall(rec, req, args)
	log.Printf("[CREW_TEAM] project=%s actor=%s event=online_agent member=%s status=%d", team, logUsernameForUserID(claims.UserID), project, rec.Code)
	if rec.Code < 300 {
		crewTeamWakeSoon()
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(rec.Code)
	_, _ = w.Write(rec.Body.Bytes())
}
