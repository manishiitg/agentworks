package server

import (
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
)

// PLAT-746-885: selection for opening only. Never use this for message routing
// or persist its result as Main. All candidates come from the caller's audience.
func (api *StreamingAPI) crewOpeningAgent(r *http.Request, profile agentprofiles.Profile, project, requested string) (string, error) {
	user := productWorkspaceUserID(r.Context())
	crew, err := readCrewProjectBinding(r.Context(), user, profile, project, "")
	if err != nil {
		return "", err
	}
	var choices []AgentProfileSideChat
	if crew.OwnedByCaller {
		copyRequest := r.Clone(r.Context())
		urlCopy := *r.URL
		copyRequest.URL = &urlCopy
		query := urlCopy.Query()
		query.Set("with_main", "1")
		copyRequest.URL.RawQuery = query.Encode()
		choices, err = api.sideChatsFor(copyRequest, user, profileAndBinding{profile: profile, binding: crew.Binding, canBuild: true}, project)
	} else {
		var agents []sharedCrewAgent
		agents, err = crewShareAgents(r.Context(), user, crew.Binding.WorkspacePath, false)
		for _, agent := range agents {
			choices = append(choices, AgentProfileSideChat{ConversationKey: agent.ChatID, Position: agent.tab.Identity.Position, IsMain: agent.tab.Record.IsMainChat})
		}
		sort.SliceStable(choices, func(i, j int) bool {
			a, b := choices[i].Position, choices[j].Position
			if (a > 0) != (b > 0) {
				return a > 0
			}
			if a != b {
				return a < b
			}
			return choices[i].ConversationKey < choices[j].ConversationKey
		})
	}
	if err != nil {
		return "", err
	}
	available := choices[:0]
	for _, choice := range choices {
		if ordinaryProjectChatKey(project, choice.ConversationKey) {
			available = append(available, choice)
		}
	}
	choices = available
	requested = strings.TrimSpace(requested)
	if requested != "" {
		for _, choice := range choices {
			if choice.ConversationKey == requested {
				return requested, nil
			}
		}
		return "", fmt.Errorf("Choose an available agent in this Crew.")
	}
	for _, choice := range choices {
		if choice.IsMain {
			return choice.ConversationKey, nil
		}
	}
	if len(choices) > 0 {
		return choices[0].ConversationKey, nil
	}
	return "", nil
}
