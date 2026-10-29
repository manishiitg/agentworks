package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
)

type namedAgentAttachment struct {
	LastStartupDiagnosticID string    `json:"last_startup_diagnostic_id,omitempty"`
	Repository              string    `json:"repository,omitempty"`
	Branch                  string    `json:"branch,omitempty"`
	Activity                string    `json:"activity,omitempty"`
	ActivityAt              time.Time `json:"activity_at,omitempty"`
	DeliveryContract        int       `json:"delivery_contract,omitempty"`
	DeliveryRoute           string    `json:"delivery_route,omitempty"`
	ID                      string    `json:"attachment_id"`
	Provider                string    `json:"provider"`
	SessionID               string    `json:"native_session_id"`
	ConnectionID            string    `json:"connection_id"`
	ClientBuild             string    `json:"client_build,omitempty"` // the helper build last reported by this exact connection
}

// A guest-run product keeps the owner's storage namespace but its own actor.
func sameAccountAgentSender(source agentMessageEndpoint, userID string) bool {
	return source.UserID == userID && (source.Guest == "" || source.Guest == userID)
}

type agentLiveStream struct {
	attachmentID string
	cancel       context.CancelFunc
}

var agentLiveEvents = struct {
	sync.Mutex
	changed      chan struct{}
	online       map[string]*agentLiveStream
	lastRecorded map[string]time.Time
}{changed: make(chan struct{}), online: map[string]*agentLiveStream{}, lastRecorded: map[string]time.Time{}}

// Attachment is a person-configured CLI operation, not an agent MCP tool.
// The native bridge gets this credential from its explicit launch config.
func (api *StreamingAPI) handleAgentAttachment(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer "+cliOAuthAccessPrefix) {
		externalError(w, 403, "attachment_refused", "Configure attachment through your signed-in AgentWorks CLI, not an agent tool.")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	var args map[string]any
	if json.NewDecoder(r.Body).Decode(&args) != nil || !slices.Contains([]string{"attach", "detach", "validate", "connection", "label", "inbox", "inspect", "startup_failure"}, externalArg(args, "action")) {
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
	if externalArg(args, "action") == "inbox" {
		api.readAttachedAgentInbox(w, r, args)
		return
	}
	if externalArg(args, "action") == "validate" {
		api.validateAgentInboxMessages(w, r, args)
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

// Presence is projected by the server for every directory surface.
func namedAgentPresence(a namedMessageAgent) string {
	switch agentDeliveryMode(a) {
	case "live":
		return "Wakes now"
	case "next_step":
		return "At next step"
	case "offline":
		return "Offline; messages wait"
	case "unknown":
		return "Connected; delivery unknown"
	default:
		return "Checks inbox"
	}
}

func namedAgentOnline(a namedMessageAgent) bool {
	agentLiveEvents.Lock()
	defer agentLiveEvents.Unlock()
	stream := agentLiveEvents.online[a.ID]
	return a.Attachment != nil && a.Attachment.DeliveryContract == 2 && stream != nil && stream.attachmentID == a.Attachment.ID
}

// Discovery expires; the address, publication and stored messages do not.
// Hosted product chats are projected separately and do not use this directory.
func namedAgentDiscoverable(a namedMessageAgent, now time.Time) bool {
	if namedAgentOnline(a) {
		return true
	}
	last := a.CreatedAt
	if a.LastOnlineAt.After(last) {
		last = a.LastOnlineAt
	}
	return !last.IsZero() && !last.Before(now.Add(-12*time.Hour))
}

// Only an authenticated exact stream, or a successfully authorized mailbox
// read/send, records activity. This never creates a lease or a read receipt.
func recordNamedAgentActivity(ctx context.Context, agentID, attachmentID string, force bool) {
	if agentID == "" {
		return
	}
	now := time.Now().UTC()
	agentLiveEvents.Lock()
	recent := now.Sub(agentLiveEvents.lastRecorded[agentID]) < time.Minute
	agentLiveEvents.Unlock()
	if !force && recent {
		return
	}
	if !agentMessageMu.TryLock() {
		return // A later authenticated heartbeat retries; mail never waits for presence.
	}
	defer agentMessageMu.Unlock()
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	s, err := readAgentMessageStore(ctx)
	if err != nil {
		log.Printf("[AGENT-PRESENCE] check=read status=failed agent=%s class=%s", agentID, agentDeliveryErrorClass(err))
		return
	}
	for i := range s.Agents {
		a := &s.Agents[i]
		if a.ID != agentID {
			continue
		}
		if attachmentID != "" {
			if a.Attachment == nil || a.Attachment.ID != attachmentID {
				return
			}
		} else if a.EverAttached || a.Attachment != nil {
			return // Reading an offline CLI inbox does not invent a live stream.
		}
		if !force && now.Sub(a.LastOnlineAt) < time.Minute {
			return
		}
		a.LastOnlineAt = now
		if err := saveAgentMessageStore(ctx, s); err != nil {
			log.Printf("[AGENT-PRESENCE] check=save status=failed agent=%s class=%s", agentID, agentDeliveryErrorClass(err))
			return
		}
		agentLiveEvents.Lock()
		agentLiveEvents.lastRecorded[agentID] = now
		agentLiveEvents.Unlock()
		log.Printf("[AGENT-PRESENCE] status=recorded agent=%s last_online_at=%s", agentID, now.Format(time.RFC3339Nano))
		return
	}
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
	ackDelivery, previousAttachment := "", ""
	receiptUnchanged := false
	var startupDiagnostic *agentworksclient.StartupDiagnostic
	switch externalArg(args, "action") {
	case "inspect":
		// Current metadata for this account-owned name; no body or receipt.
		var attachment any
		if a.Attachment != nil {
			attachment = map[string]any{"attachment_id": a.Attachment.ID}
		}
		externalJSON(w, map[string]any{"agent_id": a.ID, "attachment": attachment})
		return
	case "attach":
		provider, session, purpose := externalArg(args, "provider"), externalArg(args, "native_session_id"), strings.TrimSpace(externalArg(args, "purpose"))
		if !slices.Contains([]string{"codex", "claude", "muse", "cursor", "agy", "pi"}, provider) || session == "" || len(session) > 128 || strings.ContainsAny(session, "/\\\r\n\x00") || utf8.RuneCountInString(purpose) > 512 || strings.ContainsAny(purpose, "\r\n") {
			externalError(w, 400, "invalid_arguments", "Attach requires a supported provider, its exact native_session_id and an optional purpose (at most 512 characters).")
			return
		}
		purpose = cleanAgentPurpose(purpose)

		// Automatic login recovery may renew its own lease, never reclaim one
		// that an explicit attach has already moved to another chat.
		if expected := externalArg(args, "attachment_id"); expected != "" && (a.Attachment == nil || a.Attachment.ID != expected) {
			externalError(w, 409, "agent_attachment_closed", "This chat was replaced by another attachment. Connect it again if you want to move the name back.")
			return
		}
		if purpose != "" {
			a.Purpose = purpose
		}
		a.Label = cleanAgentLabel(externalArg(args, "label"))
		a.EverAttached = true
		if a.Attachment != nil {
			previousAttachment = a.Attachment.ID
		}
		a.Attachment = &namedAgentAttachment{DeliveryContract: 2, ID: "attach-" + uuid.NewString(), Provider: provider, SessionID: session, ConnectionID: claims.AccessToken.ID, ClientBuild: cleanClientBuild(externalArg(args, "client_build"))}
	case "startup_failure":
		if a.Attachment == nil || a.Attachment.ID != externalArg(args, "attachment_id") || a.Attachment.ConnectionID != claims.AccessToken.ID {
			externalError(w, 409, "attachment_changed", "The exact attachment is no longer current.")
			return
		}
		raw, _ := json.Marshal(args["diagnostic"])
		var d agentworksclient.StartupDiagnostic
		decoder := json.NewDecoder(strings.NewReader(string(raw)))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&d) != nil || !d.Valid() {
			externalError(w, 400, "invalid_arguments", "Startup diagnostics accept only build, time and failure-category metadata.")
			return
		}
		receiptUnchanged = a.Attachment.LastStartupDiagnosticID == d.ID
		if !receiptUnchanged {
			a.Attachment.LastStartupDiagnosticID = d.ID
			startupDiagnostic = &d
		}
	case "label":
		if a.Attachment == nil || a.Attachment.ID != externalArg(args, "attachment_id") || a.Attachment.ConnectionID != claims.AccessToken.ID {
			externalError(w, 409, "attachment_changed", "The exact attachment is no longer current.")
			return
		}
		if route, supplied := args["delivery_route"]; supplied {
			value, ok := route.(string)
			providers := map[string]string{"codex_desktop": "codex", "codex_queue": "codex", "claude_session": "claude", "claude_channel": "claude", "muse_monitor": "muse", "pi_extension": "pi", "codex_hook": "codex", "claude_hook": "claude", "cursor_hook": "cursor", "muse_hook": "muse", "agy_hook": "agy"}
			if !ok || value != "" && value != "inbox" && providers[value] != a.Attachment.Provider {
				externalError(w, 400, "invalid_arguments", "Delivery route must match this chat's provider.")
				return
			}
			a.Attachment.DeliveryRoute = value
		}
		if build := cleanClientBuild(externalArg(args, "client_build")); build != "" {
			a.Attachment.ClientBuild = build
		}
		a.Attachment.Repository = cleanAgentLabel(externalArg(args, "repository"))
		a.Attachment.Branch = cleanAgentLabel(externalArg(args, "branch"))
		activity := externalArg(args, "activity")
		if activity != "" && activity != "busy" && activity != "idle" && activity != "unknown" {
			externalError(w, 400, "invalid_arguments", "Use a reported busy or idle state, or unknown.")
			return
		}
		var at time.Time
		if raw := externalArg(args, "activity_at"); raw != "" {
			var parseErr error
			at, parseErr = time.Parse(time.RFC3339Nano, raw)
			if parseErr != nil || at.After(time.Now().Add(time.Minute)) {
				externalError(w, 400, "invalid_arguments", "Activity time must be a valid observed time.")
				return
			}
		}
		a.Attachment.Activity, a.Attachment.ActivityAt = activity, at
		if agentBridgeOnlyDeployment() {
			a.Attachment.Repository, a.Attachment.Branch, a.Attachment.Activity = "", "", ""
			a.Attachment.ActivityAt = time.Time{}
		}
		a.Label = cleanAgentLabel(externalArg(args, "label"))
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
		if delivery != "delivered" || !slices.Contains([]string{"codex_queue", "codex_desktop", "claude_channel", "claude_session", "claude_hook", "codex_hook", "cursor_hook", "muse_hook", "agy_hook", "pi_hook", "pi_extension", "mailbox"}, transport) {
			externalError(w, 400, "invalid_arguments", "Use delivery=delivered when this chat has received the message.")
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
				// A mailbox receipt follows an explicit body read by this exact
				// attached agent. Recheck current authority instead of requiring an
				// earlier SSE offer, which may belong to its previous lease.
				if !serverAuthoredKind(m.Kind) {
					if api.recheckAgentMessageAdmission(r.Context(), *c, *m) != nil || api.externalAuthorizeMessagePeer(r.Context(), claims, c.Endpoints[m.From], false) != nil {
						externalError(w, 409, "receipt_refused", "Incoming message is no longer authorized.")
						return
					}
				}
				if !s.shareSenderAllowed(*c, m.From, claims.UserID, time.Now().UTC()) {
					externalError(w, 409, "receipt_refused", "The sender is no longer allowed to message this account.")
					return
				}
				providerForTransport := map[string]string{"claude_session": "claude", "claude_channel": "claude", "claude_hook": "claude", "codex_queue": "codex", "codex_desktop": "codex", "codex_hook": "codex", "cursor_hook": "cursor", "muse_hook": "muse", "agy_hook": "agy", "pi_hook": "pi", "pi_extension": "pi"}
				if providerForTransport[transport] != "" && a.Attachment.Provider != providerForTransport[transport] {
					externalError(w, 400, "invalid_arguments", "Receipt transport does not match the attached provider.")
					return
				}
				found = true
				ackDelivery = delivery
				receiptUnchanged = !m.DeliveredAt.IsZero()
				if !receiptUnchanged {
					m.Delivery = delivery
					stampReceipt(m, delivery, time.Now().UTC())
					m.LiveAttachment = a.Attachment.ID
					m.LiveTransport = transport
					m.LiveSession = a.Attachment.SessionID
				}
			}
		}
		if !found {
			externalError(w, 404, "not_found", "Incoming message unavailable or access denied.")
			return
		}
	}
	if !receiptUnchanged {
		if err = saveAgentMessageStore(r.Context(), s); err != nil {
			externalCommunicationError(w, 400, "attachment_refused", err)
			return
		}
	}
	if d := startupDiagnostic; d != nil {
		log.Printf("[AGENT-STARTUP] id=%s account=%s attachment=%s at=%s version=%s build=%s os=%s arch=%s cause=%s executable_sha256=%s", d.ID, a.UserID, a.Attachment.ID, d.At.Format(time.RFC3339Nano), d.Version, d.Build, d.OS, d.Arch, d.Cause, d.ExecutableSHA256)
	}
	out := map[string]any{"agent_id": a.ID, "agent_name": a.Name, "label": agentLabel(*a), "purpose": a.Purpose, "status": externalArg(args, "action") + "ed"}
	if externalArg(args, "action") == "attach" {
		log.Printf("[AGENT-ATTACH] agent=%s account=%s previous=%s attachment=%s provider=%s session=%s", a.ID, a.UserID, previousAttachment, a.Attachment.ID, a.Attachment.Provider, a.Attachment.SessionID)
	}
	if externalArg(args, "action") == "label" {
		log.Printf("[AGENT-LABEL] agent=%s attachment=%s route=%s status=updated", a.ID, a.Attachment.ID, a.Attachment.DeliveryRoute)
	}
	if externalArg(args, "action") == "ack" {
		out["delivery"] = ackDelivery
		out["message_id"] = externalArg(args, "message_id")
		log.Printf("[AGENT-MESSAGE-ACK] message=%s agent=%s attachment=%s delivery=%s transport=%s repeated=%t", externalArg(args, "message_id"), a.Name, a.Attachment.ID, ackDelivery, externalArg(args, "transport"), receiptUnchanged)
	}
	if a.Attachment != nil {
		out["attachment_id"] = a.Attachment.ID
		out["provider"] = a.Attachment.Provider
		out["client_build"] = a.Attachment.ClientBuild
		out["delivery_route"] = a.Attachment.DeliveryRoute
		out["native_session_id"] = a.Attachment.SessionID
	}
	externalJSON(w, out)
}

var errAgentLoginUnavailable = errors.New("connection revoked")

func currentAgentAttachment(ctx context.Context, claims *UserClaims, name, id string) (namedMessageAgent, *UserClaims, error) {
	if claims == nil || claims.AccessToken == nil {
		return namedMessageAgent{}, nil, fmt.Errorf("agent connection required")
	}
	live, err := agentMessageConnectionClaims(ctx, claims.AccessToken.ID)
	if err != nil || live.UserID != claims.UserID || !namedAgentOperationAllowed(live, false) {
		return namedMessageAgent{}, nil, errAgentLoginUnavailable
	}
	a, err := namedAgentForClaims(ctx, live, name)
	if err != nil || a.Attachment == nil || a.Attachment.DeliveryContract != 2 || a.Attachment.ID != id || a.Attachment.ConnectionID != live.AccessToken.ID {
		return namedMessageAgent{}, nil, fmt.Errorf("attachment changed")
	}
	return a, live, nil
}

// This is an outbound authenticated SSE stream, not a message-read polling
// loop. Durable writes wake it, and heartbeats recheck revocation when idle.
func (api *StreamingAPI) handleAgentEvents(w http.ResponseWriter, r *http.Request) {
	claims := GetUserFromContext(r.Context())
	name, id := r.URL.Query().Get("agent_name"), r.URL.Query().Get("attachment_id")
	if name == "" && id == "" {
		api.handleAgentWakeEvents(w, r)
		return
	}
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
	// An authorized reconnect replaces the old socket, keeping the exact lease.
	// A half-open listener must not block recovery until a proxy timeout.
	streamCtx, cancel := context.WithCancel(r.Context())
	defer cancel()
	r = r.WithContext(streamCtx)
	stream := &agentLiveStream{attachmentID: id, cancel: cancel}
	agentLiveEvents.Lock()
	if previous := agentLiveEvents.online[a.ID]; previous != nil && previous.cancel != nil {
		previous.cancel()
		log.Printf("[AGENT-STREAM] replaced agent=%s old=%s new=%s", a.ID, previous.attachmentID, id)
	}
	log.Printf("[AGENT-STREAM] connected agent=%s attachment=%s", a.ID, id)
	agentLiveEvents.online[a.ID] = stream
	agentLiveEvents.Unlock()
	recordNamedAgentActivity(r.Context(), a.ID, id, true)
	defer func() {
		log.Printf("[AGENT-STREAM] disconnected agent=%s attachment=%s", a.ID, id)
		agentLiveEvents.Lock()
		if agentLiveEvents.online[a.ID] == stream {
			delete(agentLiveEvents.online, a.ID)
		}
		agentLiveEvents.Unlock()
		recordNamedAgentActivity(r.Context(), a.ID, id, true)
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
	sentPings := map[string]bool{}
	lastHint := 0
	for {
		changed := agentLiveChanged()
		_, _, e := currentAgentAttachment(r.Context(), claims, name, id)
		if e != nil {
			kind := "closed"
			if errors.Is(e, errAgentLoginUnavailable) {
				kind = "login_changed"
			}
			_ = write(map[string]any{"type": kind})
			return
		}
		store, e := readAgentMessageSnapshot(r.Context())
		if e != nil {
			return
		}
		type personPing struct {
			inbox   string
			message agentMessage
		}
		pings := []personPing{}
		latest := 0
		now := time.Now().UTC()
		for _, conversation := range store.Conversations {
			for _, message := range conversation.Messages {
				dest := conversation.Endpoints[1-message.From]
				if dest.NamedAgentID != a.ID || dest.UserID != claims.UserID ||
					!message.InboxRemovedAt.IsZero() || message.Delivery != "stored" || agentMessagePendingExpired(message, now) {
					continue
				}
				if message.Kind == "ping" {
					if !sentPings[message.ID] {
						pings = append(pings, personPing{conversation.ID, message})
					}
					continue
				}
				latest = max(latest, message.AgentSequence)
			}
		}
		// The wake signal contains no peer body or sender details. The inbox
		// is the sole place that checks sender access and releases message text.
		if _, _, e = currentAgentAttachment(r.Context(), claims, name, id); e != nil {
			_ = write(map[string]any{"type": "closed"})
			return
		}
		for _, ping := range pings {
			message := ping.message
			if write(map[string]any{"type": "message", "kind": "ping", "attachment_id": id, "message_id": message.ID, "inbox_id": ping.inbox, "message": firstLineClip(message.Text, 160), "sequence": message.AgentSequence}) != nil {
				return
			}
			sentPings[message.ID] = true
		}
		if latest > lastHint {
			if write(map[string]any{"type": "inbox_changed", "attachment_id": id, "sequence": latest}) != nil {
				return
			}
			lastHint = latest
			log.Printf("[AGENT-STREAM] inbox_changed agent=%s attachment=%s sequence=%d", a.ID, id, latest)
		}
		select {
		case <-r.Context().Done():
			return
		case <-changed:
		case <-heartbeat.C:
			if write(map[string]any{"type": "heartbeat"}) != nil {
				return
			}
			recordNamedAgentActivity(r.Context(), a.ID, id, false)
		}
	}
}

// Validate explicit inbox reads and replies against the current attachment and
// sender authority. Pending delivery is owned by readAttachedAgentInbox.
func (api *StreamingAPI) validateAgentInboxMessages(w http.ResponseWriter, r *http.Request, args map[string]any) {
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
	s, err := readAgentMessageSnapshot(r.Context())
	if err != nil {
		externalError(w, 500, "store_unavailable", "Cannot validate pending messages.")
		return
	}
	permitted := []string{}
	inboxes := map[string]string{}
	reasons := map[string]string{} // message id -> why it was left out (logged; counted for the client)
	for _, id := range ids {
		for _, c := range s.Conversations {
			for _, m := range c.Messages {
				if m.ID != id {
					continue
				}
				switch {
				case c.Endpoints[1-m.From].NamedAgentID != a.ID || c.Endpoints[1-m.From].UserID != a.UserID:
					reasons[id] = "not_addressed_to_this_agent"
					continue
				case !m.InboxRemovedAt.IsZero():
					reasons[id] = "removed_from_inbox"
					continue
				}
				src := c.Endpoints[m.From]
				if serverAuthoredKind(m.Kind) {
					permitted = append(permitted, id)
					inboxes[id] = c.ID
					continue
				}
				if !s.shareSenderAllowed(c, m.From, live.UserID, time.Now().UTC()) {
					reasons[id] = "share_check_failed"
					continue
				}
				if api.externalAuthorizeMessagePeer(r.Context(), live, src, false) != nil {
					reasons[id] = "sender_authorization_failed"
					continue
				}
				// Reply context belongs to the current recipient and sharing,
				// not the sending credential on an older submission. This map
				// contains no body; a new send rechecks current inbox authority.
				inboxes[id] = c.ID
				if api.recheckAgentMessageAdmission(r.Context(), c, m) != nil {
					reasons[id] = "admission_recheck_failed"
					continue
				}
				permitted = append(permitted, id)
			}
		}
	}
	held := map[string]int{}
	for _, id := range ids {
		reason, left := reasons[id]
		if !left && slices.Contains(permitted, id) {
			continue
		}
		if !left {
			reason = "not_found"
		}
		held[reason]++
		if validateLogDue(id, reason, time.Now()) {
			log.Printf("[AGENT-MESSAGE-VALIDATE] left out message=%s agent=%s attachment=%s reason=%s request_id=%q", id, a.Name, a.Attachment.ID, reason, r.Header.Get("X-Request-ID"))
		}
	}
	if _, _, err = currentAgentAttachment(r.Context(), live, a.Name, a.Attachment.ID); err != nil {
		externalError(w, 403, "attachment_refused", "Attachment changed.")
		return
	}
	externalJSON(w, map[string]any{"message_ids": permitted, "inbox_ids": inboxes, "held_reasons": held})
}

// validateLogged remembers when each (message, reason) was last written: a held message is checked every few seconds, so a
// line is written the first time and then at most every ten minutes.
var validateLogged = struct {
	sync.Mutex
	at map[string]time.Time
}{at: map[string]time.Time{}}

func validateLogDue(id, reason string, now time.Time) bool {
	validateLogged.Lock()
	defer validateLogged.Unlock()
	if len(validateLogged.at) > 5000 {
		for k, t := range validateLogged.at {
			if now.Sub(t) > 10*time.Minute {
				delete(validateLogged.at, k)
			}
		}
	}
	key := id + "|" + reason
	if t, ok := validateLogged.at[key]; ok && now.Sub(t) < 10*time.Minute {
		return false
	}
	validateLogged.at[key] = now
	return true
}
