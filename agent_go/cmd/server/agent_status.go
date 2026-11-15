package server

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Read-only selection over the caller's already-authorized directory. Names
// are usable only when unique; this never resolves a private target by probing.
func agentStatusRow(directory map[string]interface{}, target string) (map[string]interface{}, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return nil, fmt.Errorf("Choose an agent name or address from list_agents")
	}
	rows, _ := directory["agents"].([]map[string]interface{})
	var matches []map[string]interface{}
	for _, row := range rows {
		for _, field := range []string{"id", "target", "agent_name", "name", "label"} {
			value, _ := row[field].(string)
			if value != "" && strings.EqualFold(value, target) {
				matches = append(matches, row)
				break
			}
		}
	}
	if len(matches) != 1 {
		return nil, fmt.Errorf("Agent unavailable or name is ambiguous; use its exact address from list_agents")
	}
	out := map[string]interface{}{}
	for _, field := range []string{"id", "target", "agent_id", "agent_name", "chat_id", "name", "label", "cli", "engine", "model_id", "reasoning_effort", "activity", "last_active_at", "last_active_label", "group_names", "crew_name", "yours"} {
		if value, ok := matches[0][field]; ok {
			out[field] = value
		}
	}
	return out, nil
}

func (api *StreamingAPI) readAgentStatus(ctx context.Context, claims *UserClaims, target string, publicOnly bool) (map[string]interface{}, error) {
	directory, err := listAgentDirectory(ctx, claims, api)
	if err != nil {
		return nil, err
	}
	if publicOnly {
		publicAgentDirectoryOnly(directory)
	}
	row, err := agentStatusRow(directory, target)
	if err != nil {
		return nil, err
	}
	// A network status is a bounded authenticated GET to that directory's
	// server, never an agent message or a call through its input lane.
	address, _ := row["id"].(string)
	if connectionID, peer, ok := splitDesktopAgentNetworkAddress(address); IsLocalMode() && ok && !publicOnly {
		connection, err := desktopConnectionFor(claims.UserID, connectionID)
		if err != nil {
			return nil, err
		}
		read, done := context.WithTimeout(ctx, 8*time.Second)
		defer done()
		var remote map[string]interface{}
		if err := crewTeamRemoteJSON(read, claims.UserID, connection, http.MethodGet, "/api/agent-directory?agent_status="+url.QueryEscape(peer), nil, &remote); err != nil {
			return nil, err
		}
		return remote, nil
	}
	// Private previews are not part of public directory metadata. Only an
	// unrestricted owner status request may read this registration snapshot.
	yours, _ := row["yours"].(bool)
	if yours && !publicOnly {
		store, readErr := readAgentMessageSnapshot(ctx)
		if readErr == nil {
			for _, agent := range store.Agents {
				if agent.UserID == claims.UserID && agent.ID == row["agent_id"] && agent.Attachment != nil && agent.Attachment.RuntimeInfo != nil {
					if status := agent.Attachment.RuntimeInfo.OwnerStatus; status != nil {
						if status.CurrentStep != "" {
							row["current_step"] = status.CurrentStep
						}
						if status.CurrentTask != "" {
							row["current_task"] = status.CurrentTask
						}
						if len(status.LastMessages) > 0 {
							row["last_messages"] = status.LastMessages
						}
					}
				}
			}
		}
	}
	if yours && !publicOnly && row["last_messages"] == nil {
		if doc, err := defaultProductConversationRegistryStore().loadDocument(ctx, productConversationRegistryPath(claims.UserID)); err == nil {
			for _, record := range doc.Entries {
				if key, _ := row["chat_id"].(string); key != "" && key == record.ConversationKey || row["agent_name"] == desktopAppAgentName(desktopMachineID(), record.ConversationID) {
					status := api.ownerStatusForRecord(ctx, claims.UserID, record)
					if status.CurrentTask != "" {
						row["current_task"] = status.CurrentTask
					}
					if status.CurrentStep != "" {
						row["current_step"] = status.CurrentStep
					}
					if len(status.LastMessages) > 0 {
						row["last_messages"] = status.LastMessages
					}
					break
				}
			}
		}
	}
	if !yours && !publicOnly {
		address, _ := row["id"].(string)
		if admitted, ok := api.reachableAgentRuntimeInfo(ctx, claims.UserID)[address]; ok {
			if admitted.Doing != "" {
				row["current_task"] = admitted.Doing
			}
			if admitted.Reach.ChatShare == crewChatShareLive && admitted.Info.OwnerStatus != nil {
				if lines := admitted.Info.OwnerStatus.LastMessages; len(lines) > 0 {
					row["last_messages"] = lines
				}
			}
		}
		if store, err := readAgentMessageSnapshot(ctx); err == nil {
			var latest time.Time
			for _, conversation := range store.Conversations {
				for side, endpoint := range conversation.Endpoints {
					same := endpoint.NamedAgentID != "" && endpoint.NamedAgentID == row["agent_id"]
					if endpoint.TeamAgentID != "" {
						same = crewTeamAgentAddress(endpoint.ID, endpoint.TeamAgentID) == row["id"]
					}
					if !same || conversation.Endpoints[1-side].UserID != claims.UserID {
						continue
					}
					if recheckAgentConversationContact(ctx, conversation) != nil {
						continue
					}
					for _, message := range conversation.Messages {
						if message.From == side && message.InboxRemovedAt.IsZero() && message.Delivery != "suppressed" && message.SentAt.After(latest) {
							latest = message.SentAt
							row["replies"] = []string{crewTeamMessageLine(message.Text)}
						}
					}
				}
			}
		}
	}
	log.Printf("[AGENT_STATUS] caller=%s agent=%v action=read owner_text=%t", claims.UserID, row["agent_id"], yours && !publicOnly)
	return map[string]interface{}{"agent": row, "notice": "Read existing status only; no message was sent and no agent was started."}, nil
}

type agentOwnerStatus struct {
	CurrentStep  string   `json:"current_step,omitempty"`
	CurrentTask  string   `json:"current_task,omitempty"`
	LastMessages []string `json:"last_messages,omitempty"`
}

func (api *StreamingAPI) ownerStatusForRecord(ctx context.Context, owner string, record ProductConversationRecord, cached ...map[string]crewTeamMessagePreview) *agentOwnerStatus {
	status := &agentOwnerStatus{}
	if session, ok := api.getActiveSession(record.SessionID); ok && session != nil && session.UserID == owner {
		status.CurrentTask = crewTeamMessageLine(session.Title)
		status.CurrentStep = crewTeamMessageLine(session.PhaseName)
	}
	if team, err := readCrewTeam(record.ResourceID); err == nil {
		posts := crewTeamActivityPostsFor(team, owner)
		for _, post := range posts.Doing {
			if post.OwnerID == owner && post.ChatKey == record.ConversationKey {
				status.CurrentTask = crewTeamMessageLine(post.Line)
				break
			}
		}
	}
	previews := map[string]crewTeamMessagePreview{}
	if len(cached) > 0 {
		previews = cached[0]
	} else {
		previews = crewTeamMessagePreviews(ctx, owner, record.ResourceID)
	}
	if preview := previews[record.ConversationKey]; preview.LastMessage != "" {
		status.LastMessages = []string{preview.LastMessage}
	}
	return status
}
