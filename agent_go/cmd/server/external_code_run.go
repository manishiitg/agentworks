package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/codeproduct"
)

// Running your own Code over the external API and MCP (owner decision 2026-10-10: test Code features, folder guards and
// the sandbox, without clicking). Four tools, merged into one `code` tool: list your Code projects, list a project's
// chats (tabs), ask a chat a question (a turn in that chat, like ask_crew) and read the project's state. They need the
// code:run scope, which is never in a default set, and they work on the caller's OWN Code only. Laptop tools are excluded; these turns run on the server. Every ask is logged.

var externalCodeRunTools = map[string]bool{
	"list_my_code_projects": true,
	"list_my_code_chats":    true,
	"ask_my_code":           true,
	"read_my_code_chat":     true,
	"get_my_code_state":     true,
	"open_my_code_chat":     true,
	"close_my_code_chat":    true,
	"stop_my_code_chat":     true,
	"rename_my_code_chat":   true,
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
	externalWatcherDefinitions(add)
	const suffix = " Your own Code projects only. Requires code:run."
	project := map[string]any{"project_id": externalString("Code project ID from code action=projects.")}
	add("list_my_code_projects", "List your own Code projects: id, title, last update."+suffix, false, false, nil)
	add("list_my_code_chats", "List the agents (tabs) of one of your Code projects: the main chat and its named side tabs, each with its name, id and whether it is working now. Talk to the agent <name> with code action=ask chat_id=<name>."+suffix, false, false, project, "project_id")
	add("ask_my_code", "Send an explicit conversational message into one of your own Code chats. Starts a turn in that tab like typing there (a closed session is resumed). With no chat_id it goes to your own tab, named after you or your app and opened the first time; it never goes to the main tab unless you name it. Returns a delivery receipt and next_after; pass wait_seconds (max 25) to get that turn's answer in the same call, else read it with code action=read after=next_after."+suffix, true, false, map[string]any{
		"project_id":    externalString("Code project ID from code action=projects."),
		"message":       externalString("Message to send."),
		"chat_id":       externalString("The tab (agent) to talk to: its name or id from code action=chats. Leave it out to use your own tab, named after you or your app; it is opened the first time."),
		"inbox_id":      externalString("Inbox from a previous send to this Code chat."),
		"submission_id": externalString("Unique message submission ID; reuse after uncertain delivery."),
		"wait_seconds":  externalInteger(0, externalCrewMaxWaitSeconds),
	}, "project_id", "message")
	add("read_my_code_chat", "Read the latest of one of your own Code chats (tabs) as plain text: what you asked, what the agent answered and which tools it used (names only). Pass after (the next_after of a previous read or ask) to get only what is new, and wait_seconds (max 25) to wait until the chat has finished its turn."+suffix, false, false, map[string]any{
		"project_id":   project["project_id"],
		"chat_id":      externalString("The tab (agent) to read: its name or id from code action=chats. Leave it out for your own tab."),
		"after":        externalInteger(0, 1<<30),
		"limit":        externalInteger(1, 100),
		"wait_seconds": externalInteger(0, externalCrewMaxWaitSeconds),
	}, "project_id")
	chatArg := map[string]any{"project_id": project["project_id"], "chat_id": externalString("A side chat id from code action=chats.")}
	add("open_my_code_chat", "Open a new side chat (tab) in one of your Code projects (up to "+strconv.Itoa(externalCrewMaxSideChats)+"), as the + in the app does; give it a name saying what the agent is for (required, unique in a project); talk in it with code action=ask chat_id=<the id returned>. Chats in one project can message each other with the agent's list_project_chats and ask_project_chat tools."+suffix, true, false, map[string]any{"project_id": project["project_id"], "name": externalString("A short tab name saying what the agent is for.")}, "project_id", "name")
	add("rename_my_code_chat", "Rename one of your Code project's side chats (tab). The main chat keeps its project name."+suffix, true, false, map[string]any{"project_id": project["project_id"], "chat_id": externalString("A side chat id from code action=chats."), "name": externalString("The new tab name.")}, "project_id", "chat_id", "name")
	add("close_my_code_chat", "Close one of your Code project's side chats (tab). The main chat cannot be closed, and a chat that is still working must be stopped first."+suffix, true, false, chatArg, "project_id", "chat_id")
	add("stop_my_code_chat", "Stop the turn running in one of your Code project's tabs (name or id; with none, your own tab)."+suffix, true, false, map[string]any{"project_id": project["project_id"], "chat_id": externalString("A chat id from code action=chats; default main.")}, "project_id")
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
func (api *StreamingAPI) externalCodeRunChats(ctx context.Context, userID, projectID string, binding productConversationBinding, profileIDs ...string) ([]codeChat, error) {
	profileID := codeproduct.ProfileID
	if len(profileIDs) > 0 {
		profileID = profileIDs[0]
	}
	records, err := defaultProductConversationRegistryStore().projectChats(ctx, userID, profileID, projectID)
	if err != nil {
		return nil, err
	}
	titles := map[string]string{}
	if sessions, listErr := ListChatHistorySessions(userID, 200, 0, binding.WorkspacePath); listErr == nil {
		for _, session := range sessions {
			titles[session.SessionID] = strings.TrimSpace(session.Title)
		}
	}
	identities, _ := defaultProductConversationRegistryStore().agentIdentities(ctx, userID, profileID, projectID)
	var chats []codeChat
	for _, record := range records {
		if !workspacePathsMatchForUser(userID, record.WorkspacePath, binding.WorkspacePath) {
			continue
		}
		chat := codeChat{Key: record.ConversationKey, SessionID: record.SessionID, UpdatedAt: record.UpdatedAt}
		chat.ID = strings.TrimPrefix(record.ConversationKey, projectID+codeChatSideMarker)
		chat.IsMain = record.IsMainChat
		chat.Name = firstNonEmptyTrimmed(titles[record.SessionID], "Chat "+chat.ID)
		if chat.IsMain {
			chat.Name = "Main chat"
		}
		if identity, ok := identities[record.ConversationKey]; ok {
			chat.Name = firstNonEmptyTrimmed(identity.Name, chat.Name)
			chat.Purpose = identity.Purpose
		}
		chats = append(chats, chat)
	}
	sort.SliceStable(chats, func(i, j int) bool {
		if chats[i].IsMain != chats[j].IsMain {
			return chats[i].IsMain
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
			item := map[string]any{"chat_id": chat.ID, "name": chat.Name, "is_main": chat.IsMain, "working": api.conversationTurnOccupied(chat.SessionID)}
			if chat.Purpose != "" {
				item["purpose"] = chat.Purpose
			}
			out = append(out, item)
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
		chatName, nameErr := externalChatName(args)
		if nameErr != nil {
			externalError(w, http.StatusBadRequest, "invalid_name", nameErr.Error())
			return
		}
		id, status, message := api.externalCodeRunOpenTab(r, claims, binding, projectID, chats, chatName)
		if id == "" {
			externalError(w, status, "too_many_chats", message)
			return
		}
		externalJSON(w, map[string]any{"project_id": projectID, "chat_id": id, "name": chatName, "next": "Talk in it with code action=ask chat_id=" + id + "."})
	case "rename_my_code_chat":
		chatName, nameErr := externalChatName(args)
		target := findCodeChat(chats, str("chat_id"))
		if target != nil && target.IsMain {
			target = nil
		}
		if target != nil && chatName != "" && codeChatNameTaken(chats, chatName, target.ID) {
			externalError(w, http.StatusConflict, "name_taken", "Another tab in this project is already called "+chatName+"; pick another name.")
			return
		}
		if target == nil || nameErr != nil || chatName == "" {
			externalError(w, http.StatusNotFound, "chat_not_found", "Pass a side tab (its name or chat_id from code action=chats) and a new name up to 120 characters.")
			return
		}
		_ = RenameChatHistorySession(claims.UserID, target.SessionID, binding.WorkspacePath, chatName)
		if err := setCodeTabName(ctx, claims.UserID, target.Key, chatName); err != nil {
			externalError(w, http.StatusBadGateway, "rename_failed", "The chat could not be renamed.")
			return
		}
		externalJSON(w, map[string]any{"project_id": projectID, "chat_id": target.ID, "name": chatName})
	case "close_my_code_chat", "stop_my_code_chat":
		chatID := str("chat_id")
		if name == "stop_my_code_chat" && chatID == "" {
			chatID = externalCallerTabName(ctx, claims)
		}
		chat := findCodeChat(chats, chatID)
		if chat == nil || (name == "close_my_code_chat" && chat.IsMain) {
			externalError(w, http.StatusNotFound, "chat_not_found", "Pass the tab (its name or chat_id from code action=chats); to close it must be a side tab.")
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
		var chat *codeChat
		if str("chat_id") != "" {
			chat = findCodeChat(chats, str("chat_id"))
		} else {
			// No tab named: the caller's own tab, named after it, opened the first time (never the main tab).
			own := externalCallerTabName(ctx, claims)
			if chat = findCodeChat(chats, own); chat == nil {
				id, status, message := api.externalCodeRunOpenTab(r, claims, binding, projectID, chats, own)
				if id == "" {
					externalError(w, status, "too_many_chats", message)
					return
				}
				if again, e := api.externalCodeRunChats(ctx, claims.UserID, projectID, binding); e == nil {
					chats = again
					chat = findCodeChat(chats, id)
				}
			}
		}
		if chat == nil {
			externalError(w, http.StatusNotFound, "chat_not_found", "No tab with that name or id in this project; see code action=chats.")
			return
		}
		target := triggerTarget{Kind: triggerCallerCrew, Path: binding.WorkspacePath, Label: "Code " + projectID + " · " + chat.Name, CrewID: projectID, CrewProfile: codeproduct.ProfileID, CrewOwner: claims.UserID}
		target.Chat = chat
		log.Printf("[CODE_RUN] %s sends to Code %s chat %s via an external connection", claims.UserID, projectID, chat.ID)
		wait := externalCrewWait(args)
		_, cursor, _ := codeChatTranscript(ctx, claims.UserID, chat.SessionID, binding.WorkspacePath, 0, 0)
		if wait <= 0 {
			rec := &externalMCPRecorder{header: http.Header{}}
			api.externalSendMessage(rec, r, args, target)
			externalCodeRunRespond(w, rec, map[string]any{"next_after": cursor, "next": "Read the answer with code action=read chat_id=" + chat.ID + " after=" + strconv.Itoa(cursor) + " wait_seconds=25."})
			return
		}
		rec := &externalMCPRecorder{header: http.Header{}}
		api.externalSendMessage(rec, r, args, target)
		var receipt map[string]any
		if rec.status >= 300 || json.Unmarshal(rec.body.Bytes(), &receipt) != nil {
			externalCodeRunRespond(w, rec, nil)
			return
		}
		messageID, _ := receipt["message_id"].(string)
		items, turnDone := api.waitForCodeTurn(ctx, claims.UserID, chat.SessionID, binding.WorkspacePath, projectID, chat.ID, cursor, messageID, wait)
		receipt["next_after"] = cursor
		if len(items) > 0 {
			receipt["next_after"] = items[len(items)-1].Seq
			receipt["messages"] = items
		}
		receipt["working"] = !turnDone
		if turnDone {
			receipt["status"] = "answered"
		} else {
			receipt["status"] = "running"
			receipt["next"] = "Still working. Read it with code action=read chat_id=" + chat.ID + " after=" + strconv.Itoa(cursor) + " wait_seconds=25."
		}
		externalJSON(w, receipt)
	case "read_my_code_chat":
		chat := findCodeChat(chats, firstNonEmptyTrimmed(str("chat_id"), externalCallerTabName(ctx, claims)))
		if chat == nil {
			externalError(w, http.StatusNotFound, "chat_not_found", "No tab with that name or id in this project; see code action=chats. Your own tab is opened the first time you ask.")
			return
		}
		after, limit := 0, 40
		if v, ok := args["after"].(float64); ok && v > 0 {
			after = int(v)
		}
		if v, ok := args["limit"].(float64); ok && v >= 1 {
			limit = min(int(v), 100)
		}
		wait := externalCrewWait(args)
		deadline := time.Now().Add(wait)
		for {
			items, last, err := codeChatTranscript(ctx, claims.UserID, chat.SessionID, binding.WorkspacePath, after, limit)
			working := api.conversationTurnOccupied(chat.SessionID)
			if err != nil {
				externalError(w, http.StatusBadGateway, "workspace_unavailable", "Cannot read that chat right now.")
				return
			}
			if wait <= 0 || time.Now().After(deadline) || (!working && len(items) > 0) || ctx.Err() != nil {
				externalJSON(w, map[string]any{"project_id": projectID, "chat_id": chat.ID, "messages": items, "next_after": last, "working": working})
				return
			}
			time.Sleep(700 * time.Millisecond)
		}
	default:
		externalError(w, http.StatusNotFound, "unknown_tool", "Tool is not exposed by this API.")
	}
}

// externalCodeRunRespond writes a recorded send's answer, adding fields to a successful JSON receipt.
func externalCodeRunRespond(w http.ResponseWriter, rec *externalMCPRecorder, extra map[string]any) {
	status := rec.status
	if status == 0 {
		status = http.StatusOK
	}
	var receipt map[string]any
	if status < 300 && extra != nil && json.Unmarshal(rec.body.Bytes(), &receipt) == nil {
		for k, v := range extra {
			receipt[k] = v
		}
		externalJSON(w, receipt)
		return
	}
	for k, v := range rec.header {
		w.Header()[k] = v
	}
	w.WriteHeader(status)
	_, _ = w.Write(rec.body.Bytes())
}

// codeTurnItem is one line of a Code chat as plain text.
type codeTurnItem struct {
	Seq  int    `json:"seq"`
	Turn string `json:"turn,omitempty"`
	Role string `json:"role"` // you, assistant or tool
	Text string `json:"text"`
	At   string `json:"at,omitempty"`
}

// codeChatTranscript reads a Code chat's saved events as plain lines: what was asked, the answer of each turn and the
// names of the tools used (never their output). after is the last seq already seen; the returned last is the newest seq.
func codeChatTranscript(ctx context.Context, userID, sessionID, workspacePath string, after, limit int) ([]codeTurnItem, int, error) {
	data, err := ReadChatHistoryConversation(userID, sessionID, workspacePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) || strings.Contains(strings.ToLower(err.Error()), "not found") {
			return nil, after, nil // a tab nobody has written to yet has nothing to read
		}
		return nil, after, err
	}
	var doc struct {
		Events []struct {
			Sequence  int    `json:"sequence"`
			Type      string `json:"type"`
			Timestamp string `json:"timestamp"`
			Data      struct {
				Data struct {
					Content     string `json:"content"`
					FinalResult string `json:"final_result"`
					ToolName    string `json:"tool_name"`
					ServerName  string `json:"server_name"`
					Metadata    struct {
						TurnID string `json:"turn_id"`
					} `json:"metadata"`
				} `json:"data"`
			} `json:"data"`
		} `json:"ui_events"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, after, err
	}
	clip := func(s string) string {
		r := []rune(strings.TrimSpace(s))
		if len(r) > 8000 {
			return string(r[:8000]) + "…"
		}
		return string(r)
	}
	var items []codeTurnItem
	last := after
	for _, e := range doc.Events {
		if e.Sequence > last {
			last = e.Sequence
		}
		if e.Sequence <= after {
			continue
		}
		d := e.Data.Data
		switch e.Type {
		case "user_message":
			items = append(items, codeTurnItem{Seq: e.Sequence, Turn: d.Metadata.TurnID, Role: "you", Text: clip(d.Content), At: e.Timestamp})
		case "unified_completion":
			if strings.TrimSpace(d.FinalResult) != "" {
				items = append(items, codeTurnItem{Seq: e.Sequence, Turn: d.Metadata.TurnID, Role: "assistant", Text: clip(d.FinalResult), At: e.Timestamp})
			}
		case "tool_call_start":
			if name := firstNonEmptyTrimmed(d.ToolName, d.ServerName); name != "" {
				items = append(items, codeTurnItem{Seq: e.Sequence, Turn: d.Metadata.TurnID, Role: "tool", Text: name, At: e.Timestamp})
			}
		}
	}
	if limit > 0 && len(items) > limit {
		items = items[len(items)-limit:]
	}
	return items, last, nil
}

// waitForCodeTurn waits for the turn that carries messageID (the delivered text names it) to answer, up to wait.
// It logs when the turn starts and when it answers, so a resume of a closed session is visible in the log.
func (api *StreamingAPI) waitForCodeTurn(ctx context.Context, userID, sessionID, workspacePath, projectID, chatID string, after int, messageID string, wait time.Duration) ([]codeTurnItem, bool) {
	started := time.Now()
	deadline := started.Add(wait)
	turn, announced := "", false
	for {
		items, _, err := codeChatTranscript(ctx, userID, sessionID, workspacePath, after, 0)
		if err == nil {
			for _, it := range items {
				if turn == "" && it.Role == "you" && messageID != "" && strings.Contains(it.Text, messageID) {
					turn = it.Turn
					if !announced {
						announced = true
						log.Printf("[CODE_RUN] turn started in Code %s chat %s for message %s after %s (turn %s)", projectID, chatID, messageID, time.Since(started).Round(time.Millisecond), turn)
					}
				}
				if turn != "" && it.Role == "assistant" && it.Turn == turn {
					log.Printf("[CODE_RUN] turn answered in Code %s chat %s for message %s after %s", projectID, chatID, messageID, time.Since(started).Round(time.Millisecond))
					return items, true
				}
			}
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return nil, false
		}
		time.Sleep(700 * time.Millisecond)
	}
}

// findCodeChat picks a tab by id or by name (case-insensitive). A name that two tabs share picks nothing.
func findCodeChat(chats []codeChat, key string) *codeChat {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil
	}
	for i := range chats {
		if strings.EqualFold(key, codeChatMainName) && chats[i].IsMain {
			alias := chats[i]
			alias.Key, _, _ = strings.Cut(alias.Key, codeChatSideMarker)
			return &alias
		}
		if strings.EqualFold(chats[i].ID, key) {
			return &chats[i]
		}
	}
	var found *codeChat
	for i := range chats {
		if strings.EqualFold(strings.TrimSpace(chats[i].Name), key) {
			if found != nil {
				return nil
			}
			found = &chats[i]
		}
	}
	return found
}

// codeChatNameTaken reports whether a tab already has this name (any case), so two tabs are never one agent.
func codeChatNameTaken(chats []codeChat, name, exceptID string) bool {
	for _, c := range chats {
		if !strings.EqualFold(c.ID, exceptID) && strings.EqualFold(strings.TrimSpace(c.Name), strings.TrimSpace(name)) {
			return true
		}
	}
	return false
}

// externalCallerTabName names the tab a call without a tab gets: the attached agent's name, else the app that signed in.
func externalCallerTabName(ctx context.Context, claims *UserClaims) string {
	token := claims.AccessToken
	if token == nil {
		return "Website"
	}
	name := strings.TrimSpace(token.BoundAgentName)
	if name == "" && strings.HasPrefix(token.ID, "oauth-") {
		if store, err := openMCPOAuthStore(); err == nil {
			defer store.Close()
			if grant, e := store.ActiveFamily(ctx, strings.TrimPrefix(token.ID, "oauth-")); e == nil {
				if grant.ClientID == cliOAuthClientID {
					name = "AgentWorks CLI"
				} else if client, e2 := store.Client(ctx, grant.ClientID); e2 == nil {
					name = strings.TrimSpace(client.Name)
				}
			}
		}
	}
	name = firstNonEmptyTrimmed(name, token.Name, "AI app")
	if title, err := normalizeChatHistoryTitle(name); err == nil && title != "" {
		return title
	}
	return "AI app"
}

// externalCodeRunOpenTab opens a new side tab, optionally named, as the + in the app does. It returns the tab id, or an
// HTTP status and plain message when it cannot.
func (api *StreamingAPI) externalCodeRunOpenTab(r *http.Request, claims *UserClaims, binding productConversationBinding, projectID string, chats []codeChat, chatName string, profileIDs ...string) (string, int, string) {
	profileID := codeproduct.ProfileID
	if len(profileIDs) > 0 {
		profileID = profileIDs[0]
	}
	ids := make([]string, len(chats))
	mainID := codeChatMainName
	names := []string{}
	for i, c := range chats {
		ids[i] = c.ID
		if c.IsMain {
			mainID = c.ID
		} else {
			names = append(names, c.Name)
		}
	}
	if sideChatsOf(ids, mainID) >= externalCrewMaxSideChats {
		return "", http.StatusConflict, fmt.Sprintf("This project already has %d side tabs. Close one first (code action=close_chat); they are: %s.", externalCrewMaxSideChats, strings.Join(names, ", "))
	}
	if strings.TrimSpace(chatName) == "" {
		return "", http.StatusBadRequest, "Give the new tab a name, a few words saying what this agent is for."
	}
	if codeChatNameTaken(chats, chatName, "") {
		return "", http.StatusConflict, "A tab named " + chatName + " already exists in this project; use it, or pick another name."
	}
	raw := make([]byte, 4)
	_, _ = rand.Read(raw)
	id := hex.EncodeToString(raw)
	// The conversation route names the new tab (its agent identity and history title).
	status, body := scheduleHandler(r, api.handleResolveAgentProfileConversation, http.MethodPost, "/api/agent-profiles/code/conversation", map[string]string{"id": profileID}, nil, AgentProfileConversationRequest{ConversationKey: projectID + codeChatSideMarker + id, Name: chatName})
	if status >= 400 {
		return "", status, string(body)
	}
	return id, 0, ""
}

// setCodeTabName gives a Code tab its name as an agent identity, which is what the tab list and name lookups use.
func setCodeTabName(ctx context.Context, userID, conversationKey, name string, profileIDs ...string) error {
	profileID := codeproduct.ProfileID
	if len(profileIDs) > 0 {
		profileID = profileIDs[0]
	}
	_, err := defaultProductConversationRegistryStore().setAgentIdentity(ctx, userID, profileID, conversationKey, &name, nil, "tab name")
	return err
}
