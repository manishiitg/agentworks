package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"

	"github.com/gorilla/mux"
)

// The first-time Team wizard (owner, 9 Nov, approved mock "First-time Team wizard"). The server says when a Crew's
// Team is new (crewTeamIsNew, sent as new_team on the Team tab reply) and supplies each step's options
// (GET /crew-teams/{project}/setup); the page only renders them. One save at Finish (POST .../setup) runs the saves
// that already exist, in order: the Team save (Team on, Works with me people, folder choice), each Dashboards only
// person through the People route, and the owner's own agent choices. Nothing is saved before Finish, and no access
// rule is new: every step goes through the route that owns it. From chat or MCP the same writes are the team_sync
// tool's settings, people and agent actions.

// crewTeamIsNew: the Crew has no Team yet (no record, or off with nobody in it) and the viewer is the Crew's owner.
func crewTeamIsNew(t crewTeam, viewer string) bool {
	return viewer != "" && viewer == t.OwnerID && !t.On && len(t.People) == 0
}

type crewWizardPerson struct {
	UserID string `json:"user_id"`
	Name   string `json:"name"`
	Email  string `json:"email,omitempty"`
}

type crewWizardAgent struct {
	Key  string `json:"key"`
	Name string `json:"name"`
	// Main is the default agent: always on, so it is ticked and cannot be unticked.
	Main bool `json:"main,omitempty"`
}

type crewWizardFolders struct {
	Default string   `json:"default"`
	Folders []string `json:"folders"`
	Offline string   `json:"offline,omitempty"`
}

type crewWizardView struct {
	NewTeam      bool               `json:"new_team"`
	People       []crewWizardPerson `json:"people"`
	Levels       []crewPeopleLevel  `json:"levels"`
	DefaultLevel string             `json:"default_level"`
	Folders      crewWizardFolders  `json:"folders"`
	Agents       []crewWizardAgent  `json:"agents"`
}

type crewWizardChoice struct {
	People []struct {
		UserID string `json:"user_id"`
		Level  string `json:"level"`
	} `json:"people"`
	Whole  bool     `json:"whole"`
	Folder string   `json:"folder"`
	Agents []string `json:"agents"` // keys of the agents the Team may ask (Main is always on)
}

// crewWizardAgents are the owner's own agents as the Team tab lists them while the Team is off, Main first.
func crewWizardAgents(ctx context.Context, t crewTeam) []crewWizardAgent {
	out := []crewWizardAgent{}
	for _, a := range crewTeamSoloAgents(ctx, t, crewTeamBridgesFor(t), crewTabSetupSource{}) {
		out = append(out, crewWizardAgent{Key: a.Key, Name: a.Name, Main: a.Tab == macMainTab || crewTeamMainAgent(t, t.OwnerID, a.Key)})
	}
	slices.SortStableFunc(out, func(a, b crewWizardAgent) int {
		switch {
		case a.Main == b.Main:
			return 0
		case a.Main:
			return -1
		}
		return 1
	})
	return out
}

// handleCrewTeamSetup is GET (the wizard's options) and POST (Finish) on /crew-teams/{project}/setup.
func (api *StreamingAPI) handleCrewTeamSetup(w http.ResponseWriter, r *http.Request) {
	claims := GetUserFromContext(r.Context())
	project := mux.Vars(r)["project"]
	write := r.Method == http.MethodPost
	if !crewTeamClaims(claims, project, write) {
		externalError(w, 403, "team_forbidden", "Use your own account to set up this Team.")
		return
	}
	t, err := readCrewTeamOrNew(project, claims.UserID)
	if err != nil || !crewTeamCanManage(t, claims.UserID) || t.OwnerID != ownerOfCrewProjectID(project) || !crewTeamAccountActive(t.OwnerID) {
		externalError(w, 403, "team_owner_required", "Only this Crew's owner can set up its Team.")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if write {
		api.finishCrewTeamSetup(w, r, claims, t)
		return
	}
	view := crewWizardView{NewTeam: crewTeamIsNew(t, claims.UserID), People: []crewWizardPerson{}, Agents: []crewWizardAgent{}, DefaultLevel: crewPeopleWorks,
		Levels: crewPeopleLevels(crewPeopleKind(project)), Folders: crewWizardFolders{Default: crewTeamDefaultFolder, Folders: []string{}}}
	if !view.NewTeam || crewOwnerOnlineOfMacCrew(r, project, claims.UserID) {
		view.NewTeam = false
		externalJSON(w, view)
		return
	}
	if directory, _ := loadUserDirectory(); directory != nil {
		for _, u := range directory.Users {
			if u.ID != t.OwnerID && crewTeamAccountActive(u.ID) {
				view.People = append(view.People, crewWizardPerson{UserID: u.ID, Name: crewPersonShortName(u.ID), Email: u.Email})
			}
		}
	}
	slices.SortFunc(view.People, func(a, b crewWizardPerson) int { return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)) })
	// The Crew's folders, through the Team tab's own folder route (a Crew on a Mac is read from its Mac).
	rec := httptest.NewRecorder()
	api.handleCrewTeamFolders(rec, mux.SetURLVars(httptest.NewRequest(http.MethodGet, "/api/crew-teams/"+url.PathEscape(project)+"/folders", nil).WithContext(r.Context()), map[string]string{"project": project}))
	var listing struct {
		Folders []string `json:"folders"`
		Offline string   `json:"offline"`
	}
	if rec.Code < 300 && json.Unmarshal(rec.Body.Bytes(), &listing) == nil {
		view.Folders.Folders, view.Folders.Offline = listing.Folders, listing.Offline
	}
	if !slices.Contains(view.Folders.Folders, crewTeamDefaultFolder) {
		view.Folders.Folders = append([]string{crewTeamDefaultFolder}, view.Folders.Folders...) // a missing one is made
	}
	view.Agents = crewWizardAgents(r.Context(), t)
	externalJSON(w, view)
}

// finishCrewTeamSetup is the wizard's one save.
func (api *StreamingAPI) finishCrewTeamSetup(w http.ResponseWriter, r *http.Request, claims *UserClaims, t crewTeam) {
	project := t.ProjectID
	if !crewTeamIsNew(t, claims.UserID) {
		externalError(w, 409, "team_exists", "This Crew already has a Team. Change it on the Team tab.")
		return
	}
	var in crewWizardChoice
	if json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&in) != nil || len(in.People) > 100 {
		externalError(w, 400, "invalid_team", "Choose people and what this Team shares.")
		return
	}
	team := crewTeam{ProjectID: project, OwnerID: t.OwnerID, On: true, People: []crewTeamPerson{}}
	var dashboards []string
	seen := map[string]bool{}
	for _, p := range in.People {
		if p.UserID == t.OwnerID || seen[p.UserID] || !crewTeamAccountActive(p.UserID) {
			externalError(w, 400, "invalid_person", "Choose active people from your company.")
			return
		}
		seen[p.UserID] = true
		switch p.Level {
		case crewPeopleWorks:
			team.People = append(team.People, crewTeamPerson{UserID: p.UserID})
		case crewPeopleDashboards:
			dashboards = append(dashboards, p.UserID)
		default:
			externalError(w, 400, "invalid_person", "Choose Works with me or Dashboards only for each person.")
			return
		}
	}
	if in.Whole {
		team.Folder = crewTeamFolder{Whole: true}
	} else {
		team.Folder = crewTeamFolder{Folders: []string{firstNonEmptyTrimmed(in.Folder, crewTeamDefaultFolder)}}
	}
	// The Team save: the same route and checks as the Team tab (folder limits, invites, owner only).
	if !crewPeopleRunShare(w, r, func(rec http.ResponseWriter, req *http.Request) { api.putCrewTeam(rec, req, project, team) }) {
		return
	}
	var left []string
	for _, id := range dashboards {
		rec := httptest.NewRecorder()
		api.changeCrewPerson(rec, r, claims, project, crewPeopleChange{Person: id, Level: crewPeopleDashboards, Confirm: true})
		if rec.Code >= 300 {
			var failure struct {
				Error struct {
					Message string `json:"message"`
				} `json:"error"`
			}
			_ = json.Unmarshal(rec.Body.Bytes(), &failure)
			left = append(left, fmt.Sprintf("%s: %s", crewPersonShortName(id), firstNonEmptyTrimmed(failure.Error.Message, "not added")))
		}
	}
	// The owner's agents: ticked ones stay In team (the default), the rest become Only me. Main is always on.
	fresh, err := readCrewTeam(project)
	if err != nil {
		fresh = team
	}
	for _, a := range crewWizardAgents(r.Context(), fresh) {
		if !a.Main && !slices.Contains(in.Agents, a.Key) {
			if err := setCrewTeamAgentChoice(r.Context(), project, t.OwnerID, a.Key, false); err != nil {
				left = append(left, a.Name+": "+err.Error())
			}
		}
	}
	log.Printf("[CREW_TEAM] project=%s actor=%s event=wizard_finished works=%d dashboards=%d whole=%t left=%d", project, logUsernameForUserID(claims.UserID), len(team.People), len(dashboards), in.Whole, len(left))
	if len(left) > 0 {
		externalError(w, 409, "setup_partial", "The Team is on, but some choices were not saved. "+strings.Join(left, " · "))
		return
	}
	externalJSON(w, map[string]any{"saved": true})
}
