package server

import (
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strings"

	"github.com/gorilla/mux"
)

// handleCrewTeamPeople adds or removes one person (owner, 5 Nov: the Team tab sends the person's choice, the server
// changes the record). It is the Team save (handleCrewTeam PUT) on the current record with that one change, so the
// invite, removal and access rules stay in one place.
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
	var in struct {
		crewPeopleChange
		// Add and Remove are the Team tab's own shorthand for Works with me, saved at once; person and level (with
		// confirm) are the People list's change.
		Add    string `json:"add"`
		Remove string `json:"remove"`
	}
	if json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&in) != nil {
		externalError(w, 400, "invalid_person", "Choose one person to add or remove.")
		return
	}
	if strings.TrimSpace(in.Person) != "" {
		api.changeCrewPerson(w, r, claims, project, in.crewPeopleChange)
		return
	}
	if (strings.TrimSpace(in.Add) == "") == (strings.TrimSpace(in.Remove) == "") {
		externalError(w, 400, "invalid_person", "Choose one person to add or remove.")
		return
	}
	t, err := readCrewTeam(project)
	if err != nil || !crewTeamCanManage(t, claims.UserID) {
		externalError(w, 403, "team_owner_required", "Join this Crew's Team to change its people.")
		return
	}
	add, remove := strings.TrimSpace(in.Add), strings.TrimSpace(in.Remove)
	if add != "" && !slices.ContainsFunc(t.People, func(p crewTeamPerson) bool { return p.UserID == add }) {
		t.People = append(t.People, crewTeamPerson{UserID: add})
	}
	if remove != "" {
		if remove == claims.UserID {
			externalError(w, 400, "invalid_person", "Use Leave Team to leave it yourself.")
			return
		}
		t.People = slices.DeleteFunc(slices.Clone(t.People), func(p crewTeamPerson) bool { return p.UserID == remove })
	}
	t.People = slices.DeleteFunc(t.People, func(p crewTeamPerson) bool { return !crewTeamAccountActive(p.UserID) && p.UserID != add })
	api.putCrewTeam(w, r, project, t)
}
