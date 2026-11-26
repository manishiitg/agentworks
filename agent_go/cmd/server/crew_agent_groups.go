package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
	"strings"

	"github.com/gorilla/mux"
)

// A person's own left-menu groups of a Crew's agents (#3288, owner 19 Nov). They are layout only: a group never
// changes who can open, message or change an agent. Members are agents by conversation key, the person's own or a
// teammate's agent shared with the Team; a fork is not a member of its own, it follows its parent in the menu.

const (
	agentGroupNameMaxRunes = 60
	agentGroupMembersMax   = 200
	agentGroupKeyMaxBytes  = 512
)

// agentGroupEdit is one change to the person's groups: new (put an agent in a new group), move (put it in an
// existing group, or take it out with an empty group), rename and ungroup.
type agentGroupEdit struct {
	Op    string `json:"op"`
	Key   string `json:"key"`
	Group string `json:"group"`
	Name  string `json:"name"`
}

func agentGroupName(raw string) (string, error) {
	name := strings.Join(strings.Fields(raw), " ")
	if name == "" {
		return "", fmt.Errorf("Give the group a name.")
	}
	if len([]rune(name)) > agentGroupNameMaxRunes {
		return "", fmt.Errorf("Keep the group name under %d characters.", agentGroupNameMaxRunes)
	}
	return name, nil
}

// agentGroupFind returns the saved spelling of a group name, compared without case.
func agentGroupFind(members map[string]string, name string) (string, bool) {
	for _, group := range members {
		if strings.EqualFold(group, name) {
			return group, true
		}
	}
	return "", false
}

// agentGroups returns the person's groups of one project: conversation key → group name.
func (store productConversationRegistryStore) agentGroups(ctx context.Context, userID, profileID, projectID string) (map[string]string, error) {
	path := productConversationRegistryPath(userID)
	mutex := productConversationRegistryMutex(path)
	mutex.Lock()
	defer mutex.Unlock()
	document, err := store.directoryReadDocument(ctx, path)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for key, group := range document.AgentGroups[productConversationRegistryEntryKey(profileID, projectID)] {
		out[key] = group
	}
	return out, nil
}

// editAgentGroups applies one change and returns the project's groups after it. The server decides the names:
// a new group may not reuse a name, a move joins the existing group of that name, a rename may not take another's.
func (store productConversationRegistryStore) editAgentGroups(ctx context.Context, userID, profileID, projectID string, edit agentGroupEdit) (map[string]string, error) {
	path := productConversationRegistryPath(userID)
	mutex := productConversationRegistryMutex(path)
	mutex.Lock()
	defer mutex.Unlock()
	document, err := store.loadDocument(ctx, path)
	if err != nil {
		return nil, err
	}
	project := productConversationRegistryEntryKey(profileID, projectID)
	members := map[string]string{}
	for key, group := range document.AgentGroups[project] {
		members[key] = group
	}
	key := strings.TrimSpace(edit.Key)
	needKey := func() error {
		if key == "" || len(key) > agentGroupKeyMaxBytes {
			return fmt.Errorf("Choose the agent to move.")
		}
		if _, ok := members[key]; !ok && len(members) >= agentGroupMembersMax {
			return fmt.Errorf("You can group up to %d agents in one project.", agentGroupMembersMax)
		}
		return nil
	}
	existing := func(raw string) (string, error) {
		name, err := agentGroupName(raw)
		if err != nil {
			return "", err
		}
		saved, ok := agentGroupFind(members, name)
		if !ok {
			return "", fmt.Errorf("You have no group called %s.", name)
		}
		return saved, nil
	}
	switch strings.TrimSpace(edit.Op) {
	case "new":
		if err := needKey(); err != nil {
			return nil, err
		}
		name, err := agentGroupName(edit.Name)
		if err != nil {
			return nil, err
		}
		if saved, ok := agentGroupFind(members, name); ok {
			return nil, fmt.Errorf("You already have a group called %s. Pick another name.", saved)
		}
		members[key] = name
	case "move":
		if err := needKey(); err != nil {
			return nil, err
		}
		if strings.TrimSpace(edit.Group) == "" {
			delete(members, key)
			break
		}
		group, err := existing(edit.Group)
		if err != nil {
			return nil, err
		}
		members[key] = group
	case "rename":
		group, err := existing(edit.Group)
		if err != nil {
			return nil, err
		}
		name, err := agentGroupName(edit.Name)
		if err != nil {
			return nil, err
		}
		if saved, ok := agentGroupFind(members, name); ok && !strings.EqualFold(saved, group) {
			return nil, fmt.Errorf("You already have a group called %s. Pick another name.", saved)
		}
		for member, current := range members {
			if current == group {
				members[member] = name
			}
		}
	case "ungroup":
		group, err := existing(edit.Group)
		if err != nil {
			return nil, err
		}
		for member, current := range members {
			if current == group {
				delete(members, member)
			}
		}
	default:
		return nil, fmt.Errorf("Choose new, move, rename or ungroup.")
	}
	if document.AgentGroups == nil {
		document.AgentGroups = map[string]map[string]string{}
	}
	if len(members) == 0 {
		delete(document.AgentGroups, project)
	} else {
		document.AgentGroups[project] = members
	}
	document.Version = productConversationRegistryVersion
	if err := store.writeDocument(ctx, path, document); err != nil {
		return nil, err
	}
	log.Printf("[AGENT_GROUPS] person=%s project=%s op=%s members=%d", userID, projectID, strings.TrimSpace(edit.Op), len(members))
	return members, nil
}

// agentGroupNames lists the groups' names in the order the menu shows them.
func agentGroupNames(members map[string]string) []string {
	seen := map[string]bool{}
	names := []string{}
	for _, group := range members {
		if !seen[group] {
			seen[group] = true
			names = append(names, group)
		}
	}
	sort.Slice(names, func(i, j int) bool { return strings.ToLower(names[i]) < strings.ToLower(names[j]) })
	return names
}

// handleAgentGroups reads (GET ?project_id=) or changes (POST agentGroupEdit + project_id) the caller's own groups
// of one Crew's agents: /api/agent-profiles/{id}/side-chats/groups. Both answer {"groups": {conversation key: name}}.
func (api *StreamingAPI) handleAgentGroups(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	var input struct {
		agentGroupEdit
		ProjectID string `json:"project_id"`
	}
	if r.Method == http.MethodPost {
		if err := json.NewDecoder(io.LimitReader(r.Body, maxAgentProfileRequestBytes)).Decode(&input); err != nil {
			writeAgentProfileError(w, http.StatusBadRequest, "send the project and the change to its groups")
			return
		}
	} else {
		input.ProjectID = r.URL.Query().Get("project_id")
	}
	projectID := strings.TrimSpace(input.ProjectID)
	if api.crewMacTwinForOwner(r, projectID) {
		if r.Method == http.MethodPost {
			writeAgentProfileError(w, http.StatusConflict, "Arrange this project's agents on your Mac.")
			return
		}
		writeAgentProfileJSON(w, http.StatusOK, map[string]any{"groups": map[string]string{}})
		return
	}
	userID, slot, ok := api.agentProfileConversationSlot(w, r, projectID)
	if !ok {
		return
	}
	if mux.Vars(r)["id"] != crewProfileID || !limitsSideChats(slot.profile.ID) {
		writeAgentProfileError(w, http.StatusBadRequest, "only a project's agents can be grouped")
		return
	}
	store := defaultProductConversationRegistryStore()
	var groups map[string]string
	var err error
	if r.Method == http.MethodPost {
		groups, err = store.editAgentGroups(r.Context(), userID, slot.profile.ID, projectID, input.agentGroupEdit)
		if err != nil {
			writeAgentProfileError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
	} else if groups, err = store.agentGroups(r.Context(), userID, slot.profile.ID, projectID); err != nil {
		writeAgentProfileError(w, http.StatusServiceUnavailable, "Groups are temporarily unavailable. Try again.")
		return
	}
	writeAgentProfileJSON(w, http.StatusOK, map[string]any{"groups": groups})
}
