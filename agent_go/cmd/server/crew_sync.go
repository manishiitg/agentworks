package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	"github.com/manishiitg/coding-agent-loop/workspace/crewsync"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
	"github.com/manishiitg/multi-llm-provider-go/pkg/sessiontransfer"
)

type crewSyncRequest struct {
	NetworkMessage        *crewNetworkMessage        `json:"network_message,omitempty"`
	MacMainConversationID string                     `json:"mac_main_conversation_id,omitempty"`
	PublishAgent          *crewAgentPublishSelection `json:"publish_agent,omitempty"`
	Provider              string                     `json:"provider,omitempty"`
	Native                []*sessiontransfer.Bundle  `json:"native,omitempty"`
	ArchiveOnly           bool                       `json:"archive_only,omitempty"`
	Mode                  string                     `json:"mode,omitempty"`
	MacID                 string                     `json:"mac_id,omitempty"`
	SplitFiles            bool                       `json:"split_files,omitempty"`
	TransferID            string                     `json:"transfer_id,omitempty"`
	Action                string                     `json:"action"`
	PairID                string                     `json:"pair_id,omitempty"`
	SourceID              string                     `json:"source_id,omitempty"`
	// PreviousProjectID (create): the online copy this Mac Crew had before Mac online was turned off; its Team moves to
	// the new copy, so people and the shared folder survive off and on (owner, 5 Nov).
	PreviousProjectID string `json:"previous_project_id,omitempty"`
	// TeamMessage (team_message): a Mac agent's message to a Team member's agent (crew_team_message.go).
	TeamMessage *crewTeamMessageRequest `json:"team_message,omitempty"`
	Name        string                  `json:"name,omitempty"`
	MacName     string                  `json:"mac_name,omitempty"`
	Editing     string                  `json:"editing,omitempty"`
	Snapshot    *crewsync.Snapshot      `json:"snapshot,omitempty"`
	Agents      []macSharedAgent        `json:"agents,omitempty"` // share_agents: the Mac Crew's shared agents
	// Standby (standby_check, standby_store): the Mac's standby copy of one In team agent (crew_agent_standby.go).
	Standby *crewStandbyRequest `json:"standby,omitempty"`
}
type crewServerCopy struct {
	Name   string `json:"name"`
	Detail string `json:"detail"`
}

type crewSyncReply struct {
	NetworkMessage map[string]any `json:"network_message,omitempty"`
	// Where the caller's own agents in this Crew work when they are a Team member: "mac" or "web" (crew_team_where.go).
	Where string `json:"where,omitempty"`
	// AccessView is which Access page this person gets: "team" (the Team tab alone: a teammate, or the website copy
	// of a Mac Crew), "server_crew" (a Crew on the server: its page and the Team tab), "summary" (a shared Crew).
	AccessView string `json:"access_view,omitempty"`
	// ChatOnly: the owner reaches this Mac Crew through the server, so the screen shows its chat and no side panel (owner,
	// 6 Nov: "mac online.. just reach my agent.. from phone"; crew_owner_on_mac.go).
	ChatOnly           bool                          `json:"chat_only,omitempty"`
	Role               string                        `json:"role,omitempty"`
	OnServer           []crewServerCopy              `json:"on_server,omitempty"`
	Publish            *crewAgentPublishPreview      `json:"publish,omitempty"`
	Placements         map[string]crewAgentPlacement `json:"placements,omitempty"`
	SchedulesPaused    bool                          `json:"schedules_paused"`
	Message            string                        `json:"message,omitempty"`
	BytesDone          int64                         `json:"bytes_done"`
	BytesTotal         int64                         `json:"bytes_total"`
	BackupPath         string                        `json:"backup_path,omitempty"`
	LocalFolderKept    string                        `json:"local_folder_kept,omitempty"`
	RuntimeReadiness   *crewDependencyReport         `json:"runtime_readiness,omitempty"`
	ConversationNotice string                        `json:"conversation_notice,omitempty"`
	TransferID         string                        `json:"transfer_id,omitempty"`
	ProjectID          string                        `json:"project_id"`
	Workspace          string                        `json:"workspace,omitempty"` // the Crew's folder on this server, for its Slack setup from the Mac app
	Binding            *crewroot.SyncBinding         `json:"binding"`
	Snapshot           *crewsync.Snapshot            `json:"snapshot,omitempty"`
	// Place is where the Crew lives for the Access row: mac, mac_off, moving or online (the server decides).
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
	// InboxID: team_message's conversation, for the Mac agent's later reads.
	InboxID string `json:"inbox_id,omitempty"`
	// The Access page's shape (crew_access_page.go): whether this app holds the Mac's controls ("mac"), its tabs,
	// the Mac-only rows as text elsewhere, and its Who can use it and laptop access sections.
	AccessHere string             `json:"access_here,omitempty"`
	AccessTabs []crewAccessTab    `json:"access_tabs,omitempty"`
	AccessMac  *crewAccessMacCard `json:"access_mac,omitempty"`
	// AccessMacLocked: Mac online stays on while the Team is on (owner, 6 Nov); the line its switch shows, locked on.
	AccessMacLocked string          `json:"access_mac_locked,omitempty"`
	AccessPublish   *crewAccessText `json:"access_publish,omitempty"`
	AccessLaptop    bool            `json:"access_laptop,omitempty"`
	// Standby answers standby_check and standby_store (crew_agent_standby.go), handback_pull and handback_done
	// (crew_agent_handback.go).
	Standby *crewStandbyReply `json:"standby,omitempty"`
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
func readCrewSyncManifest(id string) (crewsync.Manifest, error) {
	p, err := crewSyncManifestPath(id)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return crewsync.Manifest{}, nil
	}
	if err != nil {
		return nil, err
	}
	var files crewsync.Manifest
	err = json.Unmarshal(b, &files)
	return files, err
}
func saveCrewSyncManifest(id string, files crewsync.Manifest) error {
	p, err := crewSyncManifestPath(id)
	if err != nil {
		return err
	}
	return fsutil.WriteJSONAtomic(p, files, 0600)
}

// crewSyncClaimsGate is the part of the sync admission that depends only on the connection: a personal sign-in (or full
// Builder connection) allowed to author Crews, with the whole project in reach. The ownership check comes after it.
func crewSyncClaimsGate(claims *UserClaims, id string) *crewSyncFailure {
	if !personalLaptopSettingsClaims(claims) {
		return &crewSyncFailure{Code: crewSyncForbiddenCode, Status: 403, Message: "use your own interactive Crew chat or personal Builder connection to sync"}
	}
	if code, message := externalCrewAuthorGate(claims); code != 0 {
		return &crewSyncFailure{Code: crewSyncForbiddenCode, Status: 403, Message: message}
	}
	if claims.AccessToken != nil && (!claims.AccessToken.Allows("crews:write") || !claims.AccessToken.AllowsCrew(workspaceTokenProjectID(id))) {
		return &crewSyncFailure{Code: crewSyncForbiddenCode, Status: 403, Message: "this connection cannot sync this Crew"}
	}
	if token := claims.AccessToken; token != nil && token.FileGuard != nil {
		guard := token.FileGuard
		if !guard.Allows(".", false) || !guard.Allows(".", true) || len(guard.BlockedPaths)+len(guard.BlockedWritePaths)+len(guard.ReadOnlyPaths) > 0 {
			return &crewSyncFailure{Code: crewSyncForbiddenCode, Status: 403, Message: "sync needs access to the whole project; use the Mac app or a full-project Builder connection"}
		}
	}
	return nil
}

func (api *StreamingAPI) crewSyncRoots(ctx context.Context, claims *UserClaims, id string) (crewroot.Roots, error) {
	if failure := crewSyncClaimsGate(claims, id); failure != nil {
		return crewroot.Roots{}, failure
	}
	binding, _, ok := api.resolveExternalWorkspace(ctx, claims, id)
	if !ok || !binding.OwnedByCaller || binding.OwnerID != claims.UserID {
		if ok && binding.OwnerID != "" {
			return crewroot.Roots{}, &crewSyncFailure{Code: crewSyncForbiddenCode, Status: 403, Message: fmt.Sprintf("only this project's main owner can move it between computers; ask %s to open it on their Mac", shareOwnerLabel(binding.OwnerID))}
		}
		return crewroot.Roots{}, &crewSyncFailure{Code: crewSyncForbiddenCode, Status: 403, Message: "only this project's main owner can move it between computers; ask its owner to open it on their Mac"}
	}
	roots, found, err := crewStorageRoots(binding.Binding.WorkspacePath)
	if err != nil {
		return roots, err
	}
	if !found {
		return roots, fmt.Errorf("this Crew's folder is unavailable")
	}
	return roots, nil
}

// Status and send paths run outside the transfer's workspace lock. Re-read
// under that same lock and change only the requested field, preserving a
// PendingAction or receipt committed by an in-flight transfer.

const crewNativeDivergedCode = "native_history_diverged"
const crewPairInUseCode = "crew_pair_in_use"
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

// The transport carries project content, never paths or credentials chosen by
// another machine. Every operation rechecks the destination's actual owner.
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
	if input.Action == "network_message" {
		api.crewNetworkMessageOnServer(w, r, mux.Vars(r)["project"], input)
		return
	}
	if input.Action == "team_message" {
		api.crewTeamMessage(w, r, mux.Vars(r)["project"], input)
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
			api.crewAccessAddSlack(r, &reply, caller)
			externalJSON(w, reply)
			return
		}
	}
	// A shared server Crew may have Run people or registered co-owners outside a Team.
	// Use the normal project admission; the summary grants no sync or owner controls.
	if r.Method == http.MethodGet && !IsLocalMode() && websiteDeviceClaims(GetUserFromContext(r.Context())) {
		claims := GetUserFromContext(r.Context())
		project := mux.Vars(r)["project"]
		if binding, _, ok := api.resolveExternalWorkspace(r.Context(), claims, project); ok && binding.OwnerID != claims.UserID && crewTeamAccountActive(binding.OwnerID) {
			w.Header().Set("Cache-Control", "no-store")
			externalJSON(w, crewSyncReply{ProjectID: project, Role: "shared", AccessView: "summary", AccessSummary: "Shared with you by " + shareOwnerLabel(binding.OwnerID)})
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
	out.RuntimeReadiness = readCrewDependencies(out.ProjectID)
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
		api.crewAccessAddSlack(r, &out, GetUserIDFromContext(r.Context()))
		// The person's own AgentWorks project is never shared: its Access page is one line, no tabs.
		if agentWorksPrivateProject(r.Context(), out.ProjectID) {
			out.AccessView, out.AccessSummary, out.AccessTabs, out.AccessMac, out.AccessPublish, out.AccessLaptop = "summary", agentWorksPrivateLine, nil, nil, nil, false
			// Its menu keeps the agents and the dashboards it makes, without the Team line or who can open them.
			out.AccessTree = slices.DeleteFunc(out.AccessTree, func(line crewAccessLine) bool { return line.Kind != "agents" && line.Kind != "dashboard" })
			for i := range out.AccessTree {
				out.AccessTree[i].Tag, out.AccessTree[i].Target = "", ""
			}
		}
	}
	externalJSON(w, out)
}

func (api *StreamingAPI) crewSync(ctx context.Context, claims *UserClaims, id string, input crewSyncRequest) (crewSyncReply, error) {
	if input.Action == "publish_accounts" || input.Action == "publish_agent" || input.Action == "stage_agent" {
		return api.receiveCrewAgentPublish(ctx, claims, id, input)
	}
	if input.Action == "standby_check" || input.Action == "standby_store" {
		return api.receiveCrewStandby(ctx, claims, id, input)
	}
	if input.Action == "handback_pull" || input.Action == "handback_done" {
		return api.receiveCrewHandback(ctx, claims, id, input)
	}
	if input.Action == "stage" {
		return api.prepareCrewTransfer(ctx, claims, id, input)
	}
	if input.Action == "create" {
		if input.PairID == "" || input.SourceID == "" || input.Name == "" {
			return crewSyncReply{}, fmt.Errorf("choose a local Crew to sync")
		}
		if !personalLaptopSettingsClaims(claims) || !workspaceCreateTokenAllowed(claims) {
			return crewSyncReply{}, fmt.Errorf("this connection cannot create a Crew")
		}
		// A retried relay create (a lost reply) returns the Mac Crew's existing twin, never a second one.
		if id, b := api.existingCrewRelayTwin(ctx, claims, input.SourceID); b != nil {
			return crewSyncReply{ProjectID: id, Binding: b}, nil
		}
		created, err := api.createWorkspaceFromSpec(ctx, claims, crewProfileID, crewSpec{Name: input.Name}, false)
		if err != nil {
			return crewSyncReply{}, err
		}
		roots, err := api.crewSyncRoots(ctx, claims, created)
		if err != nil {
			return crewSyncReply{}, err
		}
		if input.SplitFiles {
			if err = os.MkdirAll(filepath.Join(roots.Files, crewroot.FilesSegment), 0770); err != nil {
				return crewSyncReply{}, err
			}
		}
		binding := crewroot.SyncBinding{Mode: "relay", ArchiveOnly: input.ArchiveOnly, SplitFiles: input.SplitFiles, PairID: input.PairID, Side: "server", Editing: "local", PeerProjectID: input.SourceID}
		// A relay twin is empty: the owner uses the Mac Crew from the website while the Mac is on (PLAT-856-509).
		binding.MacName, binding.MacID = input.MacName, input.MacID
		if err = crewroot.WriteSync(created, binding); err != nil {
			return crewSyncReply{}, err
		}
		if input.PreviousProjectID != "" {
			moveCrewTeamToTwin(claims.UserID, input.PreviousProjectID, created)
		}
		return crewSyncReply{ProjectID: created, Binding: &binding}, nil
	}
	if input.Action == "status" && IsLocalMode() && websiteDeviceClaims(claims) {
		if receipt, e := readCrewMoveReceipt(id); e == nil && receipt != nil && receipt.Roots.Owner == claims.UserID && receipt.Complete {
			return crewMoveReply(receipt), nil
		}
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
		out.Placements, e = readCrewAgentPlacements(id)
		if e != nil {
			return out, e
		}
		for _, entry := range out.Placements {
			out.OnServer = append(out.OnServer, crewServerCopy{Name: entry.Name, Detail: "native chat, memory, skills and selected working files"})
		}
		out.BytesDone, out.BytesTotal = crewMoveBytes(roots, b)
		if b != nil && b.SchedulesPaused {
			out.SchedulesPaused = true
			out.Message = "Your Mac schedules are paused for this move. Retry Move online to finish."
		}
		if IsLocalMode() && b != nil && b.Side == "local" && b.PendingAction == "server" {
			connection, e := desktopConnectionFor(claims.UserID, b.ConnectionID)
			if e != nil {
				return out, e
			}
			remote, e := desktopCrewSyncCall(ctx, claims.UserID, &connection, b.PeerProjectID, crewSyncRequest{Action: "status"})
			if e != nil {
				return out, e
			}
			if remote.Binding == nil || remote.Binding.PairID != b.PairID {
				return out, fmt.Errorf("the paired Crew is unavailable")
			}
			out.BytesDone, out.BytesTotal = remote.BytesDone, remote.BytesTotal
		}
		return out, nil
	}
	// Main-chat initialization may write the project manifest through workspace
	// HTTP. Complete it before taking the file lock used by that endpoint.
	var main ProductConversationRecord
	if input.Action == "push" && input.Editing == "server" {
		main, err = api.crewHandoffMain(ctx, claims, roots.ID)
		if err != nil {
			return crewSyncReply{}, err
		}
	}
	unlock, err := wf.LockWorkspace(ctx, roots.Files)
	if err != nil {
		return crewSyncReply{}, err
	}
	defer unlock()
	binding, err := crewroot.ReadSync(roots.ID)
	if err != nil {
		return crewSyncReply{}, err
	}
	out := crewSyncReply{ProjectID: roots.ID, Binding: binding, Workspace: roots.Virtual}
	if input.Action == "status" {
		return out, nil
	}
	if binding == nil || input.PairID != binding.PairID || binding.Side != "server" {
		return out, fmt.Errorf("choose the server paired with this Crew")
	}
	switch input.Action {
	case "push":
		if input.TransferID == "" {
			return out, fmt.Errorf("a sync transfer identity is required")
		}
		if input.TransferID == binding.LastTransfer {
			return out, nil
		}
		if err := api.crewSyncIdle(roots); err != nil {
			return out, err
		}
		if binding.Editing != "local" || input.Snapshot == nil {
			return out, fmt.Errorf("this Crew already lives online")
		}
		if input.Editing != "local" && input.Editing != "server" {
			return out, fmt.Errorf("choose where to work")
		}
		if err = verifyCrewTransferDescriptor(roots.ID, binding.PairID, input.TransferID, input.Snapshot); err != nil {
			return out, err
		}
		if err = hydrateCrewPayloads(roots.ID, binding.PairID, input.TransferID, "upload", input.Snapshot); err != nil {
			return out, err
		}
		defer dropCrewPayloadBytes(input.Snapshot)
		if input.Editing == "server" {
			if err = validateCrewMoveData(input.Snapshot.StatePath, binding.PeerProjectID, roots.ID, main.ConversationKey); err != nil {
				return out, err
			}
		}
		if err = applyCrewSyncSnapshot(ctx, roots, binding.PairID, input.Snapshot); err != nil {
			return out, err
		}
		ensureSharedCrewFolderAccess(strings.TrimPrefix(roots.Virtual, "Crew/"), roots.Owner)
		if err = saveCrewSyncSnapshot(roots, *input.Snapshot); err != nil {
			return out, err
		}
		if input.Editing == "server" {
			if err = importCrewMoveDatabase(ctx, roots, input.Snapshot.DatabasePath); err != nil {
				return out, err
			}
			if err = api.importCrewMoveData(ctx, claims, roots, binding.PairID, input.TransferID, main, input.Snapshot.StatePath); err != nil {
				return out, err
			}
			out.ConversationNotice = crewMoveNotice(input.Snapshot.StatePath)
		}
		binding.Editing = input.Editing
		binding.LastTransfer = input.TransferID
	default:
		return out, fmt.Errorf("unknown sync action")
	}
	binding.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	if input.Action == "push" {
		binding.LastCompletedAt = binding.UpdatedAt
	}
	if err = crewroot.WriteSync(roots.ID, *binding); err != nil {
		return out, err
	}
	if input.Action == "push" {
		cleanupCrewPayloads(roots.ID, binding.PairID, input.TransferID, "upload")
	}
	log.Printf("[CREW_SYNC] owner=%s project=%s action=%s editing=%s transfer=%s", claims.UserID, roots.ID, input.Action, binding.Editing, input.TransferID)
	return out, nil
}

func readCrewSyncGit(id string) (*crewsync.GitSnapshot, error) {
	p, err := crewSyncManifestPath(id)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p + ".git")
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var state crewsync.GitSnapshot
	if err = json.Unmarshal(b, &state); err != nil {
		return nil, err
	}
	if state.IndexHash == "" {
		// One-time conversion of the old on-disk baseline. Ignoring its
		// staged patch would mistake unchanged destination staging for edits.
		var old struct {
			IndexPatch []byte `json:"index_patch"`
		}
		if err = json.Unmarshal(b, &old); err != nil {
			return nil, err
		}
		sum := sha256.Sum256(old.IndexPatch)
		state.IndexHash = hex.EncodeToString(sum[:])
		backup := p + ".git." + time.Now().UTC().Format("20060102T150405.000000000Z") + ".bak"
		if err = os.WriteFile(backup, b, 0600); err != nil {
			return nil, err
		}
		if err = fsutil.WriteJSONAtomic(p+".git", state, 0600); err != nil {
			return nil, err
		}
		log.Printf("[CREW_SYNC] project=%s old_git_index=migrated", id)
	}
	return &state, nil
}
func captureCrewSyncSnapshot(ctx context.Context, roots crewroot.Roots, previous crewsync.Manifest) (crewsync.Snapshot, error) {
	stageRoot, err := crewSyncStagingRoot(roots.ID)
	if err != nil {
		return crewsync.Snapshot{}, err
	}
	snapshot, err := captureCrewFiles(ctx, roots, previous, stageRoot, nil)
	if err != nil {
		return snapshot, err
	}
	prior, err := readCrewSyncGit(roots.ID)
	if err != nil {
		return snapshot, err
	}
	snapshot.Git, err = crewsync.CaptureGit(ctx, roots.Files, prior, snapshot.StagingDir)
	if err != nil {
		return snapshot, err
	}
	binding, err := crewroot.ReadSync(roots.ID)
	if err != nil {
		return snapshot, err
	}
	if binding == nil {
		return snapshot, fmt.Errorf("pair this Crew before syncing")
	}
	return snapshot, nil
}

// captureCrewFiles captures the Crew's files with its warm cache (what was read before, hashes and chunk ids only), so
// an unchanged file is not read again, and writes one line of what the capture did.
func captureCrewFiles(ctx context.Context, roots crewroot.Roots, previous crewsync.Manifest, stageRoot string, include func(string, bool) bool) (crewsync.Snapshot, error) {
	p, err := crewSyncManifestPath(roots.ID)
	if err != nil {
		return crewsync.Snapshot{}, err
	}
	cache, err := crewsync.LoadCache(p + ".cache")
	if err != nil {
		return crewsync.Snapshot{}, err
	}
	started := time.Now()
	snapshot, err := crewsync.CaptureCached(ctx, roots, previous, stageRoot, include, cache)
	gaveUp := ""
	var busy *crewsync.BusyFileError
	if errors.As(err, &busy) {
		gaveUp = busy.Key
	}
	log.Printf("[CHUNK_TRANSFER] kind=capture project=%s files=%d read=%d reused=%d copied=%d changes=%d retried=%d gave_up=%q ms=%d", roots.ID, len(snapshot.Files), snapshot.Read, snapshot.Reused, snapshot.Copied, len(snapshot.Changes), snapshot.Retried, gaveUp, time.Since(started).Milliseconds())
	if saveErr := cache.Save(p + ".cache"); err == nil {
		err = saveErr
	}
	return snapshot, err
}

// crewContentBytes is the size of a snapshot's changed files.
func crewContentBytes(s *crewsync.Snapshot) (size int64) {
	for _, change := range s.Changes {
		if change.After != nil {
			size += change.After.Size
		}
	}
	return size
}
func saveCrewSyncSnapshot(roots crewroot.Roots, snapshot crewsync.Snapshot) error {
	id := roots.ID
	binding, err := crewroot.ReadSync(id)
	if err != nil {
		return err
	}
	if binding == nil {
		return fmt.Errorf("Crew is not paired")
	}
	if err := saveCrewSyncManifest(id, snapshot.Files); err != nil {
		return err
	}
	if snapshot.Git == nil {
		return nil
	}
	p, err := crewSyncManifestPath(id)
	if err != nil {
		return err
	}
	state := *snapshot.Git
	state.BundlePath = ""
	return fsutil.WriteJSONAtomic(p+".git", state, 0600)
}

// Editing handoff is between turns. Existing native processes retain the
// permissions they started with, so a running/retained terminal must finish.
func (api *StreamingAPI) crewSyncIdle(roots crewroot.Roots) error {
	if api.crewNativeTurnRunning(roots) {
		return fmt.Errorf("finish or stop this Crew's running chat and terminal before moving online")
	}
	codeShells.Lock()
	defer codeShells.Unlock()
	for _, shell := range codeShells.byID {
		if shell.projectID == roots.ID && shell.ownerID == roots.Owner {
			return fmt.Errorf("stop this Crew's open terminal, then retry Move online")
		}
	}
	return nil
}

func applyCrewSyncSnapshot(ctx context.Context, roots crewroot.Roots, pair string, snapshot *crewsync.Snapshot) (err error) {
	started := time.Now()
	size := crewContentBytes(snapshot)
	defer func() {
		log.Printf("[CREW_SYNC] owner=%s project=%s pair=%s phase=apply bytes=%d paths=%d duration_ms=%d success=%t reason=%q", GetUserIDFromContext(ctx), roots.ID, pair, size, len(snapshot.Changes), time.Since(started).Milliseconds(), err == nil, fmt.Sprint(err))
	}()
	defer snapshot.Close()
	return crewsync.Apply(ctx, roots, *snapshot)
}
func crewSnapshotChanged(snapshot crewsync.Snapshot) bool {
	if len(snapshot.Changes) > 0 {
		return true
	}
	g := snapshot.Git
	if g == nil {
		return false
	}
	return g.Head != g.Before || g.Branch != g.BeforeBranch || g.IndexHash != g.BeforeIndexHash
}
