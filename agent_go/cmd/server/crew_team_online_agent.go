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
// agent in one of them instead of on the Mac. Anyone who works in that project may: its main owner, a co-owner, or one
// of the Team's people (a User of it through the Team). The agent is opened by the same code as manage_crew_chats
// side_open, as that person, so the project's own checks decide; it is theirs and starts as Only me.

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
	if !crewTeamProjectMakerAllowed(t, project, claims.UserID) {
		externalError(w, 403, "not_in_project", "Only people who work in that project make agents there. Ask "+crewOwnerDisplayName(ownerOfCrewProjectID(project))+" to add you.")
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
