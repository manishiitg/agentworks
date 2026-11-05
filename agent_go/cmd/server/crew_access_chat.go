package server

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// crewAccessChatAction is the rest of a Mac Crew's Access page from its owner's own chat (owner, 3 Nov: everything on
// the screen also works from chat): the ways in, and who can message each agent. It reuses the screen's own paths:
// macAgentRows and pushMacSharedAgents, so the server's checks are the same.
func (api *StreamingAPI) crewAccessChatAction(ctx context.Context, project, action string, args map[string]any) (string, error) {
	claims, binding, connection, err := api.desktopCrewChatBinding(ctx, project)
	if err != nil || binding.Mode != "relay" {
		return "", fmt.Errorf("Turn on Use from phone and web first (manage_crew_sync reach_on)")
	}
	out := func(value any) (string, error) {
		data, _ := json.Marshal(value)
		return string(data), nil
	}
	if action == "links" {
		link := binding.PeerServer + "/crew/projects/" + binding.PeerProjectID
		return out(map[string]any{
			"website":           link,
			"phone":             link + " (open it on the phone, or scan the code in Access, Phone, Show code)",
			"chatgpt_or_claude": "Add " + binding.PeerServer + "/api/external/v1/mcp as a connector in ChatGPT or Claude, sign in, then say \"ask my Crew to…\". Access, ChatGPT or Claude, Connect shows the steps.",
			"slack":             "Access, Slack, Connect sets it up; signing in to Slack is the person's own click.",
		})
	}
	rows, err := api.macAgentRows(ctx, claims, project)
	if err != nil {
		return "", err
	}
	shared := readMacSharedAgents(project)
	if action == "agents" {
		// In team is the Team record's (crew_team_in_team.go), read from the server.
		team, teamErr := liveCrewTeam(project)
		list := []map[string]any{}
		for _, row := range rows {
			item := map[string]any{"agent": row.Name, "purpose": row.Purpose, "in_team": false}
			if at := slices.IndexFunc(shared, func(agent macSharedAgent) bool { return agent.Tab == row.Tab }); at >= 0 {
				agent := shared[at]
				rule := team.agentRule(team.OwnerID, agent.Tab)
				item["in_team"], item["can_make_changes"], item["public_on_agentbridge"] = teamErr == nil && team.On && crewTeamInTeam(agent) && team.agentInTeam(team.OwnerID, agent.Tab), rule.CanMakeChanges, agent.AgentBridgePublic
				item["when_others_ask"] = rule.WhenOthersAsk
				if agent.Address != "" {
					item["address_for_their_agents"] = "agent:" + agent.Address
				}
			}
			list = append(list, item)
		}
		return out(map[string]any{"agents": list})
	}
	name := strings.TrimSpace(externalArg(args, "agent"))
	at := slices.IndexFunc(rows, func(row macAgentRow) bool { return strings.EqualFold(row.Name, name) || row.Tab == name })
	if at < 0 {
		names := []string{}
		for _, row := range rows {
			names = append(names, row.Name)
		}
		return "", fmt.Errorf("No agent named %q. This Crew's agents: %s", name, strings.Join(names, ", "))
	}
	tab := rows[at].Tab
	next := slices.DeleteFunc(slices.Clone(shared), func(agent macSharedAgent) bool { return agent.Tab == tab })
	existing := macSharedAgent{Tab: tab}
	if at := slices.IndexFunc(shared, func(agent macSharedAgent) bool { return agent.Tab == tab }); at >= 0 {
		existing = shared[at]
	}
	// Public on AgentBridge (Mac online) and the people audience (share/unshare) are separate (owner, 5 Nov).
	if action == "public" || action == "only_my_agents" {
		existing.AgentBridgePublic = action == "public"
		if existing.AgentBridgePublic || existing.Audience != "" {
			next = append(next, existing)
		}
	} else {
		// share / unshare is In team / Only me (owner, 5 Nov): the owner's own choice in the Team record on the server.
		if err = api.setOwnAgentInTeam(ctx, claims.UserID, macSharedAgentChatKey(project, tab), action == "share"); err != nil {
			return "", err
		}
		if action == "unshare" {
			return api.crewAccessChatAction(ctx, project, "agents", nil)
		}
		// It only answers them unless the owner says it can make changes for them (the default, like the screen); the
		// choice and the owner's optional "When others ask" rule are kept in the Team record (crew_when_others_ask.go).
		changes, _ := args["can_make_changes"].(bool)
		var text *string
		if value, ok := args["when_others_ask"].(string); ok {
			text = &value
		}
		if _, err = api.setOwnAgentRule(ctx, claims.UserID, macSharedAgentChatKey(project, tab), text, &changes); err != nil {
			return "", err
		}
		existing.Audience = "team"
		next = append(next, existing)
	}
	if _, _, err = api.pushMacSharedAgents(ctx, claims, project, binding, connection, rows, next); err != nil {
		return "", err
	}
	return api.crewAccessChatAction(ctx, project, "agents", nil)
}
