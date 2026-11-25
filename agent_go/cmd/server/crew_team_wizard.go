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
	"os"
	"path/filepath"
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
	// Main is a role, not a forced sharing choice.
	Main   bool `json:"main,omitempty"`
	InTeam bool `json:"in_team"`
}

type crewWizardFolders struct {
	Default string   `json:"default"`
	Folders []string `json:"folders"`
	Offline string   `json:"offline,omitempty"`
	// Taken: folders a picked online project already has, with the line why it can't be shared (#3797).
	Taken map[string]string `json:"taken,omitempty"`
}

type crewWizardView struct {
	NewTeam      bool               `json:"new_team"`
	People       []crewWizardPerson `json:"people"`
	Levels       []crewPeopleLevel  `json:"levels"`
	DefaultLevel string             `json:"default_level"`
	Folders      crewWizardFolders  `json:"folders"`
	Agents       []crewWizardAgent  `json:"agents"`
	// Projects: the owner's projects on this server that can join as online projects, and why the others can't.
	Projects []crewTeamProjectChoice `json:"projects"`
}

type crewWizardChoice struct {
	People []struct {
		UserID string `json:"user_id"`
		Level  string `json:"level"`
	} `json:"people"`
	Folder          string   `json:"folder"`
	Agents          []string `json:"agents"`   // explicit keys of the agents the Team may ask, Main included
	Projects        []string `json:"projects"` // online projects to add, with the Team panel's add checks
	CreateDashboard bool     `json:"create_dashboard"`
}

// crewWizardAgents are the owner's own agents as the Team tab lists them while the Team is off, Main first.
func crewWizardAgents(ctx context.Context, t crewTeam) []crewWizardAgent {
	mainKey := crewOwnerMainChatKey(t.ProjectID)
	out := []crewWizardAgent{}
	for _, a := range crewTeamSoloAgents(ctx, t, crewTeamBridgesFor(t), crewTabSetupSource{}, mainKey) {
		inTeam := crewTeamDefaultInTeam(nil)
		if saved, ok := t.registeredAgent(t.OwnerID, a.WatchKey); ok {
			inTeam = saved.InTeam
		}
		out = append(out, crewWizardAgent{Key: a.Key, Name: a.Name, InTeam: inTeam, Main: a.Tab == macMainTab || crewTeamMainAgentWithKey(t, t.OwnerID, a.WatchKey, mainKey)})
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
		externalError(w, 403, "team_owner_required", "Only this project's owner can set up its Team.")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if write {
		api.finishCrewTeamSetup(w, r, claims, t)
		return
	}
	view := crewWizardView{NewTeam: crewTeamIsNew(t, claims.UserID), People: []crewWizardPerson{}, Agents: []crewWizardAgent{}, DefaultLevel: crewPeopleWorks,
		Levels: crewPeopleLevels(crewPeopleKind(project)), Folders: crewWizardFolders{Default: crewTeamDefaultFolder, Folders: []string{}}, Projects: []crewTeamProjectChoice{}}
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
	slices.SortFunc(view.People, func(a, b crewWizardPerson) int {
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
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
	if !IsLocalMode() {
		view.Projects, _ = crewTeamProjectChoices(r.Context(), claims, t, nil)
	}
	// Share comes after Who (#3797): with the picked projects (?projects=a,b) the server marks the folders they already
	// have and suggests one that is free in all of them.
	picked := crewWizardPickedRoots(view.Projects, strings.Split(r.URL.Query().Get("projects"), ","))
	view.Folders.Default = crewWizardSuggestedFolder(crewTeamProjectName(project), picked)
	if !slices.Contains(view.Folders.Folders, view.Folders.Default) {
		view.Folders.Folders = append([]string{view.Folders.Default}, view.Folders.Folders...) // a missing one is made
	}
	for _, folder := range view.Folders.Folders {
		if why := crewWizardFolderTaken(folder, picked); why != "" {
			if view.Folders.Taken == nil {
				view.Folders.Taken = map[string]string{}
			}
			view.Folders.Taken[folder] = why
		}
	}
	view.Agents = crewWizardAgents(r.Context(), t)
	externalJSON(w, view)
}

// crewWizardPickedRoot is one picked online project's name and files folder.
type crewWizardPickedRoot struct{ name, files string }

// crewWizardPickedRoots keeps only the picked projects the server offered as available.
func crewWizardPickedRoots(choices []crewTeamProjectChoice, ids []string) []crewWizardPickedRoot {
	out := []crewWizardPickedRoot{}
	for _, c := range choices {
		if !c.Available || !slices.Contains(ids, c.ProjectID) {
			continue
		}
		if files, err := crewTeamProjectFilesRoot(c.ProjectID); err == nil {
			out = append(out, crewWizardPickedRoot{name: c.Name, files: files})
		}
	}
	return out
}

// crewWizardFolderTaken is why this folder can't be the Team's shared folder with these projects in it, or "".
func crewWizardFolderTaken(folder string, picked []crewWizardPickedRoot) string {
	for _, p := range picked {
		if _, err := os.Lstat(filepath.Join(p.files, filepath.FromSlash(folder))); err == nil {
			return fmt.Sprintf("%s already has %s/. Pick another so its own files stay its own.", p.name, folder)
		}
	}
	return ""
}

// crewWizardSuggestedFolder is team/ when it is free in every picked project, else a name made from this project's.
func crewWizardSuggestedFolder(name string, picked []crewWizardPickedRoot) string {
	slug := strings.Trim(strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			return r
		}
		return '-'
	}, strings.ToLower(name)), "-")
	candidates := []string{crewTeamDefaultFolder}
	if slug != "" && slug != "project" {
		candidates = append(candidates, slug+"-shared")
	}
	candidates = append(candidates, "team-shared")
	for i := 2; i <= 9; i++ {
		candidates = append(candidates, fmt.Sprintf("team-%d", i))
	}
	for _, c := range candidates {
		if crewWizardFolderTaken(c, picked) == "" {
			return c
		}
	}
	return candidates[len(candidates)-1]
}

// finishCrewTeamSetup is the wizard's one save.
func (api *StreamingAPI) finishCrewTeamSetup(w http.ResponseWriter, r *http.Request, claims *UserClaims, t crewTeam) {
	project := t.ProjectID
	if !crewTeamIsNew(t, claims.UserID) {
		externalError(w, 409, "team_exists", "This project already has a Team. Change it on the Team tab.")
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
	team.Folder = crewTeamFolder{Folders: []string{firstNonEmptyTrimmed(in.Folder, crewTeamDefaultFolder)}}
	// Online projects (#3797): refuse before anything is saved when one can't join, so the choices stay on screen.
	if len(in.Projects) > crewTeamMaxProjects {
		externalError(w, 400, "invalid_team", fmt.Sprintf("A Team has at most %d AI teammates.", crewTeamMaxProjects))
		return
	}
	on := team
	for _, member := range in.Projects {
		if refusal := crewTeamProjectChangeRefusal(on, claims.UserID, member, crewProjectRootByID(member), "add"); refusal != "" {
			externalError(w, 403, "team_forbidden", refusal)
			return
		}
		files, err := crewTeamProjectFilesRoot(member)
		if err != nil {
			externalError(w, 409, "project_unavailable", "That project's folder is unavailable now. Try again in a minute.")
			return
		}
		if taken := crewTeamPathsTaken(files, team.Folder.Folders); len(taken) > 0 {
			externalError(w, 409, "project_has_team_paths", crewTeamTakenLine(crewTeamProjectName(member), taken))
			return
		}
	}
	// PLAT-746: ask for a dashboard before saving the Team, so the wizard and its choices stay available.
	// This is the same explicit creation choice as People; checking Dashboards only is not creation consent.
	if len(dashboards) > 0 {
		target, err := api.dashboardTarget(r.Context(), claims, crewProjectRootByID(project))
		if err != nil {
			dashboardError(w, err)
			return
		}
		if !hasPublishedDashboard(r.Context(), target.Root) {
			if !in.CreateDashboard {
				externalError(w, 409, "dashboard_needed", "This project has no dashboard yet. Create a standard dashboard for the people with Dashboards only access?")
				return
			}
			if _, _, err := api.ensureDefaultDashboard(r.Context(), claims, target); err != nil || !hasPublishedDashboard(r.Context(), target.Root) {
				externalError(w, 409, "dashboard_needed", "The dashboard could not be created. Try again, or publish your own dashboard first.")
				return
			}
		}
	}
	// The Team save: the same route and checks as the Team tab (folder limits, invites, owner only).
	if !crewPeopleRunShare(w, r, func(rec http.ResponseWriter, req *http.Request) { api.putCrewTeam(rec, req, project, team) }) {
		return
	}
	var left []string
	for _, id := range dashboards {
		rec := httptest.NewRecorder()
		api.changeCrewPerson(rec, r, claims, project, crewPeopleChange{Person: id, Level: crewPeopleDashboards, Confirm: true, CreateDashboard: in.CreateDashboard})
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
	for _, member := range in.Projects {
		if _, _, _, refusal := changeCrewTeamProject(project, claims.UserID, "add", member); refusal != "" {
			left = append(left, crewTeamProjectName(member)+": "+refusal)
		}
	}
	// The owner's agents: apply the selection explicitly, including old Only me choices.
	// Turn the checked agents on first, so hiding the others never hides the last shared agent.
	fresh, err := readCrewTeam(project)
	if err != nil {
		fresh = team
	}
	agents := crewWizardAgents(r.Context(), fresh)
	if in.Agents == nil {
		for _, a := range agents {
			if a.InTeam {
				in.Agents = append(in.Agents, a.Key)
			}
		}
	}
	for _, a := range agents {
		if slices.Contains(in.Agents, a.Key) {
			if err := setCrewTeamAgentChoice(r.Context(), project, t.OwnerID, a.Key, true); err != nil {
				left = append(left, a.Name+": "+err.Error())
			}
		}
	}
	for _, a := range agents {
		if !slices.Contains(in.Agents, a.Key) {
			if err := setCrewTeamAgentChoice(r.Context(), project, t.OwnerID, a.Key, false); err != nil {
				left = append(left, a.Name+": "+err.Error())
			}
		}
	}
	log.Printf("[CREW_TEAM] project=%s actor=%s event=wizard_finished works=%d dashboards=%d folders=%d projects=%d left=%d", project, logUsernameForUserID(claims.UserID), len(team.People), len(dashboards), len(team.Folder.Folders), len(in.Projects), len(left))
	if len(left) > 0 {
		externalError(w, 409, "setup_partial", "The Team is on, but some choices were not saved. "+strings.Join(left, " · "))
		return
	}
	externalJSON(w, map[string]any{"saved": true})
}
