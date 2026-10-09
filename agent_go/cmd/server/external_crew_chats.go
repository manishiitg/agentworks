package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

// A person's own chats with a Crew over MCP, through the Crew screen's own
// conversation routes: list, read, start a new main chat, open or close side
// chats, delete an earlier chat, stop a running turn. Only the caller's own
// chats: the registry and chat history are per person.

const externalCrewMaxSideChats = 4

func externalCrewChatDefinitions(add func(string, string, bool, bool, map[string]any, ...string)) {
	add("manage_crew_chats", "Your own chats with a Crew, as in the Crew screen. list: your main chat, side chats and earlier chats. read: one chat's messages (chat_id or session_id from list). new: archive your main chat and start a fresh one. side_open / side_close: open (up to 4) or close a side chat; talk in it with ask_crew chat_id. delete: remove an earlier chat (not the live main chat). stop: stop the turn running in one of your chats.", true, false, map[string]any{
		"crew_id":    externalString("Crew ID from list_crews."),
		"action":     map[string]any{"type": "string", "enum": []any{"list", "read", "new", "side_open", "side_close", "delete", "stop"}},
		"chat_id":    externalString("main or a side chat id from list."),
		"session_id": externalString("An earlier chat's session_id from list, for read or delete."),
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
	records, err := defaultProductConversationRegistryStore().projectChats(r.Context(), userID, "work", crewID)
	if err != nil {
		return nil
	}
	titles := map[string]string{}
	if sessions, listErr := ListChatHistorySessions(userID, 200, 0, workspace); listErr == nil {
		for _, session := range sessions {
			titles[session.SessionID] = strings.TrimSpace(session.Title)
		}
	}
	chats := []externalCrewChat{}
	for _, record := range records {
		chat := externalCrewChat{SessionID: record.SessionID, UpdatedAt: record.UpdatedAt, key: record.ConversationKey}
		switch {
		case record.ConversationKey == crewID:
			chat.ChatID, chat.Name = "main", "Main chat"
		case strings.HasPrefix(record.ConversationKey, crewID+codeChatSideMarker):
			chat.ChatID = strings.TrimPrefix(record.ConversationKey, crewID+codeChatSideMarker)
			chat.Name = firstNonEmptyTrimmed(titles[record.SessionID], "Chat "+chat.ChatID)
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
		if action == "list" || action == "read" {
			scope = "crews:read"
		}
		if !t.Allows(scope) || !t.AllowsCrew(crewID) {
			externalError(w, 403, "insufficient_scope", "This connection does not allow "+action+" on this Crew.")
			return
		}
	}
	crew, _, _, ok := api.externalCrewResolve(ctx, claims, crewID)
	if !ok {
		externalError(w, 404, "not_found", "Crew not found or not allowed for this connection.")
		return
	}
	workspace := crew.Binding.WorkspacePath
	chats := api.crewChats(r, claims.UserID, crewID, workspace)
	respond := func(status int, body []byte) { externalScheduleRespond(w, status, body) }
	vars := map[string]string{"id": "work"}
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
		externalJSON(w, map[string]any{"crew_id": crewID, "chats": chats, "earlier": earlier(), "side_chats_left": max(0, externalCrewMaxSideChats-(len(chats)-1))})
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
		if len(chats)-1 >= externalCrewMaxSideChats {
			externalError(w, 409, "too_many_chats", "You already have 4 side chats with this Crew; close one first.")
			return
		}
		raw := make([]byte, 4)
		_, _ = rand.Read(raw)
		id := hex.EncodeToString(raw)
		status, body := scheduleHandler(r, api.handleResolveAgentProfileConversation, http.MethodPost, "/api/agent-profiles/work/conversation", vars, nil, AgentProfileConversationRequest{ConversationKey: crewID + codeChatSideMarker + id})
		if status >= 400 {
			respond(status, body)
			return
		}
		externalJSON(w, map[string]any{"crew_id": crewID, "chat_id": id, "next": "Talk in it with ask_crew chat_id=" + id + "."})
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
		respond(scheduleHandler(r, api.handleDeleteAgentProfileConversation, http.MethodDelete, "/api/agent-profiles/work/conversations/"+url.PathEscape(sessionID), map[string]string{"id": "work", "session_id": sessionID}, url.Values{"conversation_key": {crewID}}, nil))
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
