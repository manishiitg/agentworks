package server

import (
	"context"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Identity projection reads no message bodies and sends nothing. The existing
// inbox participants (or a person already allowed to watch its exact Live chat)
// are the only readers. A routing ID is never treated as a permission grant.
func (api *StreamingAPI) handleAgentMessageIdentity(w http.ResponseWriter, r *http.Request) {
	claims := GetUserFromContext(r.Context())
	if claims == nil || claims.UserID == "" || claims.AccessToken != nil || claims.Scope != "" {
		externalError(w, 403, "identity_unavailable", "Agent identity is unavailable.")
		return
	}
	inbox := strings.TrimSpace(r.URL.Query().Get("inbox_id"))
	if connectionID, peer, ok := splitDesktopAgentNetworkAddress(inbox); IsLocalMode() && ok {
		if claims.UserID != GetDefaultUserID() {
			externalError(w, 404, "identity_unavailable", "Agent identity is unavailable.")
			return
		}
		connection, err := desktopConnectionFor(claims.UserID, connectionID)
		if err != nil {
			externalError(w, 404, "identity_unavailable", "Agent identity is unavailable.")
			return
		}
		read, done := context.WithTimeout(r.Context(), 8*time.Second)
		defer done()
		query := url.Values{"inbox_id": {peer}, "direction": {r.URL.Query().Get("direction")}, "message_id": {r.URL.Query().Get("message_id")}}
		if session := r.Header.Get("X-Session-ID"); session != "" {
			// This selects a Live-view authorization identity, not cached labels.
			if doc, err := defaultProductConversationRegistryStore().loadDocument(r.Context(), productConversationRegistryPath(claims.UserID)); err == nil {
				for _, record := range doc.Entries {
					if record.SessionID == session && record.ProfileID == crewProfileID && record.ResourceID != "" && record.ConversationID != "" && appAgentChatKey(record.ResourceID, record.ConversationKey) && crewProjectOwnedByCallerStrict(claims.UserID, record.WorkspacePath) {
						query.Set("viewer_agent_name", desktopAppAgentName(desktopMachineID(), record.ConversationID))
						break
					}
				}
			}
		}
		var out map[string]interface{}
		if err := crewTeamRemoteJSON(read, claims.UserID, connection, http.MethodGet, "/api/agent-message-identity?"+query.Encode(), nil, &out); err != nil {
			externalError(w, 404, "identity_unavailable", "Agent identity is unavailable.")
			return
		}
		out["avatar_connection"] = connectionID
		w.Header().Set("Cache-Control", "no-store")
		externalJSON(w, out)
		return
	}
	snapshot, err := readAgentMessageSnapshot(r.Context())
	if err != nil {
		externalError(w, 503, "identity_unavailable", "Agent identity is unavailable.")
		return
	}
	session := r.Header.Get("X-Session-ID")
	sent := r.URL.Query().Get("direction") == "sent"
	for _, conversation := range snapshot.Conversations {
		if conversation.ID != inbox {
			continue
		}
		viewerSide := -1
		peerSide := -1
		// Message IDs bind a displayed incoming row to its actual sender,
		// including a conversation between two agents of the same person.
		if id := r.URL.Query().Get("message_id"); id != "" {
			for _, message := range conversation.Messages {
				if message.ID == id {
					side := 1 - message.From
					if sent {
						side = message.From
					}
					endpoint := conversation.Endpoints[side]
					if endpoint.UserID == claims.UserID || session != "" && endpoint.Session == session && api.crewSessionTabView(r, session) {
						viewerSide = side
						peerSide = 1 - side
					}
					break
				}
			}
		}
		if viewerSide < 0 {
			for side, endpoint := range conversation.Endpoints {
				if session != "" && endpoint.Session == session && (endpoint.UserID == claims.UserID || api.crewSessionTabView(r, session)) {
					viewerSide = side
					break
				}
				if name := r.URL.Query().Get("viewer_agent_name"); name != "" && endpoint.UserID == claims.UserID && endpoint.NamedAgentID != "" {
					if agent, err := namedAgentByID(r.Context(), claims.UserID, endpoint.NamedAgentID); err == nil && agent.Name == name {
						viewerSide = side
						break
					}
				}
			}
		}
		if viewerSide < 0 && conversation.Endpoints[0].UserID != conversation.Endpoints[1].UserID {
			for side, endpoint := range conversation.Endpoints {
				if endpoint.UserID == claims.UserID {
					viewerSide = side
					break
				}
			}
		}
		if viewerSide < 0 {
			break
		}
		if peerSide < 0 {
			peerSide = 1 - viewerSide
		}
		peer := conversation.Endpoints[peerSide]
		// Older MCP endpoints did not keep a named agent, but the message
		// keeps its authenticated connection. Read that exact binding if it
		// still exists, without substituting an unrelated connection.
		if peer.NamedAgentID == "" {
			for index := len(conversation.Messages) - 1; index >= 0; index-- {
				message := conversation.Messages[index]
				if message.From != peerSide || message.SenderConnectionID == "" {
					continue
				}
				if actor, err := agentMessageConnectionClaims(r.Context(), message.SenderConnectionID); err == nil && actor.UserID == peer.UserID && actor.AccessToken != nil {
					peer.NamedAgentID = actor.AccessToken.BoundAgentID
				}
				break
			}
		}
		label, _, _ := agentMessageSender(r.Context(), peer, claims.UserID)
		log.Printf("[AGENT_MESSAGE_IDENTITY] caller=%s inbox=%s action=read", claims.UserID, inbox)
		w.Header().Set("Cache-Control", "no-store")
		address, _ := crewTeamEndpointAddress(r.Context(), peer)
		if address == "" {
			address = firstNonEmptyTrimmed(peer.ChatKey, peer.ID)
		}
		avatar := agentMessageAvatarFor(peer)
		// Your own agents' messages show an agent icon, not your own photo back to you.
		if peer.UserID == claims.UserID {
			avatar = userAvatarFields{AvatarIcon: "bot"}
		}
		out := map[string]interface{}{"label": label, "address": address, "avatar_url": avatar.AvatarURL, "avatar_icon": avatar.AvatarIcon, "avatar_color": avatar.AvatarColor}
		externalJSON(w, out)
		return
	}
	log.Printf("[AGENT_MESSAGE_IDENTITY] caller=%s action=skipped reason=not_participant", claims.UserID)
	externalError(w, 404, "identity_unavailable", "Agent identity is unavailable.")
}
