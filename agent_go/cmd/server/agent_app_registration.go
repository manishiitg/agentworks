package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

const appAgentDeliveryRoute = "app_chat"

// An app uses its existing website login, not a Crew pair or a CLI child grant.
// Only the non-secret session identifier is kept with the attachment.
func appAgentLoginID(claims *UserClaims) (string, error) {
	if !websiteDeviceClaims(claims) || !namedAgentOperationAllowed(claims, true) ||
		claims.ID == "" || claims.ExpiresAt == nil || !claims.ExpiresAt.After(time.Now()) || sessionRevoked(claims.ID) {
		return "", errAgentLoginUnavailable
	}
	return "website:" + claims.ID, nil
}

// The app and CLI share names, inboxes, receipts and the live notification
// stream. This endpoint registers only a message recipient: it grants no remote
// chat control, file access, Crew pairing or Team membership (PLAT-746-890).
func (api *StreamingAPI) handleAppAgentRegistration(w http.ResponseWriter, r *http.Request) {
	claims := GetUserFromContext(r.Context())
	if _, err := appAgentLoginID(claims); err != nil {
		externalError(w, 403, "registration_refused", "Use your signed-in app account.")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	var args map[string]any
	if json.NewDecoder(r.Body).Decode(&args) != nil {
		externalError(w, 400, "invalid_arguments", "Agent registration is invalid.")
		return
	}
	switch externalArg(args, "action") {
	case "registrations", "remove":
		// Reconciliation of this device's registrations uses the same store,
		// not a second directory. No chat history or filesystem path is sent.
		device := externalArg(args, "device_id")
		if device == "" {
			externalError(w, 400, "invalid_arguments", "Give this app's device identity.")
			return
		}
		agentMessageMu.Lock()
		defer agentMessageMu.Unlock()
		store, err := readAgentMessageStore(r.Context())
		if err != nil {
			externalCommunicationError(w, 503, "registration_unavailable", err)
			return
		}
		rows := []map[string]string{}
		remove := externalArg(args, "action") == "remove"
		found := false
		for i, a := range store.Agents {
			if a.UserID != claims.UserID || a.Attachment == nil || a.Attachment.DeliveryRoute != appAgentDeliveryRoute || a.Attachment.DeviceID != device {
				continue
			}
			if remove {
				if a.ID != externalArg(args, "agent_id") || a.Attachment.SessionID != externalArg(args, "conversation_id") {
					continue
				}
				store.Agents = slices.Delete(store.Agents, i, i+1)
				store.revokeServerShares(a.ID, claims.UserID, time.Now().UTC())
				found = true
				break
			}
			rows = append(rows, map[string]string{"agent_id": a.ID, "agent_name": a.Name, "conversation_id": a.Attachment.SessionID})
		}
		if remove {
			if !found {
				externalError(w, 404, "registration_unavailable", "The exact app chat is no longer registered.")
				return
			}
			if err := saveAgentMessageStore(r.Context(), store); err != nil {
				externalCommunicationError(w, 503, "registration_unavailable", err)
				return
			}
		}
		externalJSON(w, map[string]any{"agents": rows, "removed": found})
	case "register":
		a, err := registerAppMessageAgent(r.Context(), claims, args)
		if err != nil {
			externalCommunicationError(w, 409, "registration_refused", err)
			return
		}
		externalJSON(w, map[string]any{"agent_id": a.ID, "agent_name": a.Name, "attachment_id": a.Attachment.ID, "delivery_route": appAgentDeliveryRoute})
	case "send", "read":
		if _, _, err := currentAgentAttachment(r.Context(), claims, externalArg(args, "agent_name"), externalArg(args, "attachment_id")); err != nil {
			externalError(w, 403, "attachment_refused", "This app chat is no longer connected.")
			return
		}
		// This route is message-only: never reinterpret a target as a Crew,
		// workflow, file or remote-control operation.
		target, inbox := externalArg(args, "target"), externalArg(args, "inbox_id")
		if target != "" && !strings.HasPrefix(target, "agent:") || inbox != "" && !strings.HasPrefix(inbox, "inbox-") || externalArg(args, "crew_id") != "" || externalArg(args, "workflow_id") != "" || externalArg(args, "chat_id") != "" {
			externalError(w, 400, "invalid_arguments", "Use a named agent or its reply inbox.")
			return
		}
		if target != "" {
			resolved, err := api.externalMessageTargetForOperation(r.Context(), claims, args, externalArg(args, "action") == "send")
			publicOnly, _ := args["public_only"].(bool)
			if err != nil || !appAgentPeerAllowed(r.Context(), claims.UserID, targetMessageEndpoint(claims.UserID, resolved)) || (publicOnly && !appAgentPublished(r.Context(), resolved.NamedAgentID)) {
				externalError(w, 404, "not_found", "Named agent unavailable or access denied.")
				return
			}
		}
		if inbox != "" {
			caller, err := api.externalMessageCaller(r.Context(), claims, args)
			if err != nil {
				externalError(w, 403, "inbox_refused", "This app chat cannot use that inbox.")
				return
			}
			peer, err := api.externalMessagePeerForCaller(r.Context(), claims, caller, inbox, externalArg(args, "action") == "send")
			if err != nil || !appAgentPeerAllowed(r.Context(), claims.UserID, peer) {
				externalError(w, 403, "inbox_refused", "Use this named agent's reply inbox.")
				return
			}
		}
		api.externalAgentMessages(w, r, args)
	case "inbox":
		api.readAttachedAgentInbox(w, r, args)
	case "validate":
		api.validateAgentInboxMessages(w, r, args)
	case "ack":
		// The existing receipt path verifies the exact attachment and rechecks both
		// participants before marking the message delivered.
		args["transport"] = appAgentDeliveryRoute
		api.externalAgentAttachment(w, r, args)
	default:
		externalError(w, 400, "invalid_arguments", "Choose register, send, read, inbox, validate or ack.")
	}
}

func registerAppMessageAgent(ctx context.Context, claims *UserClaims, args map[string]any) (namedMessageAgent, error) {
	empty := namedMessageAgent{}
	connection, err := appAgentLoginID(claims)
	if err != nil {
		return empty, err
	}
	name, err := normalizeMessageAgentName(externalArg(args, "agent_name"))
	if err != nil {
		return empty, err
	}
	device, session := externalArg(args, "device_id"), externalArg(args, "conversation_id")
	purpose := strings.TrimSpace(externalArg(args, "purpose"))
	validID := func(s string) bool { return s != "" && len(s) <= 128 && !strings.ContainsAny(s, "/\\\r\n\x00") }
	if !validID(device) || !validID(session) || utf8.RuneCountInString(purpose) > 512 || strings.ContainsAny(purpose, "\r\n") {
		return empty, fmt.Errorf("register an exact device and conversation with a single-line purpose of at most 512 characters")
	}
	agentMessageMu.Lock()
	defer agentMessageMu.Unlock()
	store, err := readAgentMessageStore(ctx)
	if err != nil {
		return empty, err
	}
	index := -1
	for i, a := range store.Agents {
		if a.UserID == claims.UserID && a.Name == name {
			index = i
			break
		}
	}
	if index < 0 {
		store.Agents = append(store.Agents, namedMessageAgent{ID: "agent-" + uuid.NewString(), UserID: claims.UserID, Name: name, CreatedAt: time.Now().UTC()})
		index = len(store.Agents) - 1
	} else {
		old := store.Agents[index].Attachment
		// Reconnecting the same app chat may renew a login, but must never claim a
		// CLI name or a name currently belonging to another app chat/device.
		if old == nil || old.DeliveryRoute != appAgentDeliveryRoute || old.DeviceID != device || old.SessionID != session {
			return empty, fmt.Errorf("this agent name already belongs to another chat; choose a different name")
		}
	}
	a := &store.Agents[index]
	changed := a.Attachment == nil || a.Attachment.ConnectionID != connection || a.Label != cleanAgentLabel(externalArg(args, "label")) || a.Purpose != cleanAgentPurpose(purpose)
	if a.Attachment == nil || a.Attachment.ConnectionID != connection {
		a.Attachment = &namedAgentAttachment{ID: "attach-" + uuid.NewString(), DeliveryContract: 2, DeliveryRoute: appAgentDeliveryRoute, Provider: "agentworks", DeviceID: device, SessionID: session, ConnectionID: connection}
	}
	a.Label, a.Purpose = cleanAgentLabel(externalArg(args, "label")), cleanAgentPurpose(purpose)
	a.EverAttached = true
	if !changed {
		return *a, nil
	}
	if err := saveAgentMessageStore(ctx, store); err != nil {
		return empty, err
	}
	return *a, nil
}

// Own named agents need no publication. Other accounts use the existing public
// AgentBridge grant, not a private Crew/Team or old person-specific share.
func appAgentPeerAllowed(ctx context.Context, user string, peer agentMessageEndpoint) bool {
	if peer.NamedAgentID == "" {
		return false
	}
	if peer.UserID == user {
		return true
	}
	store, err := readAgentMessageSnapshot(ctx)
	if err != nil {
		return false
	}
	for _, share := range store.Shares {
		if share.ID == peer.ShareID && share.serverScope() {
			_, err := store.authorizeShare(share.ID, peer.ShareVersion, time.Now().UTC())
			return err == nil
		}
	}
	return false
}

func appAgentPublished(ctx context.Context, agentID string) bool {
	store, err := readAgentMessageSnapshot(ctx)
	if err != nil || agentID == "" {
		return false
	}
	for _, share := range store.serverSharesOf(agentID, time.Now().UTC()) {
		if _, err := store.authorizeShare(share.ID, share.RevocationVersion, time.Now().UTC()); err == nil {
			return true
		}
	}
	return false
}
