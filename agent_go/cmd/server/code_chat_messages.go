package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"sort"
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
	TeamAgentID    string // Team identity; Key remains the native conversation binding.
	OwnerID        string
	IsDefaultAgent bool
	Key            string // conversation key: <projectId> or <projectId>:chat:<id>
	ID             string // "main" or the side chat's id
	Name           string
	Purpose        string
	Examples       []string
	SessionID      string
	// Hidden: the person took this tab off their strip (Close keeps it as a hidden agent). Lists leave it out.
	Hidden    bool
	UpdatedAt string
	// Group is the person's own left-menu group holding this agent; ForkedFrom names the agent it was forked from
	// (#3288). Both are layout and history only, never access.
	Group         string
	ForkedFrom    string
	forkedFromKey string
	// Teammate: in the Mac app, an agent a teammate shares with this Crew's Team (addMacTeammates).
	Teammate  bool
	OwnerName string
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
	unavailable := fmt.Errorf("messaging other chats is available only in your own project or project")
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
		chat.IsDefaultAgent = tab.OwnerID == userID && (record.IsDefaultAgent || record.ConversationKey == crewOwnerDefaultChatKey(manifest.ID, userID))
		chat.Name = "Chat " + chat.ID
		if chat.IsDefaultAgent {
			chat.Name = "Main chat"
		} else if record.IsDefaultAgent || record.ConversationKey == crewOwnerDefaultChatKey(manifest.ID, tab.OwnerID) || strings.HasPrefix(chat.ID, "root-") {
			chat.Name = "Main" // another person's Main, by its name in the Team (never a raw root id)
		}
		// The person's or the agent's own name wins over the chat's automatic title.
		if identity := tab.Identity; identity.Name != "" || identity.Purpose != "" || len(identity.Examples) > 0 {
			chat.Name = firstNonEmptyTrimmed(identity.Name, chat.Name)
			chat.Purpose = identity.Purpose
			chat.Examples = identity.Examples
		}
		chat.forkedFromKey = tab.Identity.ForkedFrom
		// Main hides like any agent (owner, 25 Nov, #4046: Main is not special).
		chat.Hidden = tab.Identity.Unpinned
		if own, ok := mine[record.ConversationKey]; ok && tab.OwnerID != userID {
			chat.Hidden = own.Unpinned
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
				if _, online := crewTeamMemberProjectID(person.UserID); online {
					continue // an online project's agents live in that project; teammates reach them by address
				}
				for _, agent := range person.Agents {
					if person.UserID == userID && agent.ChatKey == project.Self.Key {
						project.Self.TeamAgentID = agent.ID
					}
					if !agent.InTeam {
						continue
					}
					project.Chats = append(project.Chats, codeChat{TeamAgentID: agent.ID, ID: agent.ID, Key: crewTeamNativeChatKey(manifest.ID, person.UserID, agent), OwnerID: person.UserID, Name: agent.Name, Purpose: agent.Purpose, Examples: agent.Examples, SessionID: agent.sessionID, IsDefaultAgent: person.UserID == userID && crewTeamMainAgent(team, userID, agent.ChatKey)})
				}
			}
		}
	}
	// The person's groups and each fork's parent, by name (#3288).
	groups, _ := defaultProductConversationRegistryStore().agentGroups(ctx, userID, manifest.Product, manifest.ID)
	names := map[string]string{}
	for _, chat := range project.Chats {
		names[chat.Key] = chat.Name
	}
	for i := range project.Chats {
		project.Chats[i].Group = groups[project.Chats[i].Key]
		if parent := project.Chats[i].forkedFromKey; parent != "" {
			project.Chats[i].ForkedFrom = firstNonEmptyTrimmed(names[parent], "an agent that is no longer listed")
		}
	}
	sort.SliceStable(project.Chats, func(i, j int) bool {
		return project.Chats[i].UpdatedAt > project.Chats[j].UpdatedAt
	})
	return project, nil
}

// find returns the chat named by its id or exact name; "main" is no alias (#4046), only a name an agent may have.
func (p codeChatProject) find(raw string) (codeChat, error) {
	want := strings.ToLower(strings.TrimSpace(raw))
	if want == "" {
		return codeChat{}, fmt.Errorf("chat is required: pass an id or name from list_project_chats")
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
	defaultName := "Agent"
	if tab != "" {
		defaultName = "Chat"
	}
	name = firstNonEmptyTrimmed(name, record.Title, defaultName)
	role := fmt.Sprintf("\n\n## Your role in this project\n\nYou are the %q agent in this project.\n", name)
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
func (api *StreamingAPI) registerCodeChatTools(registrar definitionToolRegistrar, gate *productToolGate, userID, sessionID, workspacePath string, messages *agentSendHolder) error {
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
			if chat.IsDefaultAgent {
				name = "Main"
			}
			item := map[string]interface{}{"id": chat.ID, "name": name, "is_default_agent": chat.IsDefaultAgent, "owner_id": chat.OwnerID, "owner_name": firstNonEmptyTrimmed(chat.OwnerName, shareOwnerLabel(chat.OwnerID)), "can_write": chat.OwnerID == userID, "working": api.conversationTurnOccupied(chat.SessionID)}
			if chat.Teammate {
				// A teammate's agent runs on their side: its work state is not known here.
				item["teammate"] = true
				delete(item, "working")
			}
			if chat.Purpose != "" {
				item["purpose"] = chat.Purpose
				item["examples"] = chat.Examples
			}
			if chat.Hidden {
				item["hidden"] = true
			}
			if chat.Group != "" {
				item["group"] = chat.Group
			}
			if chat.ForkedFrom != "" {
				item["forked_from"] = chat.ForkedFrom
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
		{"list_project_chats", "List the agents of this project (its tabs; main = the default agent), with id, name, purpose when it has one, whether each is working now, and which one is this chat. group is the person's own left-menu group holding it (agents in one group usually work together; ask each other first), forked_from the agent it was forked from; neither changes access. To hand work to an agent, discover its admitted address with messages action=list_agents and send through AgentBridge. All agents means these: tabs the person hid are left out unless include_hidden is true.", map[string]interface{}{"type": "object", "properties": map[string]interface{}{
			"include_hidden": map[string]interface{}{"type": "boolean", "description": "Also list tabs the person hid. Only when they ask for hidden agents."},
		}},
			func(ctx context.Context, args map[string]interface{}) (string, error) {
				ctx = trusted(ctx)
				project, err := api.codeChatsFor(ctx, userID, sessionID, workspacePath)
				if err != nil {
					return "", err
				}
				project.addMacTeammates(ctx, userID)
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
		{"set_my_agent_identity", "Set this agent's own name and purpose (a few words and one short line; either alone keeps the other) so the other agents and people know what it is for. When its own person asks, also save this agent's own standing instructions (instructions). The name is this chat's own (its tab); it never renames the project (only set_work_identity does, when the person explicitly asks to rename the project). An agent can be shared only with a purpose. Only this agent and the person change them; another agent can ask this one to.", map[string]interface{}{
			"type": "object", "properties": map[string]interface{}{
				"name":            map[string]interface{}{"type": "string", "maxLength": productAgentNameMaxRunes},
				"examples":        agentPurposeExamplesSchema(),
				"purpose":         map[string]interface{}{"type": "string", "maxLength": productAgentPurposeMaxRunes},
				"in_team":         map[string]interface{}{"type": "boolean", "description": "In a project with a Team: true (the default) lets the others who build this project see this agent and its chat; false is Only me. Only when the person asks."},
				"when_others_ask": map[string]interface{}{"type": "string", "maxLength": whenOthersAskMaxRunes, "description": "In a project with a Team: the person's own rule for what this agent may do when someone else asks it (\"\" clears it). It is added to those turns only; an instruction, not a lock. Only when the person asks."},
				"instructions":    map[string]interface{}{"type": "string", "maxLength": productAgentInstructionsMaxRunes, "description": "This agent's own standing instructions (several lines; \"\" clears them). Added to this agent's prompt only, after the platform, project and Team rules. Save the person's words; only when this agent's own person asks, from their own chat."},
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
				if value, ok := args["when_others_ask"].(string); ok {
					ruleText = &value
				}
				setRule := ruleText != nil
				instructions, setInstructions := args["instructions"].(string)
				if name == nil && purpose == nil && !examplesPresent && !setTeam && !setRule && !setInstructions {
					return "", fmt.Errorf("pass a name, a purpose, instructions, in_team or when_others_ask")
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
					rule, err := api.setOwnAgentRule(ctx, userID, project.Self.Key, ruleText)
					if err != nil {
						return "", err
					}
					if name == nil && purpose == nil && !examplesPresent && !setTeam {
						return jsonOut(map[string]interface{}{"when_others_ask": rule.WhenOthersAsk})
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
		{"fork_project_chat", "Fork one of your own chats into an independent tab with its saved conversation and native context where supported. Prefer this over a new empty tab for related work that needs context. The fork is shown in the person's left menu under you (or under its source when you fork your own chat), or beside you with place=beside, and follows that agent's group; group puts it in a named group instead. With target_project (must be tagged with # in this turn), forks the chat as a top-level agent in that project instead (conversation only; files, memory and logins stay behind). With task, the fork starts on it at once as your AgentBridge ask and its answer comes back to you; without task no turn starts. purpose and instructions set the fork up in the same call when the person asks for an agent with its own rules.", map[string]interface{}{
			"type": "object", "required": []string{"chat"}, "properties": map[string]interface{}{
				"place":            map[string]interface{}{"type": "string", "enum": []string{"under", "beside"}, "description": "Where it sits in the person's left menu: under (the default) as your helper that reports back, or beside you as its own agent at your level that works with the person and does not report back (other work that needs this context but stands on its own)."},
				"group":            map[string]interface{}{"type": "string", "maxLength": agentGroupNameMaxRunes, "description": "Optional: put the new agent in this group of the person's left menu (see group in sessions action=list): it joins the group of that name or starts it, as its own agent under no one. Only when the person names a group."},
				"purpose":          map[string]interface{}{"type": "string", "maxLength": productAgentPurposeMaxRunes, "description": "Optional: the new agent's one-line purpose, saved on it."},
				"instructions":     map[string]interface{}{"type": "string", "maxLength": productAgentInstructionsMaxRunes, "description": "Optional: the new agent's own standing instructions (several lines), saved on it before it starts, as sessions action=identity would from its own chat. Pass them when the person asks you to set up an agent with rules, in their words; only from the person's own chat."},
				"task":             map[string]interface{}{"type": "string", "description": "The fork's first task, in full. It starts working on it right away and replies to you. Pass it whenever you know what the fork should do; do not ask the person to open the fork and say go."},
				"chat":             map[string]interface{}{"type": "string", "description": "Source chat id or name from sessions action=list; main selects your default chat."},
				"name":             map[string]interface{}{"type": "string", "maxLength": 120, "description": "Optional name for the new fork; otherwise the source name plus (fork)."},
				"target_project":   map[string]interface{}{"type": "string", "description": "Optional: destination project ID to fork this chat into (must be tagged with # in the current turn). Omit to fork in the current project."},
				"engine":           map[string]interface{}{"type": "string", "description": "Optional: run the fork on this provider (an engine from models action=list, e.g. cursor-cli). It starts a new native session from the copied conversation. Same checks as models action=set scope=tab; a refused choice makes no fork. Omit to keep the source's model."},
				"model_id":         map[string]interface{}{"type": "string", "description": "With engine: a model that provider offers."},
				"reasoning_effort": map[string]interface{}{"type": "string", "description": "With engine: an optional reasoning level that model offers."},
				"connection_id":    map[string]interface{}{"type": "string", "description": "With engine: your personal or available company account ID."},
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
				targetProject := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(stringToolArg(args, "target_project")), "crew:"))
				crossProject := targetProject != "" && targetProject != project.ProjectID
				destProjectID := project.ProjectID
				if crossProject {
					if reach, reachErr := crewReachFrom(ctx, workspacePath); reachErr != nil || (reach != nil && !reach.projects[targetProject]) {
						return "", fmt.Errorf("tag that project with # in your message first to fork an agent into it, or fork from the app menu")
					}
					destProjectID = targetProject
				}
				if claims := GetUserFromContext(ctx); claims != nil && claims.AccessToken != nil {
					if !claims.AccessToken.Allows("crews:write") || !claims.AccessToken.AllowsCrew(workspaceTokenProjectID(project.ProjectID)) || (crossProject && !claims.AccessToken.AllowsCrew(workspaceTokenProjectID(destProjectID))) {
						return "", fmt.Errorf("this connection does not allow forking this project chat")
					}
				}
				tabModel, err := newTabModelFromArgs(args)
				if err != nil {
					return "", err
				}
				request := httptest.NewRequest(http.MethodPost, "/", nil).WithContext(ctx)
				if status, reason := api.refuseNewTabModel(request, project.ProfileID, destProjectID, tabModel); status != 0 {
					return "", fmt.Errorf("%s. Choose a provider, model and reasoning level offered by models action=list; no fork was made", reason)
				}
				task := strings.TrimSpace(stringToolArg(args, "task"))
				if task != "" && (messages == nil || messages.send == nil) {
					return "", fmt.Errorf("this chat cannot send a fork its task; fork without task and tell the person what to ask it")
				}
				place, err := spawnPlace(args)
				if err != nil {
					return "", err
				}
				group, err := spawnGroupArg(args)
				if err != nil {
					return "", err
				}
				identity, err := api.spawnIdentityArgs(ctx, sessionID, userID, args)
				if err != nil {
					return "", err
				}
				payload := map[string]any{"conversation_key": chat.Key, "name": stringToolArg(args, "name"), "task": task}
				if crossProject {
					payload["target_project"] = destProjectID
				}
				status, body := scheduleHandler(request, api.handleForkCrewChat, http.MethodPost, "/api/agent-profiles/work/conversation/fork", map[string]string{"id": project.ProfileID}, nil, payload)
				if status >= 400 {
					return "", fmt.Errorf("%s", laptopActionErrorText(string(body)))
				}
				destProject := project
				destProject.ProjectID = destProjectID
				// Cross-project forks stand as their own top-level agent in the destination project and keep their
				// cross-project first-turn handover note (naming old folder -> new folder).
				own := crossProject || place == spawnBeside || group != ""
				if !crossProject {
					fileForkUnderMaker(ctx, userID, project.ProfileID, project.Self.Key, chat.Key, body)
					var made struct {
						Key  string `json:"conversation_key"`
						Name string `json:"name"`
					}
					if json.Unmarshal(body, &made) == nil && made.Key != "" {
						if place == spawnBeside {
							placeBesideMaker(ctx, userID, project, made.Key)
						}
						if !own {
							setFirstTurnNote(ctx, userID, project, made.Key, forkFirstTurnNote(chat.Name, firstNonEmptyTrimmed(made.Name, chat.Name+" (fork)"), chat.Purpose, false))
						}
					}
				}
				out := api.applyForkModel(request, destProject.ProfileID, destProject.ProjectID, tabModel, body)
				out = placeInGroupJSON(ctx, userID, destProject, group, out)
				// Saved before the task is sent, so the fork's first turn already follows them.
				out = saveSpawnIdentityJSON(ctx, userID, destProject, identity, out)
				if task == "" {
					return string(out), nil
				}
				return api.sendForkTask(ctx, userID, destProject, messages, task, own, out), nil
			}},
		{"delete_project_chat", "Delete a fork or helper tab you made, while it is still under you in the person's left menu, when its work is done or the person asks. Call once without confirmation to see what goes, then again with the returned confirmation. Its saved chats and queued messages are deleted; project files and other agents stay. Refused for the project's default agent, for yourself, for a tab you did not make, and for one the person has moved out from under you in the left menu (then the person removes it in the app).", map[string]interface{}{
			"type": "object", "required": []string{"chat"}, "properties": map[string]interface{}{
				"chat":         map[string]interface{}{"type": "string", "description": "The fork's or helper's id or name from sessions action=list."},
				"confirmation": map[string]interface{}{"type": "string", "description": "The confirmation from the first call; omit it to preview."},
			}},
			func(ctx context.Context, args map[string]interface{}) (string, error) {
				// Another person's ask runs inside this agent's own chat with its owner's authority (#3471), and an
				// agent set to make changes still has this tool: only the owner's own turn deletes tabs.
				if api.anotherPersonsTurn(sessionID, userID) {
					return "", fmt.Errorf("only this agent's own person can have it delete tabs, from their own chat")
				}
				ctx = trusted(ctx)
				project, err := api.codeChatsFor(ctx, userID, sessionID, workspacePath)
				if err != nil {
					return "", err
				}
				if project.OwnerID != userID {
					return "", fmt.Errorf("only the owner of this project deletes its tabs")
				}
				if claims := GetUserFromContext(ctx); claims != nil && claims.AccessToken != nil {
					if !claims.AccessToken.Allows("crews:write") || !claims.AccessToken.AllowsCrew(workspaceTokenProjectID(project.ProjectID)) {
						return "", fmt.Errorf("this connection does not allow deleting this project's chats")
					}
				}
				chat, err := project.find(strings.TrimSpace(stringToolArg(args, "chat")))
				if err != nil {
					return "", err
				}
				if reason := childForkRefusal(ctx, userID, project, chat); reason != "" {
					return "", fmt.Errorf("%s", reason)
				}
				confirmation := strings.TrimSpace(stringToolArg(args, "confirmation"))
				request := httptest.NewRequest(http.MethodPost, "/", nil).WithContext(ctx)
				status, body := scheduleHandler(request, api.handleCloseAgentProfileSideChat, http.MethodPost, "/api/agent-profiles/work/conversation/close", map[string]string{"id": project.ProfileID}, nil, AgentProfileConversationRequest{ConversationKey: chat.Key, PreviewRemoval: confirmation == "", RemovalConfirmation: confirmation})
				if status >= 400 {
					return "", fmt.Errorf("%s", laptopActionErrorText(string(body)))
				}
				log.Printf("[CHAT_DELETE] parent=%s child=%s step=%s status=%d", project.Self.Key, chat.Key, map[bool]string{true: "preview", false: "delete"}[confirmation == ""], status)
				if confirmation != "" {
					return jsonOut(map[string]interface{}{"deleted": true, "name": chat.Name, "next": "Its tab and saved chats are gone; project files are unchanged."})
				}
				var preview crewAgentRemovalPreview
				if err := json.Unmarshal(body, &preview); err != nil {
					return "", err
				}
				if len(preview.Dependencies) > 0 {
					return "", fmt.Errorf("%s has linked work (schedules, Watchers or functions) that needs the person's choice; ask them to remove it from the left menu in the app", chat.Name)
				}
				return jsonOut(map[string]interface{}{"name": chat.Name, "will": preview.Effects, "confirmation": preview.Confirmation, "next": "Call delete again with this confirmation to delete it."})
			}},
		{"move_project_chat_out", "Move one of the person's forks out from under the agent it hangs under in their left menu, when they ask, so it becomes an agent of its own; with under, put it back under that agent. Its conversation, model and group stay. Once moved out, the agent it was under can no longer delete it.", map[string]interface{}{
			"type": "object", "required": []string{"chat"}, "properties": map[string]interface{}{
				"chat":  map[string]interface{}{"type": "string", "description": "The fork's id or name from sessions action=list."},
				"under": map[string]interface{}{"type": "string", "description": "Optional: the id or name of the agent to put it back under; omit to move it out."},
			}},
			func(ctx context.Context, args map[string]interface{}) (string, error) {
				// Where a fork sits is the person's own choice: another person's ask in this chat may not change it.
				if api.anotherPersonsTurn(sessionID, userID) {
					return "", fmt.Errorf("only this agent's own person can have it move forks, from their own chat")
				}
				ctx = trusted(ctx)
				project, err := api.codeChatsFor(ctx, userID, sessionID, workspacePath)
				if err != nil {
					return "", err
				}
				if project.OwnerID != userID {
					return "", fmt.Errorf("only the owner of this project moves its agents")
				}
				chat, err := project.find(strings.TrimSpace(stringToolArg(args, "chat")))
				if err != nil {
					return "", err
				}
				parent, parentName := "", ""
				if under := strings.TrimSpace(stringToolArg(args, "under")); under != "" {
					home, err := project.find(under)
					if err != nil {
						return "", err
					}
					parent, parentName = home.Key, home.Name
				}
				if status, reason := api.setForkParent(ctx, userID, project.ProfileID, project.ProjectID, project.Workspace, chat.Key, parent); status != 0 {
					return "", fmt.Errorf("%s", reason)
				}
				if parent == "" {
					return jsonOut(map[string]interface{}{"moved_out": true, "name": chat.Name, "next": chat.Name + " is now an agent of its own in the left menu."})
				}
				return jsonOut(map[string]interface{}{"moved_under": parentName, "name": chat.Name, "next": chat.Name + " is back under " + parentName + " in the left menu."})
			}},
		{"group_project_chats", "Arrange the person's own left-menu groups of this project's agents when they ask (\"put research and ui in a group called Design\"). op new puts chat in a new group called name; move puts chat in the existing group (empty group takes it out); rename renames group to name; ungroup removes group (its agents stay). chat is an id or name from list_project_chats and may be a teammate's agent shared with the Team. A fork follows its parent, so group the parent. Groups are layout only: they never change who can open or message an agent.", map[string]interface{}{
			"type": "object", "required": []string{"op"}, "properties": map[string]interface{}{
				"op":    map[string]interface{}{"type": "string", "enum": []string{"new", "move", "rename", "ungroup"}},
				"chat":  map[string]interface{}{"type": "string", "description": "new, move: the agent's id or name."},
				"group": map[string]interface{}{"type": "string", "description": "move: the group to put it in (empty takes it out). rename, ungroup: the group."},
				"name":  map[string]interface{}{"type": "string", "maxLength": agentGroupNameMaxRunes, "description": "new: the new group's name. rename: the new name."},
			}},
			func(ctx context.Context, args map[string]interface{}) (string, error) {
				ctx = trusted(ctx)
				project, err := api.codeChatsFor(ctx, userID, sessionID, workspacePath)
				if err != nil {
					return "", err
				}
				project.addMacTeammates(ctx, userID)
				edit := agentGroupEdit{Op: stringToolArg(args, "op"), Group: stringToolArg(args, "group"), Name: stringToolArg(args, "name")}
				if raw := strings.TrimSpace(stringToolArg(args, "chat")); raw != "" {
					chat, err := project.find(raw)
					if err != nil {
						return "", err
					}
					edit.Key = chat.Key
				}
				groups, err := defaultProductConversationRegistryStore().editAgentGroups(ctx, userID, project.ProfileID, project.ProjectID, edit)
				if err != nil {
					return "", err
				}
				names := map[string]string{}
				for _, chat := range project.Chats {
					names[chat.Key] = chat.Name
				}
				out := []map[string]interface{}{}
				for _, group := range agentGroupNames(groups) {
					members := []string{}
					for key, in := range groups {
						if in == group {
							members = append(members, firstNonEmptyTrimmed(names[key], "an agent no longer listed"))
						}
					}
					sort.Strings(members)
					out = append(out, map[string]interface{}{"group": group, "agents": members})
				}
				return jsonOut(map[string]interface{}{"groups": out, "next": "The person's left menu shows the change on its next refresh."})
			}},
		{"open_project_chat", "Open a new named tab (a new agent) in this project, as the + in the app does, to give it part of the work. name is required and unique in this project (an existing tab with that name is reused, not duplicated); with task it starts on it at once as your AgentBridge ask and answers you (a fresh agent: it gets only the task, not this conversation; use fork_project_chat when it needs this chat's context). Open several with tasks to work in parallel, e.g. one per area or a tester or critic of your work. engine, model_id, reasoning_effort and connection_id optionally start the new tab on that model in the same call (do not open it and then set the model); purpose and instructions set the agent up in the same call when the person asks for an agent with its own rules (they also update an existing tab with that name). This project's owner only.", map[string]interface{}{
			"type": "object", "required": []string{"name"}, "properties": map[string]interface{}{
				"name":             map[string]interface{}{"type": "string", "maxLength": 120, "description": "A short name for the tab, e.g. tests or review."},
				"purpose":          map[string]interface{}{"type": "string", "maxLength": productAgentPurposeMaxRunes, "description": "Optional: the new agent's one-line purpose, saved on it (also on an existing tab with that name)."},
				"instructions":     map[string]interface{}{"type": "string", "maxLength": productAgentInstructionsMaxRunes, "description": "Optional: the new agent's own standing instructions (several lines), saved on it before it starts (also on an existing tab with that name), as sessions action=identity would from its own chat. Pass them when the person asks you to set up an agent with rules, in their words; only from the person's own chat."},
				"task":             map[string]interface{}{"type": "string", "description": "Its first task, in full: everything it needs, since it starts without this conversation. It starts right away and replies to you. Ignored for a tab that already exists; message that one instead."},
				"place":            map[string]interface{}{"type": "string", "enum": []string{"under", "beside"}, "description": "Where it sits in the person's left menu: under (the default) as your helper that reports back, or beside you as its own agent at your level that works with the person and does not report back (other work that stands on its own)."},
				"group":            map[string]interface{}{"type": "string", "maxLength": agentGroupNameMaxRunes, "description": "Optional: put the new agent in this group of the person's left menu (see group in sessions action=list): it joins the group of that name or starts it, as its own agent under no one. Only when the person names a group."},
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
				task := strings.TrimSpace(stringToolArg(args, "task"))
				if task != "" && (messages == nil || messages.send == nil) {
					return "", fmt.Errorf("this chat cannot send a new agent its task; open it without task and tell the person what to ask it")
				}
				place, err := spawnPlace(args)
				if err != nil {
					return "", err
				}
				group, err := spawnGroupArg(args)
				if err != nil {
					return "", err
				}
				identity, err := api.spawnIdentityArgs(ctx, sessionID, userID, args)
				if err != nil {
					return "", err
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
					if !tabModel.chosen() && project.Self.Key != "" && api.agentProfiles != nil {
						// No model asked for: the new agent starts on its opener's model (owner, 26 Nov).
						if profile, err := api.agentProfiles.Resolve(project.ProfileID, 0, userID); err == nil {
							api.startOnOpenerModel(ctx, userID, profile, binding, chat.Key, project.Self.Key)
						}
					}
				}
				if !reused && project.Self.Key != "" {
					// The helper is nested under the agent that opened it (or beside it, when asked) and told once, on
					// its first turn, who opened it.
					parentKey, note := project.Self.Key, helperFirstTurnNote(chat.Name, firstNonEmptyTrimmed(project.Self.Name, "its opener"))
					if place == spawnBeside || group != "" {
						note = ownAgentFirstTurnNote(chat.Name, firstNonEmptyTrimmed(project.Self.Name, "its opener"))
					}
					if _, err := defaultProductConversationRegistryStore().setChatContextOptions(ctx, userID, project.ProfileID, chat.Key, chatContextChange{ForkedFrom: &parentKey, ForkNote: &note}, userID); err != nil {
						log.Printf("[CHAT_CONTEXT] helper %s: could not record who opened it: %v", chat.Key, err)
					}
					if place == spawnBeside {
						placeBesideMaker(ctx, userID, project, chat.Key)
					}
				}
				out := map[string]interface{}{"id": chat.ID, "name": chat.Name, "reused": reused}
				if tabModel.chosen() && !reused {
					out["engine"], out["model_id"], out["reasoning_effort"] = tabModel.engine, tabModel.modelID, tabModel.effort
				}
				placeInGroup(ctx, userID, project, chat.Key, group, out)
				// Saved before the task is sent, so the new agent's first turn already follows them.
				saveSpawnIdentity(ctx, userID, project, chat.Key, identity, out)
				if task != "" && !reused {
					for key, value := range api.sendStartTask(ctx, userID, project, messages, chat.Key, task, place == spawnBeside || group != "") {
						out[key] = value
					}
					return jsonOut(out)
				}
				out["next"] = "Find its admitted agent address with messages action=list_agents, then send the task through AgentBridge."
				if reused && task != "" {
					out["next"] = "A tab with that name already exists, so the task was not sent. Send it with messages action=ask, or open a new name."
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

// fileForkUnderMaker files a fork an agent made of another chat under that agent in the left menu: the maker sends the
// fork its work, so that is where the person looks for it. Forking its own chat changes nothing. The fork's first-turn
// note still names the chat it copied.
func fileForkUnderMaker(ctx context.Context, userID, profileID, makerKey, sourceKey string, forkResult []byte) {
	if makerKey == "" || makerKey == sourceKey {
		return
	}
	var made struct {
		Key string `json:"conversation_key"`
	}
	if json.Unmarshal(forkResult, &made) != nil || made.Key == "" {
		return
	}
	if _, err := defaultProductConversationRegistryStore().setChatContextOptions(ctx, userID, profileID, made.Key, chatContextChange{ForkedFrom: &makerKey}, userID); err != nil {
		log.Printf("[CHAT_FORK] fork %s: could not file it under the agent that made it: %v", made.Key, err)
	}
}

// childForkRefusal says why a chat may not delete another tab, or "" when it may. A chat deletes only a fork or helper
// it made that is still nested under it in the person's left menu (forked_from is that chat). When the person moves the
// fork out from under it, the fork is no longer the chat's child and only the person removes it (owner, 2026-11-21).
func childForkRefusal(ctx context.Context, userID string, project codeChatProject, chat codeChat) string {
	switch {
	case project.Self.Key == "" || chat.Key == project.Self.Key:
		return "a chat can't delete itself"
	case chat.OwnerID != "" && chat.OwnerID != userID:
		return "that tab belongs to someone else"
	}
	identity, err := defaultProductConversationRegistryStore().agentIdentity(ctx, userID, project.ProfileID, chat.Key)
	if err != nil {
		return "could not read where " + chat.Name + " sits in the left menu"
	}
	if identity.ForkedFrom != project.Self.Key {
		if identity.ForkedFrom == "" {
			return chat.Name + " was not forked or opened by you, or the person moved it out from under you; ask the person to remove it in the app"
		}
		return chat.Name + " is not under you in the left menu (another agent made it, or the person moved it); ask the person to remove it in the app"
	}
	return ""
}

// anotherPersonsTurn is true unless the chat's current turn is known to be its owner's own: a teammate's or guest's
// ask, a bot or Slack turn, or a pinned run is someone else's, the same rule list_agents applies (desktopAppTurnRestricted).
func (api *StreamingAPI) anotherPersonsTurn(sessionID, owner string) bool {
	api.lastQueryMu.RLock()
	current, known := api.lastQueryRequests[sessionID]
	api.lastQueryMu.RUnlock()
	return !known || desktopAppTurnRestricted(current, owner)
}
