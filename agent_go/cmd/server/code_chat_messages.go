package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/codeproduct"
)

// Chats of one Code or Crew explicitly send messages to sibling chats. Every
// tab is an equal agent (owner, 2026-10-18). Delivery waits behind an occupied
// conversation; replies are optional messages. Targets come from the owner's
// registry and stay within this same project.

const (
	codeChatMainName       = "main"
	codeChatSideMarker     = ":chat:"
	codeChatMessageMaxRune = 20000
)

type codeChat struct {
	IsMain    bool
	Key       string // conversation key: <projectId> or <projectId>:chat:<id>
	ID        string // "main" or the side chat's id
	Name      string
	Purpose   string
	SessionID string
	UpdatedAt string
}

type codeChatProject struct {
	OwnerID   string
	ProfileID string // codeproduct.ProfileID or crewProfileID
	ProjectID string
	Workspace string
	Binding   productConversationBinding
	Chats     []codeChat
	Self      codeChat
}

// codeChatsFor resolves the calling chat's Code or Crew and its chats. Every
// input is trusted turn state: the turn's user, session and verified
// workspace. Only the project's owner resolves it, so a Run person's turn in
// someone else's Crew never reaches the owner's tabs.
func (api *StreamingAPI) codeChatsFor(ctx context.Context, userID, sessionID, workspacePath string) (codeChatProject, error) {
	denied := codeChatProject{}
	unavailable := fmt.Errorf("messaging other chats is available only in your own Crew or Code project")
	if api == nil || api.agentProfiles == nil || strings.TrimSpace(userID) == "" || strings.TrimSpace(sessionID) == "" {
		return denied, unavailable
	}
	raw, exists, err := readFileFromWorkspace(ctx, strings.TrimSuffix(strings.TrimSpace(workspacePath), "/")+"/product.json")
	var manifest productProjectManifest
	if err != nil || !exists || json.Unmarshal([]byte(raw), &manifest) != nil || (manifest.Product != codeproduct.ProfileID && manifest.Product != crewProfileID) || strings.TrimSpace(manifest.ID) == "" {
		return denied, unavailable
	}
	profile, err := api.agentProfiles.Resolve(manifest.Product, 0, userID)
	if err != nil {
		return denied, unavailable
	}
	// The project resolves only for its owner (resolveCrewProjectBinding).
	binding, owned, err := resolveConversationBindingForUser(ctx, userID, profile, manifest.ID)
	if err != nil || !owned || !workspacePathsMatchForUser(userID, binding.WorkspacePath, workspacePath) {
		return denied, unavailable
	}
	chats, err := defaultProductConversationRegistryStore().projectChats(ctx, userID, manifest.Product, manifest.ID)
	if err != nil {
		return denied, err
	}
	project := codeChatProject{OwnerID: sanitizeUserIDForPath(userID), ProfileID: manifest.Product, ProjectID: manifest.ID, Workspace: binding.WorkspacePath, Binding: binding}
	titles := map[string]string{}
	if sessions, listErr := ListChatHistorySessions(userID, 200, 0, binding.WorkspacePath); listErr == nil {
		for _, session := range sessions {
			titles[session.SessionID] = strings.TrimSpace(session.Title)
		}
	}
	identities, _ := defaultProductConversationRegistryStore().agentIdentities(ctx, userID, manifest.Product, manifest.ID)
	found := false
	for _, record := range chats {
		if !workspacePathsMatchForUser(userID, record.WorkspacePath, binding.WorkspacePath) {
			continue
		}
		chat := codeChat{Key: record.ConversationKey, SessionID: record.SessionID, UpdatedAt: record.UpdatedAt}
		chat.ID = strings.TrimPrefix(record.ConversationKey, manifest.ID+codeChatSideMarker)
		chat.IsMain = record.IsMainChat
		chat.Name = firstNonEmptyTrimmed(titles[record.SessionID], "Chat "+chat.ID)
		if chat.IsMain {
			chat.Name = "Main chat"
		}
		// The person's or the agent's own name wins over the chat's automatic title.
		if identity, ok := identities[record.ConversationKey]; ok {
			chat.Name = firstNonEmptyTrimmed(identity.Name, chat.Name)
			chat.Purpose = identity.Purpose
		}
		if record.SessionID == sessionID {
			project.Self, found = chat, true
		}
		project.Chats = append(project.Chats, chat)
	}
	if !found {
		return denied, unavailable
	}
	sort.SliceStable(project.Chats, func(i, j int) bool {
		if project.Chats[i].IsMain != project.Chats[j].IsMain {
			return project.Chats[i].IsMain
		}
		return project.Chats[i].UpdatedAt > project.Chats[j].UpdatedAt
	})
	return project, nil
}

// find returns the chat named by id ("main", a side chat id) or exact name.
func (p codeChatProject) find(raw string) (codeChat, error) {
	want := strings.ToLower(strings.TrimSpace(raw))
	if want == "" {
		return codeChat{}, fmt.Errorf("chat is required: pass an id or name from list_project_chats")
	}
	if want == "primary" || want == "main chat" {
		want = codeChatMainName
	}
	if want == codeChatMainName {
		for _, chat := range p.Chats {
			if chat.IsMain {
				chat.Key = p.ProjectID // Explicit main is an address, not the physical ID listed to callers.
				return chat, nil
			}
		}
	}
	var matches []codeChat
	for _, chat := range p.Chats {
		if strings.ToLower(chat.ID) == want || strings.ToLower(chat.Key) == want || strings.ToLower(chat.Name) == want {
			matches = append(matches, chat)
		}
	}
	switch len(matches) {
	case 0:
		return codeChat{}, fmt.Errorf("no chat %q in this project; call list_project_chats", raw)
	case 1:
		return matches[0], nil
	default:
		return codeChat{}, fmt.Errorf("%q matches more than one chat; pass its id from list_project_chats", raw)
	}
}

// codeChatCaller is the calling chat as a call source: its project, plus the
// chat itself, all from the trusted turn.
func codeChatCaller(project codeChatProject, base triggerLinkCaller) triggerLinkCaller {
	self := project.Self
	base.Label = self.Name
	base.Chat = &self
	return base
}

// codeChatTarget is a sibling chat as a call target; codePath is the project's
// physical folder, the same one its calls are keyed by.
func codeChatTarget(project codeChatProject, chat codeChat, codePath string) triggerTarget {
	return triggerTarget{Kind: triggerCallerCrew, Path: codePath, Label: chat.Name,
		CrewID: project.ProjectID, CrewProfile: project.ProfileID, CrewOwner: project.OwnerID, Chat: &chat}
}

// codeChatTurnRequest builds the turn in the target chat's existing
// conversation, re-checking that it is still that chat of the same Code.
func (api *StreamingAPI) codeChatTurnRequest(ctx context.Context, userID string, target triggerTarget, text, fromLabel string) (map[string]interface{}, string, error) {
	if api != nil && target.Chat != nil && api.codeLocalSession(target.Chat.SessionID) {
		return nil, "", fmt.Errorf("Local Code chats cannot receive asks from other chats")
	}
	unavailable := fmt.Errorf("that chat is no longer available")
	if api == nil || api.agentProfiles == nil || target.Chat == nil {
		return nil, "", unavailable
	}
	profile, err := api.agentProfiles.Resolve(codeproduct.ProfileID, 0, userID)
	if err != nil {
		return nil, "", unavailable
	}
	binding, owned, err := resolveConversationBindingForUser(ctx, userID, profile, target.Chat.Key)
	if err != nil || !owned || !workspacePathsMatchForUser(userID, binding.WorkspacePath, target.Path) {
		return nil, "", unavailable
	}
	conversation, err := defaultProductConversationRegistryStore().resolveOrCreate(ctx, userID, profile, binding, "")
	if err != nil || (target.Chat.Key != binding.ResourceID && conversation.SessionID != target.Chat.SessionID) {
		return nil, "", unavailable
	}
	reqMap, sessionID, _, err := productBotTurnRequest(ctx, userID, profile, conversation, services.BotIncomingMessage{Text: text}, services.ThreadID{})
	if err != nil {
		return nil, "", err
	}
	// An interactive origin keeps the chat's coding session (no relaunch).
	reqMap["triggered_by"] = "code_chat"
	reqMap["triggered_by_label"] = "From " + fromLabel
	return reqMap, sessionID, nil
}

// agentRolePrompt tells an agent its own name and purpose in this project, as a fact about it (PLAT-571). Empty when
// neither is set.
func agentRolePrompt(ctx context.Context, userID, profileID, conversationKey, noun string) string {
	conversationKey = strings.TrimSpace(conversationKey)
	project, _, _ := strings.Cut(conversationKey, codeChatSideMarker)
	if project == "" {
		return ""
	}
	identities, err := defaultProductConversationRegistryStore().agentIdentities(ctx, userID, profileID, project)
	if err != nil {
		return ""
	}
	identity := identities[conversationKey]
	if noun == "" {
		noun = "Code"
	}
	switch {
	case identity.Name != "" && identity.Purpose != "":
		return fmt.Sprintf("\n\n## Your role in this %s\n\nYou are the %q agent in this %s: %s\n", noun, identity.Name, noun, identity.Purpose)
	case identity.Name != "":
		return fmt.Sprintf("\n\n## Your role in this %s\n\nYou are the %q agent in this %s.\n", noun, identity.Name, noun)
	case identity.Purpose != "":
		return fmt.Sprintf("\n\n## Your role in this %s\n\nThis agent's purpose in this %s: %s\n", noun, noun, identity.Purpose)
	}
	return ""
}

// registerCodeChatTools registers list_project_chats and ask_project_chat
// for one Code or Crew chat turn.
func (api *StreamingAPI) registerCodeChatTools(registrar definitionToolRegistrar, gate *productToolGate, userID, sessionID, workspacePath string) error {
	trusted := func(ctx context.Context) context.Context {
		return internalBotRequestContext(ctx, userID)
	}
	jsonOut := func(value interface{}) (string, error) {
		encoded, err := json.MarshalIndent(value, "", "  ")
		return string(encoded), err
	}
	describe := func(project codeChatProject) []map[string]interface{} {
		out := make([]map[string]interface{}, 0, len(project.Chats))
		for _, chat := range project.Chats {
			item := map[string]interface{}{"id": chat.ID, "name": chat.Name, "is_main": chat.IsMain, "working": api.conversationTurnOccupied(chat.SessionID)}
			if chat.Purpose != "" {
				item["purpose"] = chat.Purpose
			}
			if chat.Key == project.Self.Key || chat.SessionID == project.Self.SessionID {
				item["this_chat"] = true
			}
			out = append(out, item)
		}
		return out
	}
	resolveCaller := crewTriggerLinkCaller(workspacePath)
	tools := []struct {
		name, description string
		parameters        map[string]interface{}
		execute           func(context.Context, map[string]interface{}) (string, error)
	}{
		{"list_project_chats", "List the agents of this project: the main chat and its side chats (tabs), with id, name, purpose when it has one, whether each is working now, and which one is this chat. Use it before ask_project_chat.", map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
			func(ctx context.Context, _ map[string]interface{}) (string, error) {
				ctx = trusted(ctx)
				project, err := api.codeChatsFor(ctx, userID, sessionID, workspacePath)
				if err != nil {
					return "", err
				}
				return jsonOut(map[string]interface{}{"chats": describe(project)})
			}},
		{"set_my_agent_identity", "Set this agent's own name and/or purpose (a few words and one short line) so the other agents in this project know what it is for. Only this agent and the person change them; another agent can ask this one to.", map[string]interface{}{
			"type": "object", "properties": map[string]interface{}{
				"name":    map[string]interface{}{"type": "string", "maxLength": productAgentNameMaxRunes},
				"purpose": map[string]interface{}{"type": "string", "maxLength": productAgentPurposeMaxRunes},
			}},
			func(ctx context.Context, args map[string]interface{}) (string, error) {
				ctx = trusted(ctx)
				var name, purpose *string
				if value, ok := args["name"].(string); ok {
					name = &value
				}
				if value, ok := args["purpose"].(string); ok {
					purpose = &value
				}
				if name == nil && purpose == nil {
					return "", fmt.Errorf("pass a name, a purpose or both")
				}
				project, err := api.codeChatsFor(ctx, userID, sessionID, workspacePath)
				if err != nil {
					return "", err
				}
				identity, err := defaultProductConversationRegistryStore().setAgentIdentity(ctx, userID, project.ProfileID, project.Self.Key, name, purpose, "agent "+project.Self.Name)
				if err != nil {
					return "", err
				}
				return jsonOut(map[string]interface{}{"name": firstNonEmptyTrimmed(identity.Name, project.Self.Name), "purpose": identity.Purpose})
			}},
		{"open_project_chat", "Open a new named tab (a new agent) in this project, as the + in the app does, to give it part of the work. name is required and unique in this project (an existing tab with that name is reused, not duplicated); task is an optional first message, sent like ask_project_chat. Up to " + strconv.Itoa(externalCrewMaxSideChats) + " side tabs; this project's owner only.", map[string]interface{}{
			"type": "object", "required": []string{"name"}, "properties": map[string]interface{}{
				"name": map[string]interface{}{"type": "string", "maxLength": 120, "description": "A short name for the tab, e.g. tests or review."},
				"task": map[string]interface{}{"type": "string", "description": "Optional first task, self-contained."},
			}},
			func(ctx context.Context, args map[string]interface{}) (string, error) {
				ctx = trusted(ctx)
				name, err := normalizeChatHistoryTitle(strings.TrimSpace(stringToolArg(args, "name")))
				if err != nil || name == "" {
					return "", fmt.Errorf("give the tab a short name")
				}
				project, err := api.codeChatsFor(ctx, userID, sessionID, workspacePath)
				if err != nil {
					return "", err
				}
				if project.OwnerID != userID {
					return "", fmt.Errorf("only the owner of this project opens tabs")
				}
				binding, projectID := project.Binding, project.ProjectID
				chats, err := api.externalCodeRunChats(ctx, userID, projectID, binding, project.ProfileID)
				if err != nil {
					return "", err
				}
				claims := GetUserFromContext(ctx)
				if claims == nil {
					claims = &UserClaims{UserID: userID}
				}
				reused := true
				chat := findCodeChat(chats, name)
				if chat == nil {
					reused = false
					request := httptest.NewRequest(http.MethodPost, "/", nil).WithContext(ctx)
					id, status, message := api.externalCodeRunOpenTab(request, claims, binding, projectID, chats, name, project.ProfileID)
					if id == "" {
						return "", fmt.Errorf("%s (%d)", message, status)
					}
					if again, e := api.externalCodeRunChats(ctx, userID, projectID, binding, project.ProfileID); e == nil {
						chat = findCodeChat(again, id)
					}
					if chat == nil {
						return "", fmt.Errorf("the tab was opened but could not be found; see list_project_chats")
					}
				}
				out := map[string]interface{}{"id": chat.ID, "name": chat.Name, "reused": reused}
				task := strings.TrimSpace(stringToolArg(args, "task"))
				if task == "" {
					out["next"] = "Hand it work with ask_project_chat chat=" + chat.ID + "."
					return jsonOut(out)
				}
				if len([]rune(task)) > codeChatMessageMaxRune {
					return "", fmt.Errorf("task is longer than %d characters; put the detail in a file in the folder and point to it", codeChatMessageMaxRune)
				}
				fresh, err := api.codeChatsFor(ctx, userID, sessionID, workspacePath)
				if err != nil {
					return "", err
				}
				target, err := fresh.find(chat.ID)
				if err != nil {
					return "", err
				}
				base, err := resolveCaller(ctx)
				if err != nil {
					return "", err
				}
				sent, err := api.sendAgentMessage(ctx, userID, codeChatCaller(fresh, base), codeChatTarget(fresh, target, agentProfileRuntimeWorkspace(userID, base.Path)), task, "", "")
				if err != nil {
					return "", err
				}
				out["asked"] = sent
				return jsonOut(out)
			}},
		{"ask_project_chat", "Send an explicit message to another chat (tab) of this project. Busy chats receive it after their current turn. Returns an inbox/reply address, not a function call. The recipient chooses whether and when to reply with send_message; final chat text is not forwarded. Read messages with read_agent_messages(inbox_id). Other chats share this folder but not this conversation, so make the message self-contained.", map[string]interface{}{
			"type": "object", "required": []string{"chat", "message"}, "properties": map[string]interface{}{
				"chat":          map[string]interface{}{"type": "string", "description": "Chat id or name from list_project_chats."},
				"message":       map[string]interface{}{"type": "string"},
				"inbox_id":      map[string]interface{}{"type": "string", "description": "Existing conversation/reply address."},
				"submission_id": map[string]interface{}{"type": "string", "maxLength": 128, "description": "Stable message ID for uncertain transport retries."},
			}},
			func(ctx context.Context, args map[string]interface{}) (string, error) {
				ctx = trusted(ctx)
				message, _ := args["message"].(string)
				if strings.TrimSpace(message) == "" {
					return "", fmt.Errorf("message is required")
				}
				if len([]rune(message)) > codeChatMessageMaxRune {
					return "", fmt.Errorf("message is longer than %d characters; put the detail in a file in the folder and point to it", codeChatMessageMaxRune)
				}
				submissionID, _ := args["submission_id"].(string)
				project, err := api.codeChatsFor(ctx, userID, sessionID, workspacePath)
				if err != nil {
					return "", err
				}
				chatArg, _ := args["chat"].(string)
				chat, err := project.find(chatArg)
				if err != nil {
					return "", err
				}
				if chat.Key == project.Self.Key || chat.SessionID == project.Self.SessionID {
					return "", fmt.Errorf("a chat cannot ask itself")
				}
				base, err := resolveCaller(ctx)
				if err != nil {
					return "", err
				}
				caller, target := codeChatCaller(project, base), codeChatTarget(project, chat, agentProfileRuntimeWorkspace(userID, base.Path))
				out, err := api.sendAgentMessage(ctx, userID, caller, target, strings.TrimSpace(message), stringToolArg(args, "inbox_id"), submissionID)
				if err != nil {
					return "", err
				}
				return jsonOut(out)
			}},
	}
	for _, tool := range tools {
		if gate != nil {
			gate.Declare(tool.name)
		}
		if err := registrar.RegisterCustomTool(tool.name, tool.description, tool.parameters, tool.execute, "code_chat_tools"); err != nil {
			return err
		}
	}
	return nil
}
