package server

import (
	"bytes"
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
	if !crewTeamClaims(claims, project, true) {
		externalError(w, 403, "team_forbidden", "Use your own account to change this team.")
		return
	}
	var in struct {
		Add    string `json:"add"`
		Remove string `json:"remove"`
	}
	if json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&in) != nil || (strings.TrimSpace(in.Add) == "") == (strings.TrimSpace(in.Remove) == "") {
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
	body, _ := json.Marshal(t)
	save := r.Clone(r.Context())
	save.Method = http.MethodPut
	save.Body = io.NopCloser(bytes.NewReader(body))
	save.ContentLength = int64(len(body))
	api.handleCrewTeam(w, mux.SetURLVars(save, map[string]string{"project": project}))
}
