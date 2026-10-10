package server

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"

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
	add("ask_my_code", "Send a message into one of your Code project's chats and get the reply, like ask_crew: it runs as a turn in that chat, after its current turn if busy, and you see it in that tab. It returns at once with a call_id unless wait_seconds is set (up to 25); poll with call_id. Use it to test what the agent can and cannot do (commands, files, folder guards)."+suffix, true, false, map[string]any{
		"project_id":    externalString("Code project ID from code action=projects."),
		"message":       externalString("What to ask or tell the chat."),
		"chat_id":       externalString("A chat id from code action=chats; default is the main chat."),
		"wait_seconds":  map[string]any{"type": "integer", "minimum": 0, "maximum": externalCrewMaxWaitSeconds, "description": "Wait up to this long for the reply."},
		"call_id":       externalString("Poll a call started earlier instead of sending a new message."),
		"submission_id": externalString("Stable id for this intended ask; reuse it after an uncertain retry to get the same call."),
	})
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
	if name == "ask_my_code" && str("call_id") != "" {
		call := lookupCrewFunctionCall(str("call_id"))
		if call == nil {
			externalError(w, http.StatusNotFound, "not_found", "Call not found.")
			return
		}
		call.mu.Lock()
		owned := call.UserID == claims.UserID && call.CallerKind == triggerCallerUser && call.TargetProfileID == codeproduct.ProfileID
		call.mu.Unlock()
		if !owned {
			externalError(w, http.StatusNotFound, "not_found", "Call not found.")
			return
		}
		externalJSON(w, api.externalCodeRunCallResponse(ctx, call, externalCrewWait(args)))
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
		if chat.ID != codeChatMainName {
			target.Chat = chat
		}
		log.Printf("[CODE_RUN] %s asks Code %s chat %s via an external connection", claims.UserID, projectID, chat.ID)
		call, err := api.startCrewFunctionCall(context.WithoutCancel(ctx), claims.UserID, externalCrewCaller(claims), target, defaultAskCrewFunction(), map[string]interface{}{"message": message}, externalCrewCallTimeout, str("submission_id"))
		if err != nil {
			externalError(w, http.StatusBadRequest, "call_refused", err.Error())
			return
		}
		externalJSON(w, api.externalCodeRunCallResponse(ctx, call, externalCrewWait(args)))
	default:
		externalError(w, http.StatusNotFound, "unknown_tool", "Tool is not exposed by this API.")
	}
}

// externalCodeRunCallResponse is the call's state after waiting up to wait, with a Code-specific poll hint.
func (api *StreamingAPI) externalCodeRunCallResponse(ctx context.Context, call *crewFunctionCall, wait time.Duration) map[string]interface{} {
	out := externalCrewCallResponse(ctx, call, wait)
	if _, hasNext := out["next"]; hasNext {
		out["next"] = "Still running in the Code chat. Poll with code action=ask and this call_id."
	}
	return out
}
