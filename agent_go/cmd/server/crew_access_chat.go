package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
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
			"whatsapp":          "Link the phone once on " + binding.PeerServer + " (any Crew, Integrations, WhatsApp), then in WhatsApp's Message yourself chat send @list and pick this Crew. Only the person's own messages there reach this Mac.",
		})
	}
	rows, err := api.macAgentRows(ctx, claims, project)
	if err != nil {
		return "", err
	}
	shared := readMacSharedAgents(project)
	people, _ := desktopNetworkPeople(ctx, connection)
	if action == "agents" {
		list := []map[string]any{}
		for _, row := range rows {
			item := map[string]any{"agent": row.Name, "purpose": row.Purpose, "shared": false}
			if at := slices.IndexFunc(shared, func(agent macSharedAgent) bool { return agent.Tab == row.Tab }); at >= 0 {
				agent := shared[at]
				item["shared"], item["audience"], item["can_make_changes"] = true, agent.Audience, agent.CanChange
				if agent.Address != "" {
					item["address_for_their_agents"] = "agent:" + agent.Address
				}
				names := []string{}
				for _, id := range agent.People {
					names = append(names, networkPersonName(people, id))
				}
				if len(names) > 0 {
					item["people"] = names
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
	if action == "share" {
		agent := macSharedAgent{Tab: tab, Audience: strings.TrimSpace(externalArg(args, "audience"))}
		// It only answers them unless the owner says it can make changes for them (the default, like the screen).
		if changes, _ := args["can_make_changes"].(bool); changes {
			agent.CanChange = true
		}
		if agent.Audience == "chosen" {
			raw, _ := args["people"].([]any)
			for _, value := range raw {
				text, _ := value.(string)
				id, err := matchNetworkPerson(people, text)
				if err != nil {
					return "", err
				}
				if !crewTeamMember(project, id) {
					return "", fmt.Errorf("Add that person to the Team first with team_sync configure, then choose them for this agent")
				}
				if !slices.Contains(agent.People, id) {
					agent.People = append(agent.People, id)
				}
			}
		}
		next = append(next, agent)
	} else if len(next) == len(shared) {
		return out(map[string]any{"agent": rows[at].Name, "shared": false})
	}
	if _, _, err = api.pushMacSharedAgents(ctx, claims, project, binding, connection, rows, next); err != nil {
		return "", err
	}
	return api.crewAccessChatAction(ctx, project, "agents", nil)
}

type networkPerson struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Email    string `json:"email"`
}

// desktopNetworkPeople is the network's people list, read with the Mac's saved sign-in (the same list as the screen).
func desktopNetworkPeople(ctx context.Context, connection desktopConnection) ([]networkPerson, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, connection.Server+"/api/users/directory", nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+connection.Token)
	response, err := desktopHTTPClient().Do(request)
	if err != nil {
		return nil, fmt.Errorf("the people list could not be read")
	}
	defer response.Body.Close()
	var data struct {
		Users []networkPerson `json:"users"`
	}
	if response.StatusCode != 200 || json.NewDecoder(response.Body).Decode(&data) != nil {
		return nil, fmt.Errorf("the people list could not be read")
	}
	return data.Users, nil
}

func networkPersonName(people []networkPerson, id string) string {
	for _, person := range people {
		if person.ID == id {
			return firstNonEmptyTrimmed(person.Username, person.Email, id)
		}
	}
	return id
}

// matchNetworkPerson finds one person by exact name, email or the part of the email before @. Anything partial comes
// back as a question for the person, even a single match: sharing must never pick someone they did not name.
func matchNetworkPerson(people []networkPerson, text string) (string, error) {
	text = strings.ToLower(strings.TrimSpace(text))
	if text == "" {
		return "", fmt.Errorf("Say who, by name or email")
	}
	partial := []networkPerson{}
	for _, person := range people {
		username, email := strings.ToLower(person.Username), strings.ToLower(person.Email)
		if text == username || text == email || text == strings.SplitN(email, "@", 2)[0] {
			return person.ID, nil
		}
		if strings.Contains(username, text) || strings.Contains(email, text) {
			partial = append(partial, person)
		}
	}
	if len(partial) == 0 {
		return "", fmt.Errorf("Nobody called %q on this network. Ask the person who they meant", text)
	}
	names := []string{}
	for _, person := range partial {
		names = append(names, firstNonEmptyTrimmed(person.Email, person.Username))
	}
	return "", fmt.Errorf("%q is not an exact name. Did you mean %s? Ask the person, then use the exact name or email", text, strings.Join(names, " or "))
}
