package server

import (
	"context"
	"errors"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

var (
	agentMessageIdentityRemoteTimeout = 3 * time.Second
	agentMessageIdentityCacheTTL      = 60 * time.Second
	agentMessageIdentityGroup         singleflight.Group
	agentMessageIdentityCacheMu       sync.Mutex
	agentMessageIdentityCache         = map[string]agentMessageIdentityCacheEntry{}
)

type agentMessageIdentityCacheEntry struct {
	at   time.Time
	data map[string]interface{}
}

func cloneIdentityMap(in map[string]interface{}) map[string]interface{} {
	if in == nil {
		return nil
	}
	out := make(map[string]interface{}, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func loadAgentMessageIdentityCache(key string) (map[string]interface{}, bool) {
	if agentMessageIdentityCacheTTL <= 0 {
		return nil, false
	}
	agentMessageIdentityCacheMu.Lock()
	defer agentMessageIdentityCacheMu.Unlock()
	entry, ok := agentMessageIdentityCache[key]
	if !ok {
		return nil, false
	}
	if time.Since(entry.at) >= agentMessageIdentityCacheTTL {
		delete(agentMessageIdentityCache, key)
		return nil, false
	}
	return cloneIdentityMap(entry.data), true
}

func storeAgentMessageIdentityCache(key string, data map[string]interface{}) {
	if agentMessageIdentityCacheTTL <= 0 {
		return
	}
	now := time.Now()
	agentMessageIdentityCacheMu.Lock()
	if len(agentMessageIdentityCache) >= 256 {
		for k, v := range agentMessageIdentityCache {
			if now.Sub(v.at) >= agentMessageIdentityCacheTTL {
				delete(agentMessageIdentityCache, k)
			}
		}
	}
	agentMessageIdentityCache[key] = agentMessageIdentityCacheEntry{at: now, data: cloneIdentityMap(data)}
	agentMessageIdentityCacheMu.Unlock()
}

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
		direction := r.URL.Query().Get("direction")
		messageID := r.URL.Query().Get("message_id")
		query := url.Values{"inbox_id": {peer}, "direction": {direction}, "message_id": {messageID}}
		viewerAgentName := ""
		if session := r.Header.Get("X-Session-ID"); session != "" {
			// This selects a Live-view authorization identity, not cached labels.
			if doc, err := defaultProductConversationRegistryStore().loadDocument(r.Context(), productConversationRegistryPath(claims.UserID)); err == nil {
				for _, record := range doc.Entries {
					if record.SessionID == session && record.ProfileID == crewProfileID && record.ResourceID != "" && record.ConversationID != "" && ordinaryProjectChatKey(record.ResourceID, record.ConversationKey) && crewProjectOwnedByCallerStrict(claims.UserID, record.WorkspacePath) {
						viewerAgentName = desktopAppAgentName(desktopMachineID(), record.ConversationID)
						query.Set("viewer_agent_name", viewerAgentName)
						break
					}
				}
			}
		}
		cacheKey := claims.UserID + "\x00" + connectionID + "\x00" + peer + "\x00" + direction + "\x00" + messageID + "\x00" + viewerAgentName
		if cached, ok := loadAgentMessageIdentityCache(cacheKey); ok {
			w.Header().Set("Cache-Control", "no-store")
			externalJSON(w, cached)
			return
		}
		allowed, releaseProbe := desktopProxyTryAcquire(connectionID)
		if !allowed {
			externalError(w, 404, "identity_unavailable", "Agent identity is unavailable.")
			return
		}
		if releaseProbe != nil {
			defer releaseProbe()
		}
		val, err, _ := agentMessageIdentityGroup.Do(cacheKey, func() (interface{}, error) {
			if cached, ok := loadAgentMessageIdentityCache(cacheKey); ok {
				return cached, nil
			}
			read, done := context.WithTimeout(context.WithoutCancel(r.Context()), agentMessageIdentityRemoteTimeout)
			defer done()
			var out map[string]interface{}
			if err := crewTeamRemoteJSON(read, claims.UserID, connection, http.MethodGet, "/api/agent-message-identity?"+query.Encode(), nil, &out); err != nil {
				var transportErr *crewRemoteTransportError
				if errors.As(err, &transportErr) {
					markDesktopProxyUnreachable(connectionID)
				}
				return nil, err
			}
			clearDesktopProxyUnreachable(connectionID)
			out["avatar_connection"] = connectionID
			storeAgentMessageIdentityCache(cacheKey, out)
			return out, nil
		})
		if err != nil {
			externalError(w, 404, "identity_unavailable", "Agent identity is unavailable.")
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		externalJSON(w, cloneIdentityMap(val.(map[string]interface{})))
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
