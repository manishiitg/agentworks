package server

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

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
		var offer crewAgentFork
		err = crewTeamRemoteJSON(r.Context(), claims.UserID, connection, http.MethodPost, base+query, map[string]string{"agent_id": in.Agent, "recipient_id": in.Recipient}, &offer)
		if err == nil {
			if offer.Project != binding.PeerProjectID || offer.Sender != connection.User.ID {
				err = fmt.Errorf("the maker's paired account changed")
			} else {
				err = api.uploadCrewAgentForkFromMac(r, project, connection, offer, base, query)
			}
		}
		if err != nil && offer.ID != "" {
			_ = crewTeamRemoteJSON(r.Context(), claims.UserID, connection, http.MethodPost, base+"/"+url.PathEscape(offer.ID)+"/cancel"+query, nil, nil)
		}
		result = map[string]bool{"ready": err == nil}
	case "accept":
		result, err = api.acceptCrewAgentForkOnMac(r, project, connection, binding.PeerProjectID, in.Fork, base, query)
	default:
		externalError(w, 400, "agent_fork_unavailable", "Choose send or accept.")
		return
	}
	if err != nil {
		log.Printf("[AGENT_FORK] project=%s event=local_failed operation=%s reason=paired_copy_unavailable", project, in.Operation)
		externalError(w, 409, "agent_fork_unavailable", "Could not copy this conversation through your Crew's Team. Check the connection and try again.")
		return
	}
	externalJSON(w, result)
}

func (api *StreamingAPI) uploadCrewAgentForkFromMac(r *http.Request, project string, connection desktopConnection, offer crewAgentFork, base, query string) error {
	claims := GetUserFromContext(r.Context())
	tabs, main, err := api.macCrewAgents(r.Context(), claims, project)
	if err != nil {
		return err
	}
	key := ""
	if offer.Agent.Tab == macMainTab && main.CanWrite {
		key = main.ConversationKey
	}
	for _, tab := range tabs {
		if strings.TrimPrefix(tab.ConversationKey, project+codeChatSideMarker) == offer.Agent.Tab {
			key = tab.ConversationKey
		}
	}
	if key == "" {
		return fmt.Errorf("this source agent is not on this computer")
	}
	// Local identities are never used as remote membership assertions.
	localDir, err := crewAgentForkDirectory(offer.Project, offer.ID)
	if err != nil {
		return err
	}
	defer os.RemoveAll(localDir)
	capture := offer
	capture.Sender = claims.UserID
	payload, err := api.captureCrewAgentForkAtKey(r.Context(), claims, capture, project, key)
	if err != nil {
		return err
	}
	route := base + "/" + url.PathEscape(offer.ID)
	var plan struct {
		Missing []string `json:"missing"`
	}
	if err = crewTeamRemoteJSON(r.Context(), claims.UserID, connection, http.MethodPost, route+"/plan"+query, payload, &plan); err != nil {
		return err
	}
	if payload.Conversation.Native != nil {
		_, sources, err := crewNativeFiles(payload.Conversation.Native.Root, payload.Conversation.Native, false, nil)
		if err != nil {
			return err
		}
		if _, _, err = sendMissingChunks(r.Context(), claims.UserID, connection, route+"/chunks"+query, payload.Conversation.Native.Root, sources, plan.Missing, nil); err != nil {
			return err
		}
	}
	if err = crewTeamRemoteJSON(r.Context(), claims.UserID, connection, http.MethodPost, route+"/commit"+query, payload, nil); err != nil {
		return err
	}
	log.Printf("[AGENT_FORK] project=%s fork=%s event=uploaded", project, offer.ID)
	return nil
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
