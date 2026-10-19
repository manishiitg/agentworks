package server

import (
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
)

// AgentProfileSideChat is one open tab of a Code or Crew project as the server knows it.
type AgentProfileSideChat struct {
	ConversationKey string `json:"conversation_key"`
	ConversationID  string `json:"conversation_id,omitempty"`
	SessionID       string `json:"session_id"`
	Name            string `json:"name"`
	AgentID         string `json:"agent_id,omitempty"`
	Provider        string `json:"provider,omitempty"`
	ModelID         string `json:"model_id,omitempty"`
	ConnectionID    string `json:"connection_id,omitempty"`
	// Position is where the person put this tab in the strip (1 is first); 0 means not placed yet.
	Position int `json:"position,omitempty"`
}

// handleListAgentProfileSideChats lists the caller's open side tabs of one project: GET /api/agent-profiles/{id}/side-chats
// ?project_id=…. A tab another chat opened (open_project_chat, the code tool) exists only here until a browser asks, so
// the project screen reads this list and opens the tabs it does not have yet.
func (api *StreamingAPI) handleListAgentProfileSideChats(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	projectID := strings.TrimSpace(r.URL.Query().Get("project_id"))
	userID, slot, ok := api.agentProfileConversationSlot(w, r, projectID)
	if !ok {
		return
	}
	if !limitsSideChats(slot.profile.ID) {
		writeAgentProfileJSON(w, http.StatusOK, []AgentProfileSideChat{})
		return
	}
	out, err := api.sideChatsFor(r, userID, slot, projectID)
	if err != nil {
		writeAgentProfileError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeAgentProfileJSON(w, http.StatusOK, out)
}

// handleOrderAgentProfileSideChats saves the order the person dragged their side tabs into: POST
// /api/agent-profiles/{id}/side-chats/order {"project_id": …, "conversation_keys": [first, …]}. It answers with the
// list in its new order, which the screen shows as it is.
func (api *StreamingAPI) handleOrderAgentProfileSideChats(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	var input struct {
		ProjectID        string   `json:"project_id"`
		ConversationKeys []string `json:"conversation_keys"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, maxAgentProfileRequestBytes)).Decode(&input); err != nil || len(input.ConversationKeys) > externalCrewMaxSideChats {
		writeAgentProfileError(w, http.StatusBadRequest, "send the project and its tabs in their new order")
		return
	}
	projectID := strings.TrimSpace(input.ProjectID)
	userID, slot, ok := api.agentProfileConversationSlot(w, r, projectID)
	if !ok {
		return
	}
	if !limitsSideChats(slot.profile.ID) {
		writeAgentProfileError(w, http.StatusBadRequest, "this product has no tabs to order")
		return
	}
	if err := defaultProductConversationRegistryStore().setSideChatOrder(r.Context(), userID, slot.profile.ID, projectID, input.ConversationKeys); err != nil {
		writeAgentProfileError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	out, err := api.sideChatsFor(r, userID, slot, projectID)
	if err != nil {
		writeAgentProfileError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeAgentProfileJSON(w, http.StatusOK, out)
}

// sideChatsFor lists the caller's open side tabs of one project in the order the person arranged them; tabs they
// have not placed follow, oldest key first.
func (api *StreamingAPI) sideChatsFor(r *http.Request, userID string, slot profileAndBinding, projectID string) ([]AgentProfileSideChat, error) {
	store := defaultProductConversationRegistryStore()
	records, err := store.projectChats(r.Context(), userID, slot.profile.ID, projectID)
	if err != nil {
		return nil, err
	}
	identities, _ := store.agentIdentities(r.Context(), userID, slot.profile.ID, projectID)
	titles := map[string]string{}
	for id, session := range chatHistorySessionsByID(userID, slot.binding.WorkspacePath) {
		titles[id] = strings.TrimSpace(session.Title)
	}
	out := []AgentProfileSideChat{}
	for _, record := range records {
		if record.IsMainChat || !isSideChatConversationKey(record.ConversationKey) || !workspacePathsMatchForUser(userID, record.WorkspacePath, slot.binding.WorkspacePath) {
			continue
		}
		id := strings.TrimPrefix(record.ConversationKey, projectID+codeChatSideMarker)
		name := firstNonEmptyTrimmed(identities[record.ConversationKey].Name, titles[record.SessionID], "Chat "+id)
		row := AgentProfileSideChat{ConversationKey: record.ConversationKey, ConversationID: record.ConversationID, SessionID: record.SessionID, Name: name, Position: identities[record.ConversationKey].Position}
		if agentID := publicationAgentID(record.ConversationKey); agentID != "" {
			if p, e := readPublishedAgent(contextWithoutPublication(r.Context()), agentProfileRuntimeWorkspace(userID, slot.binding.WorkspacePath), agentID); e == nil && p != nil {
				row.AgentID, row.Provider, row.ModelID, row.ConnectionID = p.AgentID, p.Provider, p.ModelID, p.ConnectionID
				row.Name = p.Name
			}
		}
		out = append(out, row)
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i].Position, out[j].Position
		if (a > 0) != (b > 0) {
			return a > 0
		}
		if a != b {
			return a < b
		}
		return out[i].ConversationKey < out[j].ConversationKey
	})
	return out, nil
}

// AgentProfileChatModel is one chat's own model on the Crew's Models page; an empty Engine follows the Crew's model.
type AgentProfileChatModel struct {
	ConversationKey string `json:"conversation_key"`
	Engine          string `json:"engine,omitempty"`
	ModelID         string `json:"model_id,omitempty"`
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
	DisplayLabel    string `json:"display_label,omitempty"`
}

// Presentation only: the provider catalog owns readable names; model IDs and
// the runtime selection sent to a turn stay unchanged.
func chatModelDisplayLabel(runtime agentprofiles.RuntimePolicy, engine, modelID string) string {
	provider := engine
	for _, option := range runtime.ProviderOptions {
		if option.ID == engine || option.Provider == engine {
			provider = option.Provider
			if modelID == "" {
				modelID = option.ModelID
			}
			break
		}
	}
	label := providerDisplayLabel(provider)
	switch provider {
	case "cursor-cli":
		label = "Cursor"
	case "claude-code":
		label = "Claude"
	case "codex-cli":
		label = "Codex"
	case "pi-cli":
		label = "Pi"
	}
	model := modelNameForProviderModel(provider, modelID)
	if strings.EqualFold(modelID, "auto") {
		model = "auto"
	}
	if provider == "claude-code" {
		model = strings.TrimPrefix(model, "Claude ")
	}
	model = strings.TrimSuffix(model, " (Cursor Agent CLI)")
	if model == "" {
		model = "default"
	}
	if label == "" {
		return model
	}
	return label + " · " + model
}

// handleAgentProfileChatModels lists (GET ?project_id=) or sets (POST) the model of each chat of one project: the
// main chat and its tabs. Only the person whose chats they are changes them, by hand (owner, 2026-10-19).
func (api *StreamingAPI) handleAgentProfileChatModels(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	var input AgentProfileChatModel
	projectID := strings.TrimSpace(r.URL.Query().Get("project_id"))
	if r.Method == http.MethodPost {
		var body struct {
			ProjectID string `json:"project_id"`
			AgentProfileChatModel
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, maxAgentProfileRequestBytes)).Decode(&body); err != nil {
			writeAgentProfileError(w, http.StatusBadRequest, "send the project, the chat and its model")
			return
		}
		projectID, input = strings.TrimSpace(body.ProjectID), body.AgentProfileChatModel
	}
	userID, slot, ok := api.agentProfileConversationSlot(w, r, projectID)
	if !ok {
		return
	}
	store := defaultProductConversationRegistryStore()
	if r.Method == http.MethodPost {
		key := strings.TrimSpace(input.ConversationKey)
		if key != projectID && !strings.HasPrefix(key, projectID+codeChatSideMarker) {
			writeAgentProfileError(w, http.StatusBadRequest, "that chat is not part of this project")
			return
		}
		if key == projectID {
			key = slot.binding.ConversationKey
		}
		if engine := strings.TrimSpace(input.Engine); engine != "" {
			if _, offered := findProviderOptionByID(slot.profile.Runtime.ProviderOptions, engine); !offered {
				writeAgentProfileError(w, http.StatusUnprocessableEntity, "that model provider is not available here")
				return
			}
		}
		if _, err := store.setAgentModel(r.Context(), userID, slot.profile.ID, key, input.Engine, input.ModelID, input.ReasoningEffort, userID); err != nil {
			writeAgentProfileError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
	}
	identities, err := store.agentIdentities(r.Context(), userID, slot.profile.ID, projectID)
	if err != nil {
		writeAgentProfileError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := []AgentProfileChatModel{}
	for key, identity := range identities {
		if identity.Engine != "" {
			out = append(out, AgentProfileChatModel{ConversationKey: key, Engine: identity.Engine, ModelID: identity.ModelID, ReasoningEffort: identity.ReasoningEffort, DisplayLabel: chatModelDisplayLabel(slot.profile.Runtime, identity.Engine, identity.ModelID)})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ConversationKey < out[j].ConversationKey })
	provider, modelID := resolveProfileRuntimeModel(slot.profile.Runtime, "", "")
	if builder := presetPrimaryLLMForChat(slot.binding.ProjectLLMConfig); builder != nil {
		provider, modelID = builder.Provider, builder.ModelID
	} else if slot.binding.ProjectLLMConfig == nil {
		// Match turn admission for older projects: their last bound runtime
		// wins until the project configuration is backfilled.
		current, found, _, err := store.history(r.Context(), userID, slot.profile, slot.binding)
		if err != nil {
			writeAgentProfileError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if found && strings.TrimSpace(current.Provider) != "" {
			provider, modelID = current.Provider, current.ModelID
		}
	}
	writeAgentProfileJSON(w, http.StatusOK, map[string]any{"chat_models": out, "default_model_label": chatModelDisplayLabel(slot.profile.Runtime, provider, modelID)})
}
