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

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/platformtools"
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
	add("manage_crew_chats", "Crew chat tabs, as in the Crew screen. Owners see all owners' tabs; only the tab's owner can write, stop, rename, reset, close or change its model. message: send an attributed agent message to any owner’s tab, using the normal messaging path. Agents may also use sessions action=ask from their own tab. list: your main chat, side chats and earlier chats. read: one chat's messages (chat_id or session_id from list). new: archive your main chat and start a fresh one. side_open / side_close: open (up to "+strconv.Itoa(externalCrewMaxSideChats)+", name required) or close a side chat; side_rename: give a side chat a new name (chat_id, name); talk in it with workspace action=ask chat_id. set_model: give one chat (main or a side chat) its own model (engine, model_id, optional reasoning_effort), used by everything that reaches that chat; an empty engine returns it to the Crew's model; list shows each chat's own model. set_model also takes idle_compaction and, for a Claude chat, claude_terminal (true: the native Claude Code terminal; false: the chat view) for that chat; without engine only they change. list with chat_id adds that chat's context (tokens used of its window, its account's plan limits, the offered switches). compact: compact one of your chats now (summarise its conversation so the next message costs less); refused while it is busy. fresh: the chat's next message starts a new conversation that carries a short summary of this one; its history stays. delete: remove an earlier chat (not the live main chat). stop: stop the turn running in one of your chats. tools: list exact native presentation/background schemas. tool: execute tool_name with arguments in an explicit chat_id from list; only your live chat can be used. project_context: names of instructions and skills found in an own existing local folder, not a loaded claim. native_list: own Claude/Codex conversations for the exact folder and available original accounts. native_resume: reopen native_id from that list in a separate private tab; no turn starts. Human confirmations remain in the chat.", true, false, map[string]any{
		"crew_id":          externalString("Crew ID from workspace action=list."),
		"native_id":        externalString("Opaque id from native_list, required for native_resume. Owner-only local existing-folder Crews. Reopens in a separate private chat tab; no model turn starts until you send a message."),
		"action":           map[string]any{"type": "string", "enum": []any{"list", "read", "project_context", "native_list", "native_resume", "new", "side_open", "side_close", "side_rename", "set_model", "compact", "fresh", "delete", "stop", "message", "tools", "tool"}},
		"idle_compaction":  map[string]any{"type": "boolean", "description": "For set_model: Auto-compact when idle. " + idleCompactHint() + " On unless turned off. Omit to keep it."},
		"claude_terminal":  map[string]any{"type": "boolean", "description": "For set_model, a Claude chat only: true runs Claude Code as the native terminal, false as the chat view. Applies from the next message; the conversation continues. Omit to keep it."},
		"engine":           externalString("For set_model: a provider option of this Crew (as on its Models page, e.g. claude-code, codex-cli); empty returns the chat to the Crew's model."),
		"model_id":         externalString("For set_model: a model of that provider."),
		"connection_id":    externalString("For a chat model: your personal or available company account ID from provider_connections in the chats list. Only this chat uses it; empty engine restores the Crew default."),
		"reasoning_effort": externalString("For set_model: optional reasoning level."),
		"name":             externalString("A short tab name saying what the agent is for; required for side_open and side_rename."),
		"message":          externalString("For message: an attributed agent message to this tab, including another owner's tab."),
		"submission_id":    externalString("For message: stable ID for retries."),
		"inbox_id":         externalString("For message: existing reply address."),
		"chat_id":          externalString("main or a side chat id from list."),
		"tool_name":        externalString("Exact native name from tools."),
		"arguments":        map[string]any{"type": "object"},
		"session_id":       externalString("An earlier chat's session_id from list, for read or delete."),
	}, "crew_id", "action")
}

type externalCrewChat struct {
	OwnerID   string `json:"owner_id,omitempty"`
	OwnerName string `json:"owner_name,omitempty"`
	CanWrite  bool   `json:"can_write"`
	Purpose   string `json:"purpose,omitempty"`
	Shared    bool   `json:"shared,omitempty"`
	IsMain    bool   `json:"is_main"`
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
	owned, err := crewOwnerChatRecords(r.Context(), userID, product.ProfileID, workspaceTokenProjectID(crewID), workspace)
	if err != nil {
		return nil
	}
	chats := []externalCrewChat{}
	if !crewProjectOwnedByCaller(userID, workspace) {
		agents, err := crewShareAgents(r.Context(), userID, workspace, false)
		if err != nil {
			return nil
		}
		for _, agent := range agents {
			chats = append(chats, externalCrewChat{OwnerID: agent.tab.OwnerID, OwnerName: agent.OwnerName, ChatID: agent.ChatID, Name: agent.Name, Purpose: agent.Purpose, Shared: true, SessionID: agent.tab.Record.SessionID, UpdatedAt: agent.tab.Record.UpdatedAt, key: agent.ChatID})
		}
	}
	for _, tab := range owned {
		record := tab.Record
		// Dashboard user-view chats belong to that page, not the Builder tab list (as on the website, PLAT-750).
		if isUserViewConversationKey(record.ConversationKey) {
			continue
		}
		ownMain := tab.OwnerID == userID && (record.IsMainChat || record.ConversationKey == crewOwnerDefaultChatKey(crewID, userID))
		chat := externalCrewChat{OwnerID: tab.OwnerID, OwnerName: crewOwnerDisplayName(tab.OwnerID), CanWrite: tab.OwnerID == userID, SessionID: record.SessionID, UpdatedAt: record.UpdatedAt, key: record.ConversationKey, IsMain: ownMain}
		switch {
		case record.ConversationKey == crewID:
			chat.ChatID, chat.Name = "main", "Main chat"
		case strings.HasPrefix(record.ConversationKey, crewID+codeChatSideMarker):
			chat.ChatID = strings.TrimPrefix(record.ConversationKey, crewID+codeChatSideMarker)
			fallback := "Chat " + chat.ChatID
			if chat.IsMain {
				fallback = "Main chat"
			}
			chat.Name = firstNonEmptyTrimmed(tab.Identity.Name, fallback)
		default:
			continue
		}
		chat.Purpose = tab.Identity.Purpose
		if tab.Identity.Name != "" {
			chat.Name = tab.Identity.Name
		}
		chats = append(chats, chat)
	}
	sort.SliceStable(chats, func(i, j int) bool {
		if chats[i].IsMain != chats[j].IsMain {
			return chats[i].IsMain
		}
		return chats[i].UpdatedAt > chats[j].UpdatedAt
	})
	return chats
}

func findCrewChat(chats []externalCrewChat, id string) (externalCrewChat, bool) {
	for _, chat := range chats {
		if (id == "main" && chat.IsMain) || chat.ChatID == id || chat.SessionID == id {
			return chat, true
		}
	}
	return externalCrewChat{}, false
}

func crewChatActionScope(action, tool string, inputs ...map[string]any) string {
	scope := "crews:write"
	if action == "message" {
		scope = "crews:run"
	}
	if action == "list" || action == "read" || action == "tools" || action == "project_context" || action == "native_list" {
		scope = "crews:read"
	}
	if action == "tool" {
		var args map[string]any
		if len(inputs) > 0 {
			args = inputs[0]
		}
		identity := crewSessionToolIdentity(tool, args)
		for _, read := range []string{"list_ui_capabilities", "get_ui_state", "query_agent", "list_background_agents"} {
			if identity == platformtools.Identity(read) {
				scope = "crews:read"
			}
		}
	}
	return scope
}

func crewSessionToolIdentity(name string, args map[string]any) string {
	if platformtools.IsFamily(name) {
		return name + "." + externalArg(args, "action")
	}
	return platformtools.Identity(name)
}

func (api *StreamingAPI) externalCrewChatsCall(w http.ResponseWriter, r *http.Request, args map[string]any) {
	ctx := r.Context()
	claims := GetUserFromContext(ctx)
	crewID, action := externalArg(args, "crew_id"), externalArg(args, "action")
	if t := claims.AccessToken; t != nil {
		params, _ := args["arguments"].(map[string]any)
		scope := crewChatActionScope(action, externalArg(args, "tool_name"), params)
		if !t.Allows(scope) || !t.AllowsCrew(workspaceTokenProjectID(crewID)) {
			externalError(w, 403, "insufficient_scope", "This connection does not allow "+action+" on this Crew.")
			return
		}
	}
	if action == "project_context" || action == "native_list" || action == "native_resume" {
		localProjectRecognitionAction(api, w, r, args)
		return
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
	if action == "read" && !crew.OwnedByCaller {
		id := firstNonEmptyTrimmed(externalArg(args, "chat_id"), externalArg(args, "session_id"))
		if chat, found := findCrewChat(chats, id); found && chat.Shared {
			sharedArgs := make(map[string]any, len(args)+1)
			for key, value := range args {
				sharedArgs[key] = value
			}
			sharedArgs["chat_id"] = chat.key
			if api.externalSharedCrewConversation(w, r, "read_chat", sharedArgs, crew, manifest) {
				return
			}
		}
	}
	if action != "list" && action != "read" && action != "message" {
		if chat, found := findCrewChat(chats, externalArg(args, "chat_id")); found && !chat.CanWrite {
			externalError(w, 403, "tab_owner", crewOwnerDisplayName(chat.OwnerID)+" controls this tab. Use your own tab or send its agent a message.")
			return
		}
	}
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
		if !crew.OwnedByCaller {
			externalJSON(w, map[string]any{"crew_id": crewID, "chats": chats})
			return
		}
		var models struct {
			ChatModels []AgentProfileChatModel `json:"chat_models"`
		}
		if status, body := scheduleHandler(r, api.handleAgentProfileChatModels, http.MethodGet, "/api/agent-profiles/work/chat-models", vars, url.Values{"project_id": {crewID}}, nil); status < 400 {
			_ = json.Unmarshal(body, &models)
		}
		accounts, err := api.listProviderAccountViews(ctx, claims.UserID, false, providerAccountScope{Principal: claims.UserID, WorkspacePath: workspace, Product: manifest.Product})
		if err != nil {
			externalError(w, 500, "accounts_unavailable", "Provider accounts could not be loaded. Try again in Models.")
			return
		}
		choices, err := api.agentProfiles.Resolve(manifest.Product, 0, claims.UserID)
		if err != nil {
			externalError(w, 500, "models_unavailable", "Models could not be loaded.")
			return
		}
		choices = limitProfileModelsToAccount(ctx, claims.UserID, profileWithAvailableProviders(choices))
		out := map[string]any{"provider_connections": accounts, "provider_options": choices.Runtime.ProviderOptions, "crew_id": crewID, "chats": chats, "chat_models": models.ChatModels, "earlier": earlier(), "side_chats_left": max(0, externalCrewMaxSideChats-crewCallerSideChatCount(chats))}
		if chat, found := findCrewChat(chats, externalArg(args, "chat_id")); found && chat.CanWrite {
			if tab, own := chatContextTabForSession(ctx, productWorkspaceUserID(ctx), chat.SessionID); own {
				out["context"] = api.chatContextViewFor(productWorkspaceUserID(ctx), tab)
			}
		}
		externalJSON(w, out)
	case "message":
		chat, found := findCrewChat(chats, externalArg(args, "chat_id"))
		if !found {
			externalError(w, 404, "chat_not_found", "Choose a tab from list.")
			return
		}
		target := triggerTarget{Kind: triggerCallerCrew, Path: workspace, Label: chat.Name, CrewID: crewID, CrewProfile: manifest.Product, CrewOwner: chat.OwnerID, Chat: &codeChat{OwnerID: chat.OwnerID, ID: chat.ChatID, Key: chat.key, Name: chat.Name, SessionID: chat.SessionID}}
		api.externalSendMessage(w, r, args, target)
	case "set_model":
		chat, found := findCrewChat(chats, externalArg(args, "chat_id"))
		if !found {
			externalError(w, 404, "chat_not_found", "Pass main or a side chat's chat_id from list.")
			return
		}
		switches, argErr := chatContextArgs(args)
		if argErr != nil {
			externalError(w, 400, "invalid_arguments", argErr.Error())
			return
		}
		_, hasEngine := args["engine"]
		if !hasEngine && !switches.changesSwitch() {
			externalError(w, 400, "invalid_arguments", "Send an engine (empty uses the Crew's model), idle_compaction or claude_terminal.")
			return
		}
		if hasEngine {
			status, body := scheduleHandler(r, api.handleAgentProfileChatModels, http.MethodPost, "/api/agent-profiles/work/chat-models", vars, nil, map[string]string{
				"project_id": crewID, "conversation_key": chat.key, "engine": externalArg(args, "engine"),
				"model_id": externalArg(args, "model_id"), "reasoning_effort": externalArg(args, "reasoning_effort"), "connection_id": externalArg(args, "connection_id"),
			})
			if status >= 400 || !switches.changesSwitch() {
				respond(status, body)
				return
			}
		}
		api.externalChatContext(w, r, chat.SessionID, switches)
	case "compact", "fresh":
		chat, found := findCrewChat(chats, externalArg(args, "chat_id"))
		if !found {
			externalError(w, 404, "chat_not_found", "Pass main or a side chat's chat_id from list.")
			return
		}
		api.externalChatContext(w, r, chat.SessionID, chatContextRequest{Action: action})
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
		data, err = json.Marshal(api.nativeUsageResponse(r, sessionID, json.RawMessage(data)))
		if err != nil {
			externalError(w, 500, "chat_unavailable", "This chat could not be read.")
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
		if crewCallerSideChatCount(chats) >= externalCrewMaxSideChats {
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
		externalJSON(w, map[string]any{"crew_id": crewID, "chat_id": id, "name": name, "next": "Talk in it with workspace action=ask chat_id=" + id + "."})
	case "side_rename":
		chat, found := findCrewChat(chats, externalArg(args, "chat_id"))
		name, nameErr := externalChatName(args)
		if !found || chat.IsMain || nameErr != nil || name == "" {
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
		if !found || (action == "side_close" && chat.IsMain) {
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
		// The website's stop handler answers in plain text; MCP gets a JSON reply (PLAT-856-398).
		status, body := scheduleHandler(r, stop, http.MethodPost, "/api/session/stop", nil, nil, map[string]any{})
		if status >= 400 {
			respond(status, body)
			return
		}
		externalJSON(w, map[string]any{"crew_id": crewID, "chat_id": chat.ChatID, "stopped": true})
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
	if chatID == "" {
		chatID = "main"
	}
	chat, found := findCrewChat(api.crewChats(r, userID, crewID, workspace), chatID)
	if !found || !chat.CanWrite {
		return nil, false
	}
	return &codeChat{OwnerID: chat.OwnerID, IsMain: chat.IsMain, Key: chat.key, ID: chat.ChatID, Name: chat.Name, SessionID: chat.SessionID, UpdatedAt: chat.UpdatedAt}, true
}

func crewSideChatCount(chats []externalCrewChat) int {
	n := 0
	for _, chat := range chats {
		if !chat.IsMain {
			n++
		}
	}
	return n
}

func crewCallerSideChatCount(chats []externalCrewChat) int {
	n := 0
	for _, chat := range chats {
		if chat.CanWrite && !chat.IsMain {
			n++
		}
	}
	return n
}
