package server

import (
	"context"
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

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

// One working copy per Team member (owner, 5 Nov): a member's own agents in a Crew work either on the website (the
// server runs them, in their server copy of the shared folder) or on their Mac (after "Add to my Mac": their Mac runs
// them, relayed through this online copy like the owner's Mac agents). The server records which, per person, and
// decides from it: a member on their Mac gets no server turns and no server copy in this Crew.

// crewTeamMemberMac is the member's Mac pair when their agents in this Crew work on their Mac.
func (t crewTeam) memberMac(user string) (string, bool) {
	if user == "" || user == t.OwnerID || !t.member(user) {
		return "", false
	}
	at := slices.IndexFunc(t.People, func(p crewTeamPerson) bool { return p.UserID == user })
	if at < 0 || t.People[at].Where != "mac" || t.People[at].MacPair == "" {
		return "", false
	}
	return t.People[at].MacPair, true
}

// crewTeamMemberOnMac: this member's agents in this Crew work on their Mac (from the server's record).
func crewTeamMemberOnMac(project, user string) bool {
	t, err := readCrewTeam(project)
	if err != nil || ownerOfCrewProjectID(project) != t.OwnerID {
		return false
	}
	_, ok := t.memberMac(user)
	return ok
}

// handleCrewTeamWhere sets where the calling member's own agents work: {"where":"mac"} from their Mac (it gets a pair
// back, or keeps the one it sends), {"where":"web"} from anywhere.
func (api *StreamingAPI) handleCrewTeamWhere(w http.ResponseWriter, r *http.Request) {
	claims := GetUserFromContext(r.Context())
	project := mux.Vars(r)["project"]
	if !crewTeamClaims(claims, project, true) {
		externalError(w, 403, "team_forbidden", "Open this Team with your own account.")
		return
	}
	var in struct {
		Where string `json:"where"`
		Pair  string `json:"pair,omitempty"`
	}
	if json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&in) != nil || in.Where != "mac" && in.Where != "web" {
		externalError(w, 400, "invalid_where", "Choose your Mac or the web.")
		return
	}
	if in.Where == "mac" && !crewChatWebsiteClaims(claims) {
		externalError(w, 403, "mac_sign_in_required", "Add this Crew from the AgentWorks app on your Mac.")
		return
	}
	dir, err := crewTeamDir(project)
	if err != nil {
		externalError(w, 400, "invalid_team", err.Error())
		return
	}
	unlock, err := wf.LockWorkspace(r.Context(), dir)
	if err != nil {
		externalError(w, 409, "team_save_failed", err.Error())
		return
	}
	defer unlock()
	t, err := readCrewTeam(project)
	if err != nil || t.OwnerID == claims.UserID || !t.member(claims.UserID) || ownerOfCrewProjectID(project) != t.OwnerID || !crewTeamAccountActive(t.OwnerID) {
		externalError(w, 403, "team_member_required", "Only a joined member chooses where their own agents work.")
		return
	}
	at := slices.IndexFunc(t.People, func(p crewTeamPerson) bool { return p.UserID == claims.UserID })
	person := &t.People[at]
	switch in.Where {
	case "mac":
		person.Where = "mac"
		// One working copy: the Mac that already holds it keeps its pair; any other Mac (or a new copy) gets a new
		// one, and the previous Mac is no longer admitted for this Crew.
		if in.Pair == "" || in.Pair != person.MacPair {
			if previous := person.MacPair; previous != "" {
				dropCrewMacPair(claims.UserID, project, previous)
				log.Printf("[CREW_TEAM] project=%s actor=%s event=where_replaced previous_mac_refused=true", project, logUsernameForUserID(claims.UserID))
			}
			person.MacPair = uuid.NewString()
		}
	default:
		if person.MacPair != "" {
			dropCrewMacPair(claims.UserID, project, person.MacPair)
		}
		person.Where, person.MacPair = "", ""
	}
	t.Revision++
	if err = writeCrewTeam(t); err != nil {
		externalError(w, 409, "team_save_failed", err.Error())
		return
	}
	log.Printf("[CREW_TEAM] project=%s actor=%s event=where where=%s revision=%d", project, logUsernameForUserID(claims.UserID), in.Where, t.Revision)
	if in.Where == "mac" {
		// Their server copy goes now: their agents here work on their Mac, with its copy (crew_team_server_copy.go).
		go api.removeStaleCrewTeamServerCopies(context.WithoutCancel(r.Context()))
	}
	externalJSON(w, map[string]any{"where": in.Where, "pair": person.MacPair, "team": t.viewLocked(claims.UserID)})
}

// crewRelayReadKey marks a relay request that only reads a Mac chat (its events, status or queue).
type crewRelayReadKey struct{}

// crewTeamMemberMacRead is the member's Mac pair for a read-only relay request of their own Mac chats.
func crewTeamMemberMacRead(r *http.Request, project, user string) (string, bool) {
	if read, _ := r.Context().Value(crewRelayReadKey{}).(bool); !read {
		return "", false
	}
	t, err := readCrewTeam(project)
	if err != nil || ownerOfCrewProjectID(project) != t.OwnerID {
		return "", false
	}
	return t.memberMac(user)
}

// crewTeamMemberCopy finds this Mac's working copy of a Team's Crew (the online copy project), if it has one.
func crewTeamMemberCopy(project string) string {
	return crewLocalCrewPairedWith(project, func(id string) bool { return readCrewTeamState(id).MemberOf == project })
}

// crewLocalCopyOf is the Crew on this Mac paired with that online copy: the owner's Mac Crew or a member's copy.
func crewLocalCopyOf(project string) string {
	return crewLocalCrewPairedWith(project, func(string) bool { return true })
}

func crewLocalCrewPairedWith(project string, keep func(string) bool) string {
	state, err := workflowCLIStateRoot()
	if err != nil || project == "" {
		return ""
	}
	entries, _ := os.ReadDir(filepath.Join(state, "crew-sync"))
	for _, entry := range entries {
		id, ok := strings.CutSuffix(entry.Name(), ".json")
		if entry.IsDir() || !ok || strings.HasSuffix(id, "-files") {
			continue
		}
		if b, e := crewroot.ReadSync(id); e == nil && b != nil && b.Side == "local" && b.PeerProjectID == project && keep(id) {
			return id
		}
	}
	return ""
}

// addCrewTeamToMac makes this Mac the member's working copy of the Team's Crew: a Crew of theirs on this Mac in the
// team's folder (only their Main to start; the Crew's own setup reaches it through the team folder), paired to the
// online copy as the member, and the server records that their agents here work on this Mac.
func (api *StreamingAPI) addCrewTeamToMac(ctx context.Context, claims *UserClaims, c desktopConnection, project string, team crewTeam, folder string) (string, error) {
	local := crewTeamMemberCopy(project)
	pair := ""
	if local != "" {
		if b, err := crewroot.ReadSync(local); err == nil && b != nil {
			pair = b.PairID
		}
	}
	var where struct {
		Pair string `json:"pair"`
	}
	if err := crewTeamRemoteJSON(ctx, claims.UserID, c, http.MethodPost, "/api/crew-teams/"+url.PathEscape(project)+"/where", map[string]string{"where": "mac", "pair": pair}, &where); err != nil {
		return "", err
	}
	if where.Pair == "" {
		return "", fmt.Errorf("the server did not pair this Mac; update the server")
	}
	if local == "" {
		if folder == "" {
			var err error
			if folder, err = crewTeamDefaultJoinFolder(team.Name); err != nil {
				return "", err
			}
		}
		if err := os.MkdirAll(folder, 0o700); err != nil {
			return "", err
		}
		name := []rune(firstNonEmptyTrimmed(team.Name, "Team"))
		if len(name) > 60 {
			name = name[:60]
		}
		purpose := "Your working copy of " + firstNonEmptyTrimmed(team.OwnerName, "a teammate") + "'s Crew. Your agents here work on this Mac."
		id, err := api.createCrewFromSpec(ctx, claims, crewSpec{Name: string(name), Purpose: purpose, RootPath: folder}, false)
		if err != nil {
			return "", err
		}
		local = id
	}
	if err := writeCrewTeamState(local, func(s *crewTeamLocalState) { s.MemberOf, s.SharedBy = project, team.OwnerName }); err != nil {
		return "", err
	}
	if err := crewroot.WriteSync(local, crewroot.SyncBinding{Mode: "relay", PairID: where.Pair, Side: "local", Editing: "local", ConnectionID: c.ID,
		PeerProjectID: project, PeerServer: c.Server, MacName: desktopMacDisplayName()}); err != nil {
		return "", err
	}
	log.Printf("[CREW_TEAM_LOCAL] project=%s event=member_copy_on_mac online_project=%s", local, project)
	publishCrewPanelChanged(local)
	return local, nil
}

// handleDesktopCrewTeamRemoveFromMac moves the member's own agents of a Team's Crew back to the website: the server
// records web first, then this Mac stops offering its copy. The Crew and its folder stay on this Mac as they are.
func (api *StreamingAPI) handleDesktopCrewTeamRemoveFromMac(w http.ResponseWriter, r *http.Request) {
	if !desktopConnectionGate(w, r) {
		return
	}
	claims := GetUserFromContext(r.Context())
	connectionID, project := mux.Vars(r)["connection"], mux.Vars(r)["project"]
	if !crewTeamClientID(project) {
		externalError(w, 400, "invalid_team", "Choose a team.")
		return
	}
	c, err := desktopConnectionFor(claims.UserID, connectionID)
	if err != nil {
		externalError(w, 409, "team_connection_required", err.Error())
		return
	}
	if err = crewTeamRemoteJSON(r.Context(), claims.UserID, c, http.MethodPost, "/api/crew-teams/"+url.PathEscape(project)+"/where", map[string]string{"where": "web"}, nil); err != nil {
		externalError(w, 409, "where_failed", err.Error())
		return
	}
	// The copy stays paired by name only (Editing "server": this Mac no longer offers it), so adding it again later
	// reuses the same Crew and folder.
	local := crewTeamMemberCopy(project)
	if local != "" {
		if b, e := crewroot.ReadSync(local); e == nil && b != nil {
			b.Editing = "server"
			_ = crewroot.ReplaceSync(local, *b)
		}
	}
	log.Printf("[CREW_TEAM_LOCAL] project=%s event=member_copy_off_mac online_project=%s", local, project)
	if local != "" {
		publishCrewPanelChanged(local)
	}
	externalJSON(w, map[string]any{"where": "web", "project_id": local})
}

// dropCrewMacPair stops routing this Crew to the person's connection that offered the old pair; its next offer is
// refused (crewRelayAdmit).
func dropCrewMacPair(user, project, pair string) {
	crewMacSockets.Lock()
	defer crewMacSockets.Unlock()
	for _, sock := range crewMacSockets.byDevice {
		if sock.owner != user {
			continue
		}
		sock.mu.Lock()
		if sock.twins[project] == pair {
			delete(sock.twins, project)
			log.Printf("[CREW_RELAY] removed owner=%s device=%s project=%s reason=member_pair_replaced", sanitizeUserIDForPath(user), sock.device, project)
		}
		sock.mu.Unlock()
	}
	publishCrewPanelChanged(project)
}

// crewTeamWhereFor is where the caller's own agents in this Crew work, when they are a Team member: on the server
// from the Team record; on a Mac for its member copy, from whether this Mac still offers it.
func crewTeamWhereFor(project, user string) string {
	if IsLocalMode() {
		if readCrewTeamState(project).MemberOf == "" {
			return ""
		}
		if b, err := crewroot.ReadSync(project); err == nil && b != nil && b.Editing == "local" {
			return "mac"
		}
		return "web"
	}
	t, err := readCrewTeam(project)
	if err != nil || ownerOfCrewProjectID(project) != t.OwnerID {
		return ""
	}
	if t.member(user) && user != t.OwnerID {
		if _, onMac := t.memberMac(user); onMac {
			return "mac"
		}
		return "web"
	}
	return ""
}

// handleDesktopCrewTeamAddToMac moves the member's agents of a Team's Crew back to this Mac's existing copy ("Add to
// this Mac instead"); a Mac without one adds it from the Team invite.
func (api *StreamingAPI) handleDesktopCrewTeamAddToMac(w http.ResponseWriter, r *http.Request) {
	if !desktopConnectionGate(w, r) {
		return
	}
	claims := GetUserFromContext(r.Context())
	connectionID, project := mux.Vars(r)["connection"], mux.Vars(r)["project"]
	if !crewTeamClientID(project) {
		externalError(w, 400, "invalid_team", "Choose a team.")
		return
	}
	c, err := desktopConnectionFor(claims.UserID, connectionID)
	if err != nil {
		externalError(w, 409, "team_connection_required", err.Error())
		return
	}
	if crewTeamMemberCopy(project) == "" {
		externalError(w, 404, "no_mac_copy", "This Mac has no copy of this Crew yet. Use Add to my Mac on the Team invite.")
		return
	}
	var team crewTeam
	if err = crewTeamRemoteJSON(r.Context(), claims.UserID, c, http.MethodGet, "/api/crew-teams/"+url.PathEscape(project), nil, &team); err != nil {
		externalError(w, 409, "team_unavailable", err.Error())
		return
	}
	local, err := api.addCrewTeamToMac(r.Context(), claims, c, project, team, "")
	if err != nil {
		externalError(w, 409, "mac_copy_failed", err.Error())
		return
	}
	externalJSON(w, map[string]any{"where": "mac", "project_id": local})
}
