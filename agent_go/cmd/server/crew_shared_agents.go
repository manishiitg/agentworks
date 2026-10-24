package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
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

func crewShareAgents(ctx context.Context, caller, root string, includeUnselected bool) ([]sharedCrewAgent, error) {
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
		if key != manifest.ID && (!side || short == "" || strings.Contains(short, ":")) {
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
	if len(keys) > 100 {
		return nil, fmt.Errorf("select at most 100 agents")
	}
	out := []string{}
	for _, key := range keys {
		agent, found := byID[key]
		if !found {
			return nil, fmt.Errorf("choose an existing agent from Share")
		}
		purpose := agent.Purpose
		if value, exists := change.Purposes[key]; exists {
			purpose = strings.TrimSpace(value)
		}
		if purpose == "" {
			return nil, fmt.Errorf("give %s a purpose before sharing it", agent.Name)
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
	chat, ok := sharedCrewAgentChat(ctx, user, peer.Path, peer.ChatKey)
	return ok && chat.OwnerID == peer.UserID && chat.SessionID == peer.Session
}

// Called on sending and again at queued delivery. Both directions matter: an
// old reply address cannot retain a removed recipient's access.
func recheckSharedCrewAgentContact(ctx context.Context, a, b agentMessageEndpoint) error {
	for _, pair := range [][2]agentMessageEndpoint{{a, b}, {b, a}} {
		tab, person := pair[0], pair[1]
		if tab.Kind != triggerCallerCrew || tab.ChatKey == "" || strings.HasPrefix(tab.ChatKey, "messages:") || crewProjectOwnedByCaller(person.UserID, tab.Path) {
			continue
		}
		if !sharedCrewAgentPeer(ctx, person.UserID, tab) {
			return fmt.Errorf("shared agent unavailable or access removed")
		}
	}
	return nil
}

// The website calls the same workspace service as external MCP, with the same
// authenticated claims. This adapter exposes only listing, reading and asking.
func (api *StreamingAPI) handleCrewSharedAgents(w http.ResponseWriter, r *http.Request) {
	var args map[string]any
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 32768)).Decode(&args) != nil {
		externalError(w, 400, "invalid_arguments", "Give a Crew and agent.")
		return
	}
	switch externalArg(args, "action") {
	case "chats", "read_chat", "ask":
	default:
		externalError(w, 400, "invalid_arguments", "Choose agents, read a conversation or send a message.")
		return
	}
	api.externalWorkspaceCall(w, r, args)
}

func logSharedCrewAgent(user, key, action string) {
	log.Printf("[CREW_SHARED_AGENT] user=%q chat=%q action=%s", sanitizeUserIDForPath(user), key, action)
}
