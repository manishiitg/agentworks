package server

import (
	"log"
	"net/http"
	"time"
)

// Server-only acceptance uses the recipient's normal conversation resolver and
// private history store. It starts no model turn and carries no maker login.
// The caller reserves the Only-me registry row before releasing the Team lock.
func (api *StreamingAPI) acceptCrewAgentForkOnServer(r *http.Request, offer crewAgentFork) (ProductConversationRecord, error) {
	claims := GetUserFromContext(r.Context())
	if claims == nil || claims.UserID != offer.Recipient {
		return ProductConversationRecord{}, crewAgentForkRefuse("accept this copy where your project runs")
	}
	team, err := readCrewTeam(offer.Project)
	if err != nil {
		return ProductConversationRecord{}, err
	}
	if crewAgentForkRunsOnMac(team, claims.UserID) {
		return ProductConversationRecord{}, crewAgentForkRefuse("accept this copy on your computer")
	}
	payload, err := downloadCrewAgentFork(team, offer, claims.UserID, time.Now())
	if err != nil {
		return ProductConversationRecord{}, err
	}
	request := r.Clone(r.Context())
	request.Header = r.Header.Clone()
	request.Header.Del("X-Session-ID")
	request.Header.Del("X-Conversation-Continuation")
	destination, err := api.resolveCrewAgentForkDestination(request, crewAgentForkChatKey(offer), offer.Agent.Name+" (fork)")
	if err != nil {
		return destination, err
	}
	if destination.ResourceID != offer.Project {
		return destination, crewAgentForkRefuse("keep this copy in the same project")
	}
	roots, found, err := crewStorageRoots(destination.WorkspacePath)
	if err != nil {
		return destination, err
	}
	if !found {
		return destination, crewAgentForkRefuse("your project storage is unavailable")
	}
	if err = api.importCrewAgentForkCopy(request, offer, payload, destination, roots); err != nil {
		log.Printf("[AGENT_FORK] project=%s fork=%s copy=%s event=import_failed reason=%q", offer.Project, offer.ID, offer.CopyAgentID, crewAgentForkFailureReason(err))
		return destination, err
	}
	return destination, nil
}

// Choose only the recipient's normal model/account; a portable session bundle
// cannot select the maker's provider connection on the destination.
func (api *StreamingAPI) resolveCrewAgentForkDestination(r *http.Request, key, name string) (ProductConversationRecord, error) {
	claims := GetUserFromContext(r.Context())
	profile, err := api.agentProfiles.Resolve(crewProfileID, 0, claims.UserID)
	if err != nil {
		return ProductConversationRecord{}, err
	}
	destination, err := api.resolveAgentProfileConversation(r, profile, key, name)
	if err != nil {
		return destination, err
	}
	binding, _, err := resolveConversationBindingForUser(r.Context(), claims.UserID, profile, key)
	if err != nil {
		return destination, err
	}
	store := defaultProductConversationRegistryStore()
	doc, err := store.loadDocument(r.Context(), productConversationRegistryPath(claims.UserID))
	if err != nil {
		return destination, err
	}
	choice, err := effectiveProfileChatModel(profile, binding, destination, doc.Agents[productConversationRegistryEntryKey(crewProfileID, key)])
	if err != nil {
		return destination, err
	}
	query := QueryRequest{Provider: choice.Provider, ModelID: choice.ModelID, ConnectionID: choice.ConnectionID}
	// Resolve an unbound account before saving an explicit tab choice. Otherwise
	// the first turn fills the recipient's own account, while the saved empty
	// choice means the server account, and account admission refuses the copy.
	if query.ConnectionID == "" {
		query.ConnectionID = productChatDefaultAccount(r.Context(), claims.UserID, profile, query.Provider)
	}
	if err = constrainProductChatModel(r.Context(), &query); err != nil {
		return destination, err
	}
	if _, err = store.setAgentModel(r.Context(), claims.UserID, crewProfileID, key, choice.Engine, query.ModelID, choice.ReasoningEffort, query.ConnectionID, claims.UserID); err != nil {
		return destination, err
	}
	destination.Provider, destination.ModelID, destination.ConnectionID = query.Provider, query.ModelID, query.ConnectionID
	return destination, nil
}
