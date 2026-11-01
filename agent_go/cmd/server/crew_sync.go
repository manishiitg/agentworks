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

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/filesnapshots"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	"github.com/manishiitg/coding-agent-loop/workspace/crewsync"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
	"github.com/manishiitg/multi-llm-provider-go/pkg/sessiontransfer"
)

type crewSyncRequest struct {
	Mode         string                   `json:"mode,omitempty"`
	SplitFiles   bool                     `json:"split_files,omitempty"`
	TransferID   string                   `json:"transfer_id,omitempty"`
	Action       string                   `json:"action"`
	PairID       string                   `json:"pair_id,omitempty"`
	SourceID     string                   `json:"source_id,omitempty"`
	Name         string                   `json:"name,omitempty"`
	Editing      string                   `json:"editing,omitempty"`
	Snapshot     *crewsync.Snapshot       `json:"snapshot,omitempty"`
	Conversation *crewHandoffConversation `json:"conversation,omitempty"`
}
type crewSyncReply struct {
	RuntimeReadiness   *crewDependencyReport    `json:"runtime_readiness,omitempty"`
	ConversationNotice string                   `json:"conversation_notice,omitempty"`
	TransferID         string                   `json:"transfer_id,omitempty"`
	Auto               *crewAutoSyncStatus      `json:"auto,omitempty"`
	ProjectID          string                   `json:"project_id"`
	Binding            *crewroot.SyncBinding    `json:"binding"`
	Snapshot           *crewsync.Snapshot       `json:"snapshot,omitempty"`
	Conversation       *crewHandoffConversation `json:"conversation,omitempty"`
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
		return crewroot.Roots{}, fmt.Errorf("use your own interactive Crew chat or personal Builder connection to sync")
	}
	if code, message := externalCrewAuthorGate(claims); code != 0 {
		return crewroot.Roots{}, fmt.Errorf("%s", message)
	}
	if claims.AccessToken != nil && (!claims.AccessToken.Allows("crews:write") || !claims.AccessToken.AllowsCrew(workspaceTokenProjectID(id))) {
		return crewroot.Roots{}, fmt.Errorf("this connection cannot sync this Crew")
	}
	if token := claims.AccessToken; token != nil && token.FileGuard != nil {
		guard := token.FileGuard
		if !guard.Allows(".", false) || !guard.Allows(".", true) || len(guard.BlockedPaths)+len(guard.BlockedWritePaths)+len(guard.ReadOnlyPaths) > 0 {
			return crewroot.Roots{}, fmt.Errorf("sync needs access to the whole project; use the Mac app or a full-project Builder connection")
		}
	}
	binding, _, ok := api.resolveExternalWorkspace(ctx, claims, id)
	if !ok || !binding.OwnedByCaller || binding.OwnerID != claims.UserID {
		return crewroot.Roots{}, fmt.Errorf("only this Crew's main owner can sync it")
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
func updateCrewSyncEditing(ctx context.Context, roots crewroot.Roots, observed crewroot.SyncBinding, editing string) (*crewroot.SyncBinding, error) {
	unlock, err := wf.LockWorkspace(ctx, roots.Files)
	if err != nil {
		return nil, err
	}
	defer unlock()
	current, err := crewroot.ReadSync(roots.ID)
	if err != nil {
		return nil, err
	}
	if current == nil || current.PairID != observed.PairID {
		return nil, fmt.Errorf("paired project is unavailable")
	}
	// The remote reply describes the observed state, not a handoff that
	// committed while it was in flight. Retry authority on the next tick.
	if *current != observed {
		return current, nil
	}
	if current.Editing != editing {
		current.Editing = editing
		if err := crewroot.WriteSync(roots.ID, *current); err != nil {
			return nil, err
		}
	}
	return current, nil
}

const crewNativeDivergedCode = "native_history_diverged"

func crewSyncErrorCode(err error) string {
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
	var input crewSyncRequest
	if r.Method == http.MethodGet {
		input.Action = "status"
	} else if json.NewDecoder(io.LimitReader(r.Body, 800<<20)).Decode(&input) != nil {
		externalError(w, 400, "invalid_request", "Send a valid sync request.")
		return
	}
	out, err := api.crewSync(r.Context(), GetUserFromContext(r.Context()), mux.Vars(r)["project"], input)
	if err != nil {
		externalError(w, 409, crewSyncErrorCode(err), err.Error())
		return
	}
	out.RuntimeReadiness = readCrewDependencies(out.ProjectID)
	externalJSON(w, out)
}

func (api *StreamingAPI) crewSync(ctx context.Context, claims *UserClaims, id string, input crewSyncRequest) (crewSyncReply, error) {
	if input.Action == "create" {
		if input.PairID == "" || input.SourceID == "" || input.Name == "" {
			return crewSyncReply{}, fmt.Errorf("choose a local Crew to sync")
		}
		if !personalLaptopSettingsClaims(claims) || !workspaceCreateTokenAllowed(claims) {
			return crewSyncReply{}, fmt.Errorf("this connection cannot create a Crew")
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
		binding := crewroot.SyncBinding{SplitFiles: input.SplitFiles, PairID: input.PairID, Side: "server", Editing: "local", PeerProjectID: input.SourceID}
		if err = crewroot.WriteSync(created, binding); err != nil {
			return crewSyncReply{}, err
		}
		return crewSyncReply{ProjectID: created, Binding: &binding}, nil
	}
	roots, err := api.crewSyncRoots(ctx, claims, id)
	if err != nil {
		return crewSyncReply{}, err
	}
	// Main-chat initialization may write the project manifest through workspace
	// HTTP. Complete it before taking the file lock used by that endpoint.
	var main ProductConversationRecord
	if input.Action == "mirror-get" || input.Action == "activate" || input.Action == "pull" || (input.Action == "push" && input.Editing == "server") {
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
	out := crewSyncReply{ProjectID: roots.ID, Binding: binding, Auto: crewAutoStatus(roots.ID)}
	if input.Action == "status" {
		return out, nil
	}
	if binding == nil || input.PairID != binding.PairID || binding.Side != "server" {
		return out, fmt.Errorf("choose the server paired with this Crew")
	}
	previous, err := readCrewSyncManifest(roots.ID)
	if err != nil {
		return out, err
	}
	switch input.Action {
	case "mode":
		if input.Mode != "continuous" && input.Mode != "manual" {
			return out, fmt.Errorf("choose automatic or manual sync")
		}
		binding.Mode = input.Mode
	case "mirror-put":
		if binding.Editing != "local" {
			return out, fmt.Errorf("the server has newer work; bring it back first")
		}
		if err = validateCrewHandoffConversation(input.Conversation); err != nil {
			return out, err
		}
		if input.Conversation.Native == nil {
			return out, fmt.Errorf("native session sync is not ready for this conversation")
		}
		if err = bindCrewNativeStage(roots, input.Conversation.Native); err != nil {
			return out, err
		}
		if err = saveCrewNativeMirror(roots, input.Conversation); err != nil {
			return out, err
		}
	case "mirror-get":
		if binding.Editing != "server" {
			return out, fmt.Errorf("work is on the Mac")
		}
		if api.crewNativeTurnRunning(roots) {
			return out, nil
		}
		out.Conversation, err = api.captureCrewMirror(ctx, claims, roots, main)
		if err != nil {
			return out, err
		}
		return out, nil
	case "activate":
		if binding.Mode == "manual" {
			return out, fmt.Errorf("send this project to the server from your Mac first")
		}
		if binding.Editing == "server" {
			return out, nil
		}
		mirror, e := readCrewNativeMirror(roots)
		if e != nil {
			return out, fmt.Errorf("this session has not finished syncing yet; reconnect your Mac")
		}
		if e = api.importCrewHandoffConversation(ctx, claims, roots, binding.PairID, mirror.TransferID, mirror.Conversation, main); e != nil {
			return out, e
		}
		binding.Editing = "server"
		out.ConversationNotice = "Continued from the last completed sync. Work after the Mac lost its connection may be missing. " + mirror.Conversation.Notice
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
			return out, fmt.Errorf("editing is on the server; bring its changes back before updating it")
		}
		if input.Editing != "local" && input.Editing != "server" {
			return out, fmt.Errorf("choose where to work")
		}
		if input.Editing == "server" {
			if err = validateCrewHandoffConversation(input.Conversation); err != nil {
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
			if err = api.importCrewHandoffConversation(ctx, claims, roots, binding.PairID, input.TransferID, input.Conversation, main); err != nil {
				return out, err
			}
			out.ConversationNotice = input.Conversation.Notice
		}
		binding.Editing = input.Editing
		binding.LastTransfer = input.TransferID
	case "pull", "download":
		pendingPath, e := crewSyncManifestPath(roots.ID)
		if e != nil {
			return out, e
		}
		pendingPath += ".return"
		var pending crewSyncReply
		if raw, e := os.ReadFile(pendingPath); e == nil {
			if json.Unmarshal(raw, &pending) != nil || pending.Snapshot == nil {
				return out, fmt.Errorf("pending sync is unavailable")
			}
			// Finish the unacknowledged background copy before changing editing.

			return pending, nil
		} else if !os.IsNotExist(e) {
			return out, e
		}
		if binding.Editing != "server" && input.Action == "download" {
			// PLAT-856-442: a download queued before a completed return is
			// now empty. Any unacknowledged return was served above first.
			return out, nil
		}
		if input.Action == "pull" {
			if e := api.crewSyncIdle(roots); e != nil {
				return out, e
			}
			closeWorkflowPolicyCLI(main.SessionID, main.Provider, "moving native session to Mac")
			out.Conversation, e = api.captureCrewHandoffConversation(ctx, claims, roots, main)
			if e != nil {
				return out, e
			}
			binding.Editing = "local"
			if e := crewroot.WriteSync(roots.ID, *binding); e != nil {
				return out, e
			}
		}
		if input.Action == "download" && !crewSyncNeedsCapture(roots) {
			return out, nil
		}
		stamp := crewSyncStamp(roots)
		snapshot, e := captureCrewSyncSnapshot(ctx, roots, previous)
		if e != nil {
			return out, e
		}
		if input.Action == "download" && !crewSnapshotChanged(snapshot) {
			crewSyncRememberScan(roots, stamp)
			return out, nil
		}
		crewSyncRememberScan(roots, stamp)
		out.Snapshot = &snapshot
		out.TransferID = uuid.NewString()
		if e = fsutil.WriteJSONAtomic(pendingPath, out, 0600); e != nil {
			return out, e
		}
		return out, nil
	case "ack":
		if input.TransferID == "" {
			return out, fmt.Errorf("a sync receipt is required")
		}
		if binding.LastTransfer == input.TransferID {
			return out, nil
		}
		p, e := crewSyncManifestPath(roots.ID)
		if e != nil {
			return out, e
		}
		p += ".return"
		raw, e := os.ReadFile(p)
		if e != nil {
			return out, e
		}
		var pending crewSyncReply
		if json.Unmarshal(raw, &pending) != nil || pending.TransferID != input.TransferID || pending.Snapshot == nil {
			return out, fmt.Errorf("receipt does not match pending sync")
		}
		if err = saveCrewSyncSnapshot(roots, *pending.Snapshot); err != nil {
			return out, err
		}
		binding.LastTransfer = input.TransferID
		binding.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
		if err = crewroot.WriteSync(roots.ID, *binding); err != nil {
			return out, err
		}
		_ = os.Remove(p)
	default:
		return out, fmt.Errorf("unknown sync action")
	}
	binding.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	if err = crewroot.WriteSync(roots.ID, *binding); err != nil {
		return out, err
	}
	log.Printf("[CREW_SYNC] project=%s action=%s editing=%s transfer=%s", roots.ID, input.Action, binding.Editing, input.TransferID)
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
	err = json.Unmarshal(b, &state)
	return &state, err
}
func captureCrewSyncSnapshot(ctx context.Context, roots crewroot.Roots, previous crewsync.Manifest) (crewsync.Snapshot, error) {
	snapshot, err := crewsync.Capture(ctx, roots, previous)
	if err != nil {
		return snapshot, err
	}
	prior, err := readCrewSyncGit(roots.ID)
	if err != nil {
		return snapshot, err
	}
	snapshot.Git, err = crewsync.CaptureGit(ctx, roots.Files, prior)
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
	state.Bundle = nil
	return fsutil.WriteJSONAtomic(p+".git", state, 0600)
}

// Editing handoff is between turns. Existing native processes retain the
// permissions they started with, so a running/retained terminal must finish.
func (api *StreamingAPI) crewSyncIdle(roots crewroot.Roots) error {
	if api.crewNativeTurnRunning(roots) {
		return fmt.Errorf("finish or stop this Crew's running chat and terminal before moving editing; after this reply, use Project sync")
	}
	codeShells.Lock()
	defer codeShells.Unlock()
	for _, shell := range codeShells.byID {
		if shell.projectID == roots.ID && shell.ownerID == roots.Owner {
			return fmt.Errorf("stop this Crew's open terminal, then retry Project sync")
		}
	}
	return nil
}

func applyCrewSyncSnapshot(ctx context.Context, roots crewroot.Roots, pair string, snapshot *crewsync.Snapshot) (err error) {
	started := time.Now()
	size := 0
	if snapshot.Content != nil {
		size = len(snapshot.Content.Bundle)
	}
	defer func() {
		log.Printf("[CREW_SYNC] project=%s pair=%s phase=apply bytes=%d paths=%d duration_ms=%d success=%t reason=%q", roots.ID, pair, size, len(snapshot.Changes), time.Since(started).Milliseconds(), err == nil, fmt.Sprint(err))
	}()
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
	hash := sha256.Sum256(g.IndexPatch)
	return g.Head != g.Before || g.Branch != g.BeforeBranch || hex.EncodeToString(hash[:]) != g.BeforeIndexHash
}
