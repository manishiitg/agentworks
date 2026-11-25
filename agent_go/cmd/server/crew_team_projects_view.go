package server

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// A Team uses a server project only by being in its Users (owner, 26 Nov, #4087): the project's main owner adds or
// removes the Team on the project's Access page (Users) or with crew_owners team_add / team_remove in its chat, choosing
// from the Teams they are in on this server. Everyone in the Team then works with the project as a User
// (crew_co_owners.go). The Team record's Projects list is where it is kept, so the Team panel and Team asks read it
// as before.

// crewProjectInTeam is one Team in a server project's Users.
type crewProjectInTeam struct {
	TeamID    string `json:"team_id"`
	Name      string `json:"name"`
	Owner     string `json:"owner"`
	People    int    `json:"people"`
	CanRemove bool   `json:"can_remove"`
}

// crewTeamsOn are the Teams on this server that are turned on.
func crewTeamsOn() []crewTeam {
	if IsLocalMode() {
		return nil
	}
	root, err := crewroot.StateRoot()
	if err != nil {
		return nil
	}
	entries, _ := os.ReadDir(filepath.Join(root, "team-folders"))
	out := []crewTeam{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if t, err := readCrewTeam(entry.Name()); err == nil && t.On {
			out = append(out, t)
		}
	}
	return out
}

// crewTeamPeopleCount is the Team's owner plus its joined people.
func crewTeamPeopleCount(t crewTeam) int {
	n := 1
	for _, p := range t.People {
		if !p.Invited && p.UserID != t.OwnerID {
			n++
		}
	}
	return n
}

func crewProjectTeamRow(t crewTeam, viewer, project, memberRoot string) crewProjectInTeam {
	return crewProjectInTeam{TeamID: t.ProjectID, Name: crewTeamProjectName(t.ProjectID), Owner: crewPersonShortName(t.OwnerID), People: crewTeamPeopleCount(t),
		CanRemove: project != "" && crewTeamProjectChangeRefusal(t, viewer, project, memberRoot, "remove") == ""}
}

// crewProjectInTeams lists the Teams in the server project's Users.
func crewProjectInTeams(project, viewer string) []crewProjectInTeam {
	if IsLocalMode() || project == "" || !crewLivesOnServer(project) {
		return nil
	}
	memberRoot := crewProjectRootByID(project)
	out := []crewProjectInTeam{}
	for _, t := range crewTeamsOn() {
		if t.hasProject(project) {
			out = append(out, crewProjectTeamRow(t, viewer, project, memberRoot))
		}
	}
	sortCrewProjectTeams(out)
	return out
}

// crewProjectTeamChoices are the Teams the viewer may add to the project's Users: Teams they are in on this server,
// turned on, not already there. Only the project's main owner adds a Team, so others get none.
func crewProjectTeamChoices(project, viewer string) []crewProjectInTeam {
	out := []crewProjectInTeam{}
	if IsLocalMode() || project == "" || !crewLivesOnServer(project) {
		return out
	}
	memberRoot := crewProjectRootByID(project)
	if memberRoot == "" || !crewProjectOwnedByCallerStrict(viewer, memberRoot) {
		return out
	}
	for _, t := range crewTeamsOn() {
		if !t.hasProject(project) && crewTeamProjectChangeRefusal(t, viewer, project, memberRoot, "add") == "" {
			out = append(out, crewProjectTeamRow(t, viewer, "", memberRoot))
		}
	}
	sortCrewProjectTeams(out)
	return out
}

func sortCrewProjectTeams(out []crewProjectInTeam) {
	sort.SliceStable(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
}
