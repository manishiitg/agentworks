package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/codeproduct"
	"log"
	"path"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
)

// A product profile can be the default destination of an account's WhatsApp
// pairing: a message in the paired chat that names no @<slug> workflow route
// runs as a turn in that profile's own conversation — the one the product's
// app shows — with the same prompt, sandbox and runtime binding the app's own
// turns get. The product opts in through product.yaml
// (runtime.capabilities.whatsapp); the connector, pairing and routing stay
// the platform's.

// profileTakesWhatsApp reports whether the profile declared the capability.
func profileTakesWhatsApp(profile agentprofiles.Profile) bool {
	requirement := strings.TrimSpace(string(profile.Runtime.Capabilities.WhatsApp))
	return requirement != "" && requirement != string(agentprofiles.CapabilityDisabled)
}

// whatsappUploadFolderFor keeps a product's attachment folder inside the
// profile's own workspace (its fixed root, or the projects root of a keyed
// profile), where the profile's folder guard lets it read. An empty request
// means the platform's per-user chat uploads.
func whatsappUploadFolderFor(profile agentprofiles.Profile, requested string) (string, error) {
	requested = strings.Trim(strings.TrimSpace(requested), "/")
	if requested == "" {
		return "", nil
	}
	root := strings.Trim(strings.TrimSpace(profile.Runtime.Workspace.Root), "/")
	if root == "" {
		root = strings.Trim(strings.TrimSpace(profile.Runtime.Workspace.ProjectsRoot), "/")
	}
	if root == "" {
		return "", fmt.Errorf("%s has no workspace root to keep WhatsApp attachments in", profile.Name)
	}
	clean := path.Clean(requested)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || strings.HasPrefix(clean, "/") {
		return "", fmt.Errorf("upload folder %q must be a folder inside %s", requested, root)
	}
	if clean == root || strings.HasPrefix(clean, root+"/") {
		return clean, nil
	}
	return path.Join(root, clean), nil
}

// whatsappDefaultProfileResolver is the WhatsAppDefaultProfileResolver for
// this server: the profile must exist for the user and declare the whatsapp
// capability.
func (api *StreamingAPI) whatsappDefaultProfileResolver(ctx context.Context, userID, profileID, uploadFolder string) (string, error) {
	if api.agentProfiles == nil {
		return "", fmt.Errorf("agent profiles are unavailable")
	}
	profile, err := api.agentProfiles.Resolve(strings.TrimSpace(profileID), 0, userID)
	if err != nil {
		return "", fmt.Errorf("agent profile %q not found", profileID)
	}
	if !profileTakesWhatsApp(profile) {
		return "", fmt.Errorf("%s does not take WhatsApp messages (product.yaml runtime.capabilities.whatsapp)", profile.Name)
	}
	return whatsappUploadFolderFor(profile, uploadFolder)
}

// whatsappEngineFor picks the engine a WhatsApp turn runs on: the one the
// conversation is already bound to, else the profile's default option, so
// the first turn from WhatsApp binds the conversation exactly as the app's
// first turn would.
func whatsappEngineFor(profile agentprofiles.Profile, conversation ProductConversationRecord) (agentprofiles.ProviderOption, bool) {
	options := profile.Runtime.ProviderOptions
	if bound := strings.TrimSpace(conversation.Provider); bound != "" {
		for _, option := range options {
			if strings.EqualFold(strings.TrimSpace(option.Provider), bound) {
				return option, true
			}
		}
	}
	for _, option := range options {
		if option.Default {
			return option, true
		}
	}
	if len(options) > 0 {
		return options[0], true
	}
	return agentprofiles.ProviderOption{}, false
}

func whatsappWorkspaceUserID(userID string) string {
	if !IsMultiUserMode() {
		return GetDefaultUserID()
	}
	return userID
}

// whatsappProfileRouter is the connector's ProfileRouteResolver: an @token
// that is not a workflow slug is offered to the pairing's default product,
// whose registered router may map it to one of the product's own profiles.
func (api *StreamingAPI) whatsappProfileRouter(ctx context.Context, userID, token string) (*services.ProfileRoute, error) {
	if api.whatsappManager == nil || api.agentProfiles == nil {
		return nil, nil
	}
	svc, err := api.whatsappManager.ServiceForUser(ctx, userID, "", "")
	if err != nil {
		return nil, nil
	}
	defaultProfileID, _ := svc.DefaultProfile()
	if defaultProfileID == "" {
		return nil, nil
	}
	workspaceUserID := whatsappWorkspaceUserID(userID)
	route, ok, err := api.agentProfiles.ResolveChannelRoute(ctx, defaultProfileID, workspaceUserID, token)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}
	target, err := api.agentProfiles.Resolve(strings.TrimSpace(route.ProfileID), 0, workspaceUserID)
	if err != nil {
		return nil, fmt.Errorf("@%s points at a chat that does not exist", token)
	}
	uploadFolder, err := whatsappUploadFolderFor(target, route.UploadFolder)
	if err != nil {
		return nil, err
	}
	return &services.ProfileRoute{
		ProfileID:       target.ID,
		ConversationKey: strings.TrimSpace(route.ConversationKey),
		UploadFolder:    uploadFolder,
		Label:           strings.TrimSpace(route.Label),
		WorkspaceUserID: workspaceUserID,
	}, nil
}

// botProfileTurn is the bot manager's ProfileTurnFunc. handled is false when
// the pairing has no default profile; an error means it has one that cannot
// take the message, which the bot manager reports back in the chat.
func (api *StreamingAPI) botProfileTurn(ctx context.Context, userID string, msg services.BotIncomingMessage, threadID services.ThreadID) (map[string]interface{}, string, bool, error) {
	if api.agentProfiles == nil {
		return nil, "", false, nil
	}
	profileID := ""
	if msg.PresetProfile == nil {
		if msg.Platform != "whatsapp" || api.whatsappManager == nil {
			return nil, "", false, nil
		}
		svc, err := api.whatsappManager.ServiceForUser(ctx, userID, "", "")
		if err != nil {
			log.Printf("[WHATSAPP] default profile lookup for user=%s: %v", userID, err)
			return nil, "", false, nil
		}
		profileID, _ = svc.DefaultProfile()
		if profileID == "" {
			return nil, "", false, nil
		}
	}
	// The pairing's owner is the request principal; durable product data
	// belongs to the configured default owner on a single-user deployment,
	// exactly as the app's own profile chat resolves it.
	workspaceUserID := userID
	if msg.PresetProfile != nil && strings.TrimSpace(msg.PresetProfile.WorkspaceUserID) != "" {
		workspaceUserID = strings.TrimSpace(msg.PresetProfile.WorkspaceUserID)
	} else if msg.Platform == "whatsapp" {
		workspaceUserID = whatsappWorkspaceUserID(userID)
	}
	conversationKey := ""
	if msg.PresetProfile != nil {
		// An @token the default product resolved to one of its own profiles
		// (a keyed one, e.g. a tutor's per-activity conversation).
		profileID = strings.TrimSpace(msg.PresetProfile.ProfileID)
		conversationKey = strings.TrimSpace(msg.PresetProfile.ConversationKey)
	}
	profile, err := api.agentProfiles.Resolve(profileID, 0, workspaceUserID)
	if err != nil {
		return nil, "", false, fmt.Errorf("the chat %q is gone — unpair and pair again from the product", profileID)
	}
	if msg.PresetProfile == nil && !profileTakesWhatsApp(profile) {
		return nil, "", false, fmt.Errorf("%s no longer takes WhatsApp messages", profile.Name)
	}
	productInteractions.Note(ctx, workspaceUserID, profile.Product)
	// A Code talks 1:1 only: a Slack DM or WhatsApp (which admits 1:1 chats
	// only), each sender in their own chat of it, and only people with
	// access to it (senderProfileTurn refuses anyone else and viewers).
	// Slack channels, group DMs and anything else are refused.
	if strings.EqualFold(profile.ID, codeproduct.ProfileID) {
		if !((msg.Platform == "slack" && msg.DirectMessage) || msg.Platform == "whatsapp") {
			return nil, "", false, fmt.Errorf("%s answers only 1:1 Slack DMs and WhatsApp, not channels or group chats", profile.Name)
		}
		return api.senderProfileTurn(ctx, userID, profile, conversationKey, msg, threadID)
	}
	// A crew reached by a 1:1 Slack DM or by WhatsApp runs in the sender's
	// own chat of it (their reader chat when someone else owns it).
	if msg.PresetProfile != nil && strings.EqualFold(profile.ID, "work") && ((msg.Platform == "slack" && msg.DirectMessage) || msg.Platform == "whatsapp") {
		return api.senderProfileTurn(ctx, userID, profile, conversationKey, msg, threadID)
	}
	// In a multi-chat project (a crew) a Slack thread is its own chat (a
	// separate native session and history), not the project's main
	// conversation: that main chat is the owner's, and every thread sharing
	// it mixed colleagues' questions into one context (RTS 2026-09-26).
	// WhatsApp, and single-conversation products, keep the main conversation.
	if msg.Platform == "slack" && msg.PresetProfile != nil && conversationKey != "" && strings.TrimSpace(threadID.ThreadTS) != "" && profileHasProjectChats(profile) {
		conversationKey = slackThreadConversationKey(conversationKey, threadID)
	}
	binding, err := resolveProductConversationBinding(ctx, workspaceUserID, profile, conversationKey)
	if err != nil {
		return nil, "", false, fmt.Errorf("resolve %s conversation: %w", profile.Name, err)
	}
	if err := initializeProductConversationWorkspace(ctx, workspaceUserID, profile, binding); err != nil {
		return nil, "", false, err
	}
	conversation, err := defaultProductConversationRegistryStore().resolveOrCreate(ctx, workspaceUserID, profile, binding, "")
	if err != nil {
		return nil, "", false, fmt.Errorf("open %s conversation: %w", profile.Name, err)
	}
	if msg.ResumeSessionID != "" {
		conversation, err = resumeProductConversation(ctx, workspaceUserID, profile, binding, msg.ResumeSessionID)
		if err != nil {
			return nil, "", false, err
		}
	}
	return productBotTurnRequest(ctx, workspaceUserID, profile, conversation, msg, threadID)
}

// senderProfileTurn runs a crew turn from a 1:1 Slack DM or WhatsApp in the
// sender's own chat of the crew: the conversation their web chat continues,
// found exactly as the web resolves it (resolveAgentProfileConversation).
// One user, one chat: every DM thread or WhatsApp message continues it; an
// owner's is the crew's own chat, a reader's is their reader chat in their
// own registry.
func (api *StreamingAPI) senderProfileTurn(ctx context.Context, senderID string, profile agentprofiles.Profile, conversationKey string, msg services.BotIncomingMessage, threadID services.ThreadID) (map[string]interface{}, string, bool, error) {
	userID := strings.TrimSpace(senderID)
	if !IsMultiUserMode() {
		userID = GetDefaultUserID()
	}
	if userID == "" {
		return nil, "", false, fmt.Errorf("the sender has no AgentWorks account")
	}
	binding, owned, err := resolveConversationBindingForUser(ctx, userID, profile, conversationKey)
	if err != nil {
		return nil, "", false, fmt.Errorf("resolve %s conversation: %w", profile.Name, err)
	}
	if owned {
		if err := initializeProductConversationWorkspace(ctx, userID, profile, binding); err != nil {
			return nil, "", false, err
		}
	}
	conversation, err := defaultProductConversationRegistryStore().resolveOrCreate(ctx, userID, profile, binding, "")
	if err != nil {
		return nil, "", false, fmt.Errorf("open %s conversation: %w", profile.Name, err)
	}
	return productBotTurnRequest(ctx, userID, profile, conversation, msg, threadID)
}

// productBotTurnRequest builds a bot turn in a resolved product
// conversation, with the engine the conversation is bound to.
func productBotTurnRequest(ctx context.Context, workspaceUserID string, profile agentprofiles.Profile, conversation ProductConversationRecord, msg services.BotIncomingMessage, threadID services.ThreadID) (map[string]interface{}, string, bool, error) {
	input := AgentProfileChatRequest{Message: msg.Text}
	if conversation.ProjectLLMConfig == nil {
		if option, ok := whatsappEngineFor(profile, conversation); ok {
			input.Engine = option.ID
			if strings.TrimSpace(conversation.Provider) != "" {
				input.ModelID = conversation.ModelID
				input.ReasoningEffort = conversation.ReasoningEffort
			}
		}
	}
	query, err := prepareProductConversationTurn(ctx, workspaceUserID, profile, input, conversation)
	if err != nil {
		return nil, "", false, err
	}

	reqMap, err := queryRequestToMap(query)
	if err != nil {
		return nil, "", false, err
	}
	if query.resolvedResumeTarget != nil {
		reqMap["_trusted_resume_target"] = query.resolvedResumeTarget
	}
	// The channel prompt (WhatsApp's markup subset) applies to this turn only;
	// the app's own turns in the same conversation send no bot_platform.
	services.ApplyBotThreadFields(reqMap, msg.Platform, threadID)
	reqMap["triggered_by"] = "bot:" + msg.Platform
	return reqMap, conversation.SessionID, true, nil
}

// slackThreadConversationKey names a Slack thread's own chat in a keyed
// project: "<project>:slack-<hash>", the multi-chat key form the project
// binding already resolves to the same project folder and permissions.
func slackThreadConversationKey(projectKey string, thread services.ThreadID) string {
	project, _, _ := strings.Cut(strings.TrimSpace(projectKey), ":")
	sum := sha256.Sum256([]byte(thread.ConnectionID + "|" + thread.ChannelID + "|" + thread.ThreadTS))
	return project + ":slack-" + hex.EncodeToString(sum[:])[:16]
}

// sameProjectConversation reports whether key is the route's project
// conversation or one of that project's own chats ("<project>:<suffix>").
func sameProjectConversation(routeKey, key string) bool {
	routeKey, key = strings.TrimSpace(routeKey), strings.TrimSpace(key)
	if routeKey == key {
		return true
	}
	project, _, _ := strings.Cut(routeKey, ":")
	return project != "" && strings.HasPrefix(key, project+":")
}

// profileHasProjectChats reports a keyed project profile (a crew), whose
// projects hold several chats ("<project>:<suffix>").
func profileHasProjectChats(profile agentprofiles.Profile) bool {
	conversation := profile.Runtime.Conversation
	return strings.EqualFold(strings.TrimSpace(conversation.Mode), agentprofiles.ConversationModeKeyed) &&
		strings.EqualFold(strings.TrimSpace(conversation.KeyType), agentprofiles.ConversationKeyTypeProject)
}
