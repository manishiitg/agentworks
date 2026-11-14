package server

import (
	"encoding/json"
	"strings"
)

// Personal sidebar keys contain a client connection id and the stable Crew id.
// Different saved connection views keep their own membership; no owner's
// preferences are read on behalf of someone else.
func agentGroupProject(group menuAgentGroup, project string) bool {
	var key []string
	return json.Unmarshal([]byte(group.Project), &key) == nil && len(key) == 2 && key[1] == project
}

func (m menuGroups) agentGroupNames(project, chat string) []string {
	var names []string
	seen := map[string]bool{}
	for _, group := range m.AgentGroups {
		if !agentGroupProject(group, project) {
			continue
		}
		for _, member := range group.Agents {
			if member == chat && !seen[group.Name] {
				names = append(names, group.Name)
				seen[group.Name] = true
			}
		}
	}
	return names
}

func (m *menuGroups) inheritAgentGroup(project, source, fork string) {
	for i := range m.AgentGroups {
		group := &m.AgentGroups[i]
		if !agentGroupProject(*group, project) {
			continue
		}
		for at, member := range group.Agents {
			if member == source {
				agents := append([]string{}, group.Agents[:at+1]...)
				agents = append(agents, fork)
				group.Agents = append(agents, group.Agents[at+1:]...)
				break
			}
		}
	}
}

// Only decorate already admitted own-agent rows. Grouping neither discovers
// agents nor changes their routing or access.
func annotateDirectoryAgentGroups(rows []map[string]interface{}, saved menuGroups, preferences productConversationRegistryDocument, device string) {
	apps := map[string]string{}
	for key, record := range preferences.Entries {
		if strings.HasPrefix(key, crewProfileID+"/") && device != "" && record.ConversationID != "" {
			apps[desktopAppAgentName(device, record.ConversationID)] = strings.TrimPrefix(key, crewProfileID+"/")
		}
	}
	var annotate func(map[string]interface{})
	annotate = func(row map[string]interface{}) {
		yours, _ := row["yours"].(bool)
		chat, _ := row["chat_id"].(string)
		if yours {
			if name, _ := row["agent_name"].(string); chat == "" {
				chat = apps[name]
			}
			if chat != "" {
				if names := saved.agentGroupNames(crewResourceProjectID(chat), chat); len(names) > 0 {
					row["group_names"] = names
				}
			}
		}
		if children, ok := row["chats"].([]map[string]interface{}); ok {
			for _, child := range children {
				annotate(child)
			}
		}
	}
	for _, row := range rows {
		annotate(row)
	}
}
