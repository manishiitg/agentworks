package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	"github.com/manishiitg/coding-agent-loop/workspace/crewsync"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

// An online project in a Mac project's Team (#3664): a project that lives on this server joins the Team as a member of
// its own kind, "Project: <name>", identified by its project ID. While it is a member, the Team's shared folders (and
// the Team instructions) sync into the project's own folder with the same protocol a member's server copy uses, run in
// process as the project; the Team's setup and skills never enter it, so the project keeps its own. Removing it, or
// the Team turning off, removes those folders from the project and nothing else.

// crewTeamProject is one online project in a Team. Name is filled in when the Team is shown, never saved.
type crewTeamProject struct {
	ProjectID string `json:"project_id"`
	AddedBy   string `json:"added_by,omitempty"`
	AddedAt   string `json:"added_at,omitempty"`
	Name      string `json:"name,omitempty"`
	// AnswersOnly are the chat keys of its agents set to "Answers only" by the project's owner or co-owners; every other
	// agent works for the Team with the project's full access (owner, 24 Nov, decisions log).
	AnswersOnly []string `json:"answers_only,omitempty"`
}

// crewTeamMaxProjects keeps the list short enough to read.
const crewTeamMaxProjects = 10

// crewTeamProjectMemberPrefix is the author ID of a project's changes in the Team folder's history.
const crewTeamProjectMemberPrefix = "project:"

func crewTeamProjectMemberID(project string) string { return crewTeamProjectMemberPrefix + project }

// crewTeamProjectMemberKey marks a Team sync request made in process by an online project's copy. Only the in-process
// transport sets it; no HTTP request can.
type crewTeamProjectMemberKey struct{}

func (t crewTeam) hasProject(project string) bool {
	return project != "" && slices.ContainsFunc(t.Projects, func(p crewTeamProject) bool { return p.ProjectID == project })
}

// crewTeamProjectAdmitted: an in-process sync request of an online project that is a member of this Team now.
func crewTeamProjectAdmitted(r *http.Request, claims *UserClaims, project string) (crewTeam, bool) {
	member, _ := r.Context().Value(crewTeamProjectMemberKey{}).(string)
	if member == "" || claims == nil || claims.UserID != crewTeamProjectMemberID(member) {
		return crewTeam{}, false
	}
	t, err := readCrewTeam(project)
	if err != nil || !t.On || !t.Folder.shared() || !t.hasProject(member) || !crewLivesOnServer(member) || ownerOfCrewProjectID(project) != t.OwnerID || !crewTeamAccountActive(t.OwnerID) {
		return crewTeam{}, false
	}
	return t, true
}

func crewTeamProjectName(project string) string {
	if root := crewProjectRootByID(project); root != "" {
		if manifest, err := readCrewProjectManifests(context.Background(), crewProfileID, root); err == nil && manifest.displayTitle() != "" {
			return manifest.displayTitle()
		}
	}
	return "Project"
}

// crewTeamProjectFilesRoot is the online project's own files folder on this server.
func crewTeamProjectFilesRoot(project string) (string, error) {
	rec, ok := crewOwnerRecord(project)
	if !ok || rec.Folder == "" {
		return "", fmt.Errorf("this project's folder is unavailable")
	}
	roots, found, err := crewStorageRoots("Crew/" + rec.Folder)
	if err == nil && !found {
		err = fmt.Errorf("this project's folder is unavailable")
	}
	if err != nil {
		return "", err
	}
	return roots.Files, nil
}

// crewTeamProjectTaken lists what the online project already has at the Team's paths. Joining would send those files
// to the whole Team, so the project must move them first.
func crewTeamProjectTaken(root string, t crewTeam) []string {
	return crewTeamPathsTaken(root, t.Folder.Folders)
}

// crewTeamPathsTaken lists which of these shared folders, and the Team instructions file, a project's files already have.
func crewTeamPathsTaken(root string, folders []string) []string {
	taken := []string{}
	for _, rel := range append(slices.Clone(folders), crewTeamInstructionsFile) {
		if crewTeamUnder(rel, "skills") {
			continue
		}
		if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(rel))); err == nil {
			taken = append(taken, rel)
		}
	}
	return taken
}

// crewTeamProjectCopyDir is the sync record of one online project's copy of one Team (by its folder generation).
func crewTeamProjectCopyDir(member, team, folderID string) (string, error) {
	state, err := crewroot.StateRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(state, "team-working", "project", crewTransferDigest(member+"\x00"+team+"\x00"+folderID+"\x00"+crewTeamSyncFormat)), nil
}

// handleCrewTeamProjects adds or removes an online project (POST {action: add|remove, project_id}). The Team's owner
// adds a project they own or co-own on this server; the Team's owner, or the project's owner or co-owner, removes it.
func (api *StreamingAPI) handleCrewTeamProjects(w http.ResponseWriter, r *http.Request) {
	claims := GetUserFromContext(r.Context())
	team := mux.Vars(r)["project"]
	if IsLocalMode() {
		externalError(w, 409, "server_only", "Online projects are added on the server.")
		return
	}
	if !crewTeamClaims(claims, team, true) {
		externalError(w, 403, "team_forbidden", "Use your own account to change this Team.")
		return
	}
	var in struct {
		Action    string `json:"action"`
		ProjectID string `json:"project_id"`
		Agent     string `json:"agent"`
		Level     string `json:"level"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	if json.NewDecoder(r.Body).Decode(&in) != nil || (in.Action != "add" && in.Action != "remove" && in.Action != "agent_level") || strings.TrimSpace(in.ProjectID) == "" {
		externalError(w, 400, "invalid_arguments", "Give action add, remove or agent_level, and the online project's ID.")
		return
	}
	if in.Action == "agent_level" {
		api.setCrewTeamProjectAgentLevel(w, r, claims.UserID, team, strings.TrimSpace(in.ProjectID), in.Agent, in.Level)
		return
	}
	t, status, code, refusal := changeCrewTeamProject(team, claims.UserID, in.Action, strings.TrimSpace(in.ProjectID))
	if refusal != "" {
		externalError(w, status, code, refusal)
		return
	}
	externalJSON(w, map[string]any{"team": t.view(claims.UserID)})
}

// changeCrewTeamProject adds or removes one online project with every check the Team panel's add has; the panel, chat
// and the Team setup's Finish all come here. It returns the saved Team, or the status, code and sentence of a refusal.
func changeCrewTeamProject(team, user, action, member string) (crewTeam, int, string, string) {
	dir, err := crewTeamDir(team)
	if err != nil {
		return crewTeam{}, 400, "team_invalid", "Choose a valid team."
	}
	mu := crewTeamRecordMutex(dir)
	mu.Lock()
	t, err := readCrewTeam(team)
	mu.Unlock()
	if err != nil {
		return crewTeam{}, 404, "team_unavailable", "This project has no Team yet. Turn the Team on first."
	}
	memberRoot := crewProjectRootByID(member)
	if refusal := crewTeamProjectChangeRefusal(t, user, member, memberRoot, action); refusal != "" {
		log.Printf("[CREW_TEAM] project=%s actor=%s event=project_%s_refused member=%s reason=%q", team, logUsernameForUserID(user), action, member, refusal)
		return crewTeam{}, 403, "team_forbidden", refusal
	}
	if action == "add" && !t.hasProject(member) {
		files, err := crewTeamProjectFilesRoot(member)
		if err != nil {
			return crewTeam{}, 409, "project_unavailable", "That project's folder is unavailable now. Try again in a minute."
		}
		if taken := crewTeamProjectTaken(files, t); len(taken) > 0 {
			return crewTeam{}, 409, "project_has_team_paths", crewTeamTakenLine(crewTeamProjectName(member), taken)
		}
	}
	mu.Lock()
	t, err = readCrewTeam(team)
	if err == nil {
		switch {
		case action == "add" && !t.hasProject(member):
			t.Projects = append(t.Projects, crewTeamProject{ProjectID: member, AddedBy: user, AddedAt: time.Now().UTC().Format(time.RFC3339)})
		case action == "remove":
			t.Projects = slices.DeleteFunc(t.Projects, func(p crewTeamProject) bool { return p.ProjectID == member })
		}
		t.Revision++
		err = writeCrewTeam(t)
	}
	mu.Unlock()
	if err != nil {
		return crewTeam{}, 500, "team_save_failed", "The Team could not be saved now. Try again in a minute."
	}
	log.Printf("[CREW_TEAM] project=%s actor=%s event=project_%s member=%s", team, logUsernameForUserID(user), action, member)
	crewTeamWakeSoon()
	return t, 0, "", ""
}

// crewTeamTakenLine is the one sentence for a project that already has the Team's paths.
func crewTeamTakenLine(name string, taken []string) string {
	return fmt.Sprintf("%s already has %s, which this Team shares. Rename it in that project, or share a different folder, then add it again.", name, strings.Join(taken, ", "))
}

// crewTeamProjectChangeRefusal is the sentence a person sees, or "" when the change is allowed.
func crewTeamProjectChangeRefusal(t crewTeam, user, member, memberRoot, action string) string {
	owns := memberRoot != "" && crewProjectOwnedByCaller(user, memberRoot)
	if action == "remove" {
		if user == t.OwnerID || owns {
			return ""
		}
		return "Only this Team's owner, or that project's owner or co-owners, can take it out of the Team."
	}
	switch {
	case user != t.OwnerID:
		return fmt.Sprintf("Only %s, the Team's owner, can add an AI teammate.", crewPersonShortName(t.OwnerID))
	case !t.On:
		return "Turn the Team on first, then add the AI teammate."
	case member == t.ProjectID || memberRoot == "" || !crewLivesOnServer(member):
		return "Choose a project that lives on this server (one you made on the website or the server), not a Mac project or its online copy."
	case !owns:
		return "You can add only a project you own or co-own as an AI teammate. Ask its owner to make you a co-owner, or to add it."
	case !t.hasProject(member) && len(t.Projects) >= crewTeamMaxProjects:
		return fmt.Sprintf("A Team has at most %d AI teammates. Remove one first.", crewTeamMaxProjects)
	}
	return ""
}

// syncCrewTeamProjectCopy brings one online project's copy of the Team's shared folders up to date both ways.
func (api *StreamingAPI) syncCrewTeamProjectCopy(ctx context.Context, t crewTeam, member string) error {
	if IsLocalMode() || !t.On || !t.Folder.shared() || !t.hasProject(member) || !crewLivesOnServer(member) {
		return nil
	}
	dir, err := crewTeamProjectCopyDir(member, t.ProjectID, t.Folder.ID)
	if err != nil {
		return err
	}
	id := crewTeamProjectMemberID(member)
	b, err := readCrewTeamLocal(dir)
	if os.IsNotExist(err) {
		root, rootErr := crewTeamProjectFilesRoot(member)
		if rootErr != nil {
			return rootErr
		}
		b = crewTeamLocal{FolderID: t.Folder.ID, Connection: "server", Project: t.ProjectID, LocalProject: member, RemoteUser: id, Client: uuid.NewString(),
			Root: root, Status: "pending", Mounted: true, ProtectPrivate: true, ProjectMember: true}
		if err = b.reconcileMounts(t.Folder); err != nil {
			return err
		}
		if err = os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
		if err = saveCrewTeamLocal(dir, b); err != nil {
			return err
		}
		log.Printf("[CREW_TEAM_PROJECT] team=%s member=%s folder=%q event=created", t.ProjectID, member, t.Folder.display())
	} else if err != nil {
		return err
	}
	c := desktopConnection{ID: "server", Server: crewTeamServerHost, User: UserInfo{ID: id}}
	sync := context.WithValue(ctx, crewTeamTransportKey{}, crewTeamInProcess{handler: api.crewTeamServerHandler(), claims: &UserClaims{UserID: id, Username: id}, projectMember: member})
	if err = syncCrewTeamLocal(sync, id, c, dir, &b); err != nil {
		log.Printf("[CREW_TEAM_PROJECT] team=%s member=%s event=sync_failed step=%s reason=%q", t.ProjectID, member, crewTeamStepOf(err), err)
		return err
	}
	return nil
}

// removeStaleCrewTeamProjectCopies removes an online project's copy once it is no longer a member: it was taken out,
// the Team is off or gone, or the shared folder changed generation. What it holds for the Team goes from the project too,
// but only files still exactly as last synced (removeCrewTeamProjectFiles): an edit made since then exists nowhere else
// and stays. A Team record or owner registry that cannot be read now removes nothing.
func (api *StreamingAPI) removeStaleCrewTeamProjectCopies(ctx context.Context) {
	state, err := crewroot.StateRoot()
	if err != nil {
		return
	}
	if _, err := defaultProjectOwners().All(); err != nil {
		return // "is it still on the server" cannot be answered now
	}
	dirs, _ := filepath.Glob(filepath.Join(state, "team-working", "project", "*"))
	for _, dir := range dirs {
		b, err := readCrewTeamLocal(dir)
		if err != nil || !b.ProjectMember {
			continue
		}
		t, err := readCrewTeam(b.Project)
		if err != nil && !os.IsNotExist(err) {
			continue
		}
		member := b.LocalProject
		if err == nil && t.On && t.Folder.shared() && t.Folder.ID == b.FolderID && t.hasProject(member) && crewLivesOnServer(member) {
			continue
		}
		unlock, lockErr := wf.LockWorkspace(ctx, dir)
		if lockErr != nil {
			continue
		}
		removed, kept := b.removeCrewTeamProjectFiles()
		err = os.RemoveAll(dir)
		unlock()
		log.Printf("[CREW_TEAM_PROJECT] team=%s member=%s event=removed files=%d kept=%d err=%v", b.Project, member, removed, kept, err)
	}
}

// removeCrewTeamProjectFiles deletes from the project the Team files this copy holds that are still exactly as last
// synced, then the folders left empty. A mount folder is reached as the sync reaches it (no links on the way) and only
// while it is the same folder (device and inode) the copy synced; a folder made later at the same name is left alone.
func (b crewTeamLocal) removeCrewTeamProjectFiles() (removed, kept int) {
	unchanged := func(local string, info os.FileInfo) bool {
		known, ok := b.Files[b.remoteRel(local)]
		return ok && info.Mode().IsRegular() && known.Stamp.Reusable(crewsync.StampOf(info), known.ReadAt)
	}
	for _, m := range b.Mounts {
		if m.Local == "" || crewTeamUnder(m.Local, "skills") {
			continue
		}
		top, err := crewTeamMountPath(b.Root, m.Local)
		if err != nil {
			log.Printf("[CREW_TEAM_PROJECT] team=%s member=%s event=remove_skipped path=%q reason=%q", b.Project, b.LocalProject, m.Local, err)
			continue
		}
		info, err := os.Lstat(top)
		if err != nil || !info.IsDir() {
			continue
		}
		if stamp := crewsync.StampOf(info); m.Inode != 0 && (stamp.Inode != m.Inode || stamp.Device != m.Device) {
			log.Printf("[CREW_TEAM_PROJECT] team=%s member=%s event=remove_skipped path=%q reason=replaced_folder", b.Project, b.LocalProject, m.Local)
			continue
		}
		dirs := []string{}
		_ = filepath.WalkDir(top, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				dirs = append(dirs, path)
				return nil
			}
			rel, relErr := filepath.Rel(b.Root, path)
			info, infoErr := d.Info()
			if relErr == nil && infoErr == nil && unchanged(filepath.ToSlash(rel), info) && os.Remove(path) == nil {
				removed++
			} else {
				kept++
			}
			return nil
		})
		for i := len(dirs) - 1; i >= 0; i-- {
			_ = os.Remove(dirs[i]) // only empty folders go
		}
	}
	path := filepath.Join(b.Root, crewTeamInstructionsFile)
	if info, err := os.Lstat(path); err == nil {
		if unchanged(crewTeamInstructionsFile, info) && os.Remove(path) == nil {
			removed++
		} else {
			kept++
		}
	}
	return removed, kept
}

// crewTeamProjectMembersView fills in each online project's name for the screen and chat.
func (t crewTeam) withProjectNames() crewTeam {
	t.Projects = slices.Clone(t.Projects)
	for i := range t.Projects {
		t.Projects[i].Name = crewTeamProjectName(t.Projects[i].ProjectID)
	}
	return t
}
