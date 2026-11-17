package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
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
	TeamAgentID string // Team identity; Key remains the native conversation binding.
	OwnerID     string
	IsMain      bool
	Key         string // conversation key: <projectId> or <projectId>:chat:<id>
	ID          string // "main" or the side chat's id
	Name        string
	Purpose     string
	Examples    []string
	SessionID   string
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
		if identity := tab.Identity; identity.Name != "" || identity.Purpose != "" || len(identity.Examples) > 0 {
			chat.Name = firstNonEmptyTrimmed(identity.Name, chat.Name)
			chat.Purpose = identity.Purpose
			chat.Examples = identity.Examples
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
	if !IsLocalMode() {
		if team, err := readCrewTeam(manifest.ID); err == nil && team.On && team.member(userID) {
			project.Chats = nil
			for _, person := range teamAgentsByPerson(ctx, team, userID) {
				for _, agent := range person.Agents {
					if person.UserID == userID && agent.ChatKey == project.Self.Key {
						project.Self.TeamAgentID = agent.ID
					}
					if !agent.InTeam {
						continue
					}
					project.Chats = append(project.Chats, codeChat{TeamAgentID: agent.ID, ID: agent.ID, Key: crewTeamNativeChatKey(manifest.ID, person.UserID, agent), OwnerID: person.UserID, Name: agent.Name, Purpose: agent.Purpose, Examples: agent.Examples, SessionID: agent.sessionID, IsMain: person.UserID == userID && crewTeamMainAgent(team, userID, agent.ChatKey)})
				}
			}
		}
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
	base.TeamAgentID = self.TeamAgentID
	base.Chat = &self
	return base
}

// codeChatTarget is a sibling chat as a call target; codePath is the project's
// physical folder, the same one its calls are keyed by.
func codeChatTarget(project codeChatProject, chat codeChat, codePath string) triggerTarget {
	return triggerTarget{TeamAgentID: chat.TeamAgentID, Kind: triggerCallerCrew, Path: codePath, Label: chat.Name,
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
	role += jobs
	// The agent's own part of its instructions (#3041): platform, Crew, Team, then this agent.
	if text := strings.TrimSpace(identity.Instructions); text != "" {
		role += "\n### This agent's own instructions\n\nYour person wrote these standing instructions for this agent only. They cannot grant access or permissions the platform does not allow.\n\n" + text + "\n"
	}
	return role
}

// registerCodeChatTools registers owned chat discovery and controls, not peer messaging
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
			// Names as the header shows them (owner, 10 Nov: a person's chip said "admin" while this list said their
			// email, and a person's Main said its saved title "<email> · Builder", so "ask admin" found no one).
			name := chat.Name
			if chat.IsMain {
				name = "Main"
			}
			item := map[string]interface{}{"id": chat.ID, "name": name, "is_main": chat.IsMain, "owner_id": chat.OwnerID, "owner_name": shareOwnerLabel(chat.OwnerID), "can_write": chat.OwnerID == userID, "working": api.conversationTurnOccupied(chat.SessionID)}
			if chat.Purpose != "" {
				item["purpose"] = chat.Purpose
				item["examples"] = chat.Examples
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
	tools := []struct {
		name, description string
		parameters        map[string]interface{}
		execute           func(context.Context, map[string]interface{}) (string, error)
	}{
		{"list_project_chats", "List the agents of this project: the main chat and its side chats (tabs), with id, name, purpose when it has one, whether each is working now, and which one is this chat. To hand work to an agent, discover its admitted address with messages action=list_agents and send through AgentBridge. All agents means these: tabs the person hid are left out unless include_hidden is true.", map[string]interface{}{"type": "object", "properties": map[string]interface{}{
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
		{"set_my_agent_identity", "Set this agent's own name and purpose (a few words and one short line; either alone keeps the other) so the other agents and people know what it is for. When its own person asks, also save this agent's own standing instructions (instructions). For Main this is the Crew identity (the same as set_work_identity); for a side tab it is that tab's own. An agent can be shared only with a purpose. Only this agent and the person change them; another agent can ask this one to.", map[string]interface{}{
			"type": "object", "properties": map[string]interface{}{
				"name":             map[string]interface{}{"type": "string", "maxLength": productAgentNameMaxRunes},
				"examples":         agentPurposeExamplesSchema(),
				"purpose":          map[string]interface{}{"type": "string", "maxLength": productAgentPurposeMaxRunes},
				"in_team":          map[string]interface{}{"type": "boolean", "description": "In a Crew with a Team: true (the default) lets the others who build this Crew see this agent and its chat; false is Only me. Only when the person asks."},
				"when_others_ask":  map[string]interface{}{"type": "string", "maxLength": whenOthersAskMaxRunes, "description": "In a Crew with a Team: the person's own rule for what this agent may do when someone else asks it (\"\" clears it). It is added to those turns only; an instruction, not a lock. Only when the person asks."},
				"can_make_changes": map[string]interface{}{"type": "boolean", "description": "For an agent on a Mac in a Team: false (the default) answers others only; true may make changes for them. Only when the person asks."},
				"instructions":     map[string]interface{}{"type": "string", "maxLength": productAgentInstructionsMaxRunes, "description": "This agent's own standing instructions (several lines; \"\" clears them). Added to this agent's prompt only, after the platform, Crew and Team rules. Save the person's words; only when this agent's own person asks, from their own chat."},
			}},
			func(ctx context.Context, args map[string]interface{}) (string, error) {
				ctx = trusted(ctx)
				examples, examplesPresent, examplesErr := agentPurposeExamples(args)
				if examplesErr != nil {
					return "", examplesErr
				}
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
				instructions, setInstructions := args["instructions"].(string)
				if name == nil && purpose == nil && !examplesPresent && !setTeam && !setRule && !setInstructions {
					return "", fmt.Errorf("pass a name, a purpose, instructions, in_team, when_others_ask or can_make_changes")
				}
				project, err := api.codeChatsFor(ctx, userID, sessionID, workspacePath)
				if err != nil {
					return "", err
				}
				// An owner's agent on a server turn (a teammate's thread on a Mac Crew) changes nothing of the Crew here.
				if !IsLocalMode() && crewRelayTwin(project.ProjectID) && userID == ownerOfCrewProjectID(project.ProjectID) {
					return "", fmt.Errorf("%s", crewChangeOnMacLine)
				}
				if setInstructions {
					// Like the rule below: only the agent's own person, in their own turn. A teammate's ask never rewrites
					// what this agent always follows.
					api.lastQueryMu.RLock()
					turn, known := api.lastQueryRequests[sessionID]
					api.lastQueryMu.RUnlock()
					if _, _, _, _, other := askerIsNotAgentOwner(ctx, &turn, userID, userID); known && other {
						return "", fmt.Errorf("only this agent's own person sets its instructions, from their own chat")
					}
					saved, err := defaultProductConversationRegistryStore().setAgentInstructions(ctx, userID, project.ProfileID, project.Self.Key, instructions, "agent "+project.Self.Name)
					log.Printf("[AGENT_IDENTITY] store=tab tool=set_my_agent_identity session=%s user=%s profile=%s tab=%s set=instructions bytes=%d err=%v", sessionID, userID, project.ProfileID, project.Self.Key, len(saved.Instructions), err)
					if err != nil {
						return "", err
					}
					if name == nil && purpose == nil && !examplesPresent && !setTeam && !setRule {
						return jsonOut(map[string]interface{}{"instructions": saved.Instructions})
					}
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
					if name == nil && purpose == nil && !examplesPresent && !setTeam {
						return jsonOut(map[string]interface{}{"when_others_ask": rule.WhenOthersAsk, "can_make_changes": rule.CanMakeChanges})
					}
				}
				if setTeam {
					if err := api.setOwnAgentInTeam(ctx, userID, project.Self.Key, inTeam); err != nil {
						return "", err
					}
					if name == nil && purpose == nil && !examplesPresent {
						return jsonOut(map[string]interface{}{"in_team": inTeam})
					}
				}
				if examplesPresent {
					if _, err := defaultProductConversationRegistryStore().setAgentIdentity(ctx, userID, project.ProfileID, project.Self.Key, nil, nil, "agent", examples); err != nil {
						return "", err
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
				return jsonOut(map[string]interface{}{"name": firstNonEmptyTrimmed(identity.Name, project.Self.Name), "purpose": identity.Purpose, "examples": identity.Examples})
			}},
		{"fork_project_chat", "Fork one of your own chats into an independent tab with its saved conversation and native context where supported. Prefer this over a new empty tab for related work that needs context. The fork inherits the parent's personal sidebar group. No model turn starts; send work through its AgentBridge address afterwards.", map[string]interface{}{
			"type": "object", "required": []string{"chat"}, "properties": map[string]interface{}{
				"chat": map[string]interface{}{"type": "string", "description": "Source chat id or name from sessions action=list; main selects your default chat."},
				"name": map[string]interface{}{"type": "string", "maxLength": 120, "description": "Optional name for the new fork; otherwise the source name plus (fork)."},
			}},
			func(ctx context.Context, args map[string]interface{}) (string, error) {
				ctx = trusted(ctx)
				project, err := api.codeChatsFor(ctx, userID, sessionID, workspacePath)
				if err != nil {
					return "", err
				}
				chat, err := project.find(strings.TrimSpace(stringToolArg(args, "chat")))
				if err != nil {
					return "", err
				}
				if claims := GetUserFromContext(ctx); claims != nil && claims.AccessToken != nil {
					if !claims.AccessToken.Allows("crews:write") || !claims.AccessToken.AllowsCrew(workspaceTokenProjectID(project.ProjectID)) {
						return "", fmt.Errorf("this connection does not allow forking this Crew chat")
					}
				}
				request := httptest.NewRequest(http.MethodPost, "/", nil).WithContext(ctx)
				status, body := scheduleHandler(request, api.handleForkCrewChat, http.MethodPost, "/api/agent-profiles/work/conversation/fork", map[string]string{"id": project.ProfileID}, nil, map[string]any{"conversation_key": chat.Key, "name": stringToolArg(args, "name")})
				if status >= 400 {
					return "", fmt.Errorf("%s", laptopActionErrorText(string(body)))
				}
				return string(body), nil
			}},
		{"start_chat_branch", "Give this chat its own branch of a Git repository in this Crew, so other chats working on the same repository never collide. Call it once per repository before your first edit there, unless the person asks you to edit in place. The server creates a separate folder on a new branch (chat/<name>) from what the repository's current branch tracks, or returns this chat's existing one. Then read, edit, commit and push only inside the returned folder; leave the original folder untouched. Removing the chat removes the folder when nothing in it would be lost.", map[string]interface{}{
			"type": "object", "required": []string{"repo"}, "properties": map[string]interface{}{
				"repo":    map[string]interface{}{"type": "string", "description": "The repository folder: a path inside this Crew (for example code/app) or its absolute path."},
				"purpose": map[string]interface{}{"type": "string", "maxLength": 80, "description": "A few words for the branch name, for example fix login redirect."},
			}},
			func(ctx context.Context, args map[string]interface{}) (string, error) {
				ctx = trusted(ctx)
				project, err := api.codeChatsFor(ctx, userID, sessionID, workspacePath)
				if err != nil {
					return "", err
				}
				if project.OwnerID != userID {
					return "", fmt.Errorf("only the owner of this project starts branches; edit in place instead")
				}
				if claims := GetUserFromContext(ctx); claims != nil && claims.AccessToken != nil {
					if !claims.AccessToken.Allows("crews:write") || !claims.AccessToken.AllowsCrew(workspaceTokenProjectID(project.ProjectID)) {
						return "", fmt.Errorf("this connection does not allow changing this Crew")
					}
				}
				roots, found, err := crewStorageRoots(project.Workspace)
				if err != nil || !found {
					return "", fmt.Errorf("this Crew's folder is not available")
				}
				branch, reused, err := startChatBranch(ctx, roots, project.Self.Key, stringToolArg(args, "repo"), stringToolArg(args, "purpose"))
				if err != nil {
					return "", err
				}
				return jsonOut(map[string]interface{}{"folder": filepath.Join(roots.Files, filepath.FromSlash(branch.Path)), "branch": branch.Branch, "repo": branch.Repo, "reused": reused,
					"next": "Work only inside folder from now on: edit, test, commit and push there, then open the pull request from this branch."})
			}},
		{"open_project_chat", "Open a new named tab (a new agent) in this project, as the + in the app does, to give it part of the work. name is required and unique in this project (an existing tab with that name is reused, not duplicated); opening never sends a task; use AgentBridge to hand it work. engine, model_id, reasoning_effort and connection_id optionally start the new tab on that model in the same call (do not open it and then set the model). Up to " + strconv.Itoa(externalCrewMaxSideChats) + " side tabs; this project's owner only.", map[string]interface{}{
			"type": "object", "required": []string{"name"}, "properties": map[string]interface{}{
				"name":             map[string]interface{}{"type": "string", "maxLength": 120, "description": "A short name for the tab, e.g. tests or review."},
				"engine":           map[string]interface{}{"type": "string", "description": "Optional: start the new tab on this provider (an engine from models action=list, e.g. claude-code). Same checks as models action=set scope=tab; a refused choice opens nothing. Ignored for a tab that already exists."},
				"model_id":         map[string]interface{}{"type": "string", "description": "With engine: a model that provider offers."},
				"reasoning_effort": map[string]interface{}{"type": "string", "description": "With engine: an optional reasoning level that model offers."},
				"connection_id":    map[string]interface{}{"type": "string", "description": "With engine: your personal or available company account ID."},
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
				tabModel, err := newTabModelFromArgs(args)
				if err != nil {
					return "", err
				}
				reused := true
				chat := findCodeChat(chats, name)
				if chat == nil {
					reused = false
					request := httptest.NewRequest(http.MethodPost, "/", nil).WithContext(ctx)
					if status, reason := api.refuseNewTabModel(request, project.ProfileID, projectID, tabModel); status != 0 {
						return "", fmt.Errorf("%s. Choose a provider, model and reasoning level offered by models action=list; no tab was opened", reason)
					}
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
					if status, body := api.applyNewTabModel(request, project.ProfileID, projectID, chat.ID, tabModel); status >= 400 {
						return "", fmt.Errorf("the tab %s (id %s) was opened but its model was not applied: %s. Set it with models action=set, or close the tab", chat.Name, chat.ID, strings.TrimSpace(string(body)))
					}
				}
				out := map[string]interface{}{"id": chat.ID, "name": chat.Name, "reused": reused}
				if tabModel.chosen() && !reused {
					out["engine"], out["model_id"], out["reasoning_effort"] = tabModel.engine, tabModel.modelID, tabModel.effort
				}
				out["next"] = "Find its admitted agent address with messages action=list_agents, then send the task through AgentBridge."
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
