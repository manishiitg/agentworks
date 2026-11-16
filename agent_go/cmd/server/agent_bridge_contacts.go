package server

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
)

// An app tab's registry identity is its Bridge contact. Titles and physical
// turn sessions may change without redirecting its address to another tab.
func bridgeChatAddress(owner, profile, project, key string) string {
	sum := sha256.Sum256([]byte(owner + "\x00" + profile + "\x00" + project + "\x00" + key))
	return fmt.Sprintf("agent:%s/chat-%x", shareOwnerLabel(owner), sum[:16])
}

// Resolve only over current admitted projects and selected chats. An address
// never grants access, and a caller cannot turn a product ID into an agent.
func (api *StreamingAPI) resolveBridgeMessageAgent(ctx context.Context, claims *UserClaims, address, sender string) (triggerTarget, error) {
	if claims == nil || claims.UserID == "" {
		return triggerTarget{}, fmt.Errorf("Sign in to discover agents")
	}
	address = strings.TrimSpace(address)
	if !strings.HasPrefix(address, "agent:") {
		return triggerTarget{}, fmt.Errorf("Choose an agent: address from messages action=list_agents")
	}
	name := strings.TrimPrefix(address, "agent:")
	if target, e := resolveNamedMessageAgent(ctx, claims.UserID, name, sender); e == nil {
		if target.Hosted != nil {
			owner := claims.UserID
			if target.ShareOwnerUserID != "" {
				owner = target.ShareOwnerUserID
			}
			if _, e := currentHostedBridgeRecord(ctx, owner, *target.Hosted); e != nil {
				return triggerTarget{}, e
			}
		}
		return target, nil
	}
	_, localName, qualified := strings.Cut(name, "/")
	if !qualified || !strings.HasPrefix(localName, "chat-") || len(localName) != len("chat-")+32 {
		return resolveNamedMessageAgent(ctx, claims.UserID, name, sender)
	}
	projects, err := listAccessibleWorkspaceProjects(ctx, claims, "")
	if err != nil {
		return triggerTarget{}, err
	}
	for _, project := range projects {
		id, _ := project["id"].(string)
		profile, _ := project["profile_id"].(string)
		root, _ := project["workspace_path"].(string)
		if crewProjectOwnedByCallerStrict(claims.UserID, root) {
			tabs, e := crewOwnerChatRecords(ctx, claims.UserID, profile, id, root)
			if e != nil {
				return triggerTarget{}, e
			}
			for _, tab := range tabs {
				record := tab.Record
				if bridgeChatAddress(tab.OwnerID, profile, id, record.ConversationKey) != address {
					continue
				}
				if tab.OwnerID != claims.UserID || hiddenFromTabStrip(record.ConversationKey) || crewFunctionTabKey(record.ConversationKey) {
					continue
				}
				agent, err := api.registerHostedBridgeChat(ctx, tab.OwnerID, hostedBridgeBinding{Profile: profile, Project: id, Chat: record.ConversationKey})
				if err != nil {
					return triggerTarget{}, err
				}
				return triggerTarget{NamedAgentID: agent.ID, Hosted: agent.Hosted, Kind: triggerCallerUser, Label: agentLabel(agent)}, nil
			}
		}
	}
	return triggerTarget{}, fmt.Errorf("agent unavailable or access denied")
}

// Named-to-named conversations are existing Bridge threads. Product mailbox
// threads without a server-issued Bridge contact are not alternate mail routes.
func bridgeAgentConversation(c agentConversation) bool {
	return c.Endpoints[0].BridgeAddress != "" || c.Endpoints[1].BridgeAddress != "" || c.Endpoints[0].NamedAgentID != "" && c.Endpoints[1].NamedAgentID != ""
}

func requireBridgeAgentInbox(ctx context.Context, id string) error {
	store, err := readAgentMessageSnapshot(ctx)
	if err != nil {
		return err
	}
	for _, c := range store.Conversations {
		if c.ID == id && bridgeAgentConversation(c) {
			return nil
		}
	}
	return fmt.Errorf("AgentBridge conversation unavailable; discover its agent address and start a new thread")
}
