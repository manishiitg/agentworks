package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
	"io"
	"log"
	"net/http"
	"net/url"
	"time"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"

	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

func (api *StreamingAPI) handleDesktopCrewSync(w http.ResponseWriter, r *http.Request) {
	if !desktopConnectionGate(w, r) {
		return
	}
	api.runDesktopCrewSync(w, r)

}

func (api *StreamingAPI) runDesktopCrewSync(w http.ResponseWriter, r *http.Request) {
	claims := GetUserFromContext(r.Context())
	if !IsLocalMode() {
		externalError(w, 403, "local_app_required", "Open the project in the local AgentWorks app.")
		return
	}
	connection, err := desktopConnectionFor(claims.UserID, mux.Vars(r)["connection"])
	if err != nil {
		externalError(w, 404, "connection_unavailable", "Choose a connected server.")
		return
	}
	var input struct {
		Action string           `json:"action"`
		Agents []macSharedAgent `json:"agents"`
	}
	if r.Method == http.MethodGet {
		input.Action = "status"
	} else if json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&input) != nil {
		externalError(w, 400, "invalid_request", "Choose a sync action.")
		return
	}
	id := mux.Vars(r)["project"]
	switch input.Action {
	// Moving a Crew or an agent to the server is removed (owner, 13 and 17 Nov).
	case "status", "relay", "offline", "agents", "share_agents":
	default:
		externalError(w, 400, "invalid_action", "Choose status, relay, offline, agents or share_agents.")
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
			externalError(w, 409, "wrong_connection", "Use this project's connected server.")
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
	// No working-files lock: every action below calls the server (up to 15
	// minutes) and none changes working files. Holding it made the project's
	// chats and edits wait for the network.
	binding, err := crewroot.ReadSync(roots.ID)
	if err != nil {
		externalError(w, 409, "sync_unavailable", err.Error())
		return
	}
	if binding != nil && (binding.Side != "local" || binding.ConnectionID != connection.ID) {
		externalError(w, 409, "wrong_connection", "Use this project's paired server.")
		return
	}
	// Picking a Crew's server (several connected) creates its empty twin there; a Crew has one server.
	if input.Action == "relay" {
		if binding == nil && !desktopServerSupportsRelay(r.Context(), claims.UserID, connection) {
			externalError(w, 409, "sync_unavailable", "This server needs an update before this project can be used from your phone.")
			return
		}
		if binding == nil {
			_, manifest, ok := api.resolveExternalWorkspace(r.Context(), claims, roots.ID)
			if !ok {
				externalError(w, 404, "sync_unavailable", "This project is unavailable.")
				return
			}
			if err = api.createDesktopCrewRelayTwin(r.Context(), claims.UserID, connection, roots.ID, manifest.displayTitle()); err != nil {
				externalError(w, 409, crewSyncErrorCode(err), err.Error())
				return
			}
			binding, _ = crewroot.ReadSync(roots.ID)
			// Shares kept from before Reach was turned off go to the new online copy through the Access card's own path.
			if shared := readMacSharedAgents(roots.ID); len(shared) > 0 && binding != nil {
				if len(shared) == 0 {
					log.Printf("[CREW_RELAY] project=%s reach_on no kept shares apply to this network", roots.ID)
				} else if rows, e := api.macAgentRows(r.Context(), claims, roots.ID); e != nil {
					log.Printf("[CREW_RELAY] project=%s reach_on could not restore its shares: %v", roots.ID, e)
				} else if _, _, e = api.pushMacSharedAgents(r.Context(), claims, roots.ID, binding, connection, rows, shared); e != nil {
					log.Printf("[CREW_RELAY] project=%s reach_on could not restore its shares: %v", roots.ID, e)
				} else {
					log.Printf("[CREW_RELAY] project=%s reach_on restored shares=%d", roots.ID, len(shared))
				}
			}
		}
		externalJSON(w, crewSyncReply{ProjectID: roots.ID, Binding: binding})
		return
	}
	// Shared agents (PLAT-856-509): "agents" lists this Crew's side tabs and which are shared; "share_agents" saves the
	// owner's choice here and on the online copy, which decides who may message them.
	if input.Action == "agents" || input.Action == "share_agents" {
		api.desktopShareMacCrewAgents(w, r, claims, roots.ID, binding, connection, input.Action, input.Agents)
		return
	}
	// Reach off: the server removes this Crew's empty online copy, then the Mac forgets the pair (crew_where_it_lives.go).
	if input.Action == "offline" {
		if binding == nil {
			externalJSON(w, crewSyncReply{ProjectID: roots.ID})
			return
		}
		if binding.Mode != "relay" || binding.Editing != "local" || binding.PendingAction != "" {
			externalError(w, 409, "sync_unavailable", "This project lives online; it stays reachable from your phone and the web.")
			return
		}
		// While the Team is on, Mac online stays on (owner, 6 Nov): the main owner turns the Team off first.
		if crewTeamKeepsMacOnline(roots.ID) {
			log.Printf("[CREW_TEAM] local project=%s server_project=%s event=offline_refused reason=team_on", roots.ID, binding.PeerProjectID)
			externalError(w, 409, "team_on", crewTeamOnOfflineRefused)
			return
		}
		if err := writeCrewTeamState(roots.ID, func(s *crewTeamLocalState) { s.PreviousProject = binding.PeerProjectID }); err != nil {
			log.Printf("[CREW_TEAM] local project=%s previous online copy not saved: %v", roots.ID, err)
		}
		if _, err = desktopCrewSyncCall(r.Context(), claims.UserID, &connection, binding.PeerProjectID, crewSyncRequest{Action: "unpair", PairID: binding.PairID}); err != nil {
			externalError(w, 409, crewSyncErrorCode(err), err.Error())
			return
		}
		// The share list stays (owner, 4 Nov): turning it back on shares the same agents with the same people.
		shared := readMacSharedAgents(roots.ID)
		removed := removeCrewSyncFiles(roots.ID)
		if len(shared) > 0 {
			if err := writeMacSharedAgents(roots.ID, shared); err != nil {
				log.Printf("[CREW_RELAY] project=%s reach_off could not keep its share list: %v", roots.ID, err)
			}
		}
		log.Printf("[CREW_RELAY] project=%s reach_off server_project=%s server=%s sync_files_removed=%d shares_kept=%d", roots.ID, binding.PeerProjectID, connection.Server, removed, len(shared))
		externalJSON(w, crewSyncReply{ProjectID: roots.ID})
		return
	}
	externalError(w, 400, "invalid_action", "Choose a sync action.")
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
	if token := response.Header.Get(renewedTokenHeader); token != "" {
		if err := saveRenewedDesktopToken(userID, *connection, token); err != nil {
			return crewSyncReply{}, err
		}
		connection.Token = token
	}
	if response.StatusCode == http.StatusUnauthorized {
		return crewSyncReply{}, &crewSyncFailure{Code: crewSyncForbiddenCode, Status: response.StatusCode, Message: "reconnect this server from Connected servers, then try again"}
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
