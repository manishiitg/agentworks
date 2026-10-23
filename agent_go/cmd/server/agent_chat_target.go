package server

import (
	"context"
	"fmt"
	"strings"
)

// Conversation messages address registered project tabs. Function execution
// never passes through here and keeps its own declared run conversation.
func (api *StreamingAPI) resolveAgentChatTarget(ctx context.Context, target triggerTarget, chatID string) (triggerTarget, error) {
	if target.Remote != nil || target.Chat != nil || target.NamedAgentID != "" {
		return target, nil
	}
	caller := GetUserIDFromContext(ctx)
	chatID = strings.TrimSpace(chatID)
	if target.Kind == triggerCallerCrew {
		if chatID != "" && chatID != "main" {
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
		registry := api.agentProfiles
		if registry == nil && api.productSchedules != nil {
			registry = api.productSchedules.registry
		}
		if registry == nil {
			return target, fmt.Errorf("Crew chat runtime unavailable")
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
	if target.Kind == triggerCallerWorkflow && chatID != "" {
		m, found, err := ReadWorkflowManifest(ctx, target.Path)
		if err != nil || !found || m == nil {
			return target, fmt.Errorf("Project unavailable")
		}
		tabs, err := api.goalOwnerTabs(ctx, workflowBuilderScope{PresetID: m.ID, Workspace: target.Path})
		if err != nil {
			return target, err
		}
		for _, tab := range tabs {
			if tab.ID != chatID && tab.SessionID != chatID {
				continue
			}
			target.Chat = &codeChat{OwnerID: tab.OwnerID, ID: tab.ID, Key: tab.ID, Name: tab.Name, SessionID: tab.SessionID, IsMain: tab.IsMain}
			target.Label += " · " + tab.Name
			return target, nil
		}
		return target, fmt.Errorf("Choose a chat_id from list_agents for this project")
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
