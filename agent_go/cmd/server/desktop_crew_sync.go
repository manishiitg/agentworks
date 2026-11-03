package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/workspace/crewsync"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"

	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

const crewSyncResetMessage = "this saved transfer cannot be resumed; your files are kept. Open this Crew in the Mac app to restart its move"

func (api *StreamingAPI) handleDesktopCrewSync(w http.ResponseWriter, r *http.Request) {
	if !desktopConnectionGate(w, r) {
		return
	}
	api.runDesktopCrewSync(w, r, true)

}

func (api *StreamingAPI) runDesktopCrewSync(w http.ResponseWriter, r *http.Request, allowPair bool) {
	started := time.Now()
	claims := GetUserFromContext(r.Context())
	if !IsLocalMode() {
		externalError(w, 403, "local_app_required", "Open the Crew in the local AgentWorks app.")
		return
	}
	connection, err := desktopConnectionFor(claims.UserID, mux.Vars(r)["connection"])
	if err != nil {
		externalError(w, 404, "connection_unavailable", "Choose a connected server.")
		return
	}
	var input struct {
		Action string `json:"action"`
	}
	if r.Method == http.MethodGet {
		input.Action = "status"
	} else if json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&input) != nil {
		externalError(w, 400, "invalid_request", "Choose a sync action.")
		return
	}
	id := mux.Vars(r)["project"]
	if input.Action != "status" && input.Action != "server" && input.Action != "relay" && input.Action != "offline" {
		externalError(w, 400, "invalid_action", "Choose Move online.")
		return
	}
	// A durable receipt permits finishing local retirement after a lost reply or
	// a restart following the managed-folder rename. Never accept peer paths.
	if receipt, e := readCrewMoveReceipt(id); e != nil {
		externalError(w, 409, "sync_unavailable", e.Error())
		return
	} else if receipt != nil {
		if !websiteDeviceClaims(claims) || receipt.Roots.Owner != claims.UserID || receipt.Binding.ConnectionID != connection.ID {
			externalError(w, 403, "sync_unavailable", "Only this Crew's owner can finish its move.")
			return
		}
		if input.Action != "status" && input.Action != "server" {
			externalError(w, 409, "sync_unavailable", "This Crew lives online. Open its online copy.")
			return
		}
		if !receipt.Complete && input.Action == "server" {
			receipt, e = finishCrewMove(r.Context(), receipt.Roots, receipt.Binding, time.Now())
			if e != nil {
				externalError(w, 409, "sync_unavailable", e.Error())
				return
			}
		}
		out := crewMoveReply(receipt)
		if !receipt.Complete {
			out.Place = "moving"
		}
		externalJSON(w, out)
		return
	}
	roots, err := api.crewSyncRoots(r.Context(), claims, id)
	if err != nil {
		externalError(w, 403, "sync_unavailable", err.Error())
		return
	}
	if input.Action == "status" {
		b, e := crewroot.ReadSync(id)
		if e != nil || (b != nil && b.ConnectionID != connection.ID) {
			externalError(w, 409, "wrong_connection", "Use this Crew's connected server.")
			return
		}
		out, e := api.crewSync(r.Context(), claims, id, crewSyncRequest{Action: "status"})
		if e != nil {
			externalError(w, 409, "sync_unavailable", e.Error())
			return
		}
		externalJSON(w, out)
		return
	}
	var main ProductConversationRecord
	if input.Action == "server" {
		main, err = api.crewHandoffMain(r.Context(), claims, roots.ID)
		if err != nil {
			externalError(w, 409, "sync_unavailable", err.Error())
			return
		}
	}
	// Serialize sync operations independently of working files. Chats and edits
	// must not wait for the network. Status is read without waiting for this lock.
	operationPath, err := crewSyncManifestPath(roots.ID)
	if err != nil {
		externalError(w, 409, "sync_unavailable", err.Error())
		return
	}
	if err = agentworksclient.EnsurePrivateDirectory(operationPath + ".operation"); err != nil {
		externalError(w, 409, "sync_unavailable", err.Error())
		return
	}
	operationUnlock, err := wf.LockWorkspace(r.Context(), operationPath+".operation")
	if err != nil {
		externalError(w, 409, "sync_unavailable", err.Error())
		return
	}
	defer operationUnlock()
	unlock, err := wf.LockWorkspace(r.Context(), roots.Files)
	if err != nil {
		externalError(w, 409, "sync_unavailable", err.Error())
		return
	}
	defer func() {
		if unlock != nil {
			unlock()
		}
	}()
	binding, err := crewroot.ReadSync(roots.ID)
	if err != nil {
		externalError(w, 409, "sync_unavailable", err.Error())
		return
	}
	if binding != nil && (binding.Side != "local" || binding.ConnectionID != connection.ID) {
		externalError(w, 409, "wrong_connection", "Use this Crew's paired server.")
		return
	}
	// Freeze the observed authority before releasing the file lock. A remote
	// activation during transfer must not be overwritten on return.
	outside := func(fn func() error) error {
		before, e := crewroot.ReadSync(roots.ID)
		if e != nil {
			return e
		}
		unlock()
		unlock = nil
		runErr := fn()
		unlock, e = wf.LockWorkspace(r.Context(), roots.Files)
		if e != nil {
			return e
		}
		if _, e = api.crewSyncRoots(r.Context(), claims, roots.ID); e != nil {
			return e
		}
		current, e := crewroot.ReadSync(roots.ID)
		if e != nil {
			return e
		}
		if (before == nil) != (current == nil) || (before != nil && (before.PairID != current.PairID || before.Editing != current.Editing || before.PendingAction != current.PendingAction || before.LastTransfer != current.LastTransfer)) {
			return fmt.Errorf("this Crew's sync state changed during transfer; retry from its current state")
		}
		return runErr
	}
	call := func(project string, request crewSyncRequest) (out crewSyncReply, e error) {
		e = outside(func() error {
			var err error
			out, err = desktopCrewSyncCall(r.Context(), claims.UserID, &connection, project, request)
			return err
		})
		return
	}

	// Picking a Crew's server (several connected) creates its empty twin there; a Crew has one server.
	if input.Action == "relay" {
		if binding == nil && !desktopServerSupportsRelay(r.Context(), claims.UserID, connection) {
			externalError(w, 409, "sync_unavailable", "This server needs an update before this Crew can be used from your phone.")
			return
		}
		if binding == nil {
			_, manifest, ok := api.resolveExternalWorkspace(r.Context(), claims, roots.ID)
			if !ok {
				externalError(w, 404, "sync_unavailable", "This Crew is unavailable.")
				return
			}
			if err = api.createDesktopCrewRelayTwin(r.Context(), claims.UserID, connection, roots.ID, manifest.displayTitle()); err != nil {
				externalError(w, 409, crewSyncErrorCode(err), err.Error())
				return
			}
			binding, _ = crewroot.ReadSync(roots.ID)
		}
		externalJSON(w, crewSyncReply{ProjectID: roots.ID, Binding: binding})
		return
	}
	// Reach off: the server removes this Crew's empty online copy, then the Mac forgets the pair (crew_where_it_lives.go).
	if input.Action == "offline" {
		if binding == nil {
			externalJSON(w, crewSyncReply{ProjectID: roots.ID})
			return
		}
		if binding.Mode != "relay" || binding.Editing != "local" || binding.PendingAction != "" {
			externalError(w, 409, "sync_unavailable", "This Crew lives online; it stays reachable from your phone and the web.")
			return
		}
		if _, err = desktopCrewSyncCall(r.Context(), claims.UserID, &connection, binding.PeerProjectID, crewSyncRequest{Action: "unpair", PairID: binding.PairID}); err != nil {
			externalError(w, 409, crewSyncErrorCode(err), err.Error())
			return
		}
		removed := removeCrewSyncFiles(roots.ID)
		log.Printf("[CREW_RELAY] project=%s reach_off server_project=%s server=%s sync_files_removed=%d", roots.ID, binding.PeerProjectID, connection.Server, removed)
		externalJSON(w, crewSyncReply{ProjectID: roots.ID})
		return
	}
	fail := func(err error) {
		pair := ""
		if binding != nil {
			pair = binding.PairID
		}
		action := input.Action
		switch action {
		case "server", "status":
		default:
			action = "unknown"
		}
		log.Printf("[CREW_SYNC] project=%s pair=%s action=%s phase=failed code=%s reason=%q", roots.ID, pair, action, crewSyncErrorCode(err), err.Error())
		message := err.Error()
		if binding != nil && binding.SchedulesPaused {
			message += ". Your Mac schedules are paused for this move. Retry Move online to finish."
		}
		externalError(w, 409, crewSyncErrorCode(err), message)
	}
	if binding == nil {
		if !allowPair {
			fail(fmt.Errorf("open this Crew in the AgentWorks app and choose its connected server first"))
			return
		}
		if input.Action != "server" {
			fail(fmt.Errorf("update the server before transferring work"))
			return
		}
		_, manifest, ok := api.resolveExternalWorkspace(r.Context(), claims, roots.ID)
		if !ok {
			fail(fmt.Errorf("this Crew is unavailable"))
			return
		}
		pair := uuid.NewString()
		remote, e := call("new", crewSyncRequest{Action: "create", SplitFiles: roots.SeparateFiles(), PairID: pair, SourceID: roots.ID, Name: manifest.displayTitle()})
		if e != nil {
			fail(e)
			return
		}
		if remote.Binding == nil || remote.ProjectID == "" {
			fail(fmt.Errorf("server did not return the new Crew"))
			return
		}
		binding = &crewroot.SyncBinding{Mode: "relay", ArchiveOnly: remote.Binding.ArchiveOnly, SplitFiles: roots.SeparateFiles(), PairID: remote.Binding.PairID, Side: "local", Editing: "local", ConnectionID: connection.ID, PeerProjectID: remote.ProjectID, PeerServer: connection.Server}
		if err = crewroot.WriteSync(roots.ID, *binding); err != nil {
			fail(err)
			return
		}
	}
	if input.Action == "server" {
		if e := outside(func() error { _, e := api.prepareCrewDependencies(r, roots, binding, connection, allowPair); return e }); e != nil {
			fail(e)
			return
		}
	}
	previous, err := readCrewSyncManifest(roots.ID)
	if err != nil {
		fail(err)
		return
	}
	conversationNotice := ""
	changedDuringHandoff := false
	switch input.Action {
	case "server":
		pendingPath, e := crewSyncManifestPath(roots.ID)
		if e != nil {
			fail(e)
			return
		}
		pendingPath += ".pending"
		var transfer crewSyncRequest
		if binding.PendingAction != "" {
			if input.Action != binding.PendingAction {
				fail(fmt.Errorf("finish the interrupted %s transfer first", binding.PendingAction))
				return
			}
			data, e := os.ReadFile(pendingPath)
			if e != nil || json.Unmarshal(data, &transfer) != nil {
				fail(fmt.Errorf("the pending transfer is unavailable; restore its private sync record before continuing"))
				return
			}
			if transfer.Snapshot == nil || transfer.Snapshot.Content == nil {
				fail(fmt.Errorf("%s", crewSyncResetMessage))
				return
			}
			if len(transfer.Snapshot.Payloads) == 0 || transfer.Snapshot.Payloads["state"].Hash == "" {
				fail(fmt.Errorf("%s", crewSyncResetMessage))
				return
			}
		}
		if binding.PendingAction == "" {
			remote, e := call(binding.PeerProjectID, crewSyncRequest{Action: "status"})
			if e != nil {
				fail(e)
				return
			}
			if remote.Binding == nil || remote.Binding.PairID != binding.PairID || remote.Binding.Editing != "local" {
				fail(fmt.Errorf("this Crew is already online"))
				return
			}
			if input.Action == "server" {
				if e := api.crewSyncIdle(roots); e != nil {
					fail(e)
					return
				}
			}
			snapshot, e := captureCrewSyncSnapshot(r.Context(), roots, previous)
			defer snapshot.Close()
			if e != nil {
				fail(e)
				return
			}
			transfer = crewSyncRequest{Action: "push", PairID: binding.PairID, Editing: "server", Snapshot: &snapshot, TransferID: uuid.NewString()}
			if e = api.captureCrewMoveData(r.Context(), claims, roots, main, &snapshot); e != nil {
				fail(e)
				return
			}
			seen := map[string]bool{}
			if e = readCrewMoveData(snapshot.StatePath, func(_ crewMoveHeader, chat *crewMoveChat) error {
				if chat == nil || chat.Conversation == nil || chat.Conversation.Native == nil {
					return nil
				}
				b := chat.Conversation.Native
				key := b.Provider + "/" + b.SessionID
				if !seen[key] {
					transfer.Native = append(transfer.Native, b)
					seen[key] = true
				}
				return nil
			}); e != nil {
				fail(e)
				return
			}
			if e = freezeCrewPayloads(roots.ID, binding.PairID, transfer.TransferID, "upload", transfer.Snapshot); e != nil {
				fail(e)
				return
			}
			if e = fsutil.WriteJSONAtomic(pendingPath, transfer, 0600); e != nil {
				fail(e)
				return
			}
			binding.PendingAction = input.Action
			binding.Editing = "server"
			if e = crewroot.WriteSync(roots.ID, *binding); e != nil {
				fail(e)
				return
			}
		}
		stage := transfer
		stage.Action = "stage"
		prepared, e := call(binding.PeerProjectID, stage)
		if e != nil {
			fail(e)
			return
		}
		if prepared.Binding == nil || prepared.Binding.LastTransfer != transfer.TransferID {
			if e = hydrateCrewPayloads(roots.ID, binding.PairID, transfer.TransferID, "upload", transfer.Snapshot); e != nil {
				fail(e)
				return
			}
			if e = outside(func() error {
				return readCrewMoveData(transfer.Snapshot.StatePath, func(_ crewMoveHeader, chat *crewMoveChat) error {
					if chat == nil || chat.Conversation == nil || chat.Conversation.Native == nil {
						return nil
					}
					b := chat.Conversation.Native
					if err := bindCrewNativeStage(roots, b); err != nil {
						return err
					}
					return desktopCrewNativeSync(r.Context(), roots, connection, binding, b)
				})
			}); e != nil {
				fail(e)
				return
			}
			if e = outside(func() error {
				return desktopCrewPayloads(r.Context(), roots.ID, connection, binding, transfer.TransferID, transfer.Snapshot)
			}); e != nil {
				fail(e)
				return
			}
		}
		if input.Action == "server" && (prepared.Binding == nil || prepared.Binding.LastTransfer != transfer.TransferID) {
			if e = api.crewSyncIdle(roots); e != nil {
				fail(e)
				return
			}
			stageRoot, e := crewSyncStagingRoot(roots.ID)
			if e != nil {
				fail(e)
				return
			}
			latest, e := crewsync.Capture(r.Context(), roots, transfer.Snapshot.Files, stageRoot)
			defer latest.Close()
			if e != nil {
				fail(e)
				return
			}
			git, e := crewsync.CaptureGit(r.Context(), roots.Files, transfer.Snapshot.Git, latest.StagingDir)
			if e != nil {
				fail(e)
				return
			}
			latest.Git = git
			changedDuringHandoff = crewSnapshotChanged(latest)
			if git != nil && git.BundlePath != "" {
				_ = os.Remove(git.BundlePath)
				_ = os.Remove(filepath.Dir(git.BundlePath))
			}
			if changedDuringHandoff {
				// Confirm the captured copy without handing execution to a stale
				// version. The next send captures the edits made during upload.
				transfer.Editing = "local"
				if e = fsutil.WriteJSONAtomic(pendingPath, transfer, 0600); e != nil {
					fail(e)
					return
				}
			}
		}
		if transfer.Editing == "server" && (prepared.Binding == nil || prepared.Binding.LastTransfer != transfer.TransferID) {
			binding.SchedulesPaused = true
			if e = crewroot.WriteSync(roots.ID, *binding); e != nil {
				fail(e)
				return
			}
			if e = api.disableCrewMoveSchedules(r.Context(), roots, transfer.Snapshot.StatePath); e != nil {
				fail(e)
				return
			}
		}
		remote, e := call(binding.PeerProjectID, transfer)
		if e != nil {
			fail(e)
			return
		}
		if remote.Binding == nil || remote.Binding.PairID != binding.PairID || remote.Binding.LastTransfer != transfer.TransferID {
			fail(fmt.Errorf("server did not confirm this transfer; retry it"))
			return
		}
		if e = saveCrewSyncSnapshot(roots, *transfer.Snapshot); e != nil {
			fail(e)
			return
		}
		log.Printf("[CREW_SYNC] project=%s pair=%s phase=receipt direction=upload bytes=%d paths=%d duration_ms=%d transfer=%s", roots.ID, binding.PairID, transfer.Snapshot.Payloads["content"].Size, len(transfer.Snapshot.Changes), time.Since(started).Milliseconds(), transfer.TransferID)
		binding.Editing = remote.Binding.Editing
		binding.LastTransfer = transfer.TransferID
		conversationNotice = remote.ConversationNotice
		binding.PendingAction = ""
		binding.SchedulesPaused = false
	default:
		fail(fmt.Errorf("choose Move online"))
		return
	}
	binding.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	binding.LastCompletedAt = binding.UpdatedAt
	if err = crewroot.WriteSync(roots.ID, *binding); err != nil {
		fail(err)
		return
	}
	if p, e := crewSyncManifestPath(roots.ID); e == nil {
		_ = os.Remove(p + ".pending")
	}
	if changedDuringHandoff {
		fail(fmt.Errorf("files changed during upload; the captured copy is saved on the server. Send again to move your latest work"))
		return
	}
	receipt, err := finishCrewMove(r.Context(), roots, binding, time.Now())
	if err != nil {
		fail(err)
		return
	}
	out := crewMoveReply(receipt)
	out.ConversationNotice = conversationNotice
	log.Printf("[CREW_SYNC] owner=%s project=%s pair=%s phase=retired local_folder_kept=%t", claims.UserID, roots.ID, binding.PairID, roots.Local)
	externalJSON(w, out)
}

func desktopCrewSyncCall(ctx context.Context, userID string, connection *desktopConnection, project string, request crewSyncRequest) (crewSyncReply, error) {
	// Windows phase 1 uses saved history and file bundles, never native journals.
	request.ArchiveOnly = request.ArchiveOnly || cliHostOS == "windows"
	data, _ := json.Marshal(request)
	upstream, e := http.NewRequestWithContext(ctx, http.MethodPost, connection.Server+"/api/crew-sync/"+url.PathEscape(project), bytes.NewReader(data))
	if e != nil {
		return crewSyncReply{}, e
	}
	upstream.Header.Set("Authorization", "Bearer "+connection.Token)
	upstream.Header.Set("Content-Type", "application/json")
	client := desktopHTTPClient()
	client.Timeout = 15 * time.Minute
	response, e := client.Do(upstream)
	if e != nil {
		return crewSyncReply{}, fmt.Errorf("cannot reach the server; your local files are unchanged")
	}
	defer response.Body.Close()
	if token := response.Header.Get("X-Renewed-Token"); token != "" {
		desktopConnectionsMu.Lock()
		records, renewErr := readDesktopConnections(userID)
		if renewErr == nil {
			for i := range records {
				if records[i].ID == connection.ID && records[i].Token == connection.Token {
					records[i].Token = token
				}
			}
			renewErr = writeDesktopConnections(userID, records)
		}
		desktopConnectionsMu.Unlock()
		if renewErr != nil {
			return crewSyncReply{}, renewErr
		}
		connection.Token = token
	}
	if response.StatusCode == http.StatusUnauthorized {
		return crewSyncReply{}, &crewSyncFailure{Code: crewSyncForbiddenCode, Status: response.StatusCode, Message: "reconnect this server from Connected servers, then retry Move online"}
	}

	if response.StatusCode != 200 {
		var failure struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.NewDecoder(io.LimitReader(response.Body, 8192)).Decode(&failure)
		err := crewSyncPeerError(failure.Error.Code, firstNonEmptyTrimmed(failure.Error.Message, http.StatusText(response.StatusCode)))
		return crewSyncReply{}, &crewSyncFailure{Code: failure.Error.Code, Status: response.StatusCode, Message: err.Error(), Cause: err}
	}
	var out crewSyncReply
	e = json.NewDecoder(io.LimitReader(response.Body, crewSyncMetadataBytes)).Decode(&out)
	return out, e
}
