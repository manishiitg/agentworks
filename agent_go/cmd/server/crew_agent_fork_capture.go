package server

import (
	"context"
	"fmt"
	"log"
)

// Server-run chats are captured under the maker's authenticated registry and
// account. The request selects an owned registry agent, never a history path,
// session ID or native directory. Only the portable conversation travels.
func (api *StreamingAPI) captureCrewAgentFork(ctx context.Context, claims *UserClaims, offer crewAgentFork) (crewAgentForkPayload, error) {
	return api.captureCrewAgentForkAtKey(ctx, claims, offer, offer.Project, offer.Agent.ChatKey)
}

func (api *StreamingAPI) captureCrewAgentForkAtKey(ctx context.Context, claims *UserClaims, offer crewAgentFork, localProject, key string) (crewAgentForkPayload, error) {
	var payload crewAgentForkPayload
	if claims == nil || claims.UserID != offer.Sender {
		return payload, fmt.Errorf("send one of your own agents")
	}
	doc, err := defaultProductConversationRegistryStore().loadDocument(ctx, productConversationRegistryPath(claims.UserID))
	if err != nil {
		return payload, err
	}
	record, ok := doc.Entries[productConversationRegistryEntryKey(crewProfileID, key)]
	if !ok || record.ProfileID != crewProfileID || record.ConversationKey != key || crewResourceProjectID(record.ConversationKey) != localProject {
		return payload, fmt.Errorf("this agent has no saved conversation on this computer")
	}
	roots, found, err := crewStorageRoots(record.WorkspacePath)
	if err != nil {
		return payload, err
	}
	if !found {
		return payload, fmt.Errorf("this agent's project is unavailable")
	}
	payload.Conversation, err = api.captureCrewHandoffConversation(ctx, claims, roots, record)
	if err != nil {
		return payload, err
	}
	if payload.Conversation.Native != nil {
		store, err := crewAgentForkChunkStore(offer)
		if err != nil {
			return payload, err
		}
		payload.NativeFiles, _, err = crewNativeFiles(payload.Conversation.Native.Root, payload.Conversation.Native, false, func(id string, data []byte) error {
			return putChunk(store, id, data)
		})
		if err != nil {
			return payload, err
		}
	}
	log.Printf("[AGENT_FORK] project=%s fork=%s source=%s event=captured native=%t", offer.Project, offer.ID, offer.Agent.ID, payload.Conversation.Native != nil)
	return payload, validateCrewAgentForkPayload(payload)
}
