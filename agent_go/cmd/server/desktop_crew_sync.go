package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	"github.com/manishiitg/coding-agent-loop/workspace/crewsync"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

func (api *StreamingAPI) handleDesktopCrewSync(w http.ResponseWriter, r *http.Request) {
	if !desktopConnectionGate(w, r) {
		return
	}
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
		e = json.NewDecoder(io.LimitReader(response.Body, 360<<20)).Decode(&out)
		return out, e
	}
	fail := func(err error) { externalError(w, 409, "sync_unavailable", err.Error()) }
	if input.Action == "status" {
		out := crewSyncReply{ProjectID: roots.ID, Binding: binding}
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
		remote, e := call("new", crewSyncRequest{Action: "create", PairID: pair, SourceID: roots.ID, Name: manifest.displayTitle()})
		if e != nil {
			fail(e)
			return
		}
		if remote.Binding == nil || remote.ProjectID == "" {
			fail(fmt.Errorf("server did not return the new Crew"))
			return
		}
		binding = &crewroot.SyncBinding{PairID: pair, Side: "local", Editing: "local", ConnectionID: connection.ID, PeerProjectID: remote.ProjectID, PeerServer: connection.Server}
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
		remote, e := call(binding.PeerProjectID, crewSyncRequest{Action: "status"})
		if e != nil {
			fail(e)
			return
		}
		if remote.Binding == nil || remote.Binding.PairID != binding.PairID || remote.Binding.Editing != "local" {
			fail(fmt.Errorf("editing is on the server; choose Work on this Mac to bring its changes back"))
			return
		}
		snapshot, e := captureCrewSyncSnapshot(r.Context(), roots, previous)
		if e != nil {
			fail(e)
			return
		}
		next := "local"
		if input.Action == "server" {
			next = "server"
		}
		// Relinquish local editing before granting server editing. A failed
		// transfer is retryable without making both copies writable.
		if next == "server" {
			binding.Editing = "server"
			if err = crewroot.WriteSync(roots.ID, *binding); err != nil {
				fail(err)
				return
			}
		}
		remote, e = call(binding.PeerProjectID, crewSyncRequest{Action: "push", PairID: binding.PairID, Editing: next, Snapshot: &snapshot})
		if e != nil {
			fail(e)
			return
		}
		if err = saveCrewSyncSnapshot(roots.ID, snapshot); err != nil {
			fail(err)
			return
		}
		binding.Editing = remote.Binding.Editing
	case "local":
		remote, e := call(binding.PeerProjectID, crewSyncRequest{Action: "pull", PairID: binding.PairID})
		if e != nil {
			fail(e)
			return
		}
		if remote.Snapshot == nil {
			fail(fmt.Errorf("server did not return the project changes"))
			return
		}
		if err = crewsync.Apply(r.Context(), roots, *remote.Snapshot); err != nil {
			fail(err)
			return
		}
		if err = saveCrewSyncSnapshot(roots.ID, *remote.Snapshot); err != nil {
			fail(err)
			return
		}
		if _, err = call(binding.PeerProjectID, crewSyncRequest{Action: "ack", PairID: binding.PairID, Snapshot: &crewsync.Snapshot{Files: remote.Snapshot.Files, Git: remote.Snapshot.Git}}); err != nil {
			fail(err)
			return
		}
		binding.Editing = "local"
	default:
		fail(fmt.Errorf("choose Update server, Work on server, or Work on this Mac"))
		return
	}
	binding.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	if err = crewroot.WriteSync(roots.ID, *binding); err != nil {
		fail(err)
		return
	}
	externalJSON(w, crewSyncReply{ProjectID: roots.ID, Binding: binding})
}
