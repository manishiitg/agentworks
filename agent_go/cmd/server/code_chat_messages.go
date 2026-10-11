package server

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/codeproduct"
)

// Chats of one Code explicitly send messages to sibling chats. Delivery
// waits behind an occupied conversation; replies are optional messages.
// Targets come from the owner's registry and stay within this same Code.

const (
	codeChatMainName       = "main"
	codeChatSideMarker     = ":chat:"
	codeChatMessageMaxRune = 20000
)

type codeChat struct {
	Key       string // conversation key: <projectId> or <projectId>:chat:<id>
	ID        string // "main" or the side chat's id
	Name      string
	SessionID string
	UpdatedAt string
}

type codeChatProject struct {
	OwnerID   string
	ProjectID string
	Workspace string
	Chats     []codeChat
	Self      codeChat
}

// codeChatsFor resolves the calling chat's Code and its chats. Every input is
// trusted turn state: the turn's user, session and verified workspace.
func (api *StreamingAPI) codeChatsFor(ctx context.Context, userID, sessionID, workspacePath string) (codeChatProject, error) {
	denied := codeChatProject{}
	unavailable := fmt.Errorf("messaging other chats is available only in a Code chat")
	if api == nil || api.agentProfiles == nil || strings.TrimSpace(userID) == "" || strings.TrimSpace(sessionID) == "" {
		return denied, unavailable
	}
	raw, exists, err := readFileFromWorkspace(ctx, strings.TrimSuffix(strings.TrimSpace(workspacePath), "/")+"/product.json")
	var manifest productProjectManifest
	if err != nil || !exists || json.Unmarshal([]byte(raw), &manifest) != nil || manifest.Product != codeproduct.ProfileID || strings.TrimSpace(manifest.ID) == "" {
		return denied, unavailable
	}
	profile, err := api.agentProfiles.Resolve(codeproduct.ProfileID, 0, userID)
	if err != nil {
		return denied, unavailable
	}
	// A Code resolves only in its owner's own tree (resolveCrewProjectBinding).
	binding, owned, err := resolveConversationBindingForUser(ctx, userID, profile, manifest.ID)
	if err != nil || !owned || !workspacePathsMatchForUser(userID, binding.WorkspacePath, workspacePath) {
		return denied, unavailable
	}
	chats, err := defaultProductConversationRegistryStore().projectChats(ctx, userID, codeproduct.ProfileID, manifest.ID)
	if err != nil {
		return denied, err
	}
	project := codeChatProject{OwnerID: sanitizeUserIDForPath(userID), ProjectID: manifest.ID, Workspace: binding.WorkspacePath}
	titles := map[string]string{}
	if sessions, listErr := ListChatHistorySessions(userID, 200, 0, binding.WorkspacePath); listErr == nil {
		for _, session := range sessions {
			titles[session.SessionID] = strings.TrimSpace(session.Title)
		}
	}
	found := false
	for _, record := range chats {
		if !workspacePathsMatchForUser(userID, record.WorkspacePath, binding.WorkspacePath) {
			continue
		}
		chat := codeChat{Key: record.ConversationKey, SessionID: record.SessionID, UpdatedAt: record.UpdatedAt}
		if record.ConversationKey == manifest.ID {
			chat.ID, chat.Name = codeChatMainName, "Main chat"
		} else {
			chat.ID = strings.TrimPrefix(record.ConversationKey, manifest.ID+codeChatSideMarker)
			chat.Name = firstNonEmptyTrimmed(titles[record.SessionID], "Chat "+chat.ID)
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
		if (project.Chats[i].ID == codeChatMainName) != (project.Chats[j].ID == codeChatMainName) {
			return project.Chats[i].ID == codeChatMainName
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
	var matches []codeChat
	for _, chat := range p.Chats {
		if strings.ToLower(chat.ID) == want || strings.ToLower(chat.Key) == want || strings.ToLower(chat.Name) == want {
			matches = append(matches, chat)
		}
	}
	switch len(matches) {
	case 0:
		return codeChat{}, fmt.Errorf("no chat %q in this Code; call list_project_chats", raw)
	case 1:
		return matches[0], nil
	default:
		return codeChat{}, fmt.Errorf("%q matches more than one chat; pass its id from list_project_chats", raw)
	}
}

// codeChatCaller is the calling chat as a call source: its Code, plus the
// chat itself, all from the trusted turn.
func codeChatCaller(project codeChatProject, base triggerLinkCaller) triggerLinkCaller {
	self := project.Self
	base.Label = self.Name
	base.Chat = &self
	return base
}

// codeChatTarget is a sibling chat as a call target; codePath is the Code's
// physical folder, the same one its calls are keyed by.
func codeChatTarget(project codeChatProject, chat codeChat, codePath string) triggerTarget {
	return triggerTarget{Kind: triggerCallerCrew, Path: codePath, Label: chat.Name,
		CrewID: project.ProjectID, CrewProfile: codeproduct.ProfileID, CrewOwner: project.OwnerID, Chat: &chat}
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
	if err != nil || conversation.SessionID != target.Chat.SessionID {
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

// registerCodeChatTools registers list_project_chats and ask_project_chat
// for one Code chat turn.
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
			item := map[string]interface{}{"id": chat.ID, "name": chat.Name, "working": api.conversationTurnOccupied(chat.SessionID)}
			if chat.Key == project.Self.Key {
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
		{"list_project_chats", "List the chats of this Code: the main chat and its side chats (tabs), with id, name, whether each is working now, and which one is this chat. Use it before ask_project_chat.", map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
			func(ctx context.Context, _ map[string]interface{}) (string, error) {
				ctx = trusted(ctx)
				project, err := api.codeChatsFor(ctx, userID, sessionID, workspacePath)
				if err != nil {
					return "", err
				}
				return jsonOut(map[string]interface{}{"chats": describe(project)})
			}},
		{"ask_project_chat", "Send an explicit message to another chat (tab) of this Code. Busy chats receive it after their current turn. Returns an inbox/reply address, not a function call. The recipient chooses whether and when to reply with send_message; final chat text is not forwarded. Read messages with read_agent_messages(inbox_id). Other chats share this folder but not this conversation, so make the message self-contained.", map[string]interface{}{
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
				if chat.Key == project.Self.Key {
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
