package server

import (
	"context"
	"fmt"
	"strings"
)

// Conversation messages address registered project tabs. Function execution
// never passes through here and keeps its own declared run conversation.
func (api *StreamingAPI) resolveAgentChatTarget(ctx context.Context, target triggerTarget, chatID string) (triggerTarget, error) {
	if target.Kind == triggerCallerWorkflow {
		return target, fmt.Errorf("Workflows run directly. Send your message to a project agent instead.")
	}
	caller := GetUserIDFromContext(ctx)
	chatID = strings.TrimSpace(chatID)

	if target.Chat != nil || target.NamedAgentID != "" {
		return target, nil
	}
	if target.Kind == triggerCallerCrew {
		// "main" is no stand-in for an agent (owner, 25 Nov, #4046): a chat_id names an agent by its id.
		if chatID != "" {
			if !crewProjectOwnedByCaller(caller, target.Path) {
				chat, ok := sharedCrewAgentChat(ctx, caller, target.Path, chatID)
				if !ok {
					return target, fmt.Errorf("shared agent unavailable or access denied")
				}
				target.Chat, target.CrewOwner = chat, chat.OwnerID
				target.Label += " · " + chat.Name
				return target, nil
			}
			tabs, err := crewOwnerChatRecords(ctx, caller, target.CrewProfile, target.CrewID, target.Path)
			if err != nil {
				return target, err
			}
			for _, tab := range tabs {
				record := tab.Record
				if chatID != record.ConversationKey && chatID != strings.TrimPrefix(record.ConversationKey, target.CrewID+codeChatSideMarker) && chatID != record.SessionID {
					continue
				}
				target.CrewOwner = tab.OwnerID
				target.Chat = &codeChat{OwnerID: tab.OwnerID, ID: record.ConversationKey, Key: record.ConversationKey, Name: firstNonEmptyTrimmed(tab.Identity.Name, record.Title, "Builder"), SessionID: record.SessionID}
				target.Label = target.Label + " · " + target.Chat.Name
				return target, nil
			}
			return target, fmt.Errorf("Choose a chat_id from list_agents for this Crew")
		}
		// Run sharing does not share an owner's Builder history. These
		// callers keep the existing caller-bound ask conversation.
		if !crewProjectOwnedByCaller(caller, target.Path) {
			return target, nil
		}
		if claims := GetUserFromContext(ctx); claims != nil && claims.AccessToken != nil && !externalOwnerAuthority(claims, target) {
			return target, nil
		}
		// No agent named: the caller's only agent answers; with several, the caller chooses (no implicit Main, #4046).
		tabs, err := crewOwnerChatRecords(ctx, caller, target.CrewProfile, target.CrewID, target.Path)
		if err != nil {
			return target, err
		}
		var mine []crewOwnerChat
		for _, tab := range tabs {
			if tab.OwnerID == caller && !hiddenFromTabStrip(tab.Record.ConversationKey) {
				mine = append(mine, tab)
			}
		}
		if len(mine) == 1 {
			record := mine[0].Record
			target.CrewOwner = caller
			target.Chat = &codeChat{OwnerID: caller, ID: record.ConversationKey, Key: record.ConversationKey, Name: firstNonEmptyTrimmed(mine[0].Identity.Name, "Main"), SessionID: record.SessionID}
			target.Label += " · " + target.Chat.Name
			return target, nil
		}
		if len(mine) > 1 {
			names := make([]string, 0, len(mine))
			for _, tab := range mine {
				names = append(names, fmt.Sprintf("%s (%s)", firstNonEmptyTrimmed(tab.Identity.Name, "Main"), strings.TrimPrefix(tab.Record.ConversationKey, target.CrewID+codeChatSideMarker)))
			}
			return target, fmt.Errorf("Choose which agent should answer: pass chat_id, one of %s (list_agents shows each agent's id)", strings.Join(names, ", "))
		}
		// No agent yet: the project's first agent is made for this message.
		registry := api.agentProfiles
		if registry == nil && api.projectActions != nil {
			registry = api.projectActions.registry
		}
		if registry == nil {
			return target, fmt.Errorf("Project chat runtime unavailable")
		}
		owner := target.CrewOwner
		if actual, ok := crewProjectOwnerID(target.Path); ok {
			owner = actual
		}
		profile, err := registry.Resolve(target.CrewProfile, 0, owner)
		if err != nil {
			return target, err
		}
		binding, _, err := resolveConversationBindingForUser(ctx, owner, profile, target.CrewID)
		if err != nil {
			return target, err
		}
		record, err := defaultProductConversationRegistryStore().resolveOrCreate(ctx, owner, profile, binding, "")
		if err != nil {
			return target, err
		}
		target.CrewOwner = owner
		target.Chat = &codeChat{OwnerID: owner, ID: record.ConversationKey, Key: record.ConversationKey, Name: "Builder", SessionID: record.SessionID, IsMain: true}
		return target, nil
	}
	if chatID != "" {
		return target, fmt.Errorf("This agent has no project chat tabs")
	}
	return target, nil
}

func (api *StreamingAPI) authorizeGoalMessageTab(ctx context.Context, peer agentMessageEndpoint) bool {
	if peer.Kind != triggerCallerWorkflow || !strings.Contains(peer.ChatKey, codeChatSideMarker) {
		return false
	}
	tab, root, ok := api.goalTabForSession(ctx, peer.Session)
	return ok && root == peer.Path && tab.ID == peer.ChatKey && tab.OwnerID == peer.UserID
}
