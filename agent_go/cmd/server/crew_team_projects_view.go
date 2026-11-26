package server

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// A Team uses a server project only by being in its Users (owner, 26 Nov, #4087): the project's main owner or a
// co-owner adds or removes a Team they are in on the project's Access page (Users) or with crew_owners team_add / team_remove in its chat, choosing
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
	// Agents are this project's agents the Team can use, as the viewer may see them: the viewer's own (any sharing) and
	// others' shared with the viewer. Members are the Team's people, for choosing who an own agent is shared with.
	Agents  []crewProjectTeamAgent  `json:"agents,omitempty"`
	Members []crewProjectTeamPerson `json:"members,omitempty"`
	// NewAgents is how the viewer's new agents in this project start for the Team (only_me or whole_team), set only
	// when the viewer makes agents here (crew_team_online_agents_sharing.go).
	NewAgents string `json:"new_agents,omitempty"`
	// ProjectID is the server project these agents are in, for the online-agents route.
	ProjectID string `json:"project_id,omitempty"`
}

// crewProjectTeamAgent is one of the project's agents on its Access page, with who it is shared with in the Team.
type crewProjectTeamAgent struct {
	ID      string                  `json:"id"`
	Name    string                  `json:"name"`
	Maker   string                  `json:"maker"`
	Mine    bool                    `json:"mine"`
	Sharing string                  `json:"sharing"` // only_me, whole_team or chosen
	Chosen  []crewProjectTeamPerson `json:"chosen,omitempty"`
}

type crewProjectTeamPerson struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// crewProjectTeamAgents are the project's agent rows in t the viewer may see (crewProjectInTeam.Agents).
func crewProjectTeamAgents(t crewTeam, project, viewer string) []crewProjectTeamAgent {
	out := []crewProjectTeamAgent{}
	for _, a := range t.Agents {
		maker := crewTeamProjectMaker(a.OwnerID)
		if p, ok := crewTeamMemberProjectID(a.OwnerID); !ok || p != project || maker == "" || maker != viewer && !crewTeamReachRow(t, viewer, a) {
			continue
		}
		row := crewProjectTeamAgent{ID: a.ID, Name: firstNonEmptyTrimmed(a.Name, "Agent"), Maker: crewPersonShortName(maker), Mine: maker == viewer, Sharing: crewTeamRowAudience(t, a)}
		if row.Sharing == crewTeamAudienceChosen {
			for _, id := range a.Rule.People {
				row.Chosen = append(row.Chosen, crewProjectTeamPerson{ID: id, Name: crewPersonShortName(id)})
			}
		}
		out = append(out, row)
	}
	sort.SliceStable(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out
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

// crewProjectInTeams lists the Teams in the server project's Users, each with the project's agents it can use.
func crewProjectInTeams(project, viewer string) []crewProjectInTeam {
	if IsLocalMode() || project == "" || !crewLivesOnServer(project) {
		return nil
	}
	memberRoot := crewProjectRootByID(project)
	out := []crewProjectInTeam{}
	for _, t := range crewTeamsOn() {
		if t.hasProject(project) {
			row := crewProjectTeamRow(t, viewer, project, memberRoot)
			row.Agents = crewProjectTeamAgents(t, project, viewer)
			if crewTeamProjectMakerAllowed(t, project, viewer) {
				row.ProjectID, row.NewAgents = project, "only_me"
				if t.AgentDefaults[crewTeamProjectMakerID(project, viewer)] == crewTeamAudienceWholeTeam {
					row.NewAgents = crewTeamAudienceWholeTeam
				}
			}
			for _, id := range append([]string{t.OwnerID}, crewTeamJoinedPeople(t)...) {
				if id != viewer {
					row.Members = append(row.Members, crewProjectTeamPerson{ID: id, Name: crewPersonShortName(id)})
				}
			}
			out = append(out, row)
		}
	}
	sortCrewProjectTeams(out)
	return out
}

// crewProjectTeamChoices are the Teams the viewer may add to the project's Users: Teams they are in on this server,
// turned on, not already there. Only the project's main owner or a co-owner adds a Team, so others get none.
func crewProjectTeamChoices(project, viewer string) []crewProjectInTeam {
	out := []crewProjectInTeam{}
	if IsLocalMode() || project == "" || !crewLivesOnServer(project) {
		return out
	}
	memberRoot := crewProjectRootByID(project)
	if memberRoot == "" {
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
