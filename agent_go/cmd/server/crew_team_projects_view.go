package server

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// The Team panel's "Online projects" card (#3664): the server decides which of the person's projects can join and says
// why the others can't. It lists only projects the person can already open; adding still goes through
// handleCrewTeamProjects and its checks.
type crewTeamProjectChoice struct {
	ProjectID string `json:"project_id"`
	Name      string `json:"name"`
	Owner     string `json:"owner,omitempty"`
	Available bool   `json:"available"`
	Why       string `json:"why,omitempty"`
}

// crewTeamProjectChoices lists the person's projects on this server that could join Team t, sharing these folders:
// available ones first, and for the rest the one line why not (only shared with them, or already has a folder the Team
// shares). The Team setup passes no folders yet, so only the Team instructions file can clash there.
func crewTeamProjectChoices(ctx context.Context, claims *UserClaims, t crewTeam, folders []string) ([]crewTeamProjectChoice, error) {
	rows, err := listAccessibleWorkspaceProjects(ctx, claims, "")
	if err != nil {
		return nil, err
	}
	choices := []crewTeamProjectChoice{}
	for _, row := range rows {
		id, path := stringField(row["id"]), stringField(row["workspace_path"])
		if id == "" || id == t.ProjectID || t.hasProject(id) || !crewLivesOnServer(id) {
			continue // a Mac project's online copy is not an online project of its own
		}
		c := crewTeamProjectChoice{ProjectID: id, Name: stringField(row["name"]), Owner: stringField(row["owner"])}
		if !crewProjectOwnedByCaller(claims.UserID, path) {
			c.Why = "Only shared with you. Its owner or a co-owner can add it."
		} else if files, err := crewTeamProjectFilesRoot(id); err != nil {
			c.Why = "Its folder is unavailable now. Try again in a minute."
		} else if taken := crewTeamPathsTaken(files, folders); len(taken) > 0 {
			c.Why = "Already has " + strings.Join(taken, ", ") + ", which this Team shares. Rename it in that project, or share a different folder."
		} else {
			c.Available = true
		}
		choices = append(choices, c)
	}
	sort.SliceStable(choices, func(i, j int) bool {
		if choices[i].Available != choices[j].Available {
			return choices[i].Available
		}
		return strings.ToLower(choices[i].Name) < strings.ToLower(choices[j].Name)
	})
	return choices, nil
}

func (api *StreamingAPI) handleCrewTeamProjectChoices(w http.ResponseWriter, r *http.Request) {
	claims := GetUserFromContext(r.Context())
	team := mux.Vars(r)["project"]
	if IsLocalMode() {
		externalError(w, 409, "server_only", "Online projects are added on the server.")
		return
	}
	if !crewTeamClaims(claims, team, false) {
		externalError(w, 403, "team_forbidden", "Use your own account to see this Team.")
		return
	}
	t, err := readCrewTeam(team)
	if err != nil {
		externalError(w, 404, "team_unavailable", "This project has no Team yet. Turn the Team on first.")
		return
	}
	if claims.UserID != t.OwnerID && !t.member(claims.UserID) {
		externalError(w, 403, "team_forbidden", "Join this Team to see its AI teammates.")
		return
	}
	out := map[string]any{"projects": t.withProjectNames().Projects, "can_add": claims.UserID == t.OwnerID && t.On, "max": crewTeamMaxProjects}
	switch {
	case claims.UserID != t.OwnerID:
		out["add_line"] = "Only " + crewPersonShortName(t.OwnerID) + ", the Team's owner, can add an AI teammate."
	case !t.On:
		out["add_line"] = "Turn the Team on first, then add an AI teammate."
	}
	if claims.UserID == t.OwnerID {
		choices, err := crewTeamProjectChoices(r.Context(), claims, t, t.Folder.Folders)
		if err != nil {
			externalError(w, 503, "projects_unavailable", "Your projects can't be listed now. Try again in a minute.")
			return
		}
		out["choices"] = choices
	}
	externalJSON(w, out)
}

// crewProjectInTeam is one Team an online project is in, as its own panel and chat show it (#3664): the project has
// co-owners and no Team of its own, so this is where its owners see the Team and take the project out of it.
type crewProjectInTeam struct {
	TeamID    string `json:"team_id"`
	Name      string `json:"name"`
	Owner     string `json:"owner"`
	CanRemove bool   `json:"can_remove"`
}

// crewProjectInTeams lists the Teams on this server that have the online project as a member.
func crewProjectInTeams(project, viewer string) []crewProjectInTeam {
	if IsLocalMode() || project == "" || !crewLivesOnServer(project) {
		return nil
	}
	root, err := crewroot.StateRoot()
	if err != nil {
		return nil
	}
	entries, _ := os.ReadDir(filepath.Join(root, "team-folders"))
	memberRoot := crewProjectRootByID(project)
	out := []crewProjectInTeam{}
	for _, entry := range entries {
		t, err := readCrewTeam(entry.Name())
		if !entry.IsDir() || err != nil || !t.hasProject(project) {
			continue
		}
		out = append(out, crewProjectInTeam{TeamID: t.ProjectID, Name: crewTeamProjectName(t.ProjectID), Owner: crewPersonShortName(t.OwnerID),
			CanRemove: crewTeamProjectChangeRefusal(t, viewer, project, memberRoot, "remove") == ""})
	}
	sort.SliceStable(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out
}
