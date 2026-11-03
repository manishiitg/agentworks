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
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/filesnapshots"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	"github.com/manishiitg/coding-agent-loop/workspace/crewsync"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
	"github.com/manishiitg/multi-llm-provider-go/pkg/sessiontransfer"
)

type crewSyncRequest struct {
	Native      []*sessiontransfer.Bundle `json:"native,omitempty"`
	ArchiveOnly bool                      `json:"archive_only,omitempty"`
	Mode        string                    `json:"mode,omitempty"`
	MacID       string                    `json:"mac_id,omitempty"`
	SplitFiles  bool                      `json:"split_files,omitempty"`
	TransferID  string                    `json:"transfer_id,omitempty"`
	Action      string                    `json:"action"`
	PairID      string                    `json:"pair_id,omitempty"`
	SourceID    string                    `json:"source_id,omitempty"`
	Name        string                    `json:"name,omitempty"`
	MacName     string                    `json:"mac_name,omitempty"`
	Editing     string                    `json:"editing,omitempty"`
	Snapshot    *crewsync.Snapshot        `json:"snapshot,omitempty"`
}
type crewSyncReply struct {
	SchedulesPaused    bool                  `json:"schedules_paused"`
	Message            string                `json:"message,omitempty"`
	BytesDone          int64                 `json:"bytes_done"`
	BytesTotal         int64                 `json:"bytes_total"`
	BackupPath         string                `json:"backup_path,omitempty"`
	LocalFolderKept    string                `json:"local_folder_kept,omitempty"`
	RuntimeReadiness   *crewDependencyReport `json:"runtime_readiness,omitempty"`
	ConversationNotice string                `json:"conversation_notice,omitempty"`
	TransferID         string                `json:"transfer_id,omitempty"`
	ProjectID          string                `json:"project_id"`
	Workspace          string                `json:"workspace,omitempty"` // the Crew's folder on this server, for its Slack setup from the Mac app
	Binding            *crewroot.SyncBinding `json:"binding"`
	Snapshot           *crewsync.Snapshot    `json:"snapshot,omitempty"`
	// Place is where the Crew lives for the Access row: mac, mac_off, moving or online (the server decides).
	Place string `json:"place,omitempty"`
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

func (api *StreamingAPI) crewSyncRoots(ctx context.Context, claims *UserClaims, id string) (crewroot.Roots, error) {
	if !personalLaptopSettingsClaims(claims) {
		return crewroot.Roots{}, &crewSyncFailure{Code: crewSyncForbiddenCode, Status: 403, Message: "use your own interactive Crew chat or personal Builder connection to sync"}
	}
	if code, message := externalCrewAuthorGate(claims); code != 0 {
		return crewroot.Roots{}, &crewSyncFailure{Code: crewSyncForbiddenCode, Status: 403, Message: message}
	}
	if claims.AccessToken != nil && (!claims.AccessToken.Allows("crews:write") || !claims.AccessToken.AllowsCrew(workspaceTokenProjectID(id))) {
		return crewroot.Roots{}, &crewSyncFailure{Code: crewSyncForbiddenCode, Status: 403, Message: "this connection cannot sync this Crew"}
	}
	if token := claims.AccessToken; token != nil && token.FileGuard != nil {
		guard := token.FileGuard
		if !guard.Allows(".", false) || !guard.Allows(".", true) || len(guard.BlockedPaths)+len(guard.BlockedWritePaths)+len(guard.ReadOnlyPaths) > 0 {
			return crewroot.Roots{}, &crewSyncFailure{Code: crewSyncForbiddenCode, Status: 403, Message: "sync needs access to the whole project; use the Mac app or a full-project Builder connection"}
		}
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
func crewNativeErrorReply(err error) map[string]string {
	return map[string]string{"error": err.Error(), "code": crewSyncErrorCode(err)}
}

// The transport carries project content, never paths or credentials chosen by
// another machine. Every operation rechecks the destination's actual owner.
func (api *StreamingAPI) handleCrewSync(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("transfer") != "" {
		api.handleCrewSyncPiece(w, r)
		return
	}
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
	out.RuntimeReadiness = readCrewDependencies(out.ProjectID)
	if r.Method == http.MethodGet && out.Place == "" {
		out.Place = crewPlace(GetUserIDFromContext(r.Context()), out.ProjectID, out.Binding)
	}
	externalJSON(w, out)
}

func (api *StreamingAPI) crewSync(ctx context.Context, claims *UserClaims, id string, input crewSyncRequest) (crewSyncReply, error) {
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
	snapshot, err := crewsync.Capture(ctx, roots, previous, stageRoot)
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
	err = (filesnapshots.Store{Root: roots.Files, Private: roots.Private}).EncodeSync(ctx, binding.PairID, roots, &snapshot)
	return snapshot, err
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
	if err = (filesnapshots.Store{Root: roots.Files, Private: roots.Private}).AckSync(context.Background(), binding.PairID, snapshot.Content); err != nil {
		return err
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
	size := snapshot.Payloads["content"].Size
	defer func() {
		log.Printf("[CREW_SYNC] owner=%s project=%s pair=%s phase=apply bytes=%d paths=%d duration_ms=%d success=%t reason=%q", GetUserIDFromContext(ctx), roots.ID, pair, size, len(snapshot.Changes), time.Since(started).Milliseconds(), err == nil, fmt.Sprint(err))
	}()
	defer snapshot.Close()
	if err := (filesnapshots.Store{Root: roots.Files, Private: roots.Private}).DecodeSync(ctx, pair, snapshot); err != nil {
		return err
	}
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
