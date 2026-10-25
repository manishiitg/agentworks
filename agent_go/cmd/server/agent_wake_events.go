package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"
)

// One login-level metadata stream lets a helper wake saved dormant chats without
// keeping a socket per dormant name. Bodies still require the exact attachment.
func agentWakeClaims(ctx context.Context, claims *UserClaims) (*UserClaims, error) {
	if claims == nil || claims.AccessToken == nil {
		return nil, fmt.Errorf("agent connection required")
	}
	live, err := agentMessageConnectionClaims(ctx, claims.AccessToken.ID)
	if err != nil || live.UserID != claims.UserID || !namedAgentOperationAllowed(live, false) || live.AccessToken.BoundAgentID != "" || live.AccessToken.ProjectKind != "" {
		return nil, errAgentLoginUnavailable
	}
	return live, nil
}

func (api *StreamingAPI) handleAgentWakeEvents(w http.ResponseWriter, r *http.Request) {
	claims := GetUserFromContext(r.Context())
	if _, err := agentWakeClaims(r.Context(), claims); err != nil {
		externalError(w, 403, "attachment_refused", "Account messaging connection unavailable.")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		externalError(w, 500, "stream_unavailable", "Streaming is unavailable.")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	write := func(frame any) error {
		_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(10 * time.Second))
		raw, err := json.Marshal(frame)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(w, "data: %s\n\n", raw)
		flusher.Flush()
		return err
	}
	if write(map[string]any{"type": "connected"}) != nil {
		return
	}
	heartbeat := time.NewTicker(10 * time.Second)
	defer heartbeat.Stop()
	sent := map[string]int{}
	lastScanLog := time.Now()
	for {
		changed := agentLiveChanged()
		scanStarted := time.Now()
		live, err := agentWakeClaims(r.Context(), claims)
		if err != nil {
			_ = write(map[string]any{"type": "login_changed"})
			return
		}
		store, err := readAgentMessageSnapshot(r.Context())
		if err != nil {
			return
		}
		agents := map[string]namedMessageAgent{}
		receivers := map[string]*UserClaims{}
		for _, a := range store.Agents {
			if a.UserID != live.UserID || !namedAgentConnectionAllowed(a, live) {
				continue
			}
			agents[a.ID] = a
		}
		latest := map[string]int{}
		now := time.Now().UTC()
		for _, conversation := range store.Conversations {
			ours := false
			for _, endpoint := range conversation.Endpoints {
				if _, ok := agents[endpoint.NamedAgentID]; ok && endpoint.UserID == live.UserID {
					ours = true
					break
				}
			}
			if !ours {
				continue
			}
			for _, message := range conversation.Messages {
				dest := conversation.Endpoints[1-message.From]
				a, ok := agents[dest.NamedAgentID]
				if !ok || dest.UserID != live.UserID || message.Kind == "ping" || message.Delivery != "stored" || !message.InboxRemovedAt.IsZero() || agentMessagePendingExpired(message, now) {
					continue
				}
				receiver, checked := receivers[a.ID]
				if !checked {
					receiver = live
					if a.Attachment != nil {
						receiver, err = agentMessageConnectionClaims(r.Context(), a.Attachment.ConnectionID)
						if err != nil || !namedAgentConnectionAllowed(a, receiver) || !namedAgentOperationAllowed(receiver, false) {
							receiver = nil
						}
					}
					receivers[a.ID] = receiver
				}
				if receiver == nil {
					continue
				}
				if !serverAuthoredKind(message.Kind) && (!store.shareSenderAllowed(conversation, message.From, a.UserID, now) || api.recheckAgentMessageAdmission(r.Context(), conversation, message) != nil || api.externalAuthorizeMessagePeer(r.Context(), receivers[a.ID], conversation.Endpoints[message.From], false) != nil) {
					continue
				}
				latest[a.ID] = max(latest[a.ID], message.AgentSequence)
			}
		}
		for id, sequence := range latest {
			a := agents[id]
			attachment := ""
			var wireAttachment any
			if a.Attachment != nil {
				attachment = a.Attachment.ID
				wireAttachment = attachment
			}
			key := id + "|" + attachment
			if sequence <= sent[key] {
				continue
			}
			// Recheck the login and current name immediately before releasing metadata.
			current, err := agentWakeClaims(r.Context(), claims)
			if err != nil {
				_ = write(map[string]any{"type": "login_changed"})
				return
			}
			fresh, err := namedAgentForClaims(r.Context(), current, a.Name)
			if err != nil || fresh.ID != id {
				continue
			}
			currentAttachment := ""
			if fresh.Attachment != nil {
				currentAttachment = fresh.Attachment.ID
			}
			if currentAttachment != attachment {
				continue
			}
			if write(map[string]any{"type": "inbox_changed", "agent_id": id, "attachment_id": wireAttachment, "sequence": sequence}) != nil {
				return
			}
			sent[key] = sequence
			log.Printf("[AGENT-WAKE] agent=%s attachment=%s sequence=%d", id, attachment, sequence)
		}
		if time.Since(lastScanLog) >= time.Minute {
			log.Printf("[AGENT-WAKE] scan agents=%d conversations=%d elapsed=%s", len(agents), len(store.Conversations), time.Since(scanStarted))
			lastScanLog = time.Now()
		}
		select {
		case <-r.Context().Done():
			return
		case <-changed:
		case <-heartbeat.C:
			if write(map[string]any{"type": "heartbeat"}) != nil {
				return
			}
		}
	}
}
