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

// automationProductChatQuery builds a server-started turn (a schedule, Watcher, function or trigger follow-up) in a
// product chat. No browser sends the tab's saved choice in Models, so it is applied here: the same engine, model,
// effort and account the browser sends and crewChatUsesPersonalAccountScope checks. Without it these turns ran on
// the Crew's model and account and were refused "This chat's provider account changed".
func automationProductChatQuery(ctx context.Context, userID string, profile agentprofiles.Profile, message string, conversation ProductConversationRecord) (QueryRequest, bool, error) {
	input := AgentProfileChatRequest{Message: message}
	own, err := defaultProductConversationRegistryStore().agentIdentity(ctx, userID, profile.ID, conversation.ConversationKey)
	if err != nil {
		return QueryRequest{}, false, err
	}
	applied := false
	if _, offered := findProviderOptionByID(profile.Runtime.ProviderOptions, own.Engine); own.Engine != "" && offered {
		input.Engine, input.ModelID, input.ReasoningEffort, input.ConnectionID = own.Engine, own.ModelID, own.ReasoningEffort, own.ConnectionID
		conversation.ConnectionID = "" // the saved choice names the account; none is the company account
		applied = true
	}
	query, err := queryRequestForAgentProfileChat(profile, input, conversation)
	return query, applied, err
}
