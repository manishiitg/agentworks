package server

import (
	"context"
	"fmt"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
)

// An explicit account belongs to the person controlling this tab, rather than
// the Crew's inherited provider identity. Verify the saved tab and current
// session: a query's account/key alone never grants another person's account.
func crewChatUsesPersonalAccountScope(ctx context.Context, userID, sessionID string, req QueryRequest, profile *resolvedAgentProfile, provider, connectionID string) (bool, error) {
	if profile == nil || profile.Definition.ID != crewProfileID || !crewProjectOwnedByCaller(userID, req.SelectedFolder) {
		return false, nil
	}
	store := defaultProductConversationRegistryStore()
	path := productConversationRegistryPath(userID)
	mutex := productConversationRegistryMutex(path)
	mutex.Lock()
	defer mutex.Unlock()
	doc, err := store.loadDocument(ctx, path)
	if err != nil {
		return false, err
	}
	key := productConversationRegistryEntryKey(profile.Definition.ID, strings.TrimSpace(req.AgentProfileConversationKey))
	chat, found := doc.Entries[key]
	if !found || chat.SessionID != sessionID || !workspacePathsMatchForUser(userID, chat.WorkspacePath, req.SelectedFolder) {
		return false, nil
	}
	own := doc.Agents[key]
	// An empty account in a saved tab choice is the company (server) account.
	if own.Engine == "" {
		return false, nil
	}
	option, offered := findProviderOptionByID(profile.Definition.Runtime.ProviderOptions, own.Engine)
	if !offered || option.Provider != provider || canonicalProviderConnectionID(provider, own.ConnectionID) != canonicalProviderConnectionID(provider, connectionID) {
		return false, fmt.Errorf("This chat's provider account changed. Send your message again using its saved choice in Models.")
	}
	return true, nil
}

// A project default is canonical too: restoring it must discard the account
// retained from a former per-tab choice, including for callers without a browser.
func useProjectChatDefault(input *AgentProfileChatRequest, conversation *ProductConversationRecord, profile agentprofiles.Profile) {
	if !isProjectProfileID(profile.ID) {
		return
	}
	builder := presetPrimaryLLMForChat(conversation.ProjectLLMConfig)
	if builder == nil {
		return
	}
	for _, option := range profile.Runtime.ProviderOptions {
		if option.Provider == builder.Provider {
			input.Engine, input.ModelID, input.ConnectionID = option.ID, builder.ModelID, builder.ConnectionID
			input.ReasoningEffort, _ = builder.Options["reasoning_effort"].(string)
			conversation.ConnectionID = ""
			return
		}
	}
}
