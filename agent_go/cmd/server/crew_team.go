package server

// PLAT-856-509: a Team is a folder grant, never another person's Crew Builder.
import (
	"bytes"
	"context"
	"encoding/json"
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

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	"github.com/manishiitg/coding-agent-loop/workspace/publication"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

type crewTeamPerson struct {
	UserID              string `json:"user_id"`
	CanEditSharedFolder bool   `json:"can_edit_shared_folder"`
}
type crewTeamFolder struct {
	Path  string `json:"path"`
	Label string `json:"label"`
}
type crewTeamServerData struct {
	Kind          string `json:"kind"`
	Name          string `json:"name"`
	SizeBytes     int64  `json:"size_bytes"`
	TeamProjectID string `json:"team_project_id"`
}
type crewTeam struct {
	ServerData          *crewTeamServerData `json:"server_data,omitempty"`
	ProjectID           string              `json:"project_id"`
	Name                string              `json:"name"`
	OwnerID             string              `json:"owner_id"`
	People              []crewTeamPerson    `json:"people"`
	Folder              crewTeamFolder      `json:"folder"`
	Revision            uint64              `json:"revision"`
	OwnerName           string              `json:"owner_name,omitempty"`
	CanManage           bool                `json:"can_manage"`
	CanEditSharedFolder bool                `json:"can_edit_shared_folder"`
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
	t.CanManage = false
	t.CanEditSharedFolder = false
	t.OwnerName = ""
	return fsutil.WriteJSONAtomic(filepath.Join(dir, "team.json"), t, 0600)
}
func (t crewTeam) member(user string) bool {
	return user != "" && (user == t.OwnerID || slices.ContainsFunc(t.People, func(p crewTeamPerson) bool { return p.UserID == user }))
}
func (t crewTeam) edits(user string) bool {
	return t.Folder.Path != "" && (user == t.OwnerID || slices.ContainsFunc(t.People, func(p crewTeamPerson) bool { return p.UserID == user && p.CanEditSharedFolder }))
}
func (t crewTeam) view(user string) crewTeam {
	t.CanManage = t.OwnerID == user
	t.CanEditSharedFolder = t.edits(user)
	t.OwnerName = shareOwnerLabel(t.OwnerID)
	if t.edits(user) {
		if s, e := readCrewTeamFileState(t.ProjectID); e == nil {
			d := &crewTeamServerData{Kind: "team_folder", Name: t.Folder.Path, TeamProjectID: t.ProjectID}
			for _, v := range s.Versions {
				if v.File != nil {
					d.SizeBytes += v.File.Size
				}
			}
			t.ServerData = d
		}
	}
	// Team-only members can discover shared agents but do not learn folder contents.
	if !t.CanEditSharedFolder && !t.CanManage {
		t.Folder = crewTeamFolder{}
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
		return crewTeam{}, fmt.Errorf("ask the owner to turn on Edits shared folder for you")
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
	for _, p := range t.People {
		if IsLocalMode() || crewTeamAccountActive(p.UserID) {
			out = append(out, p.UserID)
		}
	}
	return out
}
func crewTeamMember(project, user string) bool {
	return user != "" && slices.Contains(crewTeamPeople(project), user)
}

// Seed existing chosen-agent audiences once, without granting folder access.
func ensureCrewTeamPeople(project, owner string, people []string) error {
	if IsLocalMode() {
		if b, e := crewroot.ReadSync(project); e == nil && b != nil && b.Side == "local" {
			c, e := desktopConnectionFor(owner, b.ConnectionID)
			if e != nil {
				return e
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			return crewTeamRemoteJSON(ctx, owner, c, http.MethodPost, "/api/crew-teams/"+url.PathEscape(b.PeerProjectID)+"/seed", nil, nil)
		}
	}
	if owner == "" || ownerOfCrewProjectID(project) != owner {
		return fmt.Errorf("only the Crew owner can set its team")
	}
	dir, e := crewTeamDir(project)
	if e != nil {
		return e
	}
	if e = agentworksclient.EnsurePrivateDirectory(dir); e != nil {
		return e
	}
	unlock, e := wf.LockWorkspace(context.Background(), dir)
	if e != nil {
		return e
	}
	defer unlock()
	if _, e = readCrewTeam(project); e == nil {
		return nil
	} else if !os.IsNotExist(e) {
		return e
	}
	t := crewTeam{ProjectID: project, OwnerID: owner, People: []crewTeamPerson{}}
	for _, id := range people {
		if id != owner && crewTeamAccountActive(id) && !t.member(id) {
			t.People = append(t.People, crewTeamPerson{UserID: id})
		}
	}
	log.Printf("[CREW_TEAM] project=%s actor=%s event=seeded people=%d", project, owner, len(t.People))
	return writeCrewTeam(t)
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
		if os.IsNotExist(e) && owner == claims.UserID {
			t = crewTeam{ProjectID: project, OwnerID: owner, People: []crewTeamPerson{}}
			e = nil
		}
		if e != nil || !t.member(claims.UserID) || t.OwnerID != owner || !crewTeamAccountActive(owner) {
			externalError(w, 403, "team_forbidden", "This team is not shared with you. Ask its owner to add you.")
			return
		}
		// Exclude disabled/deleted members from all live audience resolutions.
		t.People = slices.DeleteFunc(t.People, func(p crewTeamPerson) bool { return !crewTeamAccountActive(p.UserID) })
		externalJSON(w, t.view(claims.UserID))
		return
	}
	if owner == "" || owner != claims.UserID {
		externalError(w, 403, "team_owner_required", "Only this Crew's owner can change its team.")
		return
	}
	if mux.Vars(r)["op"] == "seed" {
		ids := []string{}
		for _, a := range readAllMacSharedAgents(project) {
			if a.Audience == "chosen" {
				ids = append(ids, a.People...)
			}
		}
		if e := ensureCrewTeamPeople(project, owner, ids); e != nil {
			externalError(w, 409, "team_save_failed", e.Error())
			return
		}
		t, _ := readCrewTeam(project)
		externalJSON(w, t.view(owner))
		return
	}
	var input crewTeam
	dec := json.NewDecoder(io.LimitReader(r.Body, 64<<10))
	if dec.Decode(&input) != nil {
		externalError(w, 400, "invalid_team", "Choose people and one folder for this team.")
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
	if input.Folder.Path != "" {
		if _, e := publication.CleanPath(input.Folder.Path); e != nil || publication.ExcludedDirectory(input.Folder.Path) || !crewTeamAllowed(input.Folder.Path+"/file.txt") {
			externalError(w, 400, "invalid_folder", "Choose one project subfolder, outside private state and credentials.")
			return
		}
		input.Folder.Label = path.Base(input.Folder.Path)
	}
	for _, p := range input.People {
		if p.CanEditSharedFolder && input.Folder.Path == "" {
			externalError(w, 400, "folder_required", "Choose a shared folder before allowing people to edit it.")
			return
		}
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
	if previous.Folder.Path != "" && previous.Folder.Path != input.Folder.Path {
		externalError(w, 409, "folder_in_use", "This team already has a shared folder. Keep it selected so existing files stay together.")
		return
	}
	input.ProjectID = project
	input.OwnerID = owner
	input.Revision = previous.Revision + 1
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" {
		input.Name = previous.Name
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
	log.Printf("[CREW_TEAM] project=%s actor=%s event=updated people=%d revision=%d", project, owner, len(input.People), input.Revision)
	externalJSON(w, input.view(owner))
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
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		t, e := readCrewTeam(entry.Name())
		if e == nil && t.member(claims.UserID) && ownerOfCrewProjectID(t.ProjectID) == t.OwnerID && crewTeamAccountActive(t.OwnerID) && crewTeamClaims(claims, t.ProjectID, false) {
			out = append(out, t.view(claims.UserID))
		}
	}
	externalJSON(w, map[string]any{"teams": out})
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
	req, e := http.NewRequestWithContext(ctx, method, c.Server+route, body)
	if e != nil {
		return e
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, e := desktopHTTPClient().Do(req)
	if e != nil {
		return fmt.Errorf("cannot reach your company's server; local changes stay on this computer")
	}
	defer resp.Body.Close()
	if token := resp.Header.Get("X-Renewed-Token"); token != "" {
		desktopConnectionsMu.Lock()
		records, err := readDesktopConnections(localOwner)
		if err == nil {
			for i := range records {
				if records[i].ID == c.ID && records[i].Token == c.Token {
					records[i].Token = token
				}
			}
			err = writeDesktopConnections(localOwner, records)
		}
		desktopConnectionsMu.Unlock()
		if err != nil {
			return err
		}
	}
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("%s", laptopActionErrorText(string(b)))
	}
	if out != nil {
		return json.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(out)
	}
	return nil
}

func (api *StreamingAPI) handleDesktopCrewTeam(w http.ResponseWriter, r *http.Request) {
	if !desktopConnectionGate(w, r) {
		return
	}
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
			externalError(w, 400, "invalid_team", "Choose your team and folder.")
			return
		}
		if input.Folder.Path != "" {
			folder, err := crewTeamFolderPath(roots.Files, input.Folder.Path)
			if err != nil || folder == roots.Files {
				externalError(w, 400, "invalid_folder", "Choose a folder inside this Crew.")
				return
			}
			info, err := os.Lstat(folder)
			if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				externalError(w, 400, "invalid_folder", "Choose an existing folder in this Crew.")
				return
			}
		}
	}
	var out crewTeam
	if e = crewTeamRemoteJSON(r.Context(), claims.UserID, c, r.Method, "/api/crew-teams/"+url.PathEscape(b.PeerProjectID), input, &out); e != nil {
		externalError(w, 409, "team_unavailable", e.Error())
		return
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
