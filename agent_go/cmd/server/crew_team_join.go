package server

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	"github.com/manishiitg/coding-agent-loop/workspace/crewsync"
)

// "Add to my Mac" for a Team invite (owner, 5 Nov, the decisions log): this Mac accepts the invite on the server and
// syncs the team's shared folder to a folder the person picks once, ~/AgentWorks/<Crew>/team unless they choose
// another. The server stays the authority; the binding only names where the files go.

// crewTeamJoining keeps the sync loop from making its own app folder while a join is choosing one.
var crewTeamJoining sync.Map

func crewTeamJoinKey(connection, project string) string { return connection + "\x00" + project }

// crewTeamDefaultJoinFolder is ~/AgentWorks/<Crew name>, with the name made safe for a folder: the shared files keep
// the Crew's own layout inside it (team/…, or the Whole Crew).
func crewTeamDefaultJoinFolder(name string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	clean := strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r == ':' || r == 0 || r < 32 {
			return '-'
		}
		return r
	}, strings.TrimSpace(name))
	clean = strings.Trim(clean, ". ")
	if clean == "" {
		clean = "Shared work"
	}
	return filepath.Join(home, "AgentWorks", clean), nil
}

// crewTeamJoinFolderOK: an absolute folder outside the app's private state that is new or empty, so the team's files
// never merge into unrelated local work.
func crewTeamJoinFolderOK(folder string) (string, error) {
	if !filepath.IsAbs(folder) {
		return "", fmt.Errorf("choose a folder on this Mac")
	}
	folder = filepath.Clean(folder)
	if state, err := crewroot.StateRoot(); err == nil {
		if rel, err := filepath.Rel(state, folder); err == nil && !strings.HasPrefix(rel, "..") {
			return "", fmt.Errorf("choose a folder outside the app's own data")
		}
	}
	info, err := os.Lstat(folder)
	switch {
	case os.IsNotExist(err):
		return folder, os.MkdirAll(folder, 0o755)
	case err != nil:
		return "", err
	case !info.IsDir() || info.Mode()&os.ModeSymlink != 0:
		return "", fmt.Errorf("choose a folder, not a file or link")
	}
	entries, err := os.ReadDir(folder)
	if err != nil {
		return "", err
	}
	if len(entries) > 0 {
		return "", fmt.Errorf("choose an empty folder, so your other files stay out of the team's folder")
	}
	return folder, nil
}

func (api *StreamingAPI) handleDesktopCrewTeamJoin(w http.ResponseWriter, r *http.Request) {
	if !desktopConnectionGate(w, r) {
		return
	}
	claims := GetUserFromContext(r.Context())
	connectionID, project := mux.Vars(r)["connection"], mux.Vars(r)["project"]
	if !crewTeamClientID(project) {
		externalError(w, 400, "invalid_team", "Choose a team invite.")
		return
	}
	if r.Method == http.MethodGet {
		folder, err := crewTeamDefaultJoinFolder(r.URL.Query().Get("name"))
		if err != nil {
			externalError(w, 409, "folder_unavailable", err.Error())
			return
		}
		externalJSON(w, map[string]any{"folder": folder})
		return
	}
	var input struct {
		Folder string `json:"folder"`
	}
	if json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(&input) != nil {
		externalError(w, 400, "invalid_request", "Choose where the team's folder goes on this Mac.")
		return
	}
	c, err := desktopConnectionFor(claims.UserID, connectionID)
	if err != nil {
		externalError(w, 409, "team_connection_required", err.Error())
		return
	}
	key := crewTeamJoinKey(c.ID, project)
	crewTeamJoining.Store(key, true)
	defer crewTeamJoining.Delete(key)
	var team crewTeam
	if err = crewTeamRemoteJSON(r.Context(), claims.UserID, c, http.MethodPost, "/api/crew-teams/"+url.PathEscape(project)+"/invite", map[string]bool{"accept": true}, &team); err != nil {
		externalError(w, 409, "invite_failed", err.Error())
		return
	}
	out := map[string]any{"team": team}
	// Every member gets the shared folder once the owner has chosen one (owner, 5 Nov).
	if team.Folder.ID != "" {
		folder := input.Folder
		if folder == "" {
			if folder, err = crewTeamDefaultJoinFolder(team.Name); err != nil {
				externalError(w, 409, "folder_unavailable", err.Error())
				return
			}
		}
		if folder, err = crewTeamJoinFolderOK(folder); err != nil {
			externalError(w, 400, "invalid_folder", "Joined the team, but "+err.Error()+". Pick another folder in the team's Sync view.")
			return
		}
		dir, err := crewTeamLocalDir(claims.UserID, c, project, team.Folder.ID)
		if err == nil {
			err = os.MkdirAll(dir, 0o700)
		}
		if err == nil {
			err = saveCrewTeamLocal(dir, crewTeamLocal{FolderID: team.Folder.ID, Connection: c.ID, Project: project, RemoteUser: c.User.ID, Client: uuid.NewString(),
				Root: folder, LocalBase: crewsync.Manifest{}, RemoteBase: crewsync.Manifest{}, Status: "pending"})
		}
		if err != nil {
			externalError(w, 500, "folder_unavailable", "Joined the team, but its folder could not be set up on this Mac: "+err.Error())
			return
		}
		out["folder"] = folder
	}
	log.Printf("[CREW_TEAM_LOCAL] project=%s event=joined_on_mac folder_set=%t", project, out["folder"] != nil)
	externalJSON(w, out)
}
