package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"sort"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/codeproduct"
)

// Running your own Code over the external API and MCP (owner decision 2026-10-10: test Code features, folder guards and
// the sandbox, without clicking). Four tools, merged into one `code` tool: list your Code projects, list a project's
// chats (tabs), ask a chat a question (a turn in that chat, like ask_crew) and read the project's state. They need the
// code:run scope, which is never in a default set, and they work on the caller's OWN Code only. A Code that runs on the
// person's own device (Local mode) is refused: its turns belong to that device. Every ask is logged.

var externalCodeRunTools = map[string]bool{
	"list_my_code_projects": true,
	"list_my_code_chats":    true,
	"ask_my_code":           true,
	"get_my_code_state":     true,
	"open_my_code_chat":     true,
	"close_my_code_chat":    true,
	"stop_my_code_chat":     true,
}

func isExternalCodeRunTool(name string) bool { return externalCodeRunTools[name] }

// externalCodeRunAllowed is the catalog and call gate: a token needs code:run, and the account must have Code.
func externalCodeRunAllowed(c *UserClaims) bool {
	if c == nil || !userAllowedProduct(c, codeproduct.ProfileID) {
		return false
	}
	return c.AccessToken == nil || c.AccessToken.Allows("code:run")
}

func externalCodeRunDefinitions(add func(name, description string, write, scoped bool, props map[string]any, required ...string)) {
	const suffix = " Your own Code projects only. Requires code:run."
	project := map[string]any{"project_id": externalString("Code project ID from code action=projects.")}
	add("list_my_code_projects", "List your own Code projects: id, title, last update."+suffix, false, false, nil)
	add("list_my_code_chats", "List the chats (tabs) of one of your Code projects: the main chat and its side chats, with id, name and whether each is working now."+suffix, false, false, project, "project_id")
	add("ask_my_code", "Send an explicit conversational message into one of your own Code chats. Returns an inbox delivery receipt; replies are optional agent-sent messages read with messages action=read. No call_id or automatic final-answer capture."+suffix, true, false, map[string]any{
		"project_id":    externalString("Code project ID from code action=projects."),
		"message":       externalString("Message to send."),
		"chat_id":       externalString("Chat ID from code action=chats; default main."),
		"inbox_id":      externalString("Inbox from a previous send to this Code chat."),
		"submission_id": externalString("Unique message submission ID; reuse after uncertain delivery."),
	}, "project_id", "message")
	chatArg := map[string]any{"project_id": project["project_id"], "chat_id": externalString("A side chat id from code action=chats.")}
	add("open_my_code_chat", "Open a new side chat (tab) in one of your Code projects (up to 4), as the + in the app does; talk in it with code action=ask chat_id=<the id returned>. Chats in one project can message each other with the agent's list_project_chats and ask_project_chat tools."+suffix, true, false, project, "project_id")
	add("close_my_code_chat", "Close one of your Code project's side chats (tab). The main chat cannot be closed, and a chat that is still working must be stopped first."+suffix, true, false, chatArg, "project_id", "chat_id")
	add("stop_my_code_chat", "Stop the turn running in one of your Code project's chats (the main chat or a side chat)."+suffix, true, false, map[string]any{"project_id": project["project_id"], "chat_id": externalString("A chat id from code action=chats; default main.")}, "project_id")
	add("get_my_code_state", "What the server knows about one of your Code projects right now: its mode, folder (the guard's read and write paths), the account slot commands run as, and each chat's working and Local flags. Read-only."+suffix, false, false, project, "project_id")
}

// externalCodeRunOwnProject resolves a project that is the caller's own Code. A project that is not theirs, does not
// exist or is not Code is one not-found.
func (api *StreamingAPI) externalCodeRunOwnProject(ctx context.Context, userID, projectID string) (productConversationBinding, string, bool) {
	projectID = strings.TrimSpace(projectID)
	if api == nil || api.agentProfiles == nil || projectID == "" || strings.ContainsAny(projectID, ":/") {
		return productConversationBinding{}, "", false
	}
	profile, err := api.agentProfiles.Resolve(codeproduct.ProfileID, 0, userID)
	if err != nil {
		return productConversationBinding{}, "", false
	}
	binding, owned, err := resolveConversationBindingForUser(ctx, userID, profile, projectID)
	if err != nil || !owned {
		return productConversationBinding{}, "", false
	}
	raw, exists, err := readFileFromWorkspace(ctx, strings.TrimSuffix(strings.TrimSpace(binding.WorkspacePath), "/")+"/product.json")
	var manifest productProjectManifest
	if err != nil || !exists || json.Unmarshal([]byte(raw), &manifest) != nil || manifest.Product != codeproduct.ProfileID || manifest.ID != projectID {
		return productConversationBinding{}, "", false
	}
	return binding, projectID, true
}

// externalCodeRunChats lists a project's chats: the main chat first, then the side chats, newest first.
func (api *StreamingAPI) externalCodeRunChats(ctx context.Context, userID, projectID string, binding productConversationBinding) ([]codeChat, error) {
	records, err := defaultProductConversationRegistryStore().projectChats(ctx, userID, codeproduct.ProfileID, projectID)
	if err != nil {
		return nil, err
	}
	titles := map[string]string{}
	if sessions, listErr := ListChatHistorySessions(userID, 200, 0, binding.WorkspacePath); listErr == nil {
		for _, session := range sessions {
			titles[session.SessionID] = strings.TrimSpace(session.Title)
		}
	}
	var chats []codeChat
	for _, record := range records {
		if !workspacePathsMatchForUser(userID, record.WorkspacePath, binding.WorkspacePath) {
			continue
		}
		chat := codeChat{Key: record.ConversationKey, SessionID: record.SessionID, UpdatedAt: record.UpdatedAt}
		if record.ConversationKey == projectID {
			chat.ID, chat.Name = codeChatMainName, "Main chat"
		} else {
			chat.ID = strings.TrimPrefix(record.ConversationKey, projectID+codeChatSideMarker)
			chat.Name = firstNonEmptyTrimmed(titles[record.SessionID], "Chat "+chat.ID)
		}
		chats = append(chats, chat)
	}
	sort.SliceStable(chats, func(i, j int) bool {
		if (chats[i].ID == codeChatMainName) != (chats[j].ID == codeChatMainName) {
			return chats[i].ID == codeChatMainName
		}
		return chats[i].UpdatedAt > chats[j].UpdatedAt
	})
	return chats, nil
}

func (api *StreamingAPI) externalCodeRunCall(w http.ResponseWriter, r *http.Request, name string, args map[string]any) {
	ctx := r.Context()
	claims := GetUserFromContext(ctx)
	if !externalCodeRunAllowed(claims) {
		externalError(w, http.StatusForbidden, "insufficient_scope", "Running your Code needs code:run and an account with the Code product.")
		return
	}
	str := func(key string) string {
		value, _ := args[key].(string)
		return strings.TrimSpace(value)
	}
	if name == "list_my_code_projects" {
		profile, err := api.agentProfiles.Resolve(codeproduct.ProfileID, 0, claims.UserID)
		if err != nil {
			externalError(w, http.StatusServiceUnavailable, "unavailable", "Code is unavailable on this server.")
			return
		}
		projects := []map[string]any{}
		for _, summary := range listProjectsForOwner(ctx, claims, profile, claims.UserID, false) {
			if summary.OwnerID != claims.UserID {
				continue
			}
			projects = append(projects, map[string]any{"project_id": summary.ID, "title": summary.Title, "updated_at": summary.UpdatedAt})
		}
		externalJSON(w, map[string]any{"projects": projects})
		return
	}
	binding, projectID, ok := api.externalCodeRunOwnProject(ctx, claims.UserID, str("project_id"))
	if !ok {
		externalError(w, http.StatusNotFound, "not_found", "No Code project of yours with that project_id; see code action=projects.")
		return
	}
	chats, err := api.externalCodeRunChats(ctx, claims.UserID, projectID, binding)
	if err != nil {
		externalError(w, http.StatusBadGateway, "workspace_unavailable", "Cannot read the project's chats.")
		return
	}
	root := agentProfileRuntimeWorkspace(claims.UserID, binding.WorkspacePath)
	describe := func() []map[string]any {
		out := make([]map[string]any, 0, len(chats))
		for _, chat := range chats {
			out = append(out, map[string]any{"chat_id": chat.ID, "name": chat.Name, "working": api.conversationTurnOccupied(chat.SessionID), "local": api.codeLocalSession(chat.SessionID)})
		}
		return out
	}
	switch name {
	case "list_my_code_chats":
		externalJSON(w, map[string]any{"project_id": projectID, "chats": describe()})
	case "get_my_code_state":
		guard := api.codeShellFolderGuard(ctx, claims.UserID, root)
		externalJSON(w, map[string]any{
			"project_id": projectID, "folder": root, "slot": slotHeldBy(claims.UserID),
			"folder_guard": map[string]any{"read_paths": guard.ReadPaths, "write_paths": guard.WritePaths, "blocked_write_paths": guard.BlockedWritePaths, "strict_allowlist": guard.StrictAllowlist},
			"chats":        describe(),
		})
	case "open_my_code_chat":
		if len(chats)-1 >= externalCrewMaxSideChats {
			externalError(w, http.StatusConflict, "too_many_chats", "This project already has 4 side chats; close one first.")
			return
		}
		raw := make([]byte, 4)
		_, _ = rand.Read(raw)
		id := hex.EncodeToString(raw)
		status, body := scheduleHandler(r, api.handleResolveAgentProfileConversation, http.MethodPost, "/api/agent-profiles/code/conversation", map[string]string{"id": codeproduct.ProfileID}, nil, AgentProfileConversationRequest{ConversationKey: projectID + codeChatSideMarker + id})
		if status >= 400 {
			externalScheduleRespond(w, status, body)
			return
		}
		externalJSON(w, map[string]any{"project_id": projectID, "chat_id": id, "next": "Talk in it with code action=ask chat_id=" + id + "."})
	case "close_my_code_chat", "stop_my_code_chat":
		chatID := str("chat_id")
		if name == "stop_my_code_chat" {
			chatID = firstNonEmptyTrimmed(chatID, codeChatMainName)
		}
		var chat *codeChat
		for i := range chats {
			if chatID != "" && strings.EqualFold(chats[i].ID, chatID) {
				chat = &chats[i]
				break
			}
		}
		if chat == nil || (name == "close_my_code_chat" && chat.ID == codeChatMainName) {
			externalError(w, http.StatusNotFound, "chat_not_found", "Pass a chat_id from code action=chats (close: a side chat).")
			return
		}
		if name == "close_my_code_chat" {
			status, body := scheduleHandler(r, api.handleCloseAgentProfileSideChat, http.MethodPost, "/api/agent-profiles/code/conversation/close", map[string]string{"id": codeproduct.ProfileID}, nil, AgentProfileConversationRequest{ConversationKey: chat.Key})
			externalScheduleRespond(w, status, body)
			return
		}
		if !api.sessionStartedBy(chat.SessionID, claims.UserID) {
			externalError(w, http.StatusConflict, "not_running", "Nothing is running in that chat.")
			return
		}
		stop := func(w2 http.ResponseWriter, r2 *http.Request) {
			r2.Header.Set("X-Session-ID", chat.SessionID)
			api.handleStopSession(w2, r2)
		}
		status, body := scheduleHandler(r, stop, http.MethodPost, "/api/session/stop", nil, nil, map[string]any{})
		externalScheduleRespond(w, status, body)
	case "ask_my_code":
		message := str("message")
		if message == "" {
			externalError(w, http.StatusBadRequest, "invalid_arguments", "message is required.")
			return
		}
		if len([]rune(message)) > codeChatMessageMaxRune {
			externalError(w, http.StatusBadRequest, "invalid_arguments", "message is too long; put the detail in a file in the project and point to it.")
			return
		}
		chatID := firstNonEmptyTrimmed(str("chat_id"), codeChatMainName)
		var chat *codeChat
		for i := range chats {
			if strings.EqualFold(chats[i].ID, chatID) {
				chat = &chats[i]
				break
			}
		}
		if chat == nil {
			externalError(w, http.StatusNotFound, "chat_not_found", "No chat with that chat_id; see code action=chats.")
			return
		}
		if api.codeLocalSession(chat.SessionID) {
			externalError(w, http.StatusConflict, "local_code", "This chat runs on your own device (Local mode); it cannot be asked from here.")
			return
		}
		target := triggerTarget{Kind: triggerCallerCrew, Path: binding.WorkspacePath, Label: "Code " + projectID + " · " + chat.Name, CrewID: projectID, CrewProfile: codeproduct.ProfileID, CrewOwner: claims.UserID}
		target.Chat = chat
		log.Printf("[CODE_RUN] %s sends to Code %s chat %s via an external connection", claims.UserID, projectID, chat.ID)
		api.externalSendMessage(w, r, args, target)
	default:
		externalError(w, http.StatusNotFound, "unknown_tool", "Tool is not exposed by this API.")
	}
}
