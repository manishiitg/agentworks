package server

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// Shared callers message the existing agent. They never submit a human turn
// under its owner identity, answer its private confirmations or acquire controls.
func (api *StreamingAPI) externalSharedCrewConversation(w http.ResponseWriter, r *http.Request, action string, args map[string]any, project crewProjectBinding, manifest productProjectManifest) bool {
	claims := GetUserFromContext(r.Context())
	chat, ok := sharedCrewAgentChat(r.Context(), claims.UserID, project.Binding.WorkspacePath, externalArg(args, "chat_id"))
	if !ok || chat.OwnerID == claims.UserID {
		return false
	}
	if action != "ask" && action != "read_chat" {
		externalError(w, 403, "tab_owner", "Send this agent a message; its owner controls its settings and confirmations.")
		return true
	}
	if token := claims.AccessToken; token != nil {
		scope := "crews:read"
		if action == "ask" {
			scope = "crews:run"
		}
		if !token.Allows(scope) {
			externalError(w, 403, "insufficient_scope", "This connection cannot use this agent.")
			return true
		}
	}
	after := externalInt(args, "after", 0)
	out := map[string]any{"workspace_id": manifest.Product + ":" + manifest.ID, "chat_id": chat.Key, "name": chat.Name, "purpose": chat.Purpose, "can_write": false, "shared": true}
	if action == "ask" {
		target := triggerTarget{Kind: triggerCallerCrew, Path: project.Binding.WorkspacePath, Label: chat.Name, CrewID: manifest.ID, CrewProfile: manifest.Product, CrewOwner: chat.OwnerID, Chat: chat}
		caller, err := api.externalMessageCaller(r.Context(), claims, args)
		if err != nil {
			externalCommunicationError(w, 400, "message_refused", err)
			return true
		}
		if err := api.externalAuthorizeMessagePeer(r.Context(), claims, targetMessageEndpoint(claims.UserID, target), true); err != nil {
			externalCommunicationError(w, 403, "access_denied", err)
			return true
		}
		receipt, err := api.sendAgentMessage(context.WithoutCancel(r.Context()), claims.UserID, caller, target, externalArg(args, "message"), externalArg(args, "inbox_id"), externalArg(args, "submission_id"))
		if err != nil {
			externalCommunicationError(w, 400, "message_refused", err)
			return true
		}
		for key, value := range receipt {
			out[key] = value
		}
		out["status"] = "accepted"
		logSharedCrewAgent(claims.UserID, chat.Key, "message")
		externalJSON(w, out)
		return true
	}
	deadline := time.Now().Add(externalCrewWait(args))
	for {
		current, allowed := sharedCrewAgentChat(r.Context(), claims.UserID, project.Binding.WorkspacePath, chat.Key)
		if !allowed || current.SessionID != chat.SessionID || current.OwnerID != chat.OwnerID {
			externalError(w, 404, "agent_unavailable", "This agent is no longer shared with you.")
			return true
		}
		items, last, err := codeChatTranscript(internalBotRequestContext(r.Context(), chat.OwnerID), chat.OwnerID, chat.SessionID, project.Binding.WorkspacePath, after, 0)
		if err != nil {
			externalFailure(w, fmt.Errorf("could not read this agent's conversation"))
			return true
		}
		since := crewSharedAgentSince(project.Binding.WorkspacePath, chat.Key)
		before := externalInt(args, "before_seq", 0)
		shared := items[:0]
		for _, item := range items {
			at, err := time.Parse(time.RFC3339Nano, item.At)
			if since.IsZero() || err != nil || at.Before(since) || (before > 0 && item.Seq >= before) {
				continue
			}
			if item.Role == "you" {
				item.Role = "user"
				if item.Sender == "" {
					item.Sender = crewOwnerDisplayName(chat.OwnerID)
				}
			}
			shared = append(shared, item)
		}
		items = shared
		out["has_earlier"] = len(items) > 100
		if len(items) > 100 {
			items = items[len(items)-100:]
		}
		if len(items) > 0 {
			out["first_seq"] = items[0].Seq
		}
		working := api.externalWorkspaceTurnExecution(chat.SessionID, "")
		out["working"] = working != nil && (working.Status == trackedExecutionStatusRunning)
		if len(items) > 0 || !time.Now().Before(deadline) || r.Context().Err() != nil {
			out["messages"], out["next_after"] = items, max(after, last)
			logSharedCrewAgent(claims.UserID, chat.Key, "read")
			externalJSON(w, out)
			return true
		}
		select {
		case <-r.Context().Done():
			return true
		case <-time.After(500 * time.Millisecond):
		}
	}
}
