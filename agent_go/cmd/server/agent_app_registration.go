package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
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
	case "register":
		a, err := registerAppMessageAgent(r.Context(), claims, args)
		if err != nil {
			externalCommunicationError(w, 409, "registration_refused", err)
			return
		}
		externalJSON(w, map[string]any{"agent_id": a.ID, "agent_name": a.Name, "attachment_id": a.Attachment.ID, "delivery_route": appAgentDeliveryRoute})
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
		externalError(w, 400, "invalid_arguments", "Choose register, inbox, validate or ack.")
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
	if a.Attachment == nil || a.Attachment.ConnectionID != connection {
		a.Attachment = &namedAgentAttachment{ID: "attach-" + uuid.NewString(), DeliveryContract: 2, DeliveryRoute: appAgentDeliveryRoute, Provider: "agentworks", DeviceID: device, SessionID: session, ConnectionID: connection}
	}
	a.Label, a.Purpose = cleanAgentLabel(externalArg(args, "label")), cleanAgentPurpose(purpose)
	a.EverAttached = true
	if err := saveAgentMessageStore(ctx, store); err != nil {
		return empty, err
	}
	return *a, nil
}
