package server

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowtypes"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	"github.com/manishiitg/coding-agent-loop/workspace/crewsync"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

// A Team member's own agents on the server (owner, 5 Nov, "A Team is a shared room") work in their own copy of the
// team's shared folder, kept in the app's private state (their turns run as the app account on their own login).
// The copy syncs with the team store through the same protocol a Mac uses, run in process with the member's own
// claims, so the server's checks (member, folder access, folder generation) stop it at the next round and every
// change is recorded as theirs. It syncs before each of their turns and every few seconds while it exists.

const crewTeamServerHost = "http://team-server-copy.invalid"

// crewTeamCopyReasonKey names why a member's copy is made, for its log line (turn, or accepted on joining).
type crewTeamCopyReasonKey struct{}

// crewTeamTransportKey carries an in-process transport for the team sync calls (crewTeamHTTPClient).
type crewTeamTransportKey struct{}

// crewTeamInProcess serves a team sync request inside this server, as the member it belongs to.
type crewTeamInProcess struct {
	handler http.Handler
	claims  *UserClaims
}

func (t crewTeamInProcess) RoundTrip(req *http.Request) (*http.Response, error) {
	r := req.Clone(context.WithValue(req.Context(), UserContextKey, t.claims))
	r.RequestURI = ""
	r.Header.Del("Authorization")
	recorder := httptest.NewRecorder()
	t.handler.ServeHTTP(recorder, r)
	return recorder.Result(), nil
}

// crewTeamHTTPClient is the client team sync calls use: the in-process one for a server copy, else the Mac's.
func crewTeamHTTPClient(ctx context.Context) *http.Client {
	if transport, ok := ctx.Value(crewTeamTransportKey{}).(http.RoundTripper); ok {
		return &http.Client{Transport: transport, Timeout: 2 * time.Minute}
	}
	return desktopHTTPClient()
}

func (api *StreamingAPI) crewTeamServerHandler() http.Handler {
	router := mux.NewRouter()
	api.registerCrewTeamRoutes(router.PathPrefix("/api").Subrouter())
	return router
}

// crewTeamServerCopyDir is one member's copy of one shared folder (by its generation id), private to the app.
func crewTeamServerCopyDir(member, project, folderID string) (string, error) {
	state, err := crewroot.StateRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(state, "team-working", "server", crewTransferDigest(member), crewTransferDigest(project+"\x00"+folderID)), nil
}

// syncCrewTeamServerCopy brings a member's copy of the team folder up to date both ways; create makes it on first use.
// It returns the copy's folder, or "" when the member has no shared folder in this team.
func (api *StreamingAPI) syncCrewTeamServerCopy(ctx context.Context, member, project string, create bool) (string, error) {
	if IsLocalMode() {
		return "", nil
	}
	t, err := readCrewTeam(project)
	if err != nil || member == t.OwnerID || t.Folder.ID == "" || !t.edits(member) {
		return "", nil
	}
	if _, onMac := t.memberMac(member); onMac {
		return "", nil // their agents here work on their Mac, with its copy
	}
	if !crewRelayTwin(project) {
		return "", nil // a Crew on the server: members on the web work in its own folder, like co-owners
	}
	dir, err := crewTeamServerCopyDir(member, project, t.Folder.ID)
	if err != nil {
		return "", err
	}
	b, err := readCrewTeamLocal(dir)
	if os.IsNotExist(err) {
		if !create {
			return "", nil
		}
		b = crewTeamLocal{FolderID: t.Folder.ID, Connection: "server", Project: project, RemoteUser: member, Client: uuid.NewString(),
			Root: filepath.Join(dir, "files"), LocalBase: crewsync.Manifest{}, RemoteBase: crewsync.Manifest{}, Status: "pending"}
		if err = os.MkdirAll(b.Root, 0o700); err != nil {
			return "", err
		}
		if err = saveCrewTeamLocal(dir, b); err != nil {
			return "", err
		}
		reason, _ := ctx.Value(crewTeamCopyReasonKey{}).(string)
		log.Printf("[CREW_TEAM_SERVER] project=%s member=%s folder=%q event=created reason=%s", project, logUsernameForUserID(member), t.Folder.display(), firstNonEmptyTrimmed(reason, "turn"))
	} else if err != nil {
		return "", err
	}
	c := desktopConnection{ID: "server", Server: crewTeamServerHost, User: UserInfo{ID: member}}
	sync := context.WithValue(ctx, crewTeamTransportKey{}, crewTeamInProcess{handler: api.crewTeamServerHandler(), claims: &UserClaims{UserID: member, Username: member}})
	if err = syncCrewTeamLocal(sync, member, c, dir, &b); err != nil {
		log.Printf("[CREW_TEAM_SERVER] project=%s member=%s folder=%q event=sync_failed step=%s reason=%q", project, logUsernameForUserID(member), t.Folder.display(), crewTeamStepOf(err), err)
		return b.Root, err
	}
	log.Printf("[CREW_TEAM_SERVER] project=%s member=%s folder=%q event=synced status=%s", project, logUsernameForUserID(member), t.Folder.display(), b.Status)
	// Member turns on the web use the online copy's setup: it follows the Team's team-setup.json (crew_team_setup.go).
	api.reconcileCrewTeamSetup(ctx, t.OwnerID, project, b.Root, dir, false)
	return b.Root, nil
}

// startCrewTeamServerCopies keeps every existing member copy in step with the team while nobody is in a turn.
func (api *StreamingAPI) startCrewTeamServerCopies(ctx context.Context) {
	if IsLocalMode() {
		return
	}
	go func() {
		for ctx.Err() == nil {
			removeStaleCrewTeamServerCopies(ctx)
			for _, t := range crewTeamsOnServer() {
				// A Crew on the server: its own folder is the Team store's copy for members on their Mac.
				round, done := context.WithTimeout(ctx, 2*time.Minute)
				api.syncServerCrewTeamStore(round, t)
				done()
				for _, p := range t.People {
					if p.Invited {
						continue
					}
					round, done := context.WithTimeout(ctx, 2*time.Minute)
					_, _ = api.syncCrewTeamServerCopy(round, p.UserID, t.ProjectID, false)
					done()
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
		}
	}()
}

// removeStaleCrewTeamServerCopies deletes a member's copy once they no longer have that folder: they left or were
// removed, the Team is off, or the folder changed. It holds team files they have no right to, and nothing needs it
// (their changes were uploaded already, or they lost the right to upload them).
func removeStaleCrewTeamServerCopies(ctx context.Context) {
	state, err := crewroot.StateRoot()
	if err != nil {
		return
	}
	dirs, _ := filepath.Glob(filepath.Join(state, "team-working", "server", "*", "*"))
	for _, dir := range dirs {
		b, err := readCrewTeamLocal(dir)
		if err != nil {
			continue
		}
		t, err := readCrewTeam(b.Project)
		_, onMac := t.memberMac(b.RemoteUser)
		// The owner's copy of a Crew on the server is the Crew folder itself: kept while it serves Mac members, and
		// only its sync record goes when it is no longer needed (the Crew folder is never removed).
		if err == nil && b.RemoteUser == t.OwnerID && !crewRelayTwin(b.Project) {
			if serverCrewTeamStoreNeeded(t) && t.Folder.ID == b.FolderID {
				continue
			}
			if e := os.RemoveAll(dir); e == nil {
				log.Printf("[CREW_TEAM_SERVER] project=%s event=owner_copy_stopped", b.Project)
			}
			continue
		}
		if err == nil && t.On && b.RemoteUser != t.OwnerID && t.Folder.ID == b.FolderID && t.edits(b.RemoteUser) && !onMac && crewRelayTwin(b.Project) {
			continue
		}
		unlock, err := wf.LockWorkspace(ctx, dir)
		if err != nil {
			continue
		}
		err = os.RemoveAll(dir)
		unlock()
		reason := "no_access"
		if onMac {
			reason = "on_mac" // their agents here work on their Mac, with its own copy
		}
		log.Printf("[CREW_TEAM_SERVER] project=%s member=%s event=removed reason=%s err=%v", b.Project, logUsernameForUserID(b.RemoteUser), reason, err)
	}
}

func crewTeamsOnServer() []crewTeam {
	root, err := crewroot.StateRoot()
	if err != nil {
		return nil
	}
	entries, _ := os.ReadDir(filepath.Join(root, "team-folders"))
	out := []crewTeam{}
	for _, entry := range entries {
		if t, err := readCrewTeam(entry.Name()); err == nil && entry.IsDir() && t.On && t.Folder.ID != "" {
			out = append(out, t)
		}
	}
	return out
}

var crewTeamAliasClean = regexp.MustCompile(`[^a-z0-9]+`)

// crewTeamServerFolders are a member's team folder copies on this server as attached folders, so their agents find
// them as WORK_FOLDER_<NAME> in the folders section they already know (read and write).
func crewTeamServerFolders(claims *UserClaims) []workflowtypes.WorkflowFolderGrant {
	if IsLocalMode() || claims == nil || claims.UserID == "" {
		return nil
	}
	var grants []workflowtypes.WorkflowFolderGrant
	used := map[string]bool{}
	for _, t := range crewTeamsOnServer() {
		if claims.UserID == t.OwnerID || !t.edits(claims.UserID) {
			continue
		}
		dir, err := crewTeamServerCopyDir(claims.UserID, t.ProjectID, t.Folder.ID)
		if err != nil {
			continue
		}
		b, err := readCrewTeamLocal(dir)
		if err != nil || b.RemoteUser != claims.UserID || b.FolderID != t.Folder.ID {
			continue
		}
		if info, err := os.Lstat(b.Root); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		// The copy has the Crew's layout, so it is named after the Crew.
		alias := strings.Trim(crewTeamAliasClean.ReplaceAllString(strings.ToLower(t.Name), "_"), "_")
		if alias == "" {
			alias = "team"
		}
		for n := 2; used[alias]; n++ {
			alias = fmt.Sprintf("%s_%d", strings.TrimRight(alias, "_0123456789"), n)
		}
		used[alias] = true
		grants = append(grants, workflowtypes.WorkflowFolderGrant{ID: "team-" + crewTransferDigest(t.ProjectID + "\x00" + t.Folder.ID)[:12], Alias: alias, Path: b.Root,
			Access: workflowtypes.FolderAccessReadWrite, Reason: t.Name + " · shared with the team: " + t.Folder.display() + " (your copy; it syncs with the team)"})
	}
	return grants
}
