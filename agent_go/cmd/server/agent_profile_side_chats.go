package server

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
)

// AgentProfileSideChat is one open tab of a Code or Crew project as the server knows it.
type AgentProfileSideChat struct {
	TabOwnerID      string `json:"tab_owner_id,omitempty"`
	TabOwnerName    string `json:"tab_owner_name,omitempty"`
	CanWrite        bool   `json:"can_write"`
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
	// Pinned keeps the tab on the strip; the rest are in the Agents menu. Every tab starts pinned.
	Pinned bool `json:"pinned"`
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

// handlePinAgentProfileSideChat saves whether a side tab stays on the strip: POST
// /api/agent-profiles/{id}/side-chats/pin {"project_id": …, "conversation_key": …, "pinned": true}. It answers with the list.
func (api *StreamingAPI) handlePinAgentProfileSideChat(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	var input struct {
		ProjectID       string `json:"project_id"`
		ConversationKey string `json:"conversation_key"`
		Pinned          bool   `json:"pinned"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, maxAgentProfileRequestBytes)).Decode(&input); err != nil || strings.TrimSpace(input.ConversationKey) == "" {
		writeAgentProfileError(w, http.StatusBadRequest, "send the project, the tab and whether it is pinned")
		return
	}
	projectID := strings.TrimSpace(input.ProjectID)
	userID, slot, ok := api.agentProfileConversationSlot(w, r, projectID)
	if !ok {
		return
	}
	if !limitsSideChats(slot.profile.ID) {
		writeAgentProfileError(w, http.StatusBadRequest, "this product has no tabs to pin")
		return
	}
	if err := defaultProductConversationRegistryStore().setSideChatPinned(r.Context(), userID, slot.profile.ID, projectID, input.ConversationKey, input.Pinned); err != nil {
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
	owned, err := crewOwnerChatRecords(r.Context(), userID, slot.profile.ID, projectID, slot.binding.WorkspacePath)
	if err != nil {
		return nil, err
	}
	out := []AgentProfileSideChat{}
	for _, tab := range owned {
		record := tab.Record
		// Dashboard user-view chats belong to that page, not the Builder tab strip.
		if isUserViewConversationKey(record.ConversationKey) {
			continue
		}
		ownMain := tab.OwnerID == userID && (record.IsMainChat || record.ConversationKey == crewOwnerDefaultChatKey(projectID, userID))
		if ownMain || !isSideChatConversationKey(record.ConversationKey) || !workspacePathsMatchForUser(userID, record.WorkspacePath, slot.binding.WorkspacePath) {
			continue
		}
		id := strings.TrimPrefix(record.ConversationKey, projectID+codeChatSideMarker)
		name := firstNonEmptyTrimmed(tab.Identity.Name, "Chat "+id)
		row := AgentProfileSideChat{ConversationKey: record.ConversationKey, ConversationID: record.ConversationID, SessionID: record.SessionID, Name: name, Position: tab.Identity.Position, Pinned: !tab.Identity.Unpinned, TabOwnerID: tab.OwnerID, TabOwnerName: crewOwnerDisplayName(tab.OwnerID), CanWrite: tab.OwnerID == userID}
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
	ConnectionID    string `json:"connection_id,omitempty"`
	Engine          string `json:"engine,omitempty"`
	ModelID         string `json:"model_id,omitempty"`
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
	DisplayLabel    string `json:"display_label,omitempty"`
}

type AgentProfileEffectiveChatModel struct {
	AgentProfileChatModel
	Provider string `json:"provider"`
	OwnModel bool   `json:"own_model"`
	Error    string `json:"error,omitempty"`
}

// Resolve metadata with the same engine/model validator used for a turn.
// Only the current project choice and an explicit chat choice participate.
func effectiveProfileChatModel(profile agentprofiles.Profile, binding productConversationBinding, chat ProductConversationRecord, own ProductAgentIdentity) (AgentProfileEffectiveChatModel, error) {
	provider, model := resolveProfileRuntimeModel(profile.Runtime, "", "")
	connection, effort := "", ""
	if builder := presetPrimaryLLMForChat(binding.ProjectLLMConfig); builder != nil {
		provider, model, connection = builder.Provider, builder.ModelID, builder.ConnectionID
		effort, _ = builder.Options["reasoning_effort"].(string)
	}
	engine := ""
	for _, option := range profile.Runtime.ProviderOptions {
		if option.Provider == provider {
			engine = option.ID
			if effort == "" {
				effort, _ = option.Options["reasoning_effort"].(string)
			}
			break
		}
	}
	if own.Engine != "" {
		option, offered := findProviderOptionByID(profile.Runtime.ProviderOptions, own.Engine)
		if !offered {
			return AgentProfileEffectiveChatModel{}, fmt.Errorf("chat engine %q is unavailable", own.Engine)
		}
		if own.ConnectionID != "" {
			connection = own.ConnectionID
		} else if option.Provider != provider {
			connection = ""
		}
		engine, model, effort = own.Engine, own.ModelID, own.ReasoningEffort
	}
	if engine == "" {
		return AgentProfileEffectiveChatModel{}, fmt.Errorf("the project's model provider is unavailable")
	}
	// An explicit engine/model avoids the legacy runtime fallback. Keep the
	// current account binding: turn admission retains it only for this provider.
	query, err := queryRequestForAgentProfileChat(profile, AgentProfileChatRequest{Message: "Model metadata", Engine: engine, ModelID: model, ConnectionID: connection, ReasoningEffort: effort}, chat)
	if err != nil {
		return AgentProfileEffectiveChatModel{}, err
	}
	return AgentProfileEffectiveChatModel{
		AgentProfileChatModel: AgentProfileChatModel{ConversationKey: chat.ConversationKey, Engine: engine, ModelID: query.ModelID, ReasoningEffort: query.ReasoningEffort, ConnectionID: query.ConnectionID},
		Provider:              query.Provider, OwnModel: own.Engine != "",
	}, nil
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
	// Phase timings: a reasoning change on server C took 14 s with no log of where (PLAT-872-76).
	started := time.Now()
	userID, slot, ok := api.agentProfileConversationSlot(w, r, projectID)
	if !ok {
		return
	}
	slotDone := time.Now()
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
		if err := crewTabWriteError(r.Context(), userID, slot.profile.ID, key, slot.binding.WorkspacePath); err != nil {
			externalError(w, 403, "tab_owner", err.Error())
			return
		}
		if engine := strings.TrimSpace(input.Engine); engine != "" {
			option, offered := findProviderOptionByID(slot.profile.Runtime.ProviderOptions, engine)
			if !offered {
				writeAgentProfileError(w, http.StatusUnprocessableEntity, "that model provider is not available here")
				return
			}
			if connection := strings.TrimSpace(input.ConnectionID); connection != "" {
				if _, err := api.admitProviderAccount(r.Context(), providerAccountScope{Principal: userID, WorkspacePath: slot.binding.WorkspacePath, Product: slot.profile.Product}, option.Provider, connection); err != nil {
					writeAgentProfileError(w, http.StatusForbidden, err.Error())
					return
				}
			}
			// Refuse now what the next turn would refuse, so the chat keeps a model that runs.
			if model := strings.TrimSpace(input.ModelID); model != "" && !providerOptionOffersModel(option, currentCodingAgentModel(option.Provider, model)) {
				writeAgentProfileError(w, http.StatusUnprocessableEntity, "that model is not available for this coding agent")
				return
			}
			if effort := strings.TrimSpace(input.ReasoningEffort); effort != "" && !reasoningEffortOffered(option, effort) {
				writeAgentProfileError(w, http.StatusUnprocessableEntity, "that reasoning level is not available for this model")
				return
			}
		}
		if _, err := store.setAgentModel(r.Context(), userID, slot.profile.ID, key, input.Engine, input.ModelID, input.ReasoningEffort, input.ConnectionID, userID); err != nil {
			writeAgentProfileError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
	}
	saveDone := time.Now()
	allTabs, err := crewOwnerChatRecords(r.Context(), userID, slot.profile.ID, projectID, slot.binding.WorkspacePath)
	recordsDone := time.Now()
	if err != nil {
		writeAgentProfileError(w, 500, err.Error())
		return
	}
	identities := map[string]ProductAgentIdentity{}
	tabOwners := map[string]string{}
	for _, tab := range allTabs {
		identities[tab.Record.ConversationKey] = tab.Identity
		tabOwners[tab.Record.ConversationKey] = tab.OwnerID
	}
	out := []AgentProfileChatModel{}
	for key, identity := range identities {
		if tabOwners[key] != userID {
			identity.ConnectionID = ""
		}
		if identity.Engine != "" {
			out = append(out, AgentProfileChatModel{ConversationKey: key, Engine: identity.Engine, ModelID: identity.ModelID, ReasoningEffort: identity.ReasoningEffort, ConnectionID: identity.ConnectionID, DisplayLabel: chatModelDisplayLabel(slot.profile.Runtime, identity.Engine, identity.ModelID)})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ConversationKey < out[j].ConversationKey })
	effective := []AgentProfileEffectiveChatModel{}
	for _, tab := range allTabs {
		chat := tab.Record
		if publicationAgentID(chat.ConversationKey) != "" || !workspacePathsMatchForUser(userID, chat.WorkspacePath, slot.binding.WorkspacePath) {
			continue
		}
		row, err := effectiveProfileChatModel(slot.profile, slot.binding, chat, identities[chat.ConversationKey])
		if err == nil && tab.OwnerID == userID {
			query := QueryRequest{Provider: row.Provider, ModelID: row.ModelID, ConnectionID: row.ConnectionID}
			err = constrainProductChatModel(r.Context(), &query)
			row.ModelID = query.ModelID
		}
		if err != nil {
			// One unavailable selection must not hide other chats or prevent
			// opening Models to repair it. Do not invent a replacement runtime.
			effective = append(effective, AgentProfileEffectiveChatModel{AgentProfileChatModel: AgentProfileChatModel{ConversationKey: chat.ConversationKey, DisplayLabel: "Model unavailable"}, OwnModel: identities[chat.ConversationKey].Engine != "", Error: "Choose an available model in Models."})
			continue
		}
		if tab.OwnerID != userID {
			row.ConnectionID = ""
		}
		row.DisplayLabel = chatModelDisplayLabel(slot.profile.Runtime, row.Engine, row.ModelID)
		effective = append(effective, row)
	}
	log.Printf("[CHAT_MODELS] %s project=%s tabs=%d slot=%s save=%s records=%s rows=%s", r.Method, projectID, len(allTabs),
		slotDone.Sub(started).Round(time.Millisecond), saveDone.Sub(slotDone).Round(time.Millisecond),
		recordsDone.Sub(saveDone).Round(time.Millisecond), time.Since(recordsDone).Round(time.Millisecond))
	writeAgentProfileJSON(w, http.StatusOK, map[string]any{"chat_models": out, "effective_chat_models": effective, "can_open_models": slot.canBuild})
}
