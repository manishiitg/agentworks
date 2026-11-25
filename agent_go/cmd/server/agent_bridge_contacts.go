package server

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log"
	"sort"
	"strings"
)

// An app tab's registry identity is its Bridge contact. Titles and physical
// turn sessions may change without redirecting its address to another tab.
func bridgeChatAddress(owner, profile, project, key string) string {
	sum := sha256.Sum256([]byte(owner + "\x00" + profile + "\x00" + project + "\x00" + key))
	return fmt.Sprintf("agent:%s/chat-%x", shareOwnerLabel(owner), sum[:16])
}

// Names are conveniences over the caller's current Bridge directory, never
// product aliases or a separate authority lookup. Delivery rechecks admission.
func bridgeMessageAddress(directory map[string]interface{}, name string) (string, error) {
	name = strings.TrimSpace(name)
	rows, _ := directory["agents"].([]map[string]interface{})
	matches := map[string]string{}
	for _, row := range rows {
		// #2999: a shortened address must never select a stranger's published
		// registration on another joined network. Full addresses stay explicit.
		if strings.HasPrefix(name, "agent:") && row["yours"] != true {
			continue
		}
		address := firstNonEmptyTrimmed(stringField(row["target"]), stringField(row["id"]))
		if !strings.HasPrefix(address, "agent:") && !strings.HasPrefix(address, "network:") {
			continue
		}
		values := []string{}
		for _, field := range []string{"target", "id", "agent_name", "name", "label"} {
			values = append(values, stringField(row[field]))
		}
		if strings.HasPrefix(name, "agent:") {
			values = append(values, agentDirectoryAddresses(row)...)
		}
		for _, value := range values {
			// A short address from another network is a lookup, not a local
			// registration. Preserve its exact network once disambiguated.
			if strings.HasPrefix(name, "agent:") && strings.HasPrefix(value, "network:") {
				if _, suffix, ok := splitDesktopAgentNetworkAddress(value); ok {
					value = suffix
				}
			}
			if value != "" && strings.EqualFold(value, name) {
				identity := address
				if strings.HasPrefix(address, "agent:") && stringField(row["agent_id"]) != "" {
					identity = stringField(row["agent_id"])
				}
				// Own registrations may appear both qualified and unqualified. They
				// are the same server identity, not an ambiguous display name.
				if prior := matches[identity]; prior == "" || strings.Contains(address, "/") {
					matches[identity] = address
				}
				break
			}
		}
	}
	addresses := make([]string, 0, len(matches))
	for _, address := range matches {
		addresses = append(addresses, address)
	}
	sort.Strings(addresses)
	if len(addresses) == 1 {
		return addresses[0], nil
	}
	if len(addresses) > 0 {
		return "", fmt.Errorf("Several agents are called %s, pick one: %s", name, strings.Join(addresses, ", "))
	}
	return "", fmt.Errorf("No reachable agent has that name. Choose an agent address from messages action=list_agents")
}

func (api *StreamingAPI) resolveBridgeMessageAddress(ctx context.Context, claims *UserClaims, name string, publicOnly bool) (string, error) {
	name = strings.TrimSpace(name)
	if strings.HasPrefix(name, "network:") {
		return name, nil
	}
	if strings.HasPrefix(name, "agent:") {
		// Exact local and owner-qualified hosted addresses keep their existing
		// admission path. Only absent short names need network disambiguation.
		short := strings.TrimPrefix(name, "agent:")
		if !IsLocalMode() || strings.Contains(short, "/") {
			return name, nil
		}
		if _, err := resolveNamedMessageAgent(ctx, claims.UserID, short, ""); err == nil {
			return name, nil
		}
	}
	directory, err := listAgentDirectory(ctx, claims, api)
	if err != nil {
		return "", err
	}
	if publicOnly {
		publicAgentDirectoryOnly(directory)
	}
	address, err := bridgeMessageAddress(directory, name)
	if strings.HasPrefix(name, "agent:") {
		outcome := "resolved"
		if err != nil {
			outcome = "refused"
		}
		network, _, _ := splitDesktopAgentNetworkAddress(address)
		log.Printf("[AGENTBRIDGE] event=short_address_resolution owner=%s network=%s result=%s", claims.UserID, network, outcome)
	}
	return address, err
}

// Resolve only over current admitted projects and selected chats. An address
// never grants access, and a caller cannot turn a product ID into an agent.
func (api *StreamingAPI) resolveBridgeMessageAgent(ctx context.Context, claims *UserClaims, address, sender string) (triggerTarget, error) {
	if claims == nil || claims.UserID == "" {
		return triggerTarget{}, fmt.Errorf("Sign in to discover agents")
	}
	address = strings.TrimSpace(address)
	if !strings.HasPrefix(address, "agent:") {
		var err error
		address, err = api.resolveBridgeMessageAddress(ctx, claims, address, false)
		if err != nil {
			return triggerTarget{}, err
		}
		if !strings.HasPrefix(address, "agent:") {
			return triggerTarget{}, fmt.Errorf("Use the exact network address %s from your project chat", address)
		}
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

// Product Ask runs as the caller person, not as an invented workflow agent.
// Its inbox storage is not a second agent-to-agent ingress.
func bridgeOrProductConversation(c agentConversation) bool {
	for _, e := range c.Endpoints {
		if e.ProductAsk || e.ConditionJob != "" || e.FunctionCallID != "" {
			return true
		}
	}
	agent := func(e agentMessageEndpoint) bool { return e.NamedAgentID != "" || e.TeamAgentID != "" }
	// A person's own Team ask (the Ask box) has the person on one side, with no
	// registered agent, and a Team agent on the other (#3822).
	personAsk := func(p, t agentMessageEndpoint) bool { return p.External && !agent(p) && t.TeamAgentID != "" }
	a, b := c.Endpoints[0], c.Endpoints[1]
	return agent(a) && agent(b) || personAsk(a, b) || personAsk(b, a)
}

func requireCurrentBridgeInbox(ctx context.Context, id string) error {
	store, err := readAgentMessageSnapshot(ctx)
	if err != nil {
		return err
	}
	for _, c := range store.Conversations {
		if c.ID == id && bridgeOrProductConversation(c) {
			return nil
		}
	}
	log.Printf("[AGENT-MESSAGE] action=refused reason=retired_thread")
	return fmt.Errorf("This peer route is retired; use an AgentBridge address from list_agents")
}
