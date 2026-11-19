package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/gorilla/mux"
)

// The local app owns file capture/import. The saved pair's server alone owns
// the pending offer, recipient and Team admission. No native bytes enter a
// shared project folder or a browser response.
func (api *StreamingAPI) handleCrewAgentForkLocal(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("self") == "1" {
		api.handleCrewAgentSelfForkLocal(w, r)
		return
	}
	claims := GetUserFromContext(r.Context())
	project := mux.Vars(r)["project"]
	if !IsLocalMode() || !crewTeamClaims(claims, project, true) || ownerOfCrewProjectID(project) != claims.UserID {
		externalError(w, 403, "agent_fork_unavailable", "Use your own Crew on this computer.")
		return
	}
	localClaims, binding, connection, err := api.desktopCrewChatBinding(r.Context(), project)
	if err != nil || localClaims.UserID != claims.UserID {
		externalError(w, 409, "agent_fork_unavailable", "Connect this Crew to its Team first.")
		return
	}
	var in struct {
		Operation string `json:"operation"`
		Agent     string `json:"agent_id"`
		Recipient string `json:"recipient_id"`
		Fork      string `json:"fork_id"`
	}
	if json.NewDecoder(io.LimitReader(r.Body, 8192)).Decode(&in) != nil {
		externalError(w, 400, "agent_fork_unavailable", "Choose a teammate or pending copy.")
		return
	}
	base := "/api/crew-teams/" + url.PathEscape(binding.PeerProjectID) + "/agent-forks"
	query := "?pair=" + url.QueryEscape(binding.PairID)
	var result any
	switch in.Operation {
	case "send":
		// The copy comes from the chat the server keeps for this shared agent (#3218). Bring it up to date first,
		// so sharing and sending straight away still copies the whole chat.
		syncCtx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
		err = api.syncCrewOfflineAppProjectNow(syncCtx, claims.UserID, project)
		cancel()
		if err != nil {
			log.Printf("[AGENT_FORK] project=%s event=send_sync_failed agent=%s error_type=%T", project, in.Agent, err)
		}
		var offer crewAgentFork
		err = crewTeamRemoteJSON(r.Context(), claims.UserID, connection, http.MethodPost, base+query, map[string]string{"agent_id": in.Agent, "recipient_id": in.Recipient}, &offer)
		if err == nil && (offer.Project != binding.PeerProjectID || offer.Sender != connection.User.ID || !offer.Ready) {
			err = fmt.Errorf("the copy on the server changed; send it again")
		}
		result = map[string]bool{"ready": err == nil}
	case "accept":
		result, err = api.acceptCrewAgentForkOnMac(r, project, connection, binding.PeerProjectID, in.Fork, base, query)
	default:
		externalError(w, 400, "agent_fork_unavailable", "Choose send or accept.")
		return
	}
	if err != nil {
		// The real cause, so a failed send or accept on a Mac can be traced from its log.
		log.Printf("[AGENT_FORK] project=%s event=local_failed operation=%s agent=%s fork=%s reason=%q", project, in.Operation, in.Agent, in.Fork, err)
		line := "Could not copy this conversation through your Crew's Team. Check the connection and try again."
		var remote *crewRemoteHTTPError
		if errors.As(err, &remote) && remote.Message != "" {
			line = laptopActionErrorText(remote.Message)
		}
		externalError(w, 409, "agent_fork_unavailable", line)
		return
	}
	externalJSON(w, result)
}

func (api *StreamingAPI) acceptCrewAgentForkOnMac(r *http.Request, project string, connection desktopConnection, remoteProject, id, base, query string) (any, error) {
	claims := GetUserFromContext(r.Context())
	// Fetch the exact pending record rather than accepting a browser's descriptor.
	var pending struct {
		Forks []crewAgentFork `json:"forks"`
	}
	if err := crewTeamRemoteJSON(r.Context(), claims.UserID, connection, http.MethodGet, base, nil, &pending); err != nil {
		return nil, err
	}
	var offer crewAgentFork
	for _, candidate := range pending.Forks {
		if candidate.ID == id {
			offer = candidate
			break
		}
	}
	if offer.ID == "" || offer.Project != remoteProject || offer.Recipient != connection.User.ID {
		return nil, fmt.Errorf("this copy is not addressed to you")
	}
	route := base + "/" + url.PathEscape(offer.ID)
	var prepared struct {
		Copy crewTeamRegisteredAgent `json:"copy"`
	}
	if err := crewTeamRemoteJSON(r.Context(), claims.UserID, connection, http.MethodPost, route+"/accept"+query, nil, &prepared); err != nil {
		return nil, err
	}
	if prepared.Copy.ID != offer.CopyAgentID || prepared.Copy.OwnerID != connection.User.ID || prepared.Copy.InTeam {
		return nil, fmt.Errorf("the private copy identity changed")
	}
	var payload crewAgentForkPayload
	if err := crewTeamRemoteJSON(r.Context(), claims.UserID, connection, http.MethodPost, route+"/download"+query, nil, &payload); err != nil {
		return nil, err
	}
	if err := validateCrewAgentForkPayload(payload); err != nil {
		return nil, err
	}
	local := offer
	local.Project, local.Recipient = project, claims.UserID
	dir, err := crewAgentForkDirectory(project, offer.ID)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	if payload.Conversation.Native != nil {
		chunks := map[string]transferChunk{}
		for _, file := range payload.NativeFiles {
			for _, chunk := range file.Chunks {
				chunks[chunk.ID] = chunk
			}
		}
		needed := []transferChunk{}
		for _, chunk := range chunks {
			needed = append(needed, chunk)
		}
		if _, err = fetchChunks(r.Context(), claims.UserID, connection, route, query, dir, needed, nil); err != nil {
			return nil, err
		}
		if err = crewNativeAssembleInto(filepath.Join(dir, "native"), payload.NativeFiles, func(chunk transferChunk) ([]byte, error) { return os.ReadFile(filepath.Join(dir, chunk.ID)) }); err != nil {
			return nil, err
		}
	}
	request := r.Clone(r.Context())
	request.Header = r.Header.Clone()
	request.Header.Del("X-Session-ID")
	request.Header.Del("X-Conversation-Continuation")
	destination, err := api.resolveCrewAgentForkDestination(request, crewAgentForkChatKey(local), offer.Agent.Name+" (fork)")
	if err != nil {
		return nil, err
	}
	if destination.ResourceID != project {
		return nil, fmt.Errorf("keep this copy in the same Crew")
	}
	roots, found, err := crewStorageRoots(destination.WorkspacePath)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("your Crew storage is unavailable")
	}
	if err = api.saveCrewAgentForkCopy(request, local, payload, destination, roots); err != nil {
		return nil, err
	}
	if err = crewTeamRemoteJSON(r.Context(), claims.UserID, connection, http.MethodPost, route+"/complete"+query, nil, nil); err != nil {
		return nil, err
	}
	log.Printf("[AGENT_FORK] project=%s fork=%s copy=%s session=%s event=local_accepted", project, offer.ID, offer.CopyAgentID, destination.SessionID)
	return map[string]any{"accepted": true, "conversation_key": destination.ConversationKey, "session_id": destination.SessionID}, nil
}
