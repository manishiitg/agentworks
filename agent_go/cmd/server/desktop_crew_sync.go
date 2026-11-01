package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"io"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"

	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

func (api *StreamingAPI) handleDesktopCrewSync(w http.ResponseWriter, r *http.Request) {
	if !desktopConnectionGate(w, r) {
		return
	}
	api.runDesktopCrewSync(w, r, true)
}

func (api *StreamingAPI) runDesktopCrewSync(w http.ResponseWriter, r *http.Request, allowPair bool) {
	claims := GetUserFromContext(r.Context())
	roots, err := api.crewSyncRoots(r.Context(), claims, mux.Vars(r)["project"])
	if err != nil {
		externalError(w, 403, "sync_unavailable", err.Error())
		return
	}
	if !IsLocalMode() {
		externalError(w, 403, "local_app_required", "Open the Crew in the local Mac app.")
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
	unlock, err := wf.LockWorkspace(r.Context(), roots.Files)
	if err != nil {
		externalError(w, 409, "sync_unavailable", err.Error())
		return
	}
	defer unlock()
	binding, err := crewroot.ReadSync(roots.ID)
	if err != nil {
		externalError(w, 409, "sync_unavailable", err.Error())
		return
	}
	if binding != nil && (binding.Side != "local" || binding.ConnectionID != connection.ID) {
		externalError(w, 409, "wrong_connection", "Use this Crew's paired server.")
		return
	}
	call := func(project string, request crewSyncRequest) (crewSyncReply, error) {
		data, _ := json.Marshal(request)
		upstream, e := http.NewRequestWithContext(r.Context(), http.MethodPost, connection.Server+"/api/crew-sync/"+url.PathEscape(project), bytes.NewReader(data))
		if e != nil {
			return crewSyncReply{}, e
		}
		upstream.Header.Set("Authorization", "Bearer "+connection.Token)
		upstream.Header.Set("Content-Type", "application/json")
		client := desktopHTTPClient()
		client.Timeout = 5 * time.Minute
		response, e := client.Do(upstream)
		if e != nil {
			return crewSyncReply{}, fmt.Errorf("cannot reach the server; your local files are unchanged")
		}
		defer response.Body.Close()
		if token := response.Header.Get("X-Renewed-Token"); token != "" {
			desktopConnectionsMu.Lock()
			records, renewErr := readDesktopConnections(claims.UserID)
			if renewErr == nil {
				for i := range records {
					if records[i].ID == connection.ID && records[i].Token == connection.Token {
						records[i].Token = token
					}
				}
				renewErr = writeDesktopConnections(claims.UserID, records)
			}
			desktopConnectionsMu.Unlock()
			if renewErr != nil {
				return crewSyncReply{}, renewErr
			}
			connection.Token = token
		}
		if response.StatusCode == http.StatusUnauthorized {
			return crewSyncReply{}, fmt.Errorf("reconnect this server from Connected servers, then retry Project sync")
		}

		if response.StatusCode != 200 {
			var failure struct {
				Error struct {
					Message string `json:"message"`
				} `json:"error"`
			}
			_ = json.NewDecoder(io.LimitReader(response.Body, 8192)).Decode(&failure)
			return crewSyncReply{}, fmt.Errorf("server sync failed: %s", firstNonEmptyTrimmed(failure.Error.Message, http.StatusText(response.StatusCode)))
		}
		var out crewSyncReply
		e = json.NewDecoder(io.LimitReader(response.Body, 800<<20)).Decode(&out)
		return out, e
	}
	fail := func(err error) { externalError(w, 409, "sync_unavailable", err.Error()) }
	if input.Action == "status" {
		out := crewSyncReply{ProjectID: roots.ID, Binding: binding, Auto: crewAutoStatus(roots.ID)}
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
	if binding == nil {
		if !allowPair {
			fail(fmt.Errorf("open this Crew in the Mac app and choose a server in Project sync first"))
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
		binding = &crewroot.SyncBinding{SplitFiles: roots.SeparateFiles(), PairID: pair, Side: "local", Editing: "local", ConnectionID: connection.ID, PeerProjectID: remote.ProjectID, PeerServer: connection.Server}
		if err = crewroot.WriteSync(roots.ID, *binding); err != nil {
			fail(err)
			return
		}
	}
	previous, err := readCrewSyncManifest(roots.ID)
	if err != nil {
		fail(err)
		return
	}
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
		} else {
			remote, e := call(binding.PeerProjectID, crewSyncRequest{Action: "status"})
			if e != nil {
				fail(e)
				return
			}
			if remote.Binding == nil || remote.Binding.PairID != binding.PairID || remote.Binding.Editing != "local" {
				fail(fmt.Errorf("editing is on the server; choose Work on this Mac to bring its changes back"))
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
				externalJSON(w, crewSyncReply{ProjectID: roots.ID, Binding: binding, Auto: crewAutoStatus(roots.ID)})
				return
			}
			next := "local"
			if input.Action == "server" {
				next = "server"
			}
			transfer = crewSyncRequest{Action: "push", PairID: binding.PairID, Editing: next, Snapshot: &snapshot, TransferID: uuid.NewString()}
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
		binding.Editing = remote.Binding.Editing
		binding.PendingAction = ""
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
			if err = applyCrewSyncSnapshot(r.Context(), roots, binding.PairID, remote.Snapshot); err != nil {
				fail(err)
				return
			}
			if err = saveCrewSyncSnapshot(roots, *remote.Snapshot); err != nil {
				fail(err)
				return
			}
			if _, err = call(binding.PeerProjectID, crewSyncRequest{Action: "ack", PairID: binding.PairID, TransferID: remote.TransferID}); err != nil {
				fail(err)
				return
			}
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
		fail(fmt.Errorf("choose Update server, Work on server, or Work on this Mac"))
		return
	}
	binding.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	if err = crewroot.WriteSync(roots.ID, *binding); err != nil {
		fail(err)
		return
	}
	if p, e := crewSyncManifestPath(roots.ID); e == nil {
		_ = os.Remove(p + ".pending")
	}
	externalJSON(w, crewSyncReply{ProjectID: roots.ID, Binding: binding, Auto: crewAutoStatus(roots.ID)})
}
