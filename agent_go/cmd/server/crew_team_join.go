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
	"slices"
	"strings"
	"sync"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// "Add to my Mac" for a Team invite (owner, 5 Nov, the decisions log): this Mac accepts the invite on the server and
// syncs the team's shared folder to a folder the person picks once, ~/AgentWorks/<Crew>/team unless they choose
// another. The server stays the authority; the binding only names where the files go.

// crewTeamJoining keeps the sync loop from making its own app folder while a join is choosing one.
var crewTeamJoining sync.Map

func crewTeamJoinKey(connection, project string) string { return connection + "\x00" + project }

// crewTeamDefaultJoinFolder is ~/AgentWorks/<Crew name>, with the name made safe for a folder: the shared files keep
// the Crew's own layout inside it (team/…, code/…).
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
	// Two Teams can share a name. A folder that already holds a Crew belongs to the other one: joining into it was
	// refused after the server had recorded this Mac, so the member's agents never reached the second Team.
	folder := filepath.Join(home, "AgentWorks", clean)
	for n := 2; n < 100; n++ {
		if _, err := os.Lstat(filepath.Join(folder, ".agentworks")); os.IsNotExist(err) {
			break
		}
		folder = filepath.Join(home, "AgentWorks", fmt.Sprintf("%s (%d)", clean, n))
	}
	return folder, nil
}

// crewTeamJoinFolderOK: an absolute folder outside the app's private state. It may hold the person's own files when
// the Team shares named folders (owner, 10 Nov): only those folders, skills/ and the setup file sync, so each must be
// absent or empty and everything else stays on this Mac, untouched and never uploaded.
// If the shared folders are not known yet, require a new or empty folder.
func crewTeamJoinFolderOK(folder string, shared *crewTeamFolder) (string, error) {
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
	if len(entries) == 0 {
		return folder, nil
	}
	if shared == nil || len(shared.Folders) == 0 {
		return "", fmt.Errorf("the shared folders are not known yet; choose an empty folder so your other files stay untouched")
	}
	for _, rel := range append(slices.Clone(shared.Folders), "skills", crewTeamSetupFile) {
		path := filepath.Join(folder, filepath.FromSlash(rel))
		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return "", err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("this folder already has %s, which the team uses; choose another folder", rel)
		}
		if inside, err := os.ReadDir(path); err != nil {
			return "", err
		} else if len(inside) > 0 {
			return "", fmt.Errorf("this folder already has files in %s/, which the team shares; choose a folder without %s/ or move those files first", rel, rel)
		}
	}
	return folder, nil
}

// crewTeamJoinShared is what the Team shares, from the invite (or the Team, when already a member); nil if unknown.
func crewTeamJoinShared(r *http.Request, user string, c desktopConnection, project string) *crewTeamFolder {
	var list struct {
		Invites []crewTeamInvite `json:"invites"`
	}
	if crewTeamRemoteJSON(r.Context(), user, c, http.MethodGet, "/api/crew-teams", nil, &list) == nil {
		for _, invite := range list.Invites {
			if invite.ProjectID == project && invite.CanAddToMac {
				return &invite.Folder
			}
		}
	}
	var team crewTeam
	if crewTeamRemoteJSON(r.Context(), user, c, http.MethodGet, "/api/crew-teams/"+url.PathEscape(project), nil, &team) == nil && team.Folder.shared() {
		return &team.Folder
	}
	return nil
}

func (api *StreamingAPI) handleDesktopCrewTeamJoin(w http.ResponseWriter, r *http.Request) {
	if !desktopConnectionGate(w, r) {
		return
	}
	api.joinCrewTeamOnMac(w, r)
}

// joinCrewTeamOnMac is called only by the desktop gate or an admitted own chat.
func (api *StreamingAPI) joinCrewTeamOnMac(w http.ResponseWriter, r *http.Request) {
	claims := GetUserFromContext(r.Context())
	connectionID, project := mux.Vars(r)["connection"], mux.Vars(r)["project"]
	if r.Method == http.MethodPost {
		var finish func()
		w, r, finish = startCrewTeamJoin(w, r, project)
		defer finish()
	}
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
	var input crewTeamJoinChoice
	if json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&input) != nil {
		externalError(w, 400, "invalid_request", "Choose where the team's folder goes on this Mac.")
		return
	}
	c, err := desktopConnectionFor(claims.UserID, connectionID)
	if err != nil {
		externalError(w, 409, "team_connection_required", err.Error())
		return
	}
	// Check the chosen folder before accepting: a refused folder keeps the invite open, so the person picks another
	// on the same card instead of having joined with no folder.
	shared := crewTeamJoinShared(r, claims.UserID, c, project)
	if input.Folder != "" {
		if _, err := crewTeamJoinFolderOK(input.Folder, shared); err != nil {
			externalError(w, 400, "invalid_folder", strings.ToUpper(err.Error()[:1])+err.Error()[1:]+".")
			return
		}
	}
	key := crewTeamJoinKey(c.ID, project)
	log.Printf("[TEAM_JOIN] request_id=%s project=%s event=target_chosen target=new", crewTeamJoinRequestID(r.Context()), project)
	crewTeamJoining.Store(key, true)
	defer crewTeamJoining.Delete(key)
	var team crewTeam
	if err = crewTeamRemoteJSON(r.Context(), claims.UserID, c, http.MethodPost, "/api/crew-teams/"+url.PathEscape(project)+"/invite", map[string]bool{"accept": true}, &team); err != nil &&
		// Already a member (they accepted on the web): Add to my Mac moves their working copy here.
		crewTeamRemoteJSON(r.Context(), claims.UserID, c, http.MethodGet, "/api/crew-teams/"+url.PathEscape(project), nil, &team) != nil {
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
		folder, err = crewTeamJoinFolderOK(folder, &team.Folder)
		if err != nil {
			externalError(w, 400, "invalid_folder", "Joined the team, but "+err.Error()+". Pick another folder in the team's Sync view.")
			return
		}
		dir, err := crewTeamLocalDir(claims.UserID, c, project, team.Folder.ID)
		if err == nil {
			err = os.MkdirAll(dir, 0o700)
		}
		if err == nil {
			b := crewTeamLocal{SkillRoot: filepath.Join(folder, ".agentworks", "skills"), FolderID: team.Folder.ID, Connection: c.ID, Project: project, RemoteUser: c.User.ID, Client: uuid.NewString(),
				Root: folder, Status: "pending", Mounted: true}
			if err == nil {
				err = b.reconcileMounts(team.Folder)
			}
			if err == nil {
				err = saveCrewTeamLocal(dir, b)
			}
		}
		if err != nil {
			externalError(w, 500, "folder_unavailable", "Joined the team, but its folder could not be set up on this Mac: "+err.Error())
			return
		}
		out["folder"] = folder
	}
	// One working copy (owner, 5 Nov): after Add to my Mac this member's agents in this Crew work on this Mac.
	folder, _ := out["folder"].(string)
	local, err := api.addCrewTeamToMac(r.Context(), claims, c, project, team, folder)
	if err != nil {
		log.Printf("[CREW_TEAM_LOCAL] project=%s event=member_copy_failed reason=%q", project, err)
		externalError(w, 409, "mac_copy_failed", "Joined the team, but its project could not be set up on this Mac: "+err.Error())
		return
	}
	out["project_id"], out["where"] = local, "mac"
	out["notice"] = crewTeamJoinedNotice(team.Name, folder)
	log.Printf("[TEAM_JOIN] request_id=%s project=%s local_project=%s event=paired", crewTeamJoinRequestID(r.Context()), project, local)
	log.Printf("[CREW_TEAM_LOCAL] project=%s event=joined_on_mac folder_set=%t", project, out["folder"] != nil)
	go api.crewTeamJoinSetup(r.Context(), claims, c, project, team.Name, local, false)
	externalJSON(w, out)
}

// Both the screen and the chat submit the same choice to the join handler: where the shared folder goes.
type crewTeamJoinChoice struct {
	Folder string `json:"folder"`
}
