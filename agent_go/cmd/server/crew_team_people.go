package server

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/gorilla/mux"
)

// handleCrewTeamPeople is the People list: GET reads it, POST changes one person's level (crew_people.go). The Team's
// own save (handleCrewTeam PUT) and the Share save stay the one place each rule lives, so the invite, removal and access
// rules do not move. The earlier add/remove shorthand is gone: the card sends person and level.
func (api *StreamingAPI) handleCrewTeamPeople(w http.ResponseWriter, r *http.Request) {
	claims := GetUserFromContext(r.Context())
	project := mux.Vars(r)["project"]
	if r.Method == http.MethodGet {
		api.handleCrewPeopleView(w, r)
		return
	}
	if !crewTeamClaims(claims, project, true) {
		externalError(w, 403, "team_forbidden", "Use your own account to change this team.")
		return
	}
	if refuseCrewOwnerOnline(w, r, project, claims.UserID) {
		return
	}
	var in crewPeopleChange
	if json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&in) != nil {
		externalError(w, 400, "invalid_person", "Choose one person and a level: works, dashboards or none.")
		return
	}
	api.changeCrewPerson(w, r, claims, project, in)
}
