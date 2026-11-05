package server

import (
	"context"
	"fmt"
	"log"
	"slices"
	"strings"
	"time"
)

// A shared agent is an existing owner tab, selected by stable conversation key
// in the same server-owned record as its Crew's Share audience.
type sharedCrewAgent struct {
	ChatID         string `json:"chat_id"`
	Name           string `json:"name"`
	Purpose        string `json:"purpose"`
	Selected       bool   `json:"selected"`
	CanEditPurpose bool   `json:"can_edit_purpose"`
	OwnerName      string `json:"owner_name,omitempty"`
	tab            crewOwnerChat
}

type crewAgentShareChange struct {
	Agents   *[]string         `json:"shared_agents,omitempty"`
	Purposes map[string]string `json:"agent_purposes,omitempty"`
	// Agent / AgentOn switch one agent's selection and keep the rest (the Team tab's Public on AgentBridge).
	Agent   string `json:"shared_agent,omitempty"`
	AgentOn *bool  `json:"shared_agent_on,omitempty"`
}

func (c crewAgentShareChange) changesAgents() bool {
	return c.Agents != nil || len(c.Purposes) > 0 || c.Agent != "" && c.AgentOn != nil
}

func crewSharedAgentKeys(root string) []string {
	id, ok := projectIdentityOf(root)
	if !ok {
		return nil
	}
	record, ok := defaultProjectOwners().Lookup(id.Product, id.Folder)
	if !ok {
		return nil
	}
	return record.SharedAgents
}

func crewSharedAgentSince(root, key string) time.Time {
	id, ok := projectIdentityOf(root)
	if !ok {
		return time.Time{}
	}
	record, _ := defaultProjectOwners().Lookup(id.Product, id.Folder)
	since, _ := time.Parse(time.RFC3339Nano, record.SharedAgentSince[key])
	return since
}

func crewShareAgents(ctx context.Context, caller, root string, includeUnselected bool) ([]sharedCrewAgent, error) {
	if id, ok := projectIdentityOf(root); !ok || id.Product != crewProfileID {
		return nil, nil
	}
	owner, ok := crewProjectOwnerID(root)
	if !ok {
		return nil, fmt.Errorf("Crew unavailable")
	}
	manifest, err := readCrewProjectManifests(ctx, crewProfileID, root)
	if err != nil {
		return nil, err
	}
	tabs, err := crewOwnerChatRecords(ctx, owner, manifest.Product, manifest.ID, root)
	if err != nil {
		return nil, err
	}
	selected := crewSharedAgentKeys(root)
	agents := []sharedCrewAgent{}
	for _, tab := range tabs {
		key := tab.Record.ConversationKey
		// Only named project agents; automation/caller conversations are not
		// silently published by selecting their parent project.
		short, side := strings.CutPrefix(key, manifest.ID+codeChatSideMarker)
		if key != manifest.ID && (!side || short == "" || strings.Contains(short, ":") || hiddenFromTabStrip(key)) {
			continue
		}
		shared := containsID(selected, key)
		if !includeUnselected && !shared {
			continue
		}
		agents = append(agents, sharedCrewAgent{ChatID: key, Name: firstNonEmptyTrimmed(tab.Identity.Name, tab.Record.Title, "Builder"), Purpose: tab.Identity.Purpose, Selected: shared, CanEditPurpose: includeUnselected && tab.OwnerID == caller, OwnerName: crewOwnerDisplayName(tab.OwnerID), tab: tab})
	}
	return agents, nil
}

func validateCrewAgentShare(ctx context.Context, caller, root string, change crewAgentShareChange) ([]string, error) {
	agents, err := crewShareAgents(ctx, caller, root, true)
	if err != nil {
		return nil, err
	}
	byID := map[string]sharedCrewAgent{}
	for _, agent := range agents {
		byID[agent.ChatID] = agent
	}
	keys := crewSharedAgentKeys(root)
	if change.Agents != nil {
		keys = *change.Agents
	}
	if change.Agent != "" && change.AgentOn != nil {
		keys = slices.DeleteFunc(slices.Clone(keys), func(key string) bool { return key == change.Agent })
		if *change.AgentOn {
			keys = append(keys, change.Agent)
		}
	}
	if len(keys) > 100 {
		return nil, fmt.Errorf("select at most 100 agents")
	}
	out := []string{}
	for _, key := range keys {
		if _, found := byID[key]; !found {
			return nil, fmt.Errorf("choose an existing agent from Share")
		}
		if !containsID(out, key) {
			out = append(out, key)
		}
	}
	// Validate every requested edit before writing any identity.
	for key, purpose := range change.Purposes {
		agent, found := byID[key]
		if !found || !agent.CanEditPurpose {
			return nil, fmt.Errorf("only the tab's owner can change its purpose")
		}
		if len([]rune(strings.Join(strings.Fields(purpose), " "))) > productAgentPurposeMaxRunes {
			return nil, fmt.Errorf("agent purposes must be at most %d characters", productAgentPurposeMaxRunes)
		}
	}
	for key, purpose := range change.Purposes {
		agent := byID[key]
		if _, err := defaultProductConversationRegistryStore().setAgentIdentity(ctx, agent.tab.OwnerID, agent.tab.Record.ProfileID, key, nil, &purpose, caller); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// Current project admission and connection bounds are checked by the caller's
// existing Crew resolver. A selected tab grants messaging/history, not control.
func sharedCrewAgentChat(ctx context.Context, user, root, chatID string) (*codeChat, bool) {
	claims := &UserClaims{UserID: user}
	if userAccessForClaims(claims).Disabled || !userAllowedProduct(claims, crewProfileID) {
		return nil, false
	}
	if !crewSharedWith(root, user) && !crewProjectOwnedByCaller(user, root) {
		return nil, false
	}
	agents, err := crewShareAgents(ctx, user, root, false)
	if err != nil {
		return nil, false
	}
	for _, agent := range agents {
		if agent.ChatID != chatID {
			continue
		}
		tab := agent.tab
		return &codeChat{OwnerID: tab.OwnerID, Key: agent.ChatID, ID: agent.ChatID, Name: agent.Name, Purpose: agent.Purpose, SessionID: tab.Record.SessionID, UpdatedAt: tab.Record.UpdatedAt}, true
	}
	return nil, false
}

func sharedCrewAgentPeer(ctx context.Context, user string, peer agentMessageEndpoint) bool {
	if peer.Kind != triggerCallerCrew || peer.ChatKey == "" || strings.HasPrefix(peer.ChatKey, "messages:") {
		return false
	}
	// An agent shared from a Crew on its owner's Mac: its audience decides (PLAT-856-509).
	if !IsLocalMode() && crewRelayTwin(peer.ID) {
		_, reason := macSharedAgentForAgent(peer.ID, macSharedAgentTab(peer.ID, peer.ChatKey), user, peer.UserID)
		return reason == ""
	}
	chat, ok := sharedCrewAgentChat(ctx, user, peer.Path, peer.ChatKey)
	return ok && chat.OwnerID == peer.UserID && chat.SessionID == peer.Session
}

// The original recipient's Share grant admits this contact, including replies.
// The caller can keep their own conversation private.
func recheckSharedCrewAgentContact(ctx context.Context, caller, agent agentMessageEndpoint) error {
	if agent.Kind != triggerCallerCrew || agent.ChatKey == "" || strings.HasPrefix(agent.ChatKey, "messages:") || crewProjectOwnedByCaller(caller.UserID, agent.Path) {
		return nil
	}
	if !sharedCrewAgentPeer(ctx, caller.UserID, agent) {
		return admissionRefused("shared agent unavailable or access removed")
	}
	return nil
}

func logSharedCrewAgent(user, key, action string) {
	log.Printf("[CREW_SHARED_AGENT] user=%q chat=%q action=%s", sanitizeUserIDForPath(user), key, action)
}
