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

const crewSyncResetMessage = "this saved transfer cannot be resumed; your files are kept. Ask your administrator to reset Project sync, then pair this Crew again"

func (api *StreamingAPI) handleDesktopCrewSync(w http.ResponseWriter, r *http.Request) {
	if !desktopConnectionGate(w, r) {
		return
	}
	api.runDesktopCrewSync(w, r, true)
	// Cloud on or off also adds or removes the server copy's access to this Crew's folder on the Mac (PLAT-856-501).
	if r.Method == http.MethodPost {
		claims := GetUserFromContext(r.Context())
		if roots, err := api.crewSyncRoots(r.Context(), claims, mux.Vars(r)["project"]); err == nil {
			go api.syncCloudLaptop(claims.UserID, roots)
		}
	}
}

func (api *StreamingAPI) runDesktopCrewSync(w http.ResponseWriter, r *http.Request, allowPair bool) {
	started := time.Now()
	claims := GetUserFromContext(r.Context())
	roots, err := api.crewSyncRoots(r.Context(), claims, mux.Vars(r)["project"])
	if err != nil {
		externalError(w, 403, "sync_unavailable", err.Error())
		return
	}
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
		Mode   string `json:"mode"`
	}
	if r.Method == http.MethodGet {
		input.Action = "status"
	} else if json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&input) != nil {
		externalError(w, 400, "invalid_request", "Choose a sync action.")
		return
	}
	var main ProductConversationRecord
	if input.Action == "server" || input.Action == "local" {
		main, err = api.crewHandoffMain(r.Context(), claims, roots.ID)
		if err != nil {
			externalError(w, 409, "sync_unavailable", err.Error())
			return
		}
	}
	if api.desktopCrewSyncOff(w, r, roots, connection, input.Action, input.Mode) {
		return
	}
	// Serialize sync operations independently of working files. Chats and edits
	// must not wait for the network. Cloud Off already bypasses this lock.
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
	// activation or Cloud Off during transfer must not be overwritten on return.
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
		if (before == nil) != (current == nil) || (before != nil && (before.PairID != current.PairID || before.Editing != current.Editing || before.PendingAction != current.PendingAction || before.LastTransfer != current.LastTransfer || before.Mode != current.Mode)) {
			return fmt.Errorf("this Crew's sync state changed during transfer; retry from its current state")
		}
		return runErr
	}
	call := func(project string, request crewSyncRequest) (out crewSyncReply, e error) {
		if input.Action != "mode" && input.Action != "status" {
			if e = crewSyncOn(roots.ID); e != nil {
				return
			}
		}
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
	fail := func(err error) {
		pair := ""
		if binding != nil {
			pair = binding.PairID
		}
		action := input.Action
		switch action {
		case "update", "server", "local", "download", "mode", "status":
		default:
			action = "unknown"
		}
		log.Printf("[CREW_SYNC] project=%s pair=%s action=%s phase=failed code=%s reason=%q", roots.ID, pair, action, crewSyncErrorCode(err), err.Error())
		externalError(w, 409, crewSyncErrorCode(err), err.Error())
	}
	if input.Action == "status" {
		out := crewSyncReply{ProjectID: roots.ID, Binding: binding, RuntimeReadiness: readCrewDependencies(roots.ID), Auto: crewAutoStatus(roots.ID)}
		if binding != nil {
			remote, e := call(binding.PeerProjectID, crewSyncRequest{Action: "status"})
			if e != nil {
				fail(e)
				return
			}
			if remote.Binding == nil || remote.Binding.PairID != binding.PairID {
				fail(fmt.Errorf("the paired Crew is unavailable on this server"))
				return
			}
		}
		externalJSON(w, out)
		return
	}
	if input.Action == "mode" && binding != nil {
		if input.Mode == "continuous" {
			if e := outside(func() error { _, e := api.prepareCrewDependencies(r, roots, binding, connection, true); return e }); e != nil {
				fail(e)
				return
			}
		}
		if _, err = call(binding.PeerProjectID, crewSyncRequest{Action: "mode", PairID: binding.PairID, Mode: input.Mode}); err != nil {
			fail(err)
			return
		}
		binding, err = crewroot.SetSyncMode(roots.ID, binding.PairID, input.Mode)
		if err != nil {
			fail(err)
			return
		}
		externalJSON(w, crewSyncReply{ProjectID: roots.ID, Binding: binding})
		return
	}
	if binding != nil && binding.Mode == "off" {
		fail(fmt.Errorf("sync is off; turn on Cloud for this project before syncing"))
		return
	}
	if binding == nil {
		if !allowPair {
			fail(fmt.Errorf("open this Crew in the AgentWorks app and choose a server in Project sync first"))
			return
		}
		if input.Action != "update" && input.Action != "server" {
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
		binding = &crewroot.SyncBinding{ArchiveOnly: remote.Binding.ArchiveOnly, SplitFiles: roots.SeparateFiles(), PairID: pair, Side: "local", Editing: "local", ConnectionID: connection.ID, PeerProjectID: remote.ProjectID, PeerServer: connection.Server}
		if err = crewroot.WriteSync(roots.ID, *binding); err != nil {
			fail(err)
			return
		}
	}
	if input.Action == "update" || input.Action == "server" {
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
	case "update", "server":
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
		} else {
			remote, e := call(binding.PeerProjectID, crewSyncRequest{Action: "status"})
			if e != nil {
				fail(e)
				return
			}
			if remote.Binding == nil || remote.Binding.PairID != binding.PairID || remote.Binding.Editing != "local" {
				fail(fmt.Errorf("editing is on the server; choose Work on this computer to bring its changes back"))
				return
			}
			if input.Action == "server" {
				if e := api.crewSyncIdle(roots); e != nil {
					fail(e)
					return
				}
			}
			snapshot, e := captureCrewSyncSnapshot(r.Context(), roots, previous)
			if e != nil {
				fail(e)
				return
			}
			if input.Action == "update" && !crewSnapshotChanged(snapshot) {
				externalJSON(w, crewSyncReply{ProjectID: roots.ID, Binding: binding, RuntimeReadiness: readCrewDependencies(roots.ID), Auto: crewAutoStatus(roots.ID)})
				return
			}
			next := "local"
			if input.Action == "server" {
				next = "server"
			}
			transfer = crewSyncRequest{Action: "push", PairID: binding.PairID, Editing: next, Snapshot: &snapshot, TransferID: uuid.NewString()}
			if input.Action == "server" {
				closeWorkflowPolicyCLI(main.SessionID, main.Provider, "moving native session to server")
				transfer.Conversation, e = api.captureCrewHandoffConversation(r.Context(), claims, roots, main)
				if e != nil {
					fail(e)
					return
				}
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
			binding.Editing = next
			if e = crewroot.WriteSync(roots.ID, *binding); e != nil {
				fail(e)
				return
			}
		}
		if transfer.Conversation != nil && transfer.Conversation.Native != nil {
			b := transfer.Conversation.Native
			if err = bindCrewNativeStage(roots, b); err != nil {
				fail(err)
				return
			}
			if err = outside(func() error { return desktopCrewNativeSync(r.Context(), roots, connection, binding, b, true) }); err != nil {
				fail(err)
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
			if e = outside(func() error {
				return desktopCrewPayloads(r.Context(), roots.ID, connection, binding, transfer.TransferID, transfer.Snapshot, true)
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
			latest, e := crewsync.Capture(r.Context(), roots, transfer.Snapshot.Files)
			if e != nil {
				fail(e)
				return
			}
			git, e := crewsync.CaptureGit(r.Context(), roots.Files, transfer.Snapshot.Git)
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
				transfer.Conversation = nil
				if e = fsutil.WriteJSONAtomic(pendingPath, transfer, 0600); e != nil {
					fail(e)
					return
				}
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
		conversationNotice = remote.ConversationNotice
		binding.PendingAction = ""
		cleanupCrewPayloads(roots.ID, binding.PairID, transfer.TransferID, "upload")
	case "local", "download":
		if binding.PendingAction != "" && binding.PendingAction != input.Action {
			fail(fmt.Errorf("finish the interrupted transfer first"))
			return
		}
		if input.Action == "local" {
			if err := api.crewSyncIdle(roots); err != nil {
				fail(err)
				return
			}
		}
		binding.PendingAction = input.Action
		if err = crewroot.WriteSync(roots.ID, *binding); err != nil {
			fail(err)
			return
		}
		action := "pull"
		if input.Action == "download" {
			action = "download"
		}
		for {
			remote, e := call(binding.PeerProjectID, crewSyncRequest{Action: action, PairID: binding.PairID})
			if e != nil {
				fail(e)
				return
			}
			if remote.Snapshot == nil {
				if input.Action == "download" {
					binding.PendingAction = ""
					_ = crewroot.WriteSync(roots.ID, *binding)
					externalJSON(w, crewSyncReply{ProjectID: roots.ID, Binding: binding})
					return
				}
				fail(fmt.Errorf("server did not return the project changes"))
				return
			}
			if remote.Snapshot.Content == nil {
				fail(fmt.Errorf("%s", crewSyncResetMessage))
				return
			}
			if remote.Binding == nil || remote.Binding.PairID != binding.PairID || remote.ProjectID != binding.PeerProjectID {
				fail(fmt.Errorf("the returned changes do not belong to this Crew's paired server"))
				return
			}
			movingLocal := input.Action == "local" && remote.Binding.Editing == "local"
			if movingLocal {
				if err = validateCrewHandoffConversation(remote.Conversation); err != nil {
					fail(err)
					return
				}
			}
			if e = outside(func() error {
				return desktopCrewPayloads(r.Context(), roots.ID, connection, binding, remote.TransferID, remote.Snapshot, false)
			}); e != nil {
				fail(e)
				return
			}
			if err = hydrateCrewPayloads(roots.ID, binding.PairID, remote.TransferID, "download", remote.Snapshot); err != nil {
				fail(err)
				return
			}
			defer dropCrewPayloadBytes(remote.Snapshot)
			if err = applyCrewSyncSnapshot(r.Context(), roots, binding.PairID, remote.Snapshot); err != nil {
				fail(err)
				return
			}
			if err = saveCrewSyncSnapshot(roots, *remote.Snapshot); err != nil {
				fail(err)
				return
			}
			if movingLocal {
				if remote.Conversation.Native != nil {
					if err = outside(func() error {
						return desktopCrewNativeSync(r.Context(), roots, connection, binding, remote.Conversation.Native, false)
					}); err != nil {
						fail(err)
						return
					}
				}
				if err = api.importCrewHandoffConversation(r.Context(), claims, roots, binding.PairID, remote.TransferID, remote.Conversation, main); err != nil {
					fail(err)
					return
				}
				conversationNotice = remote.Conversation.Notice
			}
			if _, err = call(binding.PeerProjectID, crewSyncRequest{Action: "ack", PairID: binding.PairID, TransferID: remote.TransferID}); err != nil {
				fail(err)
				return
			}
			cleanupCrewPayloads(roots.ID, binding.PairID, remote.TransferID, "download")
			if input.Action == "local" && remote.Binding != nil && remote.Binding.Editing != "local" {
				continue
			}
			break
		}
		if input.Action == "local" {
			binding.Editing = "local"
		}
		binding.PendingAction = ""
	default:
		fail(fmt.Errorf("choose Update server, Work on server, or Work on this computer"))
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
	externalJSON(w, crewSyncReply{ProjectID: roots.ID, Binding: binding, RuntimeReadiness: readCrewDependencies(roots.ID), Auto: crewAutoStatus(roots.ID), ConversationNotice: conversationNotice})
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
		return crewSyncReply{}, &crewSyncFailure{Code: crewSyncForbiddenCode, Status: response.StatusCode, Message: "reconnect this server from Connected servers, then retry Project sync"}
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
	if e == nil && (request.ArchiveOnly || (out.Binding != nil && out.Binding.ArchiveOnly)) && out.Conversation != nil {
		out.Conversation.Native = nil
	}
	return out, e
}
