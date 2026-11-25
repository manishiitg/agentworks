package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"path/filepath"
	"sync"
	"time"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	"github.com/manishiitg/multi-llm-provider-go/pkg/sessiontransfer"
)

type crewSyncRequest struct {
	ArchiveOnly bool   `json:"archive_only,omitempty"`
	MacID       string `json:"mac_id,omitempty"`
	SplitFiles  bool   `json:"split_files,omitempty"`
	Action      string `json:"action"`
	PairID      string `json:"pair_id,omitempty"`
	SourceID    string `json:"source_id,omitempty"`
	// PreviousProjectID (create): the online copy this Mac Crew had before Mac online was turned off; its Team moves to
	// the new copy, so people and the shared folder survive off and on (owner, 5 Nov).
	PreviousProjectID string           `json:"previous_project_id,omitempty"`
	Name              string           `json:"name,omitempty"`
	MacName           string           `json:"mac_name,omitempty"`
	Agents            []macSharedAgent `json:"agents,omitempty"` // share_agents: the Mac Crew's shared agents
}

type crewSyncReply struct {

	// Where the caller's own agents in this Crew work when they are a Team member: "mac" or "web" (crew_team_where.go).
	Where string `json:"where,omitempty"`
	// AccessView is which Access page this person gets: "team" (the Team tab alone: a teammate, or the website copy
	// of a Mac Crew), "server_crew" (a Crew on the server: its page and the Team tab), "summary" (a shared Crew).
	AccessView string `json:"access_view,omitempty"`
	// ChatOnly: the owner reaches this Mac Crew through the server, so the screen shows its chat and no side panel (owner,
	// 6 Nov: "mac online.. just reach my agent.. from phone"; crew_owner_on_mac.go).
	ChatOnly bool `json:"chat_only,omitempty"`
	// ChatOnlyLine is the one line above that chat: which copy this is and what stays on the Mac (owner, 12 Nov).
	ChatOnlyLine string `json:"chat_only_line,omitempty"`
	// ProjectUser: the viewer is a User of this server project (crew_setup_lock.go): the screen is their chat alone
	// (ChatOnly); the project's dashboard opens from its own link.
	ProjectUser bool                  `json:"project_user,omitempty"`
	Role        string                `json:"role,omitempty"`
	Message     string                `json:"message,omitempty"`
	ProjectID   string                `json:"project_id"`
	Workspace   string                `json:"workspace,omitempty"` // the Crew's folder on this server, for its Slack setup from the Mac app
	Binding     *crewroot.SyncBinding `json:"binding"`
	// Place is where the Crew lives for the Access row: mac, mac_off, offline or online (the server decides).
	Place string `json:"place,omitempty"`
	// Agents: share_agents' answer, each with the address the server gave it.
	Agents     []macSharedAgent `json:"agents,omitempty"`
	AgentOwner string           `json:"agent_owner,omitempty"` // the owner's name in each agent's address: agent:<owner>/<address>
	// SharedAgents is how many of a Mac Crew's agents others can message (PLAT-856-509).
	SharedAgents int `json:"shared_agents,omitempty"`
	// Asking is who asked this Crew's shared agents on this Mac in the last ten minutes (the owner's own Mac only).
	Asking []macAskActivity `json:"asking,omitempty"`
	// AccessSummary is the Access row's line after where the Crew lives (crew_access_summary.go).
	AccessSummary string `json:"access_summary,omitempty"`
	// AccessTree is the side panel's tree under the Access row (crew_access_tree.go).
	AccessTree []crewAccessLine `json:"access_tree,omitempty"`
	// The Access page's shape (crew_access_page.go): whether this app holds the Mac's controls ("mac"), its tabs,
	// the Mac-only rows as text elsewhere, and its Who can use it and laptop access sections.
	AccessHere string             `json:"access_here,omitempty"`
	AccessTabs []crewAccessTab    `json:"access_tabs,omitempty"`
	AccessMac  *crewAccessMacCard `json:"access_mac,omitempty"`
	// AccessMacLocked: Mac online stays on while the Team is on (owner, 6 Nov); the line its switch shows, locked on.
	AccessMacLocked string `json:"access_mac_locked,omitempty"`
	AccessLaptop    bool   `json:"access_laptop,omitempty"`
	// AccessCoOwners: a Crew on the server; its Team page is the Access panel with Shared with and Co-owners (21 Nov).
	AccessCoOwners bool `json:"access_co_owners,omitempty"`
	// AccessInTeams: the Teams this online project is in, on its Co-owners page with Remove from Team (#3664).
	AccessInTeams []crewProjectInTeam `json:"access_in_teams,omitempty"`
	// AccessAITeammate: the project's AI teammate switch, on its Access page (owner, 25 Nov).
	AccessAITeammate *crewAITeammateView `json:"access_ai_teammate,omitempty"`
}

func crewSyncManifestPath(id string) (string, error) {
	state, err := workflowCLIStateRoot()
	if err != nil {
		return "", err
	}
	if _, err = crewroot.PrivateRoot(state, id); err != nil {
		return "", err
	}
	return filepath.Join(state, "crew-sync", id+"-files.json"), nil
}

// crewSyncClaimsGate is the part of the sync admission that depends only on the connection: a personal sign-in (or full
// Builder connection) allowed to author Crews, with the whole project in reach. The ownership check comes after it.
func crewSyncClaimsGate(claims *UserClaims, id string) *crewSyncFailure {
	if !personalLaptopSettingsClaims(claims) {
		return &crewSyncFailure{Code: crewSyncForbiddenCode, Status: 403, Message: "use your own interactive project chat or personal Builder connection to sync"}
	}
	if code, message := externalCrewAuthorGate(claims); code != 0 {
		return &crewSyncFailure{Code: crewSyncForbiddenCode, Status: 403, Message: message}
	}
	if claims.AccessToken != nil && (!claims.AccessToken.Allows("crews:write") || !claims.AccessToken.AllowsCrew(workspaceTokenProjectID(id))) {
		return &crewSyncFailure{Code: crewSyncForbiddenCode, Status: 403, Message: "this connection cannot sync this project"}
	}
	if token := claims.AccessToken; token != nil && token.FileGuard != nil {
		guard := token.FileGuard
		if !guard.Allows(".", false) || !guard.Allows(".", true) || len(guard.BlockedPaths)+len(guard.BlockedWritePaths)+len(guard.ReadOnlyPaths) > 0 {
			return &crewSyncFailure{Code: crewSyncForbiddenCode, Status: 403, Message: "sync needs access to the whole project; use the Mac app or a full-project Builder connection"}
		}
	}
	return nil
}

// Sync needs the registry's storage identity, not a model/runtime binding.
// Resolve it once per request, with fresh admission and one identity manifest
// read. Never discover every Crew or expand functions/webhooks/timers here.
func (api *StreamingAPI) crewSyncRoots(ctx context.Context, claims *UserClaims, id string) (crewroot.Roots, error) {
	if failure := crewSyncClaimsGate(claims, id); failure != nil {
		return crewroot.Roots{}, failure
	}
	id = workspaceTokenProjectID(id)
	root := crewProjectRootByID(id)
	roots, found, err := crewStorageRoots(root)
	if err != nil {
		return roots, err
	}
	if !found || roots.ID != id || roots.Owner != claims.UserID {
		return crewroot.Roots{}, &crewSyncFailure{Code: crewSyncForbiddenCode, Status: 403, Message: "only this project's owner can change how it is reached from the phone and the web; ask its owner"}
	}
	manifest, err := readCrewProjectMetadata(ctx, crewProfileID, roots.Virtual)
	if err != nil || manifest.ID != roots.ID {
		return crewroot.Roots{}, fmt.Errorf("this project's settings are unavailable")
	}
	return roots, nil
}

const crewNativeDivergedCode = "native_history_diverged"
const crewSyncForbiddenCode = "crew_sync_forbidden"

// Keep refusal identity and HTTP status separate from user-facing wording.
// Cause retains native divergence identity across wrapped peer replies.
type crewSyncFailure struct {
	Code, Message string
	Status        int
	Cause         error
}

func (e *crewSyncFailure) Error() string { return e.Message }
func (e *crewSyncFailure) Unwrap() error { return e.Cause }

func crewSyncErrorCode(err error) string {
	var failure *crewSyncFailure
	if errors.As(err, &failure) && failure.Code != "" {
		return failure.Code
	}
	if errors.Is(err, sessiontransfer.ErrDiverged) {
		return crewNativeDivergedCode
	}
	return "sync_unavailable"
}
func crewSyncPeerError(code, message string) error {
	if code == crewNativeDivergedCode {
		return sessiontransfer.ErrDiverged
	}
	return fmt.Errorf("server sync failed: %s", message)
}

// Status, the relay twin's create and unpair, and shared agents for a Mac Crew. It carries no project files, paths or
// credentials chosen by another machine; every operation rechecks the Crew's actual owner.
func (api *StreamingAPI) handleCrewSync(w http.ResponseWriter, r *http.Request) {
	var input crewSyncRequest
	if r.Method == http.MethodGet {
		input.Action = "status"
	} else if err := http.NewResponseController(w).SetReadDeadline(time.Now().Add(15 * time.Minute)); err != nil {
		// A Crew upload carries its files and takes longer than the server's 30-second read limit.
		log.Printf("[CREW_SYNC] project=%s read deadline not extended: %v", mux.Vars(r)["project"], err)
	}
	if r.Method != http.MethodGet && json.NewDecoder(io.LimitReader(r.Body, crewSyncMetadataBytes)).Decode(&input) != nil {
		externalError(w, 400, "invalid_request", "Send a valid sync request.")
		return
	}
	if input.Action == "unpair" {
		api.unpairCrewRelayTwin(w, r, mux.Vars(r)["project"], input.PairID)
		return
	}
	if input.Action == "share_agents" {
		api.shareMacCrewAgents(w, r, mux.Vars(r)["project"], input)
		return
	}

	// A joined Team member reads this Crew's Access line too (owner, 5 Nov: a member saw "Only you"); the place and
	// pairing stay the owner's. For them it is an online Crew.
	if r.Method == http.MethodGet && !IsLocalMode() {
		project, caller := mux.Vars(r)["project"], GetUserIDFromContext(r.Context())
		if t, e := readCrewTeam(project); e == nil && t.OwnerID != caller && t.member(caller) && ownerOfCrewProjectID(project) == t.OwnerID && crewTeamAccountActive(t.OwnerID) {
			w.Header().Set("Cache-Control", "no-store")
			// The side panel's Access row gets the same short tree as the owner (owner, 5 Nov: a long one-line summary was
			// cut off), written for the member.
			b, _ := crewroot.ReadSync(project)
			reply := crewSyncReply{ProjectID: project, Place: "online", Role: "member", AccessView: "team", AccessSummary: crewMemberAccessSummary(t),
				AccessTree: api.crewAccessTreeFor(r.Context(), GetUserFromContext(r.Context()), project, b), Where: crewTeamWhereFor(project, caller)}
			crewAccessPage(&reply)
			externalJSON(w, reply)
			return
		}
	}
	// A User of a Crew on the server gets their chat alone, with one line on who controls the setup (crew_setup_lock.go).
	// The dashboard is its own link (owner, 25 Nov: "keep dashboard separate... on right he won't see anything"). Checked before the co-owner page: a User also holds a seat there.
	if r.Method == http.MethodGet && !IsLocalMode() {
		project, caller := mux.Vars(r)["project"], GetUserIDFromContext(r.Context())
		if root := crewProjectRootByID(project); root != "" && crewLivesOnServer(project) && crewIsProjectUser(root, caller) {
			w.Header().Set("Cache-Control", "no-store")
			externalJSON(w, crewSyncReply{ProjectID: project, Place: "online", Role: crewRoleUser, AccessView: "summary", AccessSummary: crewSetupLockedRefusal(root),
				ProjectUser: true, ChatOnly: true, ChatOnlyLine: crewSetupLockedRefusal(root)})
			return
		}
	}
	// A co-owner of a Crew on the server gets the same Access page as its owner, without the owner's laptop access.
	if r.Method == http.MethodGet && !IsLocalMode() {
		project, caller := mux.Vars(r)["project"], GetUserIDFromContext(r.Context())
		if root := crewProjectRootByID(project); root != "" && crewLivesOnServer(project) && crewIsCoOwner(root, caller) {
			w.Header().Set("Cache-Control", "no-store")
			reply := crewSyncReply{ProjectID: project, Place: "online", Role: "co_owner", AccessView: "server_crew",
				AccessTree: api.crewAccessTreeFor(r.Context(), GetUserFromContext(r.Context()), project, nil), AccessInTeams: crewProjectInTeams(project, caller), AccessAITeammate: crewAITeammateFor(project, caller)}
			crewAccessPage(&reply)
			externalJSON(w, reply)
			return
		}
	}
	// A shared server Crew may have Run people outside a Team.
	// Use the normal project admission; the summary grants no sync or owner controls.
	if r.Method == http.MethodGet && !IsLocalMode() && websiteDeviceClaims(GetUserFromContext(r.Context())) {
		claims := GetUserFromContext(r.Context())
		project := mux.Vars(r)["project"]
		if binding, _, ok := api.resolveExternalWorkspace(r.Context(), claims, project); ok && binding.OwnerID != claims.UserID && crewTeamAccountActive(binding.OwnerID) {
			w.Header().Set("Cache-Control", "no-store")
			hasDashboard, err := crewHasPublishedDashboardEntry(r.Context(), binding.Binding.WorkspacePath)
			if err != nil {
				log.Printf("[DASHBOARD_DELETE] project=%s event=failed reason=shared_entry_read", project)
				externalError(w, 503, "dashboard_unavailable", "Could not load this project's dashboard.")
				return
			}
			log.Printf("[DASHBOARD_DELETE] project=%s event=shared_entry dashboard=%t ask_only=%t", project, hasDashboard, !hasDashboard)
			externalJSON(w, crewSyncReply{ProjectID: project, Role: "shared", AccessView: "summary", AccessSummary: "Shared with you by " + shareOwnerLabel(binding.OwnerID), ChatOnly: !hasDashboard})
			return
		}
	}

	out, err := api.crewSync(r.Context(), GetUserFromContext(r.Context()), mux.Vars(r)["project"], input)
	if err != nil {
		status := 409
		var failure *crewSyncFailure
		if errors.As(err, &failure) && failure.Status >= 400 && failure.Status < 500 {
			status = failure.Status
		}
		externalError(w, status, crewSyncErrorCode(err), err.Error())
		return
	}
	out.Role = "owner"
	if !IsLocalMode() && crewRelayTwin(out.ProjectID) {
		out.AccessView = "team"
		out.ChatOnly = crewOwnerOnlineOfMacCrew(r, out.ProjectID, GetUserIDFromContext(r.Context()))
	} else if !IsLocalMode() {
		// A Crew that lives on the server has the same Team tab (owner, 5 Nov) under its normal Access page.
		out.AccessView = "server_crew"
	}
	if r.Method == http.MethodGet && out.Place == "" {
		out.Place = crewPlace(GetUserIDFromContext(r.Context()), out.ProjectID, out.Binding)
	}
	if r.Method == http.MethodGet {
		out.SharedAgents = len(readMacSharedAgents(out.ProjectID))
		out.AccessSummary = api.crewAccessSummaryFor(r.Context(), GetUserIDFromContext(r.Context()), out.ProjectID, out.Binding)
		out.AccessTree = api.crewAccessTreeFor(r.Context(), GetUserFromContext(r.Context()), out.ProjectID, out.Binding)
		out.Where = crewTeamWhereFor(out.ProjectID, GetUserIDFromContext(r.Context()))
		if IsLocalMode() {
			out.Asking = api.recentMacAsks(out.ProjectID)
		}
		crewAccessPage(&out)
		if out.AccessCoOwners {
			out.AccessInTeams = crewProjectInTeams(out.ProjectID, GetUserIDFromContext(r.Context()))
			out.AccessAITeammate = crewAITeammateFor(out.ProjectID, GetUserIDFromContext(r.Context()))
		}
		if out.ChatOnly {
			name := out.ProjectID
			if _, doc, ok := api.resolveExternalWorkspace(r.Context(), GetUserFromContext(r.Context()), "work:"+out.ProjectID); ok {
				if title := doc.displayTitle(); title != "" {
					name = title
				}
			}
			mac := ""
			if out.Binding != nil {
				mac = out.Binding.MacName
			}
			out.ChatOnlyLine = crewChatOnlyLine(name, mac, out.Place == "mac")
		}
	}
	externalJSON(w, out)
}

// Serialize the lookup/create of one server identity for a Mac source.
var crewRelayCreateMu sync.Mutex

func (api *StreamingAPI) crewSync(ctx context.Context, claims *UserClaims, id string, input crewSyncRequest) (crewSyncReply, error) {
	if input.Action == "create" {
		if input.PairID == "" || input.SourceID == "" || input.Name == "" {
			return crewSyncReply{}, fmt.Errorf("choose a local project to sync")
		}
		if !personalLaptopSettingsClaims(claims) || !workspaceCreateTokenAllowed(claims) {
			return crewSyncReply{}, fmt.Errorf("this connection cannot create a project")
		}
		// A retried relay create (a lost reply) returns the Mac Crew's existing twin, never a second one.
		crewRelayCreateMu.Lock()
		defer crewRelayCreateMu.Unlock()
		existing, b, err := api.existingCrewRelayTwin(ctx, claims, input.SourceID)
		if err != nil {
			return crewSyncReply{}, err
		}
		if b != nil {
			return crewSyncReply{ProjectID: existing, Binding: b}, nil
		}
		binding := crewroot.SyncBinding{Mode: "relay", ArchiveOnly: input.ArchiveOnly, SplitFiles: input.SplitFiles, PairID: input.PairID, Side: "server", Editing: "local", PeerProjectID: input.SourceID, MacName: input.MacName, MacID: input.MacID}
		created, err := api.createWorkspace(ctx, claims, crewProfileID, crewSpec{Name: input.Name}, false, workspaceCreation{SyncBinding: &binding})
		if err != nil {
			return crewSyncReply{}, err
		}
		if err := crewroot.WriteSync(created, binding); err != nil {
			return crewSyncReply{}, err
		}
		if input.PreviousProjectID != "" {
			moveCrewTeamToTwin(claims.UserID, input.PreviousProjectID, created)
		}
		return crewSyncReply{ProjectID: created, Binding: &binding}, nil
	}
	roots, err := api.crewSyncRoots(ctx, claims, id)
	if err != nil {
		return crewSyncReply{}, err
	}
	if input.Action == "status" {
		b, e := crewroot.ReadSync(id)
		if e != nil {
			return crewSyncReply{}, e
		}
		out := crewSyncReply{ProjectID: id, Binding: b, Workspace: roots.Virtual, Place: crewPlace(claims.UserID, id, b)}
		return out, nil
	}
	log.Printf("[AGENT_OFFLINE_COPY] project=%s actor=%s event=refused reason=unsupported_online_action", id, claims.UserID)
	return crewSyncReply{}, fmt.Errorf("unknown sync action")
}
