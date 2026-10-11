package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

type namedAgentAttachment struct {
	ID             string   `json:"attachment_id"`
	Provider       string   `json:"provider"`
	SessionID      string   `json:"native_session_id"`
	ConnectionID   string   `json:"connection_id"`
	AllowedSenders []string `json:"allowed_sender_ids"`
}

var agentLiveEvents = struct {
	sync.Mutex
	changed chan struct{}
	online  map[string]string
}{changed: make(chan struct{}), online: map[string]string{}}

// Attachment is a person-configured CLI operation, not an agent MCP tool.
// The native bridge gets this credential from its explicit launch config.
func (api *StreamingAPI) handleAgentAttachment(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer "+cliOAuthAccessPrefix) {
		externalError(w, 403, "attachment_refused", "Configure attachment through your signed-in AgentWorks CLI, not an agent tool.")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	var args map[string]any
	if json.NewDecoder(r.Body).Decode(&args) != nil || !slices.Contains([]string{"attach", "detach", "validate", "connection"}, externalArg(args, "action")) {
		externalError(w, 400, "invalid_arguments", "Choose attach or detach for this exact native session.")
		return
	}
	if externalArg(args, "action") == "connection" {
		api.createAgentConnection(w, r, args)
		return
	}
	if token := GetUserFromContext(r.Context()).AccessToken; token != nil && token.BoundAgentID != "" && externalArg(args, "agent_name") != token.BoundAgentName {
		externalError(w, 403, "attachment_refused", "This connection belongs to another agent.")
		return
	}
	if externalArg(args, "action") == "validate" {
		api.validateAgentOffers(w, r, args)
		return
	}
	api.externalAgentAttachment(w, r, args)
}

// A closed generation wakes all listeners; they recheck the store and current
// authority. Capture the generation before reading to avoid a lost wakeup.
func agentLiveChanged() <-chan struct{} {
	agentLiveEvents.Lock()
	defer agentLiveEvents.Unlock()
	return agentLiveEvents.changed
}
func notifyAgentLiveChanged() {
	agentLiveEvents.Lock()
	close(agentLiveEvents.changed)
	agentLiveEvents.changed = make(chan struct{})
	agentLiveEvents.Unlock()
}
func namedAgentOnline(a namedMessageAgent) bool {
	agentLiveEvents.Lock()
	defer agentLiveEvents.Unlock()
	return a.Attachment != nil && agentLiveEvents.online[a.ID] == a.Attachment.ID
}

func (api *StreamingAPI) externalAgentAttachment(w http.ResponseWriter, r *http.Request, args map[string]any) {
	claims := GetUserFromContext(r.Context())
	if !namedAgentOperationAllowed(claims, true) || claims.AccessToken == nil {
		externalError(w, 403, "insufficient_scope", "An approved agent connection is required.")
		return
	}
	name, err := normalizeMessageAgentName(externalArg(args, "agent_name"))
	if err != nil {
		externalError(w, 400, "invalid_arguments", err.Error())
		return
	}
	agentMessageMu.Lock()
	defer agentMessageMu.Unlock()
	s, err := readAgentMessageStore(r.Context())
	if err != nil {
		externalCommunicationError(w, 400, "attachment_refused", err)
		return
	}
	index := -1
	for i, a := range s.Agents {
		if a.UserID == claims.UserID && a.Name == name && namedAgentConnectionAllowed(a, claims) {
			index = i
			break
		}
	}
	if index < 0 {
		externalError(w, 404, "not_found", "Named agent unavailable or access denied.")
		return
	}
	a := &s.Agents[index]
	switch externalArg(args, "action") {
	case "attach":
		provider, session, purpose := externalArg(args, "provider"), externalArg(args, "native_session_id"), strings.TrimSpace(externalArg(args, "purpose"))
		if !slices.Contains([]string{"codex", "claude", "muse", "cursor", "agy", "pi"}, provider) || session == "" || len(session) > 128 || strings.ContainsAny(session, "/\\\r\n\x00") || purpose == "" || len(purpose) > 512 {
			externalError(w, 400, "invalid_arguments", "Attach requires a supported provider, its exact native_session_id and purpose (1 to 512 bytes).")
			return
		}
		var names []string
		raw, _ := json.Marshal(args["allowed_senders"])
		if err = json.Unmarshal(raw, &names); err != nil || names == nil || len(names) > 100 {
			externalError(w, 400, "invalid_arguments", "allowed_senders must explicitly list registered sender names (at most 100).")
			return
		}
		ids := []string{}
		for _, n := range names {
			normalized, e := normalizeMessageAgentName(strings.TrimPrefix(n, "agent:"))
			if e != nil {
				externalError(w, 400, "invalid_arguments", e.Error())
				return
			}
			found := false
			for _, sender := range s.Agents {
				if sender.UserID == claims.UserID && sender.Name == normalized {
					ids = append(ids, sender.ID)
					found = true
					break
				}
			}
			if !found {
				externalError(w, 400, "invalid_arguments", "Every allowed sender must currently be registered in this account.")
				return
			}
		}
		a.Purpose = purpose
		a.Attachment = &namedAgentAttachment{ID: "attach-" + uuid.NewString(), Provider: provider, SessionID: session, ConnectionID: claims.AccessToken.ID, AllowedSenders: ids}
	case "detach":
		if a.Attachment == nil || a.Attachment.ID != externalArg(args, "attachment_id") {
			externalError(w, 409, "attachment_changed", "The exact attachment is no longer current.")
			return
		}
		a.Attachment = nil
	case "ack":
		if a.Attachment == nil || a.Attachment.ID != externalArg(args, "attachment_id") || a.Attachment.ConnectionID != claims.AccessToken.ID {
			externalError(w, 409, "attachment_changed", "The exact attachment is no longer current.")
			return
		}
		delivery, transport := externalArg(args, "delivery"), externalArg(args, "transport")
		if !slices.Contains([]string{"admitted", "typed", "processed", "failed"}, delivery) || !slices.Contains([]string{"codex_queue", "codex_desktop", "claude_channel", "claude_session", "claude_hook", "codex_hook", "cursor_hook", "muse_hook", "agy_hook", "pi_hook", "pi_extension", "muse_session", "tmux", "mailbox"}, transport) || delivery == "typed" && transport != "tmux" || delivery == "admitted" && transport == "tmux" {
			externalError(w, 400, "invalid_arguments", "Use an adapter receipt: admitted, typed (tmux only), processed (explicit agent acknowledgement), or failed.")
			return
		}
		found := false
		for ci := range s.Conversations {
			c := &s.Conversations[ci]
			for mi := range c.Messages {
				m := &c.Messages[mi]
				dest := c.Endpoints[1-m.From]
				if m.ID != externalArg(args, "message_id") || dest.NamedAgentID != a.ID || dest.UserID != claims.UserID {
					continue
				}
				if m.LiveAttachment != a.Attachment.ID || !slices.Contains(a.Attachment.AllowedSenders, c.Endpoints[m.From].NamedAgentID) {
					externalError(w, 409, "receipt_refused", "The message was not offered to this exact attachment or its sender is no longer approved.")
					return
				}
				if delivery == "processed" && m.Delivery != "admitted" && m.Delivery != "typed" && m.Delivery != "processed" {
					externalError(w, 409, "not_admitted", "Only an admitted or typed message can be acknowledged as processed.")
					return
				}
				if m.Delivery == "processed" && delivery != "processed" {
					externalError(w, 409, "receipt_refused", "A processed receipt cannot be downgraded.")
					return
				}
				m.Delivery = delivery
				m.LiveTransport = transport
				m.LiveSession = a.Attachment.SessionID
				providerForTransport := map[string]string{"claude_session": "claude", "claude_channel": "claude", "claude_hook": "claude", "codex_queue": "codex", "codex_desktop": "codex", "codex_hook": "codex", "cursor_hook": "cursor", "muse_session": "muse", "muse_hook": "muse", "agy_hook": "agy", "pi_hook": "pi", "pi_extension": "pi"}
				if providerForTransport[transport] != "" && a.Attachment.Provider != providerForTransport[transport] {
					externalError(w, 400, "invalid_arguments", "Receipt transport does not match the attached provider.")
					return
				}
				found = true
			}
		}
		if !found {
			externalError(w, 404, "not_found", "Incoming message unavailable or access denied.")
			return
		}
	}
	if err = saveAgentMessageStore(r.Context(), s); err != nil {
		externalCommunicationError(w, 400, "attachment_refused", err)
		return
	}
	out := map[string]any{"agent_id": a.ID, "agent_name": a.Name, "purpose": a.Purpose, "status": externalArg(args, "action") + "ed"}
	if externalArg(args, "action") == "ack" {
		out["delivery"] = externalArg(args, "delivery")
		out["message_id"] = externalArg(args, "message_id")
	}
	if a.Attachment != nil {
		out["attachment_id"] = a.Attachment.ID
		out["provider"] = a.Attachment.Provider
		out["native_session_id"] = a.Attachment.SessionID
	}
	externalJSON(w, out)
}

func currentAgentAttachment(ctx context.Context, claims *UserClaims, name, id string) (namedMessageAgent, *UserClaims, error) {
	if claims == nil || claims.AccessToken == nil {
		return namedMessageAgent{}, nil, fmt.Errorf("agent connection required")
	}
	live, err := agentMessageConnectionClaims(ctx, claims.AccessToken.ID)
	if err != nil || live.UserID != claims.UserID || !namedAgentOperationAllowed(live, false) {
		return namedMessageAgent{}, nil, fmt.Errorf("connection revoked")
	}
	a, err := namedAgentForClaims(ctx, live, name)
	if err != nil || a.Attachment == nil || a.Attachment.ID != id || a.Attachment.ConnectionID != live.AccessToken.ID {
		return namedMessageAgent{}, nil, fmt.Errorf("attachment changed")
	}
	return a, live, nil
}

// This is an outbound authenticated SSE stream, not a message-read polling
// loop. Durable writes wake it, and heartbeats recheck revocation when idle.
func (api *StreamingAPI) handleAgentEvents(w http.ResponseWriter, r *http.Request) {
	claims := GetUserFromContext(r.Context())
	name, id := r.URL.Query().Get("agent_name"), r.URL.Query().Get("attachment_id")
	a, _, err := currentAgentAttachment(r.Context(), claims, name, id)
	if err != nil {
		externalError(w, 403, "attachment_refused", "Exact attachment unavailable or access denied.")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		externalError(w, 500, "stream_unavailable", "Streaming is unavailable.")
		return
	}
	agentLiveEvents.Lock()
	if active, busy := agentLiveEvents.online[a.ID]; busy && active == id {
		agentLiveEvents.Unlock()
		externalError(w, 409, "agent_already_connected", "This agent already has a live listener.")
		return
	}
	agentLiveEvents.online[a.ID] = id
	agentLiveEvents.Unlock()
	defer func() {
		agentLiveEvents.Lock()
		if agentLiveEvents.online[a.ID] == id {
			delete(agentLiveEvents.online, a.ID)
		}
		agentLiveEvents.Unlock()
	}()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	write := func(frame any) error {
		raw, e := json.Marshal(frame)
		if e != nil {
			return e
		}
		_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(10 * time.Second))
		_, e = fmt.Fprintf(w, "data: %s\n\n", raw)
		flusher.Flush()
		return e
	}
	if write(map[string]any{"type": "connected", "attachment_id": id}) != nil {
		return
	}
	heartbeat := time.NewTicker(10 * time.Second)
	defer heartbeat.Stop()
	sent := map[string]bool{}
	for {
		changed := agentLiveChanged()
		current, live, e := currentAgentAttachment(r.Context(), claims, name, id)
		if e != nil {
			_ = write(map[string]any{"type": "closed"})
			return
		}
		agentMessageMu.Lock()
		store, e := readAgentMessageStore(r.Context())
		agentMessageMu.Unlock()
		if e != nil {
			return
		}
		type pending struct {
			conversation agentConversation
			message      agentMessage
		}
		var waiting []pending
		for _, c := range store.Conversations {
			for _, m := range c.Messages {
				if m.Delivery == "stored" && !sent[m.ID] && c.Endpoints[1-m.From].NamedAgentID == a.ID {
					waiting = append(waiting, pending{c, m})
				}
			}
		}
		slices.SortFunc(waiting, func(x, y pending) int {
			if x.message.AgentSequence < y.message.AgentSequence {
				return -1
			}
			if x.message.AgentSequence > y.message.AgentSequence {
				return 1
			}
			return 0
		})
		for _, p := range waiting {
			c, m := p.conversation, p.message
			if m.Delivery != "stored" || sent[m.ID] {
				continue
			}
			dest, source := c.Endpoints[1-m.From], c.Endpoints[m.From]
			if dest.NamedAgentID != a.ID || dest.UserID != claims.UserID || !slices.Contains(current.Attachment.AllowedSenders, source.NamedAgentID) {
				continue
			}
			if e = api.recheckAgentMessageAdmission(r.Context(), c, m); e != nil {
				continue
			}
			if e = api.externalAuthorizeMessagePeer(r.Context(), live, source, false); e != nil {
				continue
			}
			// Resolve the exact generation once more immediately before release.
			current, _, e = currentAgentAttachment(r.Context(), claims, name, id)
			if e != nil {
				_ = write(map[string]any{"type": "closed"})
				return
			}
			if !slices.Contains(current.Attachment.AllowedSenders, source.NamedAgentID) {
				continue
			}
			if e = recordAgentMessageOffer(r.Context(), a.ID, id, m.ID); e != nil {
				return
			}
			frame := map[string]any{"type": "message", "attachment_id": id, "message_id": m.ID, "inbox_id": c.ID, "sender": source.Label, "sender_id": source.NamedAgentID, "message": m.Text, "sequence": m.AgentSequence, "delivery": m.Delivery}
			if write(frame) != nil {
				return
			}
			sent[m.ID] = true
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

// Offering bytes is not an admission receipt. The receiving agent must
// explicitly acknowledge them; the lease prevents another session claiming it.
func recordAgentMessageOffer(ctx context.Context, agentID, attachmentID, messageID string) error {
	agentMessageMu.Lock()
	defer agentMessageMu.Unlock()
	s, err := readAgentMessageStore(ctx)
	if err != nil {
		return err
	}
	current := false
	for _, a := range s.Agents {
		if a.ID == agentID && a.Attachment != nil && a.Attachment.ID == attachmentID {
			current = true
			break
		}
	}
	if !current {
		return fmt.Errorf("attachment changed")
	}
	for ci := range s.Conversations {
		c := &s.Conversations[ci]
		for mi := range c.Messages {
			m := &c.Messages[mi]
			if m.ID == messageID && c.Endpoints[1-m.From].NamedAgentID == agentID && m.Delivery == "stored" {
				m.LiveAttachment = attachmentID
				return saveAgentMessageStore(ctx, s)
			}
		}
	}
	return fmt.Errorf("message changed")
}

// Recheck durable offers immediately before releasing a local cached body. This
// reads current authority without fabricating admitted/processed receipts.
func (api *StreamingAPI) validateAgentOffers(w http.ResponseWriter, r *http.Request, args map[string]any) {
	a, live, err := currentAgentAttachment(r.Context(), GetUserFromContext(r.Context()), externalArg(args, "agent_name"), externalArg(args, "attachment_id"))
	if err != nil {
		externalError(w, 403, "attachment_refused", "Exact attachment unavailable or access denied.")
		return
	}
	var ids []string
	raw, _ := json.Marshal(args["message_ids"])
	if json.Unmarshal(raw, &ids) != nil || len(ids) > 20 {
		externalError(w, 400, "invalid_arguments", "At most 20 message IDs required.")
		return
	}
	agentMessageMu.Lock()
	s, err := readAgentMessageStore(r.Context())
	agentMessageMu.Unlock()
	if err != nil {
		externalError(w, 500, "store_unavailable", "Cannot validate pending messages.")
		return
	}
	history, _ := args["history"].(bool)
	permitted := []string{}
	for _, id := range ids {
		for _, c := range s.Conversations {
			for _, m := range c.Messages {
				if m.ID != id || c.Endpoints[1-m.From].NamedAgentID != a.ID || !history && (m.LiveAttachment != a.Attachment.ID || m.Delivery != "stored") {
					continue
				}
				src := c.Endpoints[m.From]
				if !slices.Contains(a.Attachment.AllowedSenders, src.NamedAgentID) || api.recheckAgentMessageAdmission(r.Context(), c, m) != nil || api.externalAuthorizeMessagePeer(r.Context(), live, src, false) != nil {
					continue
				}
				permitted = append(permitted, id)
			}
		}
	}
	if _, _, err = currentAgentAttachment(r.Context(), live, a.Name, a.Attachment.ID); err != nil {
		externalError(w, 403, "attachment_refused", "Attachment changed.")
		return
	}
	externalJSON(w, map[string]any{"message_ids": permitted})
}
