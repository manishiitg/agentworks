package server

// PLAT-856-509: a Team is a folder grant, never another person's Crew Builder.
import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	"github.com/manishiitg/coding-agent-loop/workspace/publication"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

type crewTeamPerson struct {
	UserID string `json:"user_id"`
	// Invited: added by the owner but not accepted yet (owner, 5 Nov, the decisions log). An invited person has no
	// access and no folder sync; accepting on the web or on their Mac makes them a member.
	Invited bool `json:"invited,omitempty"`
	// Where this member's own agents work in this Crew (owner, 5 Nov: one working copy): "" or "web" on the server,
	// "mac" on their Mac, paired to this online copy by MacPair (crew_team_where.go).
	Where   string `json:"where,omitempty"`
	MacPair string `json:"mac_pair,omitempty"`
	// OnlyMe are this member's Mac agents (tab ids) they keep to themselves: never listed or streamed to the others.
	OnlyMe []string `json:"only_me,omitempty"`
}

// crewTeamInvite is a Team invite as the invited person sees it.
type crewTeamInvite struct {
	ProjectID string `json:"project_id"`
	Name      string `json:"name"`
	OwnerName string `json:"owner_name"`
}

// crewTeamFolder is what the Team shares (owner, 5 Nov): one or more of the Crew's folders, or the Whole Crew. Files
// sync relative to the Crew's own folder, so a teammate's copy has the same layout (team/…, code/…). ID is the
// generation: a new choice starts a new one, and the previous files stay where they are.
type crewTeamFolder struct {
	ID      string   `json:"id"`
	Whole   bool     `json:"whole,omitempty"`
	Folders []string `json:"folders,omitempty"`
}

// crewTeamMaxFolders keeps the folder list a choice people can read.
const crewTeamMaxFolders = 20

func (f crewTeamFolder) shared() bool { return f.Whole || len(f.Folders) > 0 }

// holds: the path (relative to the Crew) is inside what the Team shares. Whole Crew leaves out each person's own
// memory as well as everything crewTeamAllowed always keeps private.
func (f crewTeamFolder) holds(rel string) bool {
	if rel == crewroot.TeamCopiesFolder || strings.HasPrefix(rel, crewroot.TeamCopiesFolder+"/") {
		return false // each member's own copy (crew_team_member_copy.go), never shared, Whole Crew included
	}
	if crewTeamSetupPath(rel) {
		return f.shared() // the Crew's setup is always part of what a Team shares (crew_team_setup.go)
	}
	if f.Whole {
		return rel != "MEMORY.md" && rel != "memory" && !strings.HasPrefix(rel, "memory/")
	}
	for _, folder := range f.Folders {
		if rel == folder || strings.HasPrefix(rel, folder+"/") {
			return true
		}
	}
	return false
}

// walks: a capture descends into this folder (relative to the Crew): it is shared or leads to a shared folder.
func (f crewTeamFolder) walks(rel string) bool {
	return f.holds(rel) || !f.Whole && slices.ContainsFunc(f.Folders, func(folder string) bool { return strings.HasPrefix(folder, rel+"/") })
}

// display is how people read the choice: "Whole Crew" or "team/, code/express/".
func (f crewTeamFolder) display() string {
	if f.Whole {
		return "Whole Crew"
	}
	names := make([]string, 0, len(f.Folders))
	for _, folder := range f.Folders {
		names = append(names, folder+"/")
	}
	return strings.Join(names, ", ")
}

func (f crewTeamFolder) same(g crewTeamFolder) bool {
	return f.Whole == g.Whole && slices.Equal(f.Folders, g.Folders)
}

// cleanCrewTeamFolders checks a folder choice and keeps it short: sorted, no repeats, no folder inside another.
func cleanCrewTeamFolders(in []string) ([]string, error) {
	if len(in) > crewTeamMaxFolders {
		return nil, fmt.Errorf("choose at most %d folders", crewTeamMaxFolders)
	}
	out := []string{}
	for _, folder := range in {
		folder = strings.Trim(strings.TrimSpace(folder), "/")
		if _, e := publication.CleanPath(folder); e != nil || publication.ExcludedDirectory(folder) || !crewTeamAllowed(folder+"/file.txt") || !(crewTeamFolder{Whole: true}).holds(folder+"/file.txt") {
			return nil, fmt.Errorf("choose project subfolders, outside private state and credentials")
		}
		out = append(out, folder)
	}
	slices.Sort(out)
	out = slices.Compact(out)
	return slices.DeleteFunc(out, func(folder string) bool {
		return slices.ContainsFunc(out, func(parent string) bool { return parent != folder && strings.HasPrefix(folder, parent+"/") })
	}), nil
}

type crewTeamServerData struct {
	Kind          string `json:"kind"`
	Name          string `json:"name"`
	SizeBytes     int64  `json:"size_bytes"`
	TeamProjectID string `json:"team_project_id"`
}
type crewTeam struct {
	ServerData *crewTeamServerData `json:"server_data,omitempty"`
	ProjectID  string              `json:"project_id"`
	Name       string              `json:"name"`
	OwnerID    string              `json:"owner_id"`
	// On is the Team tab's switch (owner, 4 Nov): off, the roster stays saved but only the owner is in the team.
	On     bool             `json:"on"`
	People []crewTeamPerson `json:"people"`
	Folder crewTeamFolder   `json:"folder"`
	// LastFolders are the folders shared before the Whole Crew, for going back to Folders (owner only).
	LastFolders  []string `json:"last_folders,omitempty"`
	Revision     uint64   `json:"revision"`
	OwnerName    string   `json:"owner_name,omitempty"`
	CanManage    bool     `json:"can_manage"`
	FolderAccess bool     `json:"folder_access"`
	// Lent is what the owner lends the Team's agents in this Crew (names only, from the server, never saved).
	Lent *crewTeamLent `json:"lent,omitempty"`
	// Where the caller's own agents in this Crew work, for a member: "mac" or "web" (crew_team_where.go; never saved).
	Where string `json:"where,omitempty"`
}

func crewTeamDir(project string) (string, error) {
	if project == "" || len(project) > 128 || project == "." || project == ".." || strings.ContainsAny(project, "/\\\x00") {
		return "", fmt.Errorf("choose a valid team")
	}
	root, err := crewroot.StateRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "team-folders", project), nil
}
func readCrewTeam(project string) (crewTeam, error) {
	dir, err := crewTeamDir(project)
	if err != nil {
		return crewTeam{}, err
	}
	var t crewTeam
	b, err := os.ReadFile(filepath.Join(dir, "team.json"))
	if err != nil {
		return t, err
	}
	err = json.Unmarshal(b, &t)
	if err == nil && (t.ProjectID != project || t.OwnerID == "") {
		err = fmt.Errorf("team record is unavailable")
	}
	return t, err
}
func writeCrewTeam(t crewTeam) error {
	dir, err := crewTeamDir(t.ProjectID)
	if err != nil {
		return err
	}
	if err = agentworksclient.EnsurePrivateDirectory(dir); err != nil {
		return err
	}
	t.ServerData = nil
	t.Lent = nil
	t.CanManage = false
	t.FolderAccess = false
	t.OwnerName = ""
	err = fsutil.WriteJSONAtomic(filepath.Join(dir, "team.json"), t, 0600)
	if err == nil {
		publishCrewPanelChanged(t.ProjectID)
	}
	return err
}
func (t crewTeam) member(user string) bool {
	return user != "" && (user == t.OwnerID || t.On && slices.ContainsFunc(t.People, func(p crewTeamPerson) bool { return p.UserID == user && !p.Invited }))
}
func (t crewTeam) edits(user string) bool {
	// Every joined member reads and writes the shared folder; there is no per-person switch (owner, 5 Nov).
	return t.Folder.shared() && t.member(user)
}

// invited: the person has an open invite to this team (Team on, not accepted yet).
func (t crewTeam) invited(user string) bool {
	return user != "" && t.On && slices.ContainsFunc(t.People, func(p crewTeamPerson) bool { return p.UserID == user && p.Invited })
}

// Joined members manage the Team together. Permanent registry ownership and
// each person's own machine/agent controls remain separate.
func crewTeamCanManage(t crewTeam, user string) bool {
	return user != "" && crewTeamAccountActive(user) && (t.member(user) || crewRegistryOwnedByCaller(user, crewProjectRootByID(t.ProjectID)))
}

// A lost Team grant ends already-running work as well as refusing new turns.
// An independent registry owner keeps their rights when leaving the Team.
func (api *StreamingAPI) stopRemovedCrewTeamSessions(previous, current crewTeam) {
	root := crewProjectRootByID(previous.ProjectID)
	if root == "" || !previous.On {
		return
	}
	for _, person := range previous.People {
		if !person.Invited && !current.member(person.UserID) && !crewRegistryOwnedByCaller(person.UserID, root) {
			log.Printf("[CREW_TEAM] project=%s event=access_revoked user=%s", previous.ProjectID, logUsernameForUserID(person.UserID))
			api.stopRemovedOwnerSessions(person.UserID, root)
		}
	}
}

func (t crewTeam) view(user string) crewTeam {
	t.CanManage = crewTeamCanManage(t, user)
	t.Where = ""
	if t.member(user) && user != t.OwnerID {
		t.Where = "web"
		if _, onMac := t.memberMac(user); onMac {
			t.Where = "mac"
		}
	}
	t.FolderAccess = t.edits(user)
	t.OwnerName = shareOwnerLabel(t.OwnerID)
	if t.edits(user) {
		if s, e := readCrewTeamFileState(t.ProjectID); e == nil {
			d := &crewTeamServerData{Kind: "team_folder", Name: t.Folder.display(), TeamProjectID: t.ProjectID}
			for _, v := range s.Versions {
				if v.File != nil {
					d.SizeBytes += v.File.Size
				}
			}
			t.ServerData = d
		}
	}
	// The Mac pair is only the person's own routing fence.
	t.People = slices.Clone(t.People)
	for i := range t.People {
		if t.People[i].UserID != user {
			t.People[i].MacPair = ""
		}
	}
	// Team-only members can discover shared agents but do not learn folder contents.
	if !t.FolderAccess && !t.CanManage {
		t.Folder = crewTeamFolder{}
	}
	if !t.CanManage {
		t.LastFolders = nil
	}
	return t
}
func crewTeamClaims(claims *UserClaims, project string, write bool) bool {
	if claims == nil || claims.UserID == "" || claims.Scope != "" || claims.ExecutionPrincipal != nil || claims.BotRouteGrant != "" || userAccessForClaims(claims).Disabled || !userAllowedProduct(claims, crewProfileID) {
		return false
	}
	if websiteDeviceClaims(claims) {
		return true
	}
	if !personalLaptopSettingsClaims(claims) {
		return false
	}
	t := claims.AccessToken
	scope := "crews:read"
	if write {
		scope = "crews:write"
	}
	return t != nil && t.Allows(scope) && t.AllowsCrew(workspaceTokenProjectID(project))
}
func crewTeamAuthorize(claims *UserClaims, project, rel string, write, folder bool) (crewTeam, error) {
	if !crewTeamClaims(claims, project, write) {
		return crewTeam{}, fmt.Errorf("use your own account to open this team's shared folder")
	}
	t, err := readCrewTeam(project)
	if err != nil || !t.member(claims.UserID) || ownerOfCrewProjectID(project) != t.OwnerID || !crewTeamAccountActive(t.OwnerID) {
		return crewTeam{}, fmt.Errorf("this team is not shared with you; ask its owner to add you")
	}
	if folder && !t.edits(claims.UserID) {
		return crewTeam{}, fmt.Errorf("this team has no shared folder yet; ask its owner to choose one")
	}
	if token := claims.AccessToken; token != nil && token.FileGuard != nil && !token.FileGuard.Allows(rel, write) {
		return crewTeam{}, fmt.Errorf("this connection cannot access that file; use the team's Files panel")
	}
	return t, nil
}

// Live resolution is also used on the Mac at message time. Cached UI membership
// is never authority; a failed server lookup admits no team/chosen share.
func liveCrewTeam(project string) (crewTeam, error) {
	if IsLocalMode() {
		b, e := crewroot.ReadSync(project)
		if e != nil {
			return crewTeam{}, e
		}
		if b != nil && b.Side == "local" {
			owner := ownerOfCrewProjectID(project)
			c, e := desktopConnectionFor(owner, b.ConnectionID)
			if e != nil {
				return crewTeam{}, e
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			var t crewTeam
			e = crewTeamRemoteJSON(ctx, owner, c, http.MethodGet, "/api/crew-teams/"+url.PathEscape(b.PeerProjectID), nil, &t)
			return t, e
		}
	}
	return readCrewTeam(project)
}
func crewTeamPeople(project string) []string {
	t, err := liveCrewTeam(project)
	if err != nil {
		return nil
	}
	out := []string{t.OwnerID}
	if !t.On {
		return out
	}
	for _, p := range t.People {
		if !p.Invited && (IsLocalMode() || crewTeamAccountActive(p.UserID)) {
			out = append(out, p.UserID)
		}
	}
	return out
}
func crewTeamMember(project, user string) bool {
	return user != "" && slices.Contains(crewTeamPeople(project), user)
}

func (api *StreamingAPI) handleCrewTeam(w http.ResponseWriter, r *http.Request) {
	claims := GetUserFromContext(r.Context())
	project := mux.Vars(r)["project"]
	if project == "" {
		api.listCrewTeams(w, r)
		return
	}
	if !crewTeamClaims(claims, project, r.Method != http.MethodGet) {
		externalError(w, 403, "team_forbidden", "Use your own account to open this team.")
		return
	}
	owner := ownerOfCrewProjectID(project)
	if r.Method == http.MethodGet {
		t, e := readCrewTeam(project)
		if os.IsNotExist(e) && crewRegistryOwnedByCaller(claims.UserID, crewProjectRootByID(project)) {
			t = crewTeam{ProjectID: project, OwnerID: owner, People: []crewTeamPerson{}}
			e = nil
		}
		if e != nil || !crewTeamCanManage(t, claims.UserID) || t.OwnerID != owner || !crewTeamAccountActive(owner) {
			externalError(w, 403, "team_forbidden", "This Crew isn't shared with you right now. Ask its owner to add you to its Team.")
			return
		}
		// Exclude disabled/deleted members from all live audience resolutions.
		t.People = slices.DeleteFunc(t.People, func(p crewTeamPerson) bool { return !crewTeamAccountActive(p.UserID) })
		view := t.view(claims.UserID)
		view.Lent = crewTeamLentNow(r.Context(), t)
		externalJSON(w, view)
		return
	}
	if mux.Vars(r)["op"] == "invite" {
		api.answerCrewTeamInvite(w, r, claims, project)
		return
	}
	current, currentErr := readCrewTeam(project)
	if os.IsNotExist(currentErr) {
		current = crewTeam{ProjectID: project, OwnerID: owner}
	}
	if owner == "" || current.OwnerID != owner || !crewTeamAccountActive(owner) || !crewTeamCanManage(current, claims.UserID) {
		externalError(w, 403, "team_owner_required", "Join this Crew's Team to change its settings.")
		return
	}
	var input crewTeam
	dec := json.NewDecoder(io.LimitReader(r.Body, 64<<10))
	if dec.Decode(&input) != nil {
		externalError(w, 400, "invalid_team", "Choose people and what this team shares.")
		return
	}
	if len(input.People) > 100 {
		externalError(w, 400, "invalid_team", "Choose at most 100 people.")
		return
	}
	seen := map[string]bool{}
	for _, p := range input.People {
		if p.UserID == owner || seen[p.UserID] || !crewTeamAccountActive(p.UserID) {
			externalError(w, 400, "invalid_person", "Choose active people from your company.")
			return
		}
		seen[p.UserID] = true
	}
	if input.Folder.Whole {
		input.Folder.Folders = nil
	} else if folders, e := cleanCrewTeamFolders(input.Folder.Folders); e != nil {
		externalError(w, 400, "invalid_folder", strings.ToUpper(e.Error()[:1])+e.Error()[1:]+".")
		return
	} else {
		input.Folder.Folders = folders
	}
	dir, e := crewTeamDir(project)
	if e == nil {
		e = agentworksclient.EnsurePrivateDirectory(dir)
	}
	if e != nil {
		externalError(w, 409, "team_save_failed", e.Error())
		return
	}
	unlock, e := wf.LockWorkspace(r.Context(), dir)
	if e != nil {
		externalError(w, 409, "team_save_failed", e.Error())
		return
	}
	defer unlock()
	previous, e := readCrewTeam(project)
	if e != nil && !os.IsNotExist(e) {
		externalError(w, 409, "team_save_failed", e.Error())
		return
	}
	if os.IsNotExist(e) {
		previous = crewTeam{ProjectID: project, OwnerID: owner}
	}
	if previous.OwnerID != owner || !crewTeamCanManage(previous, claims.UserID) {
		externalError(w, 403, "team_owner_required", "Join this Crew's Team to change its settings.")
		return
	}
	// A person added to the Team is invited until they accept; existing people keep their own state.
	for i := range input.People {
		at := slices.IndexFunc(previous.People, func(p crewTeamPerson) bool { return p.UserID == input.People[i].UserID })
		input.People[i].Invited = at < 0 || previous.People[at].Invited
		input.People[i].Where, input.People[i].MacPair, input.People[i].OnlyMe = "", "", nil
		if at >= 0 { // where a member works is theirs to choose
			input.People[i].Where, input.People[i].MacPair, input.People[i].OnlyMe = previous.People[at].Where, previous.People[at].MacPair, previous.People[at].OnlyMe
		}
		if at < 0 {
			log.Printf("[CREW_TEAM] project=%s actor=%s event=invited user=%s", project, logUsernameForUserID(claims.UserID), logUsernameForUserID(input.People[i].UserID))
		}
	}
	for _, p := range previous.People {
		if !slices.ContainsFunc(input.People, func(q crewTeamPerson) bool { return q.UserID == p.UserID }) {
			log.Printf("[CREW_TEAM] project=%s actor=%s event=removed user=%s was_invited=%t", project, logUsernameForUserID(claims.UserID), logUsernameForUserID(p.UserID), p.Invited)
		}
	}
	// Back to Folders from the Whole Crew picks the folders shared before it, or team/ (owner, 5 Nov); the server decides.
	input.LastFolders = previous.LastFolders
	if len(previous.Folder.Folders) > 0 {
		input.LastFolders = previous.Folder.Folders
	}
	if previous.Folder.Whole && !input.Folder.Whole && len(input.Folder.Folders) == 0 {
		input.Folder.Folders = input.LastFolders
		if len(input.Folder.Folders) == 0 {
			input.Folder.Folders = []string{crewTeamDefaultFolder}
		}
	}
	input.Folder.ID = previous.Folder.ID
	if !previous.Folder.same(input.Folder) {
		input.Folder.ID = uuid.NewString()
		log.Printf("[CREW_TEAM] project=%s actor=%s event=folder whole=%t folders=%d generation=%s", project, logUsernameForUserID(claims.UserID), input.Folder.Whole, len(input.Folder.Folders), input.Folder.ID)
	}
	if input.Folder.shared() && input.Folder.ID == "" {
		input.Folder.ID = uuid.NewString()
	}
	input.ProjectID = project
	input.OwnerID = owner
	input.Revision = previous.Revision + 1
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" {
		input.Name = previous.Name
	}
	if input.Name == "" {
		if _, manifest, ok := api.resolveExternalWorkspace(r.Context(), claims, project); ok {
			input.Name = firstNonEmptyTrimmed(manifest.Title, manifest.Label)
		}
	}
	if input.Name == "" {
		input.Name = "Shared work"
	}
	if len(input.Name) > 200 {
		externalError(w, 400, "invalid_name", "Use a shorter team name.")
		return
	}
	if e = writeCrewTeam(input); e != nil {
		externalError(w, 409, "team_save_failed", e.Error())
		return
	}
	log.Printf("[CREW_TEAM] project=%s actor=%s event=updated on=%t people=%d revision=%d", project, logUsernameForUserID(claims.UserID), input.On, len(input.People), input.Revision)
	api.stopRemovedCrewTeamSessions(previous, input)
	externalJSON(w, input.view(claims.UserID))
}

// crewTeamLent names what a Team's agents use of the owner's in this Crew (owner, 5 Nov: "my work can depend on mcps
// and secrets I use"): the owner's server Vault apps in use here and the Crew's selected secrets. A member's turn in the
// Crew gets them from the owner (crewSecretsPerson, projectSecretsOwner); values never leave Vault.
type crewTeamLent struct {
	Apps    []string `json:"apps"`
	Secrets []string `json:"secrets"`
}

func crewTeamLentNow(ctx context.Context, t crewTeam) *crewTeamLent {
	root := crewProjectRootByID(t.ProjectID)
	if IsLocalMode() || !t.On || root == "" {
		return nil
	}
	out := &crewTeamLent{Apps: []string{}, Secrets: []string{}}
	if servers, err := vaultServersFor(ctx, t.OwnerID); err == nil {
		selected, chosenOnly, _ := projectAppSelection(ctx, root)
		for _, server := range servers {
			if projectVaultAppInUse(selected, chosenOnly, server.ID) {
				out.Apps = append(out.Apps, firstNonEmptyTrimmed(server.Label, vaultServerName(server.ID)))
			}
		}
	}
	if names, err := productSelectedGlobalSecrets(ctx, crewProfileID, root); err == nil && names != nil {
		for _, name := range *names {
			out.Secrets = append(out.Secrets, vaultSecretShortName(name))
		}
	}
	slices.Sort(out.Apps)
	slices.Sort(out.Secrets)
	out.Apps, out.Secrets = slices.Compact(out.Apps), slices.Compact(out.Secrets)
	return out
}

// answerCrewTeamInvite is the invited person's own answer: accept makes them a member, decline takes them off the roster.
func (api *StreamingAPI) answerCrewTeamInvite(w http.ResponseWriter, r *http.Request, claims *UserClaims, project string) {
	var input struct {
		Accept bool `json:"accept"`
	}
	if json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&input) != nil {
		externalError(w, 400, "invalid_request", "Accept or decline the invite.")
		return
	}
	dir, e := crewTeamDir(project)
	if e != nil {
		externalError(w, 400, "invalid_team", e.Error())
		return
	}
	unlock, e := wf.LockWorkspace(r.Context(), dir)
	if e != nil {
		externalError(w, 409, "team_save_failed", e.Error())
		return
	}
	defer unlock()
	t, e := readCrewTeam(project)
	if e != nil || ownerOfCrewProjectID(project) != t.OwnerID || !crewTeamAccountActive(t.OwnerID) || !t.invited(claims.UserID) {
		externalError(w, 404, "invite_not_found", "There is no open invite to this team for you.")
		return
	}
	at := slices.IndexFunc(t.People, func(p crewTeamPerson) bool { return p.UserID == claims.UserID })
	event := "declined"
	if input.Accept {
		t.People[at].Invited = false
		event = "accepted"
	} else {
		t.People = slices.Delete(t.People, at, at+1)
	}
	t.Revision++
	if e = writeCrewTeam(t); e != nil {
		externalError(w, 409, "team_save_failed", e.Error())
		return
	}
	log.Printf("[CREW_TEAM] project=%s actor=%s event=%s revision=%d", project, logUsernameForUserID(claims.UserID), event, t.Revision)
	if !input.Accept {
		externalJSON(w, map[string]any{"declined": true})
		return
	}
	// A new member's own copy of the shared folder is made now, so it is in their Crew's attached folders before
	// their first turn (owner, 5 Nov: the shared folder shows where folders already show). After the reply: the copy
	// syncs through this same server.
	if t.Folder.ID != "" && !IsLocalMode() {
		member := claims.UserID
		go func() {
			round, done := context.WithTimeout(context.WithValue(context.WithoutCancel(r.Context()), crewTeamCopyReasonKey{}, "accepted"), 2*time.Minute)
			defer done()
			_, _ = api.syncCrewTeamServerCopy(round, member, project, true)
		}()
	}
	externalJSON(w, t.view(claims.UserID))
}

func (api *StreamingAPI) listCrewTeams(w http.ResponseWriter, r *http.Request) {
	claims := GetUserFromContext(r.Context())
	if !crewTeamClaims(claims, "", false) {
		externalError(w, 403, "team_forbidden", "Sign in with your own account to see your teams.")
		return
	}
	root, e := crewroot.StateRoot()
	if e != nil {
		externalError(w, 500, "team_unavailable", "Teams could not be loaded.")
		return
	}
	entries, e := os.ReadDir(filepath.Join(root, "team-folders"))
	if e != nil && !os.IsNotExist(e) {
		externalError(w, 500, "team_unavailable", "Teams could not be loaded.")
		return
	}
	out := []crewTeam{}
	invites := []crewTeamInvite{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		t, e := readCrewTeam(entry.Name())
		if e != nil || ownerOfCrewProjectID(t.ProjectID) != t.OwnerID || !crewTeamAccountActive(t.OwnerID) || !crewTeamClaims(claims, t.ProjectID, false) {
			continue
		}
		if t.member(claims.UserID) {
			out = append(out, t.view(claims.UserID))
		} else if t.invited(claims.UserID) {
			invites = append(invites, crewTeamInvite{ProjectID: t.ProjectID, Name: t.Name, OwnerName: shareOwnerLabel(t.OwnerID)})
		}
	}
	externalJSON(w, map[string]any{"teams": out, "invites": invites})
}

func crewTeamRemoteJSON(ctx context.Context, localOwner string, c desktopConnection, method, route string, input, out any) error {
	var body io.Reader
	if input != nil {
		data, e := json.Marshal(input)
		if e != nil {
			return e
		}
		body = bytes.NewReader(data)
	}
	resp, e := crewTeamRemoteDo(ctx, localOwner, c, method, route, "application/json", body)
	if e != nil {
		return e
	}
	defer resp.Body.Close()
	if out != nil {
		return json.NewDecoder(io.LimitReader(resp.Body, 64<<20)).Decode(out)
	}
	return nil
}

// crewTeamRemoteDo is one request to the team's server with the saved sign-in (renewed when the server says so); a
// non-2xx answer is an error with the server's plain message. The caller closes the body.
func crewTeamRemoteDo(ctx context.Context, localOwner string, c desktopConnection, method, route, contentType string, body io.Reader) (*http.Response, error) {
	req, e := http.NewRequestWithContext(ctx, method, c.Server+route, body)
	if e != nil {
		return nil, e
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", contentType)
	resp, e := crewTeamHTTPClient(ctx).Do(req)
	if e != nil {
		if errors.Is(e, context.DeadlineExceeded) || os.IsTimeout(e) {
			return nil, fmt.Errorf("the server took too long; retrying")
		}
		return nil, fmt.Errorf("cannot reach your company's server; local changes stay on this computer")
	}
	if resp.StatusCode == http.StatusBadGateway || resp.StatusCode == http.StatusGatewayTimeout {
		resp.Body.Close()
		return nil, fmt.Errorf("the server took too long; retrying")
	}
	if err := saveRenewedDesktopToken(localOwner, c, resp.Header.Get("X-Renewed-Token")); err != nil {
		resp.Body.Close()
		return nil, err
	}
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		return nil, fmt.Errorf("%s", laptopActionErrorText(string(b)))
	}
	return resp, nil
}

func (api *StreamingAPI) handleDesktopCrewTeam(w http.ResponseWriter, r *http.Request) {
	if !desktopConnectionGate(w, r) {
		return
	}
	api.runDesktopCrewTeam(w, r)
}

func (api *StreamingAPI) runDesktopCrewTeam(w http.ResponseWriter, r *http.Request) {
	claims := GetUserFromContext(r.Context())
	id := mux.Vars(r)["project"]
	roots, e := api.crewSyncRoots(r.Context(), claims, id)
	if e != nil {
		externalError(w, 403, "team_forbidden", e.Error())
		return
	}
	b, e := crewroot.ReadSync(roots.ID)
	if e != nil || b == nil || b.Side != "local" || b.ConnectionID != mux.Vars(r)["connection"] {
		externalError(w, 409, "team_connection_required", "Turn on this Crew's connection to your company first.")
		return
	}
	c, e := desktopConnectionFor(claims.UserID, b.ConnectionID)
	if e != nil {
		externalError(w, 409, "team_connection_required", e.Error())
		return
	}
	var input *crewTeam
	if r.Method != http.MethodGet {
		input = &crewTeam{}
		if json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(input) != nil {
			externalError(w, 400, "invalid_team", "Choose your team and what it shares.")
			return
		}
		if _, manifest, ok := api.resolveExternalWorkspace(r.Context(), claims, id); ok {
			input.Name = firstNonEmptyTrimmed(manifest.Title, manifest.Label)
		}
		// Each chosen folder is an existing ordinary folder in this Crew; Whole Crew is the Crew's folder itself.
		for _, rel := range input.Folder.Folders {
			if folder, err := crewTeamFolderPath(roots.Files, rel); err != nil || folder == roots.Files {
				externalError(w, 400, "invalid_folder", "Choose existing folders inside this Crew.")
				return
			}
		}
		if input.Folder.Whole {
			files, bytes := crewTeamShareSize(roots.Files, input.Folder)
			log.Printf("[CREW_TEAM] local project=%s event=folder whole=true files=%d bytes=%d", roots.ID, files, bytes)
		} else if len(input.Folder.Folders) == 0 {
			// Back to Folders with none chosen before: the server picks team/, so it exists here (as Team on makes it).
			var current crewTeam
			if crewTeamRemoteJSON(r.Context(), claims.UserID, c, http.MethodGet, "/api/crew-teams/"+url.PathEscape(b.PeerProjectID), nil, &current) == nil && current.Folder.Whole && len(current.LastFolders) == 0 {
				if dir := filepath.Join(roots.Files, crewTeamDefaultFolder); !fileExists(dir) {
					if err := os.MkdirAll(dir, 0o755); err != nil {
						externalError(w, 409, "invalid_folder", "The team folder could not be made on this Mac.")
						return
					}
				}
			}
		}
	}
	var out crewTeam
	if e = crewTeamRemoteJSON(r.Context(), claims.UserID, c, r.Method, "/api/crew-teams/"+url.PathEscape(b.PeerProjectID), input, &out); e != nil {
		externalError(w, 409, "team_unavailable", e.Error())
		return
	}
	// This Mac remembers whether the Team is on, for Main In team by default (crew_team_all.go).
	if err := writeCrewTeamState(roots.ID, func(s *crewTeamLocalState) { s.TeamOn = out.On }); err != nil {
		log.Printf("[CREW_TEAM] local project=%s team state not saved: %v", roots.ID, err)
	}
	externalJSON(w, out)
}

func crewTeamFolderPath(root, rel string) (string, error) {
	if _, err := publication.CleanPath(rel); err != nil {
		return "", err
	}
	dir, err := os.OpenRoot(root)
	if err != nil {
		return "", err
	}
	defer dir.Close()
	for p := rel; p != "."; p = path.Dir(p) {
		info, e := dir.Lstat(p)
		if e != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("choose an ordinary folder inside this project")
		}
	}
	return filepath.Join(root, filepath.FromSlash(rel)), nil
}

// Team folder membership needs Crew access, not an AgentBridge product grant.
func crewTeamAccountActive(user string) bool {
	directory, err := loadUserDirectory()
	if err != nil || directory == nil {
		return false
	}
	record := directory.byID(user)
	return record != nil && !record.Disabled && userAllowedProduct(&UserClaims{UserID: user}, crewProfileID)
}

// Each folder selection has its own content/history and transfer identities.
// Changing a selection keeps previous bytes privately, without mixing folders.
func crewTeamContentDir(project string) (string, error) {
	d, err := crewTeamDir(project)
	if err != nil {
		return "", err
	}
	t, err := readCrewTeam(project)
	if err != nil {
		return "", err
	}
	if t.Folder.ID == "" || !t.Folder.shared() {
		return "", fmt.Errorf("choose a shared folder first")
	}
	return filepath.Join(d, "folders", crewTransferDigest(t.Folder.ID+"\x00"+crewTeamSyncFormat)), nil
}
func crewTeamRequestAuthorize(r *http.Request, claims *UserClaims, project, rel string, write, folder bool) (crewTeam, error) {
	t, err := crewTeamAuthorize(claims, project, rel, write, folder)
	if err != nil {
		return t, err
	}
	if id := r.URL.Query().Get("folder_id"); id != "" && id != t.Folder.ID || write && id == "" {
		return crewTeam{}, fmt.Errorf("the shared folder changed; reopen it before continuing")
	}
	return t, nil
}

// moveCrewTeamToTwin moves a Mac Crew's Team from its previous online copy to the new one when Mac online is turned on
// again (owner, 5 Nov: people and the shared folder survive Mac online off and on). Only the Team's own owner moves it,
// only from a copy that no longer exists, and never over a Team the new copy already has. It stays off; the owner turns
// it on again.
func moveCrewTeamToTwin(owner, previous, created string) {
	old, err := readCrewTeam(previous)
	if err != nil || old.OwnerID != owner || ownerOfCrewProjectID(previous) != "" || ownerOfCrewProjectID(created) != owner {
		return
	}
	from, err := crewTeamDir(previous)
	if err != nil {
		return
	}
	to, err := crewTeamDir(created)
	if err != nil {
		return
	}
	if _, err = os.Lstat(to); !os.IsNotExist(err) {
		return
	}
	if err = os.Rename(from, to); err != nil {
		log.Printf("[CREW_TEAM] project=%s event=moved_failed from=%s reason=%q", created, previous, err)
		return
	}
	old.ProjectID, old.On = created, false
	if err = writeCrewTeam(old); err != nil {
		log.Printf("[CREW_TEAM] project=%s event=moved_failed from=%s reason=%q", created, previous, err)
		return
	}
	log.Printf("[CREW_TEAM] project=%s event=moved from=%s people=%d folder=%q", created, previous, len(old.People), old.Folder.display())
}
