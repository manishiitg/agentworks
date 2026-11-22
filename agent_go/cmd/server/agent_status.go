package server

import (
	"context"
	"errors"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Read-only selection over the caller's already-authorized directory. Names
// are usable only when unique; this never resolves a private target by probing.
func agentStatusRow(directory map[string]interface{}, target string) (map[string]interface{}, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return nil, agentStatusLookupError{"Choose an agent name or address from list_agents"}
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
		return nil, agentStatusLookupError{"Agent unavailable or name is ambiguous; use its exact address from list_agents"}
	}
	out := map[string]interface{}{}
	for _, field := range []string{"id", "target", "agent_id", "agent_name", "chat_id", "name", "label", "cli", "engine", "model_id", "reasoning_effort", "client_build", "update_needed", "activity", "activity_source", "activity_reason", "online", "presence", "delivery_mode", "addresses", "last_active_at", "last_active_label", "crew_name", "yours", "purpose", "examples"} {
		if value, ok := matches[0][field]; ok {
			out[field] = value
		}
	}
	return out, nil
}

// agentStatusLookupError is a plain answer about the target itself (no such agent, an ambiguous name);
// agentStatusUnavailable is a plain line for a read that may work again. Raw errors stay in the log.
type agentStatusLookupError struct{ text string }

func (e agentStatusLookupError) Error() string { return e.text }

type agentStatusUnavailable struct{ text string }

func (e agentStatusUnavailable) Error() string { return e.text }

// agentStatusHTTPError answers a failed status read: 404 with the plain lookup answer, otherwise a plain line with
// what to do next (the raw error stays in the log).
func agentStatusHTTPError(w http.ResponseWriter, err error) {
	var lookup agentStatusLookupError
	if errors.As(err, &lookup) {
		externalError(w, http.StatusNotFound, "agent_unavailable", lookup.text)
		return
	}
	text := "Could not read this agent's status. Try again in a moment."
	var unavailable agentStatusUnavailable
	if errors.As(err, &unavailable) {
		text = unavailable.text
	} else {
		log.Printf("[AGENT_STATUS] action=read_failed err=%v", err)
	}
	externalError(w, http.StatusServiceUnavailable, "status_unavailable", text)
}

// qualifyNetworkAgentStatus rewrites a status read from another network so its addresses are this side's: the
// remote's own agent:... addresses are not reachable from here without the network prefix.
func qualifyNetworkAgentStatus(remote map[string]interface{}, connectionID, address string) {
	agent, _ := remote["agent"].(map[string]interface{})
	if agent == nil {
		return
	}
	agent["id"], agent["target"] = address, address
	if list, ok := agent["addresses"].([]interface{}); ok {
		for i, item := range list {
			if value, _ := item.(string); strings.HasPrefix(value, "agent:") {
				list[i] = "network:" + connectionID + ":" + value
			}
		}
	}
	qualifyDesktopAgentReplyAddresses(agent, connectionID)
}

func (api *StreamingAPI) readAgentStatus(ctx context.Context, claims *UserClaims, target string, publicOnly bool, recent int) (map[string]interface{}, error) {
	directory, err := listAgentDirectory(ctx, claims, api)
	if err != nil {
		log.Printf("[AGENT_STATUS] caller=%s action=directory_failed err=%v", claims.UserID, err)
		return nil, agentStatusUnavailable{"Could not load your agents. Try again."}
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
			log.Printf("[AGENT_STATUS] caller=%s action=network_failed connection=%s err=%v", claims.UserID, connectionID, err)
			return nil, agentStatusUnavailable{"That agent's network sign-in is unavailable. Sign in to it again, then try again."}
		}
		read, done := context.WithTimeout(ctx, 8*time.Second)
		defer done()
		var remote map[string]interface{}
		if err := crewTeamRemoteJSON(read, claims.UserID, connection, http.MethodGet, "/api/agent-directory?agent_status="+url.QueryEscape(peer)+"&limit="+strconv.Itoa(agentRecentLimit(recent)), nil, &remote); err != nil {
			var refused *crewRemoteHTTPError
			if errors.As(err, &refused) && refused.Status == http.StatusNotFound && refused.Code == "agent_unavailable" {
				return nil, agentStatusLookupError{refused.Message}
			}
			log.Printf("[AGENT_STATUS] caller=%s action=network_failed connection=%s err=%v", claims.UserID, connectionID, err)
			return nil, agentStatusUnavailable{"Could not reach that agent's network just now. Try again in a moment."}
		}
		qualifyNetworkAgentStatus(remote, connectionID, address)
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
	if yours && !publicOnly {
		if doc, err := defaultProductConversationRegistryStore().loadDocument(ctx, productConversationRegistryPath(claims.UserID)); err == nil {
			for _, record := range doc.Entries {
				if key, _ := row["chat_id"].(string); key != "" && key == record.ConversationKey || row["agent_name"] == desktopAppAgentName(desktopMachineID(), record.ConversationID) {
					api.addAgentRecent(row, claims.UserID, record, recent)
					if row["last_messages"] != nil {
						break
					}
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
			if admitted.Reach.ChatShare == crewChatShareLive && admitted.Record != nil {
				api.addAgentRecent(row, admitted.Owner, *admitted.Record, recent)
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
	items, _ := row["recent"].([]agentRecentItem)
	log.Printf("[AGENT_STATUS] caller=%s agent=%v action=read owner_text=%t recent=%d", claims.UserID, row["agent_id"], yours && !publicOnly, len(items))
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

// addAgentRecent adds the chat's last items (messages, tool calls and results) to a status row. Callers decide access
// first: the caller's own agent, or another person's agent whose chat is shared Live.
func (api *StreamingAPI) addAgentRecent(row map[string]interface{}, owner string, record ProductConversationRecord, recent int) {
	if record.SessionID == "" {
		return
	}
	history, err := api.chatHistoryForRead(owner, record.SessionID)
	if err != nil && strings.TrimSpace(record.WorkspacePath) != "" {
		// A Crew chat's history lives under its workspace; read it there, as the Team panel does.
		if saved, ok, readErr := readRestoredChatHistoryPersistTargetForSession(owner, record.SessionID, record.WorkspacePath); readErr == nil && ok {
			history, err = saved.History, nil
		}
	}
	if err != nil {
		return
	}
	if items := agentRecentItems(history, recent); len(items) > 0 {
		row["recent"] = items
	}
}
