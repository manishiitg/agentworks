package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/workproduct"
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
	OwnerID   string
	IsMain    bool
	Key       string // conversation key: <projectId> or <projectId>:chat:<id>
	ID        string // "main" or the side chat's id
	Name      string
	Purpose   string
	SessionID string
	// Hidden: the person took this tab off their strip (Close keeps it as a hidden agent). Lists leave it out.
	Hidden    bool
	UpdatedAt string
}

type codeChatProject struct {
	OwnerID   string
	ProfileID string // workproduct.ProfileID or crewProfileID
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
	if err != nil || !exists || json.Unmarshal([]byte(raw), &manifest) != nil || manifest.Product != crewProfileID || strings.TrimSpace(manifest.ID) == "" {
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
	ownedChats, err := crewOwnerChatRecords(ctx, userID, manifest.Product, manifest.ID, binding.WorkspacePath)
	if err != nil {
		return denied, err
	}
	project := codeChatProject{OwnerID: sanitizeUserIDForPath(userID), ProfileID: manifest.Product, ProjectID: manifest.ID, Workspace: binding.WorkspacePath, Binding: binding}
	// The caller's own pins for tabs other owners control, as the tab strip reads them (sideChatsFor).
	mine, _ := defaultProductConversationRegistryStore().agentIdentities(ctx, userID, manifest.Product, manifest.ID)
	found := false
	for _, tab := range ownedChats {
		record := tab.Record
		if !workspacePathsMatchForUser(userID, record.WorkspacePath, binding.WorkspacePath) {
			continue
		}
		chat := codeChat{OwnerID: tab.OwnerID, Key: record.ConversationKey, SessionID: record.SessionID, UpdatedAt: record.UpdatedAt}
		chat.ID = strings.TrimPrefix(record.ConversationKey, manifest.ID+codeChatSideMarker)
		chat.IsMain = tab.OwnerID == userID && (record.IsMainChat || record.ConversationKey == crewOwnerDefaultChatKey(manifest.ID, userID))
		chat.Name = "Chat " + chat.ID
		if chat.IsMain {
			chat.Name = "Main chat"
		} else if record.IsMainChat || record.ConversationKey == crewOwnerDefaultChatKey(manifest.ID, tab.OwnerID) || strings.HasPrefix(chat.ID, "root-") {
			chat.Name = "Main" // another person's Main, by its name in the Team (never a raw root id)
		}
		// The person's or the agent's own name wins over the chat's automatic title.
		if identity := tab.Identity; identity.Name != "" || identity.Purpose != "" {
			chat.Name = firstNonEmptyTrimmed(identity.Name, chat.Name)
			chat.Purpose = identity.Purpose
		}
		chat.Hidden = !chat.IsMain && tab.Identity.Unpinned
		if own, ok := mine[record.ConversationKey]; ok && tab.OwnerID != userID {
			chat.Hidden = !chat.IsMain && own.Unpinned
		}
		if record.SessionID == sessionID && tab.OwnerID == userID {
			project.Self, found = chat, true
		}
		// A dashboard user-view chat can be the caller, but it is not another agent to list (PLAT-750).
		if hiddenFromTabStrip(record.ConversationKey) {
			continue
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
		CrewID: project.ProjectID, CrewProfile: project.ProfileID, CrewOwner: firstNonEmptyTrimmed(chat.OwnerID, project.OwnerID), Chat: &chat}
}

// codeChatTurnRequest builds the turn in the target chat's existing
// conversation, re-checking that it is still that chat of the same Code.
func (api *StreamingAPI) codeChatTurnRequest(ctx context.Context, userID string, target triggerTarget, text, fromLabel string) (map[string]interface{}, string, error) {
	unavailable := fmt.Errorf("that chat is no longer available")
	if api == nil || api.agentProfiles == nil || target.Chat == nil {
		return nil, "", unavailable
	}
	profile, err := api.agentProfiles.Resolve(workproduct.ProfileID, 0, userID)
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

// agentRolePrompt describes only this tab, including its declared function job (PLAT-750-76).
func agentRolePrompt(ctx context.Context, userID, profileID, conversationKey, workspacePath string) string {
	conversationKey = strings.TrimSpace(conversationKey)
	project, tab, _ := strings.Cut(conversationKey, codeChatSideMarker)
	if project == "" {
		return ""
	}
	path := productConversationRegistryPath(userID)
	mutex := productConversationRegistryMutex(path)
	mutex.Lock()
	document, err := defaultProductConversationRegistryStore().loadDocument(ctx, path)
	mutex.Unlock()
	if err != nil {
		return ""
	}
	entryKey := productConversationRegistryEntryKey(profileID, conversationKey)
	record, found := document.Entries[entryKey]
	if !found || !workspacePathsMatchForUser(userID, record.WorkspacePath, workspacePath) {
		return ""
	}
	identity := document.Agents[entryKey]
	name := identity.Name
	jobs := ""
	if functionTab, ok := strings.CutPrefix(tab, crewFunctionTabPrefix); ok {
		functions, err := readCrewFunctions(ctx, triggerTarget{Kind: triggerCallerCrew, Path: workspacePath, CrewOwner: userID, CrewProfile: profileID, CrewID: project})
		if err == nil {
			sort.Slice(functions, func(i, j int) bool { return functions[i].Name < functions[j].Name })
			for _, function := range functions {
				if function.tabName() == "" || crewAgentChatID(function.tabName()) != functionTab {
					continue
				}
				name = firstNonEmptyTrimmed(name, function.tabName())
				jobs += fmt.Sprintf("\nFunction %q: %s\n", function.Name, strings.TrimSpace(function.Description))
			}
		}
	}
	defaultName := "Main chat"
	if tab != "" {
		defaultName = "Chat"
	}
	name = firstNonEmptyTrimmed(name, record.Title, defaultName)
	role := fmt.Sprintf("\n\n## Your role in this Crew\n\nYou are the %q agent in this Crew.\n", name)
	if purpose := strings.TrimSpace(identity.Purpose); purpose != "" {
		role += "Your job: " + purpose + "\n"
	}
	return role + jobs
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
			item := map[string]interface{}{"id": chat.ID, "name": chat.Name, "is_main": chat.IsMain, "owner_id": chat.OwnerID, "owner_name": crewOwnerDisplayName(chat.OwnerID), "can_write": chat.OwnerID == userID, "working": api.conversationTurnOccupied(chat.SessionID)}
			if chat.Purpose != "" {
				item["purpose"] = chat.Purpose
			}
			if chat.Hidden {
				item["hidden"] = true
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
		{"list_project_chats", "List the agents of this project: the main chat and its side chats (tabs), with id, name, purpose when it has one, whether each is working now, and which one is this chat. Use it before ask_project_chat. All agents means these: tabs the person hid are left out unless include_hidden is true.", map[string]interface{}{"type": "object", "properties": map[string]interface{}{
			"include_hidden": map[string]interface{}{"type": "boolean", "description": "Also list tabs the person hid. Only when they ask for hidden agents."},
		}},
			func(ctx context.Context, args map[string]interface{}) (string, error) {
				ctx = trusted(ctx)
				project, err := api.codeChatsFor(ctx, userID, sessionID, workspacePath)
				if err != nil {
					return "", err
				}
				// "All agents" means the visible ones: a tab the person hid is left out unless asked for (owner, 4 Nov).
				// A removed tab is gone from the registry and never listed.
				if include, _ := args["include_hidden"].(bool); !include {
					visible := project.Chats[:0:0]
					for _, chat := range project.Chats {
						if !chat.Hidden {
							visible = append(visible, chat)
						}
					}
					project.Chats = visible
				}
				return jsonOut(map[string]interface{}{"chats": describe(project)})
			}},
		{"set_my_agent_identity", "Set this agent's own name and purpose (a few words and one short line; either alone keeps the other) so the other agents and people know what it is for. For Main this is the Crew identity (the same as set_work_identity); for a side tab it is that tab's own. An agent can be shared only with a purpose. Only this agent and the person change them; another agent can ask this one to.", map[string]interface{}{
			"type": "object", "properties": map[string]interface{}{
				"name":             map[string]interface{}{"type": "string", "maxLength": productAgentNameMaxRunes},
				"purpose":          map[string]interface{}{"type": "string", "maxLength": productAgentPurposeMaxRunes},
				"in_team":          map[string]interface{}{"type": "boolean", "description": "In a Crew with a Team: true (the default) lets the others who build this Crew see this agent and its chat; false is Only me. Only when the person asks."},
				"when_others_ask":  map[string]interface{}{"type": "string", "maxLength": whenOthersAskMaxRunes, "description": "In a Crew with a Team: the person's own rule for what this agent may do when someone else asks it (\"\" clears it). It is added to those turns only; an instruction, not a lock. Only when the person asks."},
				"can_make_changes": map[string]interface{}{"type": "boolean", "description": "For an agent on a Mac in a Team: false (the default) answers others only; true may make changes for them. Only when the person asks."},
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
				inTeam, setTeam := args["in_team"].(bool)
				var ruleText *string
				var ruleChanges *bool
				if value, ok := args["when_others_ask"].(string); ok {
					ruleText = &value
				}
				if value, ok := args["can_make_changes"].(bool); ok {
					ruleChanges = &value
				}
				setRule := ruleText != nil || ruleChanges != nil
				if name == nil && purpose == nil && !setTeam && !setRule {
					return "", fmt.Errorf("pass a name, a purpose, in_team, when_others_ask or can_make_changes")
				}
				project, err := api.codeChatsFor(ctx, userID, sessionID, workspacePath)
				if err != nil {
					return "", err
				}
				if setRule {
					// Only the agent's own person, in their own turn: the Team record refuses anyone else's agent, and a
					// turn another person asked never changes the rule meant for them.
					api.lastQueryMu.RLock()
					turn, known := api.lastQueryRequests[sessionID]
					api.lastQueryMu.RUnlock()
					if _, _, _, _, other := askerIsNotAgentOwner(ctx, &turn, userID, userID); known && other {
						return "", fmt.Errorf("only this agent's own person sets what it does when others ask, from their own chat")
					}
					rule, err := api.setOwnAgentRule(ctx, userID, project.Self.Key, ruleText, ruleChanges)
					if err != nil {
						return "", err
					}
					if name == nil && purpose == nil && !setTeam {
						return jsonOut(map[string]interface{}{"when_others_ask": rule.WhenOthersAsk, "can_make_changes": rule.CanMakeChanges})
					}
				}
				if setTeam {
					if err := api.setOwnAgentInTeam(ctx, userID, project.Self.Key, inTeam); err != nil {
						return "", err
					}
					if name == nil && purpose == nil {
						return jsonOut(map[string]interface{}{"in_team": inTeam})
					}
				}
				if project.Self.IsMain && project.Workspace != "" {
					// Main's identity is the Crew identity (crew_main_identity.go), the one set_work_identity writes.
					saved, err := writeCrewMainIdentity(ctx, project.Workspace, name, purpose)
					if err != nil {
						return "", err
					}
					go api.refreshMacSharedPurposes(context.WithoutCancel(ctx), project.ProjectID)
					return jsonOut(map[string]interface{}{"name": firstNonEmptyTrimmed(saved.Name, project.Self.Name), "purpose": saved.Purpose})
				}
				identity, err := defaultProductConversationRegistryStore().setAgentIdentity(ctx, userID, project.ProfileID, project.Self.Key, name, purpose, "agent "+project.Self.Name)
				var fields []string
				for label, value := range map[string]*string{"name": name, "purpose": purpose} {
					if value != nil {
						fields = append(fields, label)
					}
				}
				sort.Strings(fields)
				log.Printf("[AGENT_IDENTITY] store=tab tool=set_my_agent_identity session=%s user=%s profile=%s tab=%s set=%s err=%v", sessionID, userID, project.ProfileID, project.Self.Key, strings.Join(fields, ","), err)
				if err != nil {
					return "", err
				}
				// A shared agent on this Mac: its new line reaches the people it is shared with too.
				go api.refreshMacSharedPurposes(context.WithoutCancel(ctx), project.ProjectID)
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
				// Another person's agent from a Crew on its owner's Mac answers only when it is shared with this person: say so
				// now, plainly, rather than accepting a message that is refused on delivery.
				if !IsLocalMode() && chat.OwnerID != userID && crewRelayTwin(project.ProjectID) && crewTeamMemberTab(ctx, project.ProjectID, chat.Key) == "" {
					if _, reason := macSharedAgentForAgent(project.ProjectID, macSharedAgentTab(project.ProjectID, chat.Key), userID, chat.OwnerID); reason != "" {
						log.Printf("[CREW_SHARED_AGENT] project=%s chat=%s action=message refused_at_send reason=%s user=%s", project.ProjectID, chat.ID, reason, logUsernameForUserID(userID))
						return "", fmt.Errorf("%s's %s isn't shared with you, so it can't be asked. Ask %s to share it from the Team tab", shareOwnerLabel(chat.OwnerID), chat.Name, shareOwnerLabel(chat.OwnerID))
					}
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
				// Live test, 6 Nov: told only "replies are optional", an agent guessed the other tab "looks idle" while its
				// answer was 40 s away. Say what happens: the answer arrives here by itself.
				out["next"] = fmt.Sprintf("Sent to %s. Its answer comes back to this chat as a new message when it is ready, also while its owner's Mac is off; there is nothing to check or wait for. Tell the person it was sent.", chat.Name)
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
