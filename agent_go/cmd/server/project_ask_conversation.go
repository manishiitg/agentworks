package server

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
)

// Resolve the current person's ordinary project conversation. Dashboard pages
// supply a project, never another person's session, profile or Builder tab.
// Sending remains on the normal chat submission/queue and admission path.
func (api *StreamingAPI) handleProjectAskConversation(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	claims := GetUserFromContext(r.Context())
	if claims == nil || claims.UserID == "" || claims.Scope != "" || claims.Provider == "bot_route" {
		externalError(w, 401, "authentication_required", "Sign in to ask this app.")
		return
	}
	var body struct {
		Workspace string `json:"workspace"`
		AgentKey  string `json:"agent_key,omitempty"`
		AsUser    bool   `json:"as_user,omitempty"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&body) != nil || decoder.Decode(new(any)) != io.EOF {
		externalError(w, 400, "invalid_arguments", "Supply the project's workspace.")
		return
	}
	target, err := api.dashboardTarget(r.Context(), claims, body.Workspace)
	if err != nil {
		dashboardError(w, err)
		return
	}
	product := map[string]string{"crew": "work", "workflow": "agentworks", "relay": "relays"}[target.Kind]
	if !userAllowedProduct(claims, product) {
		externalError(w, 404, "not_found", "This app is unavailable.")
		return
	}
	if token := claims.AccessToken; token != nil {
		scope := "runs:execute"
		if target.Kind == "crew" {
			scope = "crews:run"
		}
		if !token.Allows(scope) || token.BoundAgentID != "" || token.FileGuard != nil || token.ProjectKind != "" {
			externalError(w, 403, "insufficient_scope", "Open the app with a connection that allows its chat.")
			return
		}
	}
	var record ProductConversationRecord
	mode, profileID := "run", ""
	switch target.Kind {
	case "crew":
		if api.agentProfiles == nil {
			externalError(w, 503, "unavailable", "Chat is unavailable.")
			return
		}
		project, _, ok := projectProductForPath(target.Root)
		if !ok {
			externalError(w, 404, "not_found", "This app is unavailable.")
			return
		}
		profile, resolveErr := api.agentProfiles.Resolve(project.ProfileID, 0, claims.UserID)
		if resolveErr != nil {
			externalError(w, 404, "not_found", "This app is unavailable.")
			return
		}
		profileID = profile.ID
		type choice struct {
			Key  string `json:"key"`
			Name string `json:"name"`
		}
		choices := []choice{}
		if target.Edit && !body.AsUser {
			mode = "builder"
			tabs, e := defaultProductConversationRegistryStore().projectChats(r.Context(), claims.UserID, profile.ID, target.ID)
			if e != nil {
				externalError(w, 503, "agents_unavailable", "Could not load this Crew's agents.")
				return
			}
			identities, e := defaultProductConversationRegistryStore().agentIdentities(r.Context(), claims.UserID, profile.ID, target.ID)
			if e != nil {
				externalError(w, 503, "agents_unavailable", "Could not load this Crew's agents.")
				return
			}
			for _, tab := range tabs {
				if ordinaryProjectChatKey(target.ID, tab.ConversationKey) {
					choices = append(choices, choice{tab.ConversationKey, firstNonEmptyTrimmed(identities[tab.ConversationKey].Name, tab.Title, "Agent")})
				}
			}
		} else {
			agents, e := crewShareAgents(r.Context(), claims.UserID, target.Root, false)
			if e != nil {
				externalError(w, 503, "agents_unavailable", "Could not load this Crew's shared agents.")
				return
			}
			for _, agent := range agents {
				choices = append(choices, choice{agent.ChatID, agent.Name})
			}
		}
		key, name := strings.TrimSpace(body.AgentKey), ""
		if key == "" {
			candidate := crewOwnerMainChatKey(target.ID)
			for _, item := range choices {
				if item.Key == candidate {
					key = candidate
				}
			}
		}
		if key == "" {
			message := "Choose an agent to answer."
			if len(choices) == 0 {
				if target.Edit && !body.AsUser {
					message = "Create an agent in this Crew before asking a question."
				} else {
					message = "The owner has not shared an agent for this Crew."
				}
			}
			externalJSON(w, map[string]any{"choose_agent": true, "agents": choices, "message": message})
			return
		}
		allowed := false
		for _, item := range choices {
			if item.Key == key {
				allowed = true
			}
		}
		if !allowed {
			externalError(w, 404, "agent_unavailable", "Choose an agent shared with you in this Crew.")
			return
		}
		if target.Edit && body.AsUser {
			key = target.ID + codeChatSideMarker + "asuser-" + strings.TrimPrefix(key, target.ID+codeChatSideMarker)
			name = "As a user"
		}

		// An unrelated active tab's transport headers cannot choose this chat.
		nativeRequest := r.Clone(r.Context())
		nativeRequest.Header = r.Header.Clone()
		nativeRequest.Header.Del("X-Session-ID")
		nativeRequest.Header.Del("X-Conversation-Continuation")
		record, err = api.resolveAgentProfileConversation(nativeRequest, profile, key, name)
	case "workflow", "relay":
		access, manifest := workflowAccessForWorkspacePath(r.Context(), claims, target.Root)
		if manifest == nil || access == WorkflowAccessNone {
			externalError(w, 404, "not_found", "This app is unavailable.")
			return
		}
		if access == WorkflowAccessOwner || access == WorkflowAccessWrite {
			mode = "builder"
			var tab goalOwnerTab
			tab, err = api.createGoalOwnerTab(r.Context(), workflowBuilderScope{Workspace: target.Root, PresetID: target.ID}, "Builder", true, "")
			record = ProductConversationRecord{SessionID: tab.SessionID, ConversationKey: tab.ID, WorkspacePath: target.Root, ResourceID: target.ID}
		} else {
			// The registry is per person, just as Crew chats are. No shared Builder
			// history is selected or copied into this recipient's conversation.
			record, err = defaultProductConversationRegistryStore().resolveOrCreate(r.Context(), claims.UserID, agentprofiles.Profile{ID: goalChatProfile, Version: 1}, productConversationBinding{ConversationKey: target.ID, ResourceID: target.ID, WorkspacePath: target.Root, Title: "Chat"}, "")
		}
	default:
		externalError(w, 404, "not_found", "Chat is unavailable for this app.")
		return
	}
	if err != nil {
		log.Printf("[PROJECT_ASK_CHAT] actor=%s product=%s project=%s outcome=refused reason=%q", claims.UserID, target.Kind, target.ID, err.Error())
		externalError(w, 409, "conversation_unavailable", "Could not open this app's chat. Reopen it and try again.")
		return
	}

	// The ordinary Crew resolver may return another owner's shared tab for a
	// view request. This send adapter accepts only a record in this caller's
	// registry, and never changes a session's recorded owner.
	registryProfile := profileID
	if registryProfile == "" {
		registryProfile = goalChatProfile
	}
	own, found, _, lookupErr := defaultProductConversationRegistryStore().history(r.Context(), claims.UserID, agentprofiles.Profile{ID: registryProfile}, productConversationBinding{ConversationKey: record.ConversationKey})
	if lookupErr != nil || !found || own.SessionID != record.SessionID || own.ResourceID != target.ID || own.WorkspacePath != target.Root {
		externalError(w, 403, "own_chat_required", "Use your own chat.")
		return
	}
	if api.eventStore != nil {
		if owner := api.eventStore.GetSessionOwner(record.SessionID); owner != "" && owner != claims.UserID {
			externalError(w, 403, "own_chat_required", "Use your own chat.")
			return
		}
	}

	log.Printf("[PROJECT_ASK_CHAT] actor=%s product=%s project=%s mode=%s session=%s", claims.UserID, target.Kind, target.ID, mode, record.SessionID)
	externalJSON(w, map[string]any{"product": target.Kind, "project_id": target.ID, "workspace": target.Root, "mode": mode, "profile_id": profileID, "conversation": record})
}
