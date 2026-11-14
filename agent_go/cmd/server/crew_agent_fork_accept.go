package server

import (
	"fmt"
	"log"
	"net/http"
	"time"
)

// Server-only acceptance uses the recipient's normal conversation resolver and
// private history store. It starts no model turn and carries no maker login.
// The caller reserves the Only-me registry row before releasing the Team lock.
func (api *StreamingAPI) acceptCrewAgentForkOnServer(r *http.Request, offer crewAgentFork) (ProductConversationRecord, error) {
	claims := GetUserFromContext(r.Context())
	if claims == nil || claims.UserID != offer.Recipient || crewTeamMemberOnMac(offer.Project, claims.UserID) {
		return ProductConversationRecord{}, fmt.Errorf("accept this copy where your Crew runs")
	}
	team, err := readCrewTeam(offer.Project)
	if err != nil {
		return ProductConversationRecord{}, err
	}
	payload, err := downloadCrewAgentFork(team, offer, claims.UserID, time.Now())
	if err != nil {
		return ProductConversationRecord{}, err
	}
	profile, err := api.agentProfiles.Resolve(crewProfileID, 0, claims.UserID)
	if err != nil {
		return ProductConversationRecord{}, err
	}
	request := r.Clone(r.Context())
	request.Header = r.Header.Clone()
	request.Header.Del("X-Session-ID")
	request.Header.Del("X-Conversation-Continuation")
	destination, err := api.resolveAgentProfileConversation(request, profile, crewAgentForkChatKey(offer), offer.Agent.Name+" (fork)")
	if err != nil {
		return destination, err
	}
	if destination.ResourceID != offer.Project {
		return destination, fmt.Errorf("keep this copy in the same Crew")
	}
	roots, found, err := crewStorageRoots(destination.WorkspacePath)
	if err != nil {
		return destination, err
	}
	if !found {
		return destination, fmt.Errorf("your Crew storage is unavailable")
	}
	if err = api.importCrewAgentForkCopy(request, offer, payload, destination, roots); err != nil {
		log.Printf("[AGENT_FORK] project=%s fork=%s copy=%s event=import_failed reason=destination_unavailable", offer.Project, offer.ID, offer.CopyAgentID)
		return destination, err
	}
	return destination, nil
}
