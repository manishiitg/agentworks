package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

// A person's own chats with a Crew over MCP, through the Crew screen's own
// conversation routes: list, read, start a new main chat, open or close side
// chats, delete an earlier chat, stop a running turn. Only the caller's own
// chats: the registry and chat history are per person.

// externalCrewMaxSideChats is the number of side chats (tabs) one Code project or one Crew may
// have besides its main chat. The app shows the same number (WORK_SIDE_CHAT_LIMIT in
// frontend/src/products/work/workTabs.ts); change both together. The server enforces it where a
// side chat is created (handleResolveAgentProfileConversation), so the app, MCP and a second
// device cannot go past it.
const externalCrewMaxSideChats = 20

// sideChatsOf counts the side chats among a project's chats without assuming the main chat is
// in the list.
func sideChatsOf(ids []string, main string) int {
	n := 0
	for _, id := range ids {
		if id != main {
			n++
		}
	}
	return n
}

// externalChatName validates the name an agent gives a new or renamed tab; a tab always has one.
func externalChatName(args map[string]any) (string, error) {
	name := strings.TrimSpace(externalArg(args, "name"))
	if name == "" {
		return "", fmt.Errorf("give the tab a name, a few words saying what this agent is for")
	}
	return normalizeChatHistoryTitle(name)
}

func externalCrewChatDefinitions(add func(string, string, bool, bool, map[string]any, ...string)) {
	add("manage_crew_chats", "Your own chats with a Crew, as in the Crew screen. list: your main chat, side chats and earlier chats. read: one chat's messages (chat_id or session_id from list). new: archive your main chat and start a fresh one. side_open / side_close: open (up to "+strconv.Itoa(externalCrewMaxSideChats)+", name required) or close a side chat; side_rename: give a side chat a new name (chat_id, name); talk in it with ask_crew chat_id. set_model: give one chat (main or a side chat) its own model (engine, model_id, optional reasoning_effort), used by everything that reaches that chat; an empty engine returns it to the Crew's model; list shows each chat's own model. delete: remove an earlier chat (not the live main chat). stop: stop the turn running in one of your chats. tools: list exact native presentation/background schemas. tool: execute tool_name with arguments in an explicit chat_id from list; only your live chat can be used. Human confirmations remain in the chat.", true, false, map[string]any{
		"crew_id":          externalString("Crew ID from list_crews."),
		"action":           map[string]any{"type": "string", "enum": []any{"list", "read", "new", "side_open", "side_close", "side_rename", "set_model", "delete", "stop", "tools", "tool"}},
		"engine":           externalString("For set_model: a provider option of this Crew (as on its Models page, e.g. claude-code, codex-cli); empty returns the chat to the Crew's model."),
		"model_id":         externalString("For set_model: a model of that provider."),
		"reasoning_effort": externalString("For set_model: optional reasoning level."),
		"name":             externalString("A short tab name saying what the agent is for; required for side_open and side_rename."),
		"chat_id":          externalString("main or a side chat id from list."),
		"tool_name":        externalString("Exact native name from tools."),
		"arguments":        map[string]any{"type": "object"},
		"session_id":       externalString("An earlier chat's session_id from list, for read or delete."),
	}, "crew_id", "action")
}

type externalCrewChat struct {
	ChatID    string `json:"chat_id"`
	Name      string `json:"name"`
	SessionID string `json:"session_id"`
	UpdatedAt string `json:"updated_at,omitempty"`
	key       string
}

// crewChats lists the caller's live chats with a Crew: main first.
func (api *StreamingAPI) crewChats(r *http.Request, userID, crewID, workspace string) []externalCrewChat {
	product, _, ok := projectProductForPath(workspace)
	if !ok {
		return nil
	}
	records, err := defaultProductConversationRegistryStore().projectChats(r.Context(), userID, product.ProfileID, workspaceTokenProjectID(crewID))
	if err != nil {
		return nil
	}
	titles := map[string]string{}
	if sessions, listErr := ListChatHistorySessions(userID, 200, 0, workspace); listErr == nil {
		for _, session := range sessions {
			titles[session.SessionID] = strings.TrimSpace(session.Title)
		}
	}
	identities, _ := defaultProductConversationRegistryStore().agentIdentities(r.Context(), userID, product.ProfileID, workspaceTokenProjectID(crewID))
	chats := []externalCrewChat{}
	for _, record := range records {
		chat := externalCrewChat{SessionID: record.SessionID, UpdatedAt: record.UpdatedAt, key: record.ConversationKey}
		switch {
		case record.ConversationKey == crewID:
			chat.ChatID, chat.Name = "main", "Main chat"
		case strings.HasPrefix(record.ConversationKey, crewID+codeChatSideMarker):
			chat.ChatID = strings.TrimPrefix(record.ConversationKey, crewID+codeChatSideMarker)
			chat.Name = firstNonEmptyTrimmed(identities[record.ConversationKey].Name, titles[record.SessionID], "Chat "+chat.ChatID)
		default:
			continue
		}
		chats = append(chats, chat)
	}
	sort.SliceStable(chats, func(i, j int) bool {
		if (chats[i].ChatID == "main") != (chats[j].ChatID == "main") {
			return chats[i].ChatID == "main"
		}
		return chats[i].UpdatedAt > chats[j].UpdatedAt
	})
	return chats
}

func findCrewChat(chats []externalCrewChat, id string) (externalCrewChat, bool) {
	for _, chat := range chats {
		if chat.ChatID == id || chat.SessionID == id {
			return chat, true
		}
	}
	return externalCrewChat{}, false
}

func (api *StreamingAPI) externalCrewChatsCall(w http.ResponseWriter, r *http.Request, args map[string]any) {
	ctx := r.Context()
	claims := GetUserFromContext(ctx)
	crewID, action := externalArg(args, "crew_id"), externalArg(args, "action")
	if t := claims.AccessToken; t != nil {
		scope := "crews:run"
		if action == "list" || action == "read" || action == "tools" {
			scope = "crews:read"
		}
		if action == "tool" {
			switch externalArg(args, "tool_name") {
			case "list_ui_capabilities", "get_ui_state", "query_agent", "list_agents":
				scope = "crews:read"
			case "run_in_background":
				scope = "crews:write"
			}
		}
		if !t.Allows(scope) || !t.AllowsCrew(workspaceTokenProjectID(crewID)) {
			externalError(w, 403, "insufficient_scope", "This connection does not allow "+action+" on this Crew.")
			return
		}
	}
	crew, manifest, _, ok := api.externalCrewResolve(ctx, claims, crewID)
	if !ok {
		externalError(w, 404, "not_found", "Crew not found or not allowed for this connection.")
		return
	}
	if action == "tools" || action == "tool" {
		api.externalCrewSessionToolsCall(w, r, args, crew)
		return
	}
	crewID = manifest.ID
	workspace := crew.Binding.WorkspacePath
	chats := api.crewChats(r, claims.UserID, crewID, workspace)
	respond := func(status int, body []byte) { externalScheduleRespond(w, status, body) }
	vars := map[string]string{"id": manifest.Product}
	earlier := func() []map[string]any {
		status, body := scheduleHandler(r, api.handleListAgentProfileConversations, http.MethodGet, "/api/agent-profiles/work/conversations", vars, url.Values{"conversation_key": {crewID}}, nil)
		if status >= 400 {
			return nil
		}
		var page struct {
			Previous []map[string]any `json:"previous"`
		}
		_ = json.Unmarshal(body, &page)
		return page.Previous
	}
	switch action {
	case "list":
		var models struct {
			ChatModels []AgentProfileChatModel `json:"chat_models"`
		}
		if status, body := scheduleHandler(r, api.handleAgentProfileChatModels, http.MethodGet, "/api/agent-profiles/work/chat-models", vars, url.Values{"project_id": {crewID}}, nil); status < 400 {
			_ = json.Unmarshal(body, &models)
		}
		externalJSON(w, map[string]any{"crew_id": crewID, "chats": chats, "chat_models": models.ChatModels, "earlier": earlier(), "side_chats_left": max(0, externalCrewMaxSideChats-crewSideChatCount(chats))})
	case "set_model":
		chat, found := findCrewChat(chats, externalArg(args, "chat_id"))
		if !found {
			externalError(w, 404, "chat_not_found", "Pass main or a side chat's chat_id from list.")
			return
		}
		respond(scheduleHandler(r, api.handleAgentProfileChatModels, http.MethodPost, "/api/agent-profiles/work/chat-models", vars, nil, map[string]string{
			"project_id": crewID, "conversation_key": chat.key, "engine": externalArg(args, "engine"),
			"model_id": externalArg(args, "model_id"), "reasoning_effort": externalArg(args, "reasoning_effort"),
		}))
	case "read":
		sessionID := externalArg(args, "session_id")
		if chatID := externalArg(args, "chat_id"); chatID != "" {
			chat, found := findCrewChat(chats, chatID)
			if !found {
				externalError(w, 404, "chat_not_found", "No live chat "+chatID+"; use action list.")
				return
			}
			sessionID = chat.SessionID
		} else {
			// An earlier chat must be one of this person's chats with this Crew.
			known := false
			for _, item := range earlier() {
				if id, _ := item["session_id"].(string); id != "" && id == sessionID {
					known = true
				}
			}
			if _, live := findCrewChat(chats, sessionID); !known && !live {
				externalError(w, 404, "chat_not_found", "No chat with that session_id; use action list.")
				return
			}
		}
		data, err := ReadChatHistoryConversation(claims.UserID, sessionID, workspace)
		if err != nil {
			externalError(w, 404, "chat_not_found", "The chat's history is unavailable.")
			return
		}
		if len(data) > externalRunPageBytes {
			externalJSON(w, map[string]any{"session_id": sessionID, "truncated": true, "note": "Only the newest part is shown.", "conversation_tail": string(data[len(data)-externalRunPageBytes:])})
			return
		}
		respond(http.StatusOK, data)
	case "new":
		respond(scheduleHandler(r, api.handleRotateAgentProfileConversation, http.MethodPost, "/api/agent-profiles/work/conversation/new", vars, nil, AgentProfileConversationRequest{ConversationKey: crewID}))
	case "side_open":
		if crewSideChatCount(chats) >= externalCrewMaxSideChats {
			externalError(w, 409, "too_many_chats", fmt.Sprintf("You already have %d side chats with this Crew; close one first.", externalCrewMaxSideChats))
			return
		}
		name, nameErr := externalChatName(args)
		if nameErr != nil {
			externalError(w, 400, "invalid_name", nameErr.Error())
			return
		}
		raw := make([]byte, 4)
		_, _ = rand.Read(raw)
		id := hex.EncodeToString(raw)
		status, body := scheduleHandler(r, api.handleResolveAgentProfileConversation, http.MethodPost, "/api/agent-profiles/work/conversation", vars, nil, AgentProfileConversationRequest{ConversationKey: crewID + codeChatSideMarker + id, Name: name})
		if status >= 400 {
			respond(status, body)
			return
		}
		externalJSON(w, map[string]any{"crew_id": crewID, "chat_id": id, "name": name, "next": "Talk in it with ask_crew chat_id=" + id + "."})
	case "side_rename":
		chat, found := findCrewChat(chats, externalArg(args, "chat_id"))
		name, nameErr := externalChatName(args)
		if !found || chat.ChatID == "main" || nameErr != nil || name == "" {
			externalError(w, 404, "chat_not_found", "Pass a side chat's chat_id from list and a name up to 120 characters.")
			return
		}
		if err := RenameChatHistorySession(claims.UserID, chat.SessionID, workspace, name); err != nil {
			externalError(w, 502, "rename_failed", "The chat could not be renamed.")
			return
		}
		if err := setCodeTabName(ctx, claims.UserID, chat.key, name, manifest.Product); err != nil {
			externalError(w, 502, "rename_failed", "The chat could not be renamed.")
			return
		}
		externalJSON(w, map[string]any{"crew_id": crewID, "chat_id": chat.ChatID, "name": name})
	case "side_close", "stop":
		chat, found := findCrewChat(chats, externalArg(args, "chat_id"))
		if !found || (action == "side_close" && chat.ChatID == "main") {
			externalError(w, 404, "chat_not_found", "Pass a chat_id from list (side_close: a side chat).")
			return
		}
		if action == "side_close" {
			respond(scheduleHandler(r, api.handleCloseAgentProfileSideChat, http.MethodPost, "/api/agent-profiles/work/conversation/close", vars, nil, AgentProfileConversationRequest{ConversationKey: chat.key}))
			return
		}
		if !api.sessionStartedBy(chat.SessionID, claims.UserID) {
			externalError(w, 409, "not_running", "Nothing is running in that chat.")
			return
		}
		stop := func(w2 http.ResponseWriter, r2 *http.Request) {
			r2.Header.Set("X-Session-ID", chat.SessionID)
			api.handleStopSession(w2, r2)
		}
		respond(scheduleHandler(r, stop, http.MethodPost, "/api/session/stop", nil, nil, map[string]any{}))
	case "delete":
		sessionID := externalArg(args, "session_id")
		if sessionID == "" {
			externalError(w, 400, "invalid_arguments", "delete needs the session_id of an earlier chat.")
			return
		}
		respond(scheduleHandler(r, api.handleDeleteAgentProfileConversation, http.MethodDelete, "/api/agent-profiles/work/conversations/"+url.PathEscape(sessionID), map[string]string{"id": manifest.Product, "session_id": sessionID}, url.Values{"conversation_key": {crewID}}, nil))
	default:
		externalError(w, 400, "invalid_arguments", "Unknown action.")
	}
}

// crewAskChat resolves ask_crew's optional chat_id to one of the caller's
// live chats with the Crew.
func (api *StreamingAPI) crewAskChat(r *http.Request, userID, crewID, workspace, chatID string) (*codeChat, bool) {
	if chatID == "" || chatID == "main" {
		return nil, true
	}
	chat, found := findCrewChat(api.crewChats(r, userID, crewID, workspace), chatID)
	if !found {
		return nil, false
	}
	return &codeChat{Key: chat.key, ID: chat.ChatID, Name: chat.Name, SessionID: chat.SessionID, UpdatedAt: chat.UpdatedAt}, true
}

func crewSideChatCount(chats []externalCrewChat) int {
	ids := make([]string, len(chats))
	for i, c := range chats {
		ids[i] = c.ChatID
	}
	return sideChatsOf(ids, "main")
}
