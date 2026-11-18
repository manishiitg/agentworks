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
	// Exact routing identity wins over labels. Duplicate projections of the
	// same identity are one agent, not an ambiguous name.
	for _, fields := range [][]string{{"id", "target"}, {"agent_name", "name", "label"}} {
		seen := map[string]bool{}
		for _, row := range rows {
			values := []string{}
			for _, field := range fields {
				values = append(values, stringField(row[field]))
			}
			if fields[0] == "id" {
				values = append(values, agentDirectoryAddresses(row)...)
			}
			for _, value := range values {
				if value != "" && strings.EqualFold(value, target) {
					id, _ := row["id"].(string)
					if id == "" {
						id, _ = row["target"].(string)
					}
					if id == "" || !seen[id] {
						matches = append(matches, row)
						seen[id] = true
					}
					break
				}
			}
		}
		if len(matches) > 0 {
			break
		}
	}
	if len(matches) != 1 {
		return nil, fmt.Errorf("Agent unavailable or name is ambiguous; use its exact address from list_agents")
	}
	out := map[string]interface{}{}
	for _, field := range []string{"id", "target", "agent_id", "agent_name", "chat_id", "name", "label", "cli", "engine", "model_id", "reasoning_effort", "client_build", "update_needed", "activity", "activity_source", "activity_reason", "online", "presence", "delivery_mode", "addresses", "last_active_at", "last_active_label", "crew_name", "yours"} {
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
	if connectionID, peer, ok := splitDesktopAgentNetworkAddress(address); IsLocalMode() && ok && !publicOnly && row["activity_source"] != "local_chat" {
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
			if team, err := readCrewTeam(admitted.Reach.Team); err == nil {
				if post, ok := crewTeamActivityPostsFor(team, claims.UserID).Doing[admitted.Reach.ID]; ok {
					row["current_task"] = crewTeamMessageLine(post.Line)
				}
			}
			if admitted.Reach.ChatShare == crewChatShareLive && admitted.Info.OwnerStatus == nil && admitted.Record != nil {
				admitted.Info.OwnerStatus = api.ownerStatusForRecord(ctx, admitted.Owner, *admitted.Record)
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

type agentOwnerStatusSnapshot struct {
	Previews map[string]crewTeamMessagePreview
	Tasks    map[string]string
}

// Read posts and message previews once per Crew batch, not once per agent.
func agentOwnerStatusSources(ctx context.Context, owner, project string) agentOwnerStatusSnapshot {
	source := agentOwnerStatusSnapshot{Previews: crewTeamMessagePreviews(ctx, owner, project), Tasks: map[string]string{}}
	if team, err := readCrewTeam(project); err == nil {
		for _, post := range crewTeamActivityPostsFor(team, owner).Doing {
			if post.OwnerID == owner {
				source.Tasks[post.ChatKey] = crewTeamMessageLine(post.Line)
			}
		}
	}
	return source
}

func (api *StreamingAPI) ownerStatusForRecord(ctx context.Context, owner string, record ProductConversationRecord, cached ...agentOwnerStatusSnapshot) *agentOwnerStatus {
	status := &agentOwnerStatus{}
	if session, ok := api.getActiveSession(record.SessionID); ok && session != nil && session.UserID == owner {
		status.CurrentTask = crewTeamMessageLine(session.Title)
		status.CurrentStep = crewTeamMessageLine(session.PhaseName)
	}
	var source agentOwnerStatusSnapshot
	if len(cached) > 0 {
		source = cached[0]
	} else {
		source = agentOwnerStatusSources(ctx, owner, record.ResourceID)
	}
	if task := source.Tasks[record.ConversationKey]; task != "" {
		status.CurrentTask = task
	}
	previews := source.Previews
	if preview := previews[record.ConversationKey]; preview.LastMessage != "" {
		status.LastMessages = []string{preview.LastMessage}
	}
	return status
}
